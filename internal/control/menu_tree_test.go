package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type menuPageStore struct {
	authOnlyStore
	versions []MenuVersion
}

func (s menuPageStore) MenuVersions(context.Context) ([]MenuVersion, error) {
	return s.versions, nil
}

func folderTestProfiles() []WindowsProfile {
	return []WindowsProfile{
		{ID: 1, WindowsProfileSource: WindowsProfileSource{Title: "Windows 10x64", Folder: "Windows"}, State: "ready", InstallFile: "install.wim"},
		{ID: 2, WindowsProfileSource: WindowsProfileSource{Title: "Windows 11 x64", Folder: "Windows"}, State: "ready", InstallFile: "install.wim"},
	}
}

func TestMenuFoldersProduceNestedNavigationAndBootEntries(t *testing.T) {
	form := url.Values{
		"title": {"PXE Platform"}, "label": {"Diagnostics"}, "message": {"Ready"},
		"ubuntu": {"on"}, "winiso_1": {"on"}, "winiso_2": {"on"},
		"folder_ubuntu":   {"Linux"},
		"folder_winiso_1": {"Windows/Установщики"},
		"folder_winiso_2": {"Windows/Установщики"},
	}
	script, err := BuildMenuFromFormWithProfiles(form, "http://10.30.0.10:8080", folderTestProfiles())
	if err != nil {
		t.Fatal(err)
	}
	got := string(script)
	for _, want := range []string{
		"menu PXE Platform\n",
		"menu PXE Platform / Linux\n",
		"menu PXE Platform / Windows\n",
		"menu PXE Platform / Windows / Установщики\n",
		"item ubuntu Ubuntu Desktop 26.04.1\n",
		"iseq ${platform} efi && goto ubuntu_efi || goto ubuntu_boot\n" +
			":ubuntu_efi\n" +
			"shim http://10.30.0.10:8080/assets/ubuntu/bootx64.efi || goto ubuntu_failed\n" +
			":ubuntu_boot\n" +
			"boot || goto ubuntu_failed\n",
		"item winiso_1 Windows 10x64\n",
		"item winiso_2 Windows 11 x64\n",
		"kernel http://10.30.0.10:8080/assets/windows/wimboot\n",
		"initrd -n winpeshl.ini http://10.30.0.10:8080/assets/windows-profiles/2/winpeshl.ini\n",
		"initrd -n pxe-setup.cmd http://10.30.0.10:8080/assets/windows-profiles/2/pxe-setup.cmd\n",
		"initrd http://10.30.0.10:8080/assets/windows-profiles/2/boot.wim boot.wim\n",
		"item local Boot local disk\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in generated menu", want)
		}
	}
	if strings.Count(got, "menu PXE Platform / Windows / Установщики\n") != 1 {
		t.Fatal("shared folder was emitted more than once")
	}
	if strings.Index(got, "item winiso_1 ") > strings.Index(got, "item winiso_2 ") {
		t.Fatal("profile order changed in shared folder")
	}
	if strings.Count(got, ".. Back\n") != 3 {
		t.Fatalf("expected one Back item per folder, got script:\n%s", got)
	}
	for _, want := range []string{
		"item folder_1 Linux\nitem folder_2 Windows\nitem local Boot local disk\n",
		":folder_2\nmenu PXE Platform / Windows\nitem menu_root .. Back\nitem folder_3 Установщики\nchoose target || goto menu_root\n",
		":folder_3\nmenu PXE Platform / Windows / Установщики\nitem folder_2 .. Back\nitem winiso_1 Windows 10x64\nitem winiso_2 Windows 11 x64\nchoose target || goto folder_2\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("navigation section missing %q", want)
		}
	}
}

func TestMenuFoldersRejectUnsafeOrMalformedPaths(t *testing.T) {
	for _, folder := range []string{"Linux//Tools", "/Linux", "Linux/", "Linux\nitem evil", "Linux/${target}", "A/B/C/D/E/F"} {
		form := url.Values{"title": {"PXE"}, "label": {"Diagnostics"}, "message": {"Ready"}, "ubuntu": {"on"}, "folder_ubuntu": {folder}}
		if _, err := BuildMenuFromForm(form, "http://10.30.0.10:8080"); err == nil {
			t.Errorf("accepted Ubuntu folder %q", folder)
		}
		form = url.Values{"title": {"PXE"}, "label": {"Diagnostics"}, "message": {"Ready"}, "winiso_2": {"on"}, "folder_winiso_2": {folder}}
		if _, err := BuildMenuFromFormWithProfiles(form, "http://10.30.0.10:8080", folderTestProfiles()); err == nil {
			t.Errorf("accepted profile folder %q", folder)
		}
	}
}

func TestMenuFoldersRoundTripAndIgnoreUnselectedProfile(t *testing.T) {
	form := url.Values{
		"title": {"PXE"}, "label": {"Diagnostics"}, "message": {"Ready"},
		"ubuntu": {"on"}, "folder_ubuntu": {"Linux/Live"},
		"winiso_1": {"on"}, "folder_winiso_1": {"Windows"},
		"folder_winiso_2": {"Windows/Unused"},
	}
	script, err := BuildMenuFromFormWithProfiles(form, "http://10.30.0.10:8080", folderTestProfiles())
	if err != nil {
		t.Fatal(err)
	}
	folders := menuFolderPaths(string(script))
	if folders["ubuntu"] != "Linux/Live" || folders["winiso_1"] != "Windows" || folders["winiso_2"] != "" {
		t.Fatalf("wrong stored folder choices: %#v", folders)
	}
	if strings.Contains(string(script), "item winiso_2 ") {
		t.Fatal("unselected profile entered the menu")
	}
	if got := menuFolderPaths("#!ipxe\nmenu PXE\n"); len(got) != 0 {
		t.Fatalf("legacy menu should have no folder choices: %#v", got)
	}
}

func TestMenuFoldersPagePrefillsActiveFolder(t *testing.T) {
	form := url.Values{"title": {"PXE"}, "label": {"Diagnostics"}, "message": {"Ready"}, "ubuntu": {"on"}, "folder_ubuntu": {"Linux/Live"}}
	script, err := BuildMenuFromForm(form, "http://10.30.0.10:8080")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Store: menuPageStore{
		authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}},
		versions:      []MenuVersion{{Script: string(script), Active: true}},
	}}
	req := httptest.NewRequest(http.MethodGet, "/menu", nil)
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("menu page returned %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `name="folder_ubuntu" value="Linux/Live"`) {
		t.Fatalf("active folder not shown in form: %s", res.Body.String())
	}
}
