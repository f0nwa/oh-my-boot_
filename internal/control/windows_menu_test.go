package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"pxe-platform/internal/images"
)

type windowsMenuPageStore struct {
	menuPageStore
	profiles []WindowsProfile
}

func (s windowsMenuPageStore) WindowsProfiles(context.Context) ([]WindowsProfile, error) {
	return s.profiles, nil
}

func TestWindowsMenuPagePrefillsActiveProfile(t *testing.T) {
	p := WindowsProfile{ID: 17, WindowsProfileSource: WindowsProfileSource{Title: "Windows 11 x64", Folder: "Windows"}, State: "ready", InstallFile: "install.wim"}
	form := url.Values{"title": {"PXE"}, "label": {"Diagnostics"}, "message": {"Ready"}, "winiso_17": {"on"}, "folder_winiso_17": {"Windows/Installers"}}
	script, err := BuildMenuFromFormWithProfiles(form, "http://10.30.0.10:8080", []WindowsProfile{p})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Store: windowsMenuPageStore{menuPageStore: menuPageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}, versions: []MenuVersion{{Active: true, Script: string(script), Title: "PXE", Label: "Diagnostics", Message: "Ready"}}}, profiles: []WindowsProfile{p}}}
	req := httptest.NewRequest(http.MethodGet, "/menu", nil)
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `name="winiso_17" checked`) || !strings.Contains(res.Body.String(), `name="folder_winiso_17" value="Windows/Installers"`) {
		t.Fatalf("profile selection missing: %d %s", res.Code, res.Body.String())
	}
}

func TestWindowsMenuPublishesSeparateProfilesAndFolders(t *testing.T) {
	profiles := []WindowsProfile{
		{ID: 17, WindowsProfileSource: WindowsProfileSource{Title: "Windows 10 x64", Folder: "Windows"}, State: "ready", InstallFile: "install.wim"},
		{ID: 18, WindowsProfileSource: WindowsProfileSource{Title: "Windows 11 x64", Folder: "Windows"}, State: "ready", InstallFile: "install.esd"},
	}
	form := url.Values{"title": {"PXE Platform"}, "label": {"Diagnostics"}, "message": {"Ready"}, "winiso_17": {"on"}, "winiso_18": {"on"}, "folder_winiso_17": {"Windows"}, "folder_winiso_18": {"Windows"}}
	script, err := BuildMenuFromFormWithProfiles(form, "http://10.30.0.10:8080", profiles)
	if err != nil {
		t.Fatal(err)
	}
	got := string(script)
	for _, want := range []string{
		"menu PXE Platform / Windows\nitem menu_root .. Back\nitem winiso_17 Windows 10 x64\nitem winiso_18 Windows 11 x64\n",
		":winiso_17\nkernel http://10.30.0.10:8080/assets/windows/wimboot\n",
		"initrd -n pxe-setup.cmd http://10.30.0.10:8080/assets/windows-profiles/17/pxe-setup.cmd\n",
		"initrd http://10.30.0.10:8080/assets/windows-profiles/18/boot.wim boot.wim\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q", want)
		}
	}
	selected := menuWindowsProfileIDs(got)
	if len(selected) != 2 || !selected[17] || !selected[18] || menuFolderPaths(got)["winiso_18"] != "Windows" {
		t.Fatalf("selection did not round trip: %#v", selected)
	}
}

func TestWindowsMenuChecksCurrentSourceBeforePublication(t *testing.T) {
	modified := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	source := WindowsProfileSource{StorageID: 7, StorageRoot: "/mnt/pxe-platform-smb/key7", Path: "windows.iso", Size: 123, Modified: modified, Title: "Windows 11 x64", Folder: "Windows"}
	store := &recordingWindowsProfileStore{profile: WindowsProfile{ID: 17, WindowsProfileSource: source, State: "ready", InstallFile: "install.wim"}}
	api := &fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{{ID: 7, Kind: "smb", Root: source.StorageRoot, State: "ready"}}, Images: []images.Image{{StorageID: 7, Path: source.Path, Size: source.Size, Modified: modified, Present: true}}}}
	check := &recordingWindowsISOAPI{}
	s := &Server{Store: store, Images: api, WindowsISO: check}
	form := url.Values{"title": {"PXE"}, "label": {"Diagnostics"}, "message": {"Ready"}, "winiso_17": {"on"}}
	script, err := BuildMenuFromFormWithProfiles(form, "http://10.30.0.10:8080", []WindowsProfile{store.profile})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.verifyMenuWindowsProfiles(context.Background(), string(script)); err != nil || check.req.ID != 17 {
		t.Fatalf("ready profile rejected: %v", err)
	}
	api.catalog.Images[0].Size++
	if err := s.verifyMenuWindowsProfiles(context.Background(), string(script)); err == nil {
		t.Fatal("changed source was accepted")
	}
}

func TestWindowsMenuRejectsUnreadyOrUnknownProfile(t *testing.T) {
	form := url.Values{"title": {"PXE"}, "label": {"Diagnostics"}, "message": {"Ready"}, "winiso_17": {"on"}}
	for _, profiles := range [][]WindowsProfile{nil, {{ID: 17, State: "error", InstallFile: "install.wim", WindowsProfileSource: WindowsProfileSource{Title: "Windows 10"}}}, {{ID: 17, State: "ready", WindowsProfileSource: WindowsProfileSource{Title: "Windows 10"}}}} {
		if _, err := BuildMenuFromFormWithProfiles(form, "http://10.30.0.10:8080", profiles); err == nil {
			t.Fatalf("accepted unready profile: %+v", profiles)
		}
	}
}
