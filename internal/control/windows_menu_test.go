package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
	answers  []WindowsAnswerVariant
}

func (s windowsMenuPageStore) WindowsProfiles(context.Context) ([]WindowsProfile, error) {
	return s.profiles, nil
}
func (s windowsMenuPageStore) WindowsAnswers(context.Context) ([]WindowsAnswerVariant, error) {
	return s.answers, nil
}

func TestWindowsMenuPagePrefillsActiveProfile(t *testing.T) {
	p := WindowsProfile{ID: 17, WindowsProfileSource: WindowsProfileSource{Title: "Windows 11 x64", Folder: "Windows"}, State: "ready", InstallFile: "install.wim"}
	form := url.Values{"title": {"PXE"}, "label": {"Diagnostics"}, "message": {"Ready"}, "winiso_17": {"on"}, "folder_winiso_17": {"Windows/Installers"}}
	script, err := BuildMenuFromFormWithProfiles(form, "http://10.30.0.10:8080", []WindowsProfile{p})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Store: windowsMenuPageStore{menuPageStore: menuPageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}, versions: []MenuVersion{{Active: true, Script: string(script), Title: "PXE", Label: "Diagnostics", Message: "Ready"}}}, profiles: []WindowsProfile{p}}}
	// The selected item shows its folder path in the properties column.
	req := httptest.NewRequest(http.MethodGet, "/menu?item=winiso_17", nil)
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "winiso_17") || !strings.Contains(res.Body.String(), `value="Windows/Installers"`) {
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

func TestWindowsProfileMenuUsesPlainInitrdNamesForBIOS(t *testing.T) {
	profile := WindowsProfile{ID: 17, WindowsProfileSource: WindowsProfileSource{Title: "Windows 10 x64", Folder: "Windows"}, State: "ready", InstallFile: "install.wim"}
	form := url.Values{"title": {"PXE Platform"}, "label": {"Diagnostics"}, "message": {"Ready"}, "winiso_17": {"on"}}
	script, err := BuildMenuFromFormWithProfiles(form, "http://10.30.0.10:8080", []WindowsProfile{profile})
	if err != nil {
		t.Fatal(err)
	}
	got := string(script)
	for _, want := range []string{
		"iseq ${platform} efi && goto winiso_17_efi_initrd || goto winiso_17_bios_initrd\n",
		":winiso_17_bios_initrd\n" +
			"initrd http://10.30.0.10:8080/assets/windows-profiles/17/winpeshl.ini winpeshl.ini\n" +
			"initrd http://10.30.0.10:8080/assets/windows-profiles/17/pxe-setup.cmd pxe-setup.cmd\n" +
			"goto winiso_17_initrd_done\n",
		":winiso_17_efi_initrd\n" +
			"initrd -n winpeshl.ini http://10.30.0.10:8080/assets/windows-profiles/17/winpeshl.ini\n" +
			"initrd -n pxe-setup.cmd http://10.30.0.10:8080/assets/windows-profiles/17/pxe-setup.cmd\n" +
			":winiso_17_initrd_done\n" +
			"initrd http://10.30.0.10:8080/assets/windows-profiles/17/boot.wim boot.wim\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in generated menu", want)
		}
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

func TestWindowsMenuPublishesManualAndTwoAnswersForOneISO(t *testing.T) {
	profile := WindowsProfile{ID: 17, WindowsProfileSource: WindowsProfileSource{Title: "Windows 11 x64", Folder: "Windows"}, State: "ready", InstallFile: "install.wim"}
	answers := []WindowsAnswerVariant{
		{ID: 31, ProfileID: 17, Title: "Windows 11 Lab A", Folder: "Windows/Lab", SHA256: strings.Repeat("a", 64), Size: 100, State: "ready"},
		{ID: 32, ProfileID: 17, Title: "Windows 11 Lab B", Folder: "Windows/Lab", SHA256: strings.Repeat("b", 64), Size: 100, State: "ready"},
	}
	form := url.Values{"title": {"PXE Platform"}, "label": {"Diagnostics"}, "message": {"Ready"}, "winiso_17": {"on"}, "winanswer_31": {"on"}, "winanswer_32": {"on"}, "folder_winanswer_32": {"Windows/Other"}}
	script, err := BuildMenuFromFormWithProfilesAndAnswers(form, "http://10.30.0.10:8080", []WindowsProfile{profile}, answers)
	if err != nil {
		t.Fatal(err)
	}
	got := string(script)
	for _, want := range []string{
		"item winiso_17 Windows 11 x64\n",
		"item winanswer_31 Windows 11 Lab A\n",
		"item winanswer_32 Windows 11 Lab B\n",
		"initrd -n pxe-answer.xml http://10.30.0.10:8080/assets/windows-answers/31/pxe-answer.xml\n",
		"initrd -n pxe-setup.cmd http://10.30.0.10:8080/assets/windows-answers/32/pxe-setup.cmd\n",
		"initrd http://10.30.0.10:8080/assets/windows-profiles/17/boot.wim boot.wim\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q", want)
		}
	}
	refs := menuWindowsAnswerRefs(got)
	if len(refs) != 2 || refs[31].ProfileID != 17 || refs[31].SHA256 != strings.Repeat("a", 64) || menuFolderPaths(got)["winanswer_32"] != "Windows/Other" || !menuWindowsProfileIDs(got)[17] {
		t.Fatalf("answer selection did not round trip: refs=%+v folders=%+v", refs, menuFolderPaths(got))
	}
	delete(form, "winiso_17")
	onlyAnswers, err := BuildMenuFromFormWithProfilesAndAnswers(form, "http://10.30.0.10:8080", []WindowsProfile{profile}, answers)
	if err != nil || len(menuWindowsProfileIDs(string(onlyAnswers))) != 0 || len(menuWindowsAnswerRefs(string(onlyAnswers))) != 2 {
		t.Fatalf("answer entries incorrectly selected manual ISO: %v %s", err, onlyAnswers)
	}
}

func TestWindowsMenuRejectsUnknownOrUnreadyAnswer(t *testing.T) {
	p := WindowsProfile{ID: 17, WindowsProfileSource: WindowsProfileSource{Title: "Windows 11", Folder: "Windows"}, State: "ready", InstallFile: "install.wim"}
	form := url.Values{"title": {"PXE"}, "label": {"Diagnostics"}, "message": {"Ready"}, "winanswer_31": {"on"}}
	if _, err := BuildMenuFromFormWithProfilesAndAnswers(form, "http://10.30.0.10:8080", []WindowsProfile{p}, nil); err == nil {
		t.Fatal("accepted unknown answer variant")
	}
	answer := WindowsAnswerVariant{ID: 31, ProfileID: 17, Title: "Lab", Folder: "Windows", SHA256: strings.Repeat("a", 64), Size: 100, State: "pending"}
	if _, err := BuildMenuFromFormWithProfilesAndAnswers(form, "http://10.30.0.10:8080", []WindowsProfile{p}, []WindowsAnswerVariant{answer}); err == nil {
		t.Fatal("accepted unready answer variant")
	}
	answer.State = "ready"
	form.Set("folder_winanswer_31", "Windows/Тест")
	if _, err := BuildMenuFromFormWithProfilesAndAnswers(form, "http://10.30.0.10:8080", []WindowsProfile{p}, []WindowsAnswerVariant{answer}); err == nil {
		t.Fatal("accepted answer folder that iPXE cannot display")
	}
}

func TestWindowsMenuPagePrefillsActiveAnswer(t *testing.T) {
	p := WindowsProfile{ID: 17, WindowsProfileSource: WindowsProfileSource{Title: "Windows 11", Folder: "Windows"}, State: "ready", InstallFile: "install.wim"}
	a := WindowsAnswerVariant{ID: 31, ProfileID: 17, Title: "Lab", Folder: "Windows", SHA256: strings.Repeat("a", 64), Size: 100, State: "ready"}
	form := url.Values{"title": {"PXE"}, "label": {"Diagnostics"}, "message": {"Ready"}, "winanswer_31": {"on"}, "folder_winanswer_31": {"Windows/Tests"}}
	script, err := BuildMenuFromFormWithProfilesAndAnswers(form, "http://10.30.0.10:8080", []WindowsProfile{p}, []WindowsAnswerVariant{a})
	if err != nil {
		t.Fatal(err)
	}
	store := windowsMenuPageStore{menuPageStore: menuPageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}, versions: []MenuVersion{{Active: true, Script: string(script), Title: "PXE", Label: "Diagnostics", Message: "Ready"}}}, profiles: []WindowsProfile{p}, answers: []WindowsAnswerVariant{a}}
	s := &Server{Store: store}
	req := httptest.NewRequest(http.MethodGet, "/menu?item=winanswer_31", nil)
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "winanswer_31") || !strings.Contains(res.Body.String(), `value="Windows/Tests"`) || strings.Contains(res.Body.String(), "winiso_17") {
		t.Fatalf("answer selection not restored: %d %s", res.Code, res.Body.String())
	}
}

func TestWindowsMenuRejectsAnswerItemMissingFromSnapshotMetadata(t *testing.T) {
	modified := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	source := WindowsProfileSource{StorageID: 7, StorageRoot: "/mnt/pxe-platform-smb/key7", Path: "windows.iso", Size: 123, Modified: modified, Title: "Windows 11", Folder: "Windows"}
	p := WindowsProfile{ID: 17, WindowsProfileSource: source, State: "ready", InstallFile: "install.wim"}
	answers := []WindowsAnswerVariant{
		{ID: 31, ProfileID: 17, Title: "Lab A", Folder: "Windows", SHA256: strings.Repeat("a", 64), Size: 100, State: "ready"},
		{ID: 32, ProfileID: 17, Title: "Lab B", Folder: "Windows", SHA256: strings.Repeat("b", 64), Size: 100, State: "ready"},
	}
	form := url.Values{"title": {"PXE"}, "label": {"Diagnostics"}, "message": {"Ready"}, "winanswer_31": {"on"}, "winanswer_32": {"on"}}
	script, err := BuildMenuFromFormWithProfilesAndAnswers(form, "http://10.30.0.10:8080", []WindowsProfile{p}, answers)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := json.Marshal([]menuWindowsAnswerRef{{ID: 31, ProfileID: 17, SHA256: strings.Repeat("a", 64)}})
	if err != nil {
		t.Fatal(err)
	}
	forged := string(script)
	for _, line := range strings.Split(forged, "\n") {
		if strings.HasPrefix(line, menuAnswerMarker) {
			forged = strings.Replace(forged, line, menuAnswerMarker+base64.RawURLEncoding.EncodeToString(refs), 1)
			break
		}
	}
	store := windowsMenuPageStore{profiles: []WindowsProfile{p}, answers: answers}
	api := &fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{{ID: 7, Kind: "smb", Root: source.StorageRoot, State: "ready"}}, Images: []images.Image{{StorageID: 7, Path: source.Path, Size: source.Size, Modified: source.Modified, Present: true}}}}
	s := &Server{Store: store, Images: api, WindowsISO: &recordingWindowsISOAPI{}, WindowsAnswers: &answerAgent{}}
	if err := s.verifyMenuWindowsProfiles(context.Background(), forged); err == nil {
		t.Fatal("accepted answer boot item without hash reference")
	}
}
