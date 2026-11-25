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

	"omb/internal/images"
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
	form := url.Values{"title": {"PXE"}, "label": {"Diagnostics"}, "message": {"Ready"}, "install_1017": {"on"}, "folder_install_1017": {"Windows/Installers"}}
	script, err := buildTestMenuWithProfiles(form, "http://10.30.0.10:8080", []WindowsProfile{p})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Store: windowsMenuPageStore{menuPageStore: menuPageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}, versions: []MenuVersion{{Active: true, Script: string(script), Title: "PXE", Label: "Diagnostics", Message: "Ready", Items: []MenuVersionItem{{InstallationID: 1017, Title: p.Title, Folder: "Windows/Installers"}}}}}, profiles: []WindowsProfile{p}}}
	// The selected item shows its folder path in the properties column.
	req := httptest.NewRequest(http.MethodGet, "/menu?item=install_1017", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "install_1017") || !strings.Contains(res.Body.String(), `value="Windows/Installers"`) {
		t.Fatalf("profile selection missing: %d %s", res.Code, res.Body.String())
	}
}

func TestWindowsMenuPublishesSeparateProfilesAndFolders(t *testing.T) {
	profiles := []WindowsProfile{
		{ID: 17, WindowsProfileSource: WindowsProfileSource{Title: "Windows 10 x64", Folder: "Windows"}, State: "ready", InstallFile: "install.wim"},
		{ID: 18, WindowsProfileSource: WindowsProfileSource{Title: "Windows 11 x64", Folder: "Windows"}, State: "ready", InstallFile: "install.esd"},
	}
	form := url.Values{"title": {"oh my boot_"}, "label": {"Diagnostics"}, "message": {"Ready"}, "install_1017": {"on"}, "install_1018": {"on"}, "folder_install_1017": {"Windows"}, "folder_install_1018": {"Windows"}}
	script, err := buildTestMenuWithProfiles(form, "http://10.30.0.10:8080", profiles)
	if err != nil {
		t.Fatal(err)
	}
	got := string(script)
	for _, want := range []string{
		"menu oh my boot_ / Windows" + menuTitleSuffix + "\nitem menu_root .. Back\nitem --gap\nitem install_1017 Windows 10 x64\nitem install_1018 Windows 11 x64\n",
		":install_1017\nimgfetch --name omb-choice http://10.30.0.10:8080/boot/choice?id=1017 && imgfree omb-choice || echo -n\nkernel http://10.30.0.10:8080/assets/windows/wimboot || goto install_1017_failed\n",
		"initrd -n omb-setup.cmd http://10.30.0.10:8080/assets/windows-profiles/17/omb-setup.cmd || goto install_1017_failed\n",
		"initrd http://10.30.0.10:8080/assets/windows-profiles/18/boot.wim boot.wim || goto install_1018_failed\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q", want)
		}
	}
	selected := menuWindowsProfileIDs(got)
	if len(selected) != 2 || !selected[17] || !selected[18] || menuFolderPaths(got)["install_1018"] != "Windows" {
		t.Fatalf("selection did not round trip: %#v", selected)
	}
}

func TestWindowsProfileMenuUsesPlainInitrdNamesForBIOS(t *testing.T) {
	profile := WindowsProfile{ID: 17, WindowsProfileSource: WindowsProfileSource{Title: "Windows 10 x64", Folder: "Windows"}, State: "ready", InstallFile: "install.wim"}
	form := url.Values{"title": {"oh my boot_"}, "label": {"Diagnostics"}, "message": {"Ready"}, "install_1017": {"on"}}
	script, err := buildTestMenuWithProfiles(form, "http://10.30.0.10:8080", []WindowsProfile{profile})
	if err != nil {
		t.Fatal(err)
	}
	got := string(script)
	for _, want := range []string{
		"iseq ${platform} efi && goto install_1017_efi_initrd || goto install_1017_bios_initrd\n",
		":install_1017_bios_initrd\n" +
			"initrd http://10.30.0.10:8080/assets/windows-profiles/17/winpeshl.ini winpeshl.ini || goto install_1017_failed\n" +
			"initrd http://10.30.0.10:8080/assets/windows-profiles/17/omb-setup.cmd omb-setup.cmd || goto install_1017_failed\n" +
			"goto install_1017_initrd_done\n",
		":install_1017_efi_initrd\n" +
			"initrd -n winpeshl.ini http://10.30.0.10:8080/assets/windows-profiles/17/winpeshl.ini || goto install_1017_failed\n" +
			"initrd -n omb-setup.cmd http://10.30.0.10:8080/assets/windows-profiles/17/omb-setup.cmd || goto install_1017_failed\n" +
			":install_1017_initrd_done\n" +
			"initrd http://10.30.0.10:8080/assets/windows-profiles/17/boot.wim boot.wim || goto install_1017_failed\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in generated menu", want)
		}
	}
}

func TestWindowsMenuChecksCurrentSourceBeforePublication(t *testing.T) {
	modified := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	source := WindowsProfileSource{StorageID: 7, StorageRoot: "/mnt/omb-smb/key7", Path: "windows.iso", Size: 123, Modified: modified, Title: "Windows 11 x64", Folder: "Windows"}
	store := &recordingWindowsProfileStore{profile: WindowsProfile{ID: 17, WindowsProfileSource: source, State: "ready", InstallFile: "install.wim"}}
	api := &fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{{ID: 7, Kind: "smb", Root: source.StorageRoot, State: "ready"}}, Images: []images.Image{{StorageID: 7, Path: source.Path, Size: source.Size, Modified: modified, Present: true}}}}
	check := &recordingWindowsISOAPI{}
	s := &Server{Store: store, Images: api, WindowsISO: check}
	form := url.Values{"title": {"PXE"}, "label": {"Diagnostics"}, "message": {"Ready"}, "install_1017": {"on"}}
	script, err := buildTestMenuWithProfiles(form, "http://10.30.0.10:8080", []WindowsProfile{store.profile})
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
	form := url.Values{"title": {"PXE"}, "label": {"Diagnostics"}, "message": {"Ready"}, "install_1017": {"on"}}
	for _, profiles := range [][]WindowsProfile{nil, {{ID: 17, State: "error", InstallFile: "install.wim", WindowsProfileSource: WindowsProfileSource{Title: "Windows 10"}}}, {{ID: 17, State: "ready", WindowsProfileSource: WindowsProfileSource{Title: "Windows 10"}}}} {
		if _, err := buildTestMenuWithProfiles(form, "http://10.30.0.10:8080", profiles); err == nil {
			t.Fatalf("accepted unready profile: %+v", profiles)
		}
	}
}

func TestWindowsMenuPublishesManualAndTwoAnswersForOneISO(t *testing.T) {
	profile := WindowsProfile{ID: 17, WindowsProfileSource: WindowsProfileSource{Title: "Windows 11 x64", Folder: "Windows"}, State: "ready", InstallFile: "install.wim"}
	answers := []WindowsAnswerVariant{
		{ID: 31, Title: "Windows 11 Lab A", Folder: "Windows/Lab", SHA256: strings.Repeat("a", 64), Size: 100, State: "ready"},
		{ID: 32, Title: "Windows 11 Lab B", Folder: "Windows/Lab", SHA256: strings.Repeat("b", 64), Size: 100, State: "ready"},
	}
	form := url.Values{"title": {"oh my boot_"}, "label": {"Diagnostics"}, "message": {"Ready"}, "install_1017": {"on"}, "install_2031": {"on"}, "install_2032": {"on"}, "folder_install_2032": {"Windows/Other"}}
	script, err := buildTestMenuWithAnswers(form, "http://10.30.0.10:8080", []WindowsProfile{profile}, answers)
	if err != nil {
		t.Fatal(err)
	}
	got := string(script)
	for _, want := range []string{
		"item install_1017 Windows 11 x64\n",
		"item install_2031 Windows 11 Lab A${sp",
		"item install_2032 Windows 11 Lab B${sp",
		"initrd -n omb-answer.xml http://10.30.0.10:8080/assets/windows-answers/31/omb-answer.xml || goto install_2031_failed\n",
		"initrd -n omb-setup.cmd http://10.30.0.10:8080/assets/windows-profiles/17/omb-answer-setup.cmd || goto install_2031_failed\n",
		"initrd http://10.30.0.10:8080/assets/windows-profiles/17/boot.wim boot.wim || goto install_1017_failed\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q", want)
		}
	}
	refs := menuWindowsAnswerRefs(got)
	if len(refs) != 2 || refs[0].ID != 31 || refs[0].ProfileID != 17 || refs[0].SHA256 != strings.Repeat("a", 64) || menuFolderPaths(got)["install_2032"] != "Windows/Other" || !menuWindowsProfileIDs(got)[17] {
		t.Fatalf("answer selection did not round trip: refs=%+v folders=%+v", refs, menuFolderPaths(got))
	}
	delete(form, "install_1017")
	onlyAnswers, err := buildTestMenuWithAnswers(form, "http://10.30.0.10:8080", []WindowsProfile{profile}, answers)
	if err != nil || len(menuWindowsProfileIDs(string(onlyAnswers))) != 0 || len(menuWindowsAnswerRefs(string(onlyAnswers))) != 2 {
		t.Fatalf("answer entries incorrectly selected manual ISO: %v %s", err, onlyAnswers)
	}
}

func TestWindowsMenuSharesOneAnswerAcrossTwoProfiles(t *testing.T) {
	modified := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	p17 := WindowsProfile{ID: 17, WindowsProfileSource: WindowsProfileSource{StorageID: 7, StorageRoot: "/mnt/omb-smb/key7", Path: "win11-ru.iso", Size: 123, Modified: modified, Title: "Win 11 RU", Folder: "Windows"}, State: "ready", InstallFile: "install.wim"}
	p18 := WindowsProfile{ID: 18, WindowsProfileSource: WindowsProfileSource{StorageID: 7, StorageRoot: "/mnt/omb-smb/key7", Path: "win11-en.iso", Size: 456, Modified: modified, Title: "Win 11 EN", Folder: "Windows"}, State: "ready", InstallFile: "install.wim"}
	a := WindowsAnswerVariant{ID: 31, Title: "Lab Shared", Folder: "Windows", SHA256: strings.Repeat("a", 64), Size: 100, State: "ready"}
	sources := InstallationMenuSources{
		BootProfiles: []BootProfile{
			{ID: 101, Kind: BootProfileWindowsISO, Title: p17.Title, WindowsProfileID: 17},
			{ID: 102, Kind: BootProfileWindowsISO, Title: p18.Title, WindowsProfileID: 18},
		},
		Installations: []Installation{
			{ID: 201, BootProfileID: 101, Title: "Win 11 RU Auto", Folder: "Windows", AnswerID: 31},
			{ID: 202, BootProfileID: 102, Title: "Win 11 EN Auto", Folder: "Windows", AnswerID: 31},
		},
	}
	draft := MenuDraft{Title: "PXE", Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{InstallationID: 201}, {InstallationID: 202}}}
	script, err := buildTestMenuFromDraft(draft, "http://10.30.0.10:8080", []WindowsProfile{p17, p18}, []WindowsAnswerVariant{a}, nil, sources)
	if err != nil {
		t.Fatal(err)
	}
	got := string(script)
	for _, want := range []string{
		"http://10.30.0.10:8080/assets/windows-profiles/17/omb-answer-setup.cmd",
		"http://10.30.0.10:8080/assets/windows-profiles/18/omb-answer-setup.cmd",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	refs := menuWindowsAnswerRefs(got)
	if len(refs) != 2 {
		t.Fatalf("refs=%+v, want 2 profile references for shared answer", refs)
	}
	store := windowsMenuPageStore{profiles: []WindowsProfile{p17, p18}, answers: []WindowsAnswerVariant{a}}
	api := &fakeImagesAPI{catalog: images.Catalog{
		Storages: []images.Storage{{ID: 7, Kind: "smb", Root: "/mnt/omb-smb/key7", State: "ready"}},
		Images: []images.Image{
			{StorageID: 7, Path: "win11-ru.iso", Size: 123, Modified: modified, Present: true},
			{StorageID: 7, Path: "win11-en.iso", Size: 456, Modified: modified, Present: true},
		},
	}}
	s := &Server{Store: store, Images: api, WindowsISO: &recordingWindowsISOAPI{}, WindowsAnswers: &answerAgent{}}
	if err := s.verifyMenuWindowsProfiles(context.Background(), got, menuProfile{windowsISOID: 17, answerID: 31}, menuProfile{windowsISOID: 18, answerID: 31}); err != nil {
		t.Fatalf("verify failed: %v", err)
	}
}

func TestWindowsMenuRejectsUnknownOrUnreadyAnswer(t *testing.T) {
	p := WindowsProfile{ID: 17, WindowsProfileSource: WindowsProfileSource{Title: "Windows 11", Folder: "Windows"}, State: "ready", InstallFile: "install.wim"}
	form := url.Values{"title": {"PXE"}, "label": {"Diagnostics"}, "message": {"Ready"}, "install_2031": {"on"}}
	if _, err := buildTestMenuWithAnswers(form, "http://10.30.0.10:8080", []WindowsProfile{p}, nil); err == nil {
		t.Fatal("accepted unknown answer variant")
	}
	answer := WindowsAnswerVariant{ID: 31, Title: "Lab", Folder: "Windows", SHA256: strings.Repeat("a", 64), Size: 100, State: "pending"}
	if _, err := buildTestMenuWithAnswers(form, "http://10.30.0.10:8080", []WindowsProfile{p}, []WindowsAnswerVariant{answer}); err == nil {
		t.Fatal("accepted unready answer variant")
	}
	answer.State = "ready"
	form.Set("folder_install_2031", "Windows/Тест")
	if _, err := buildTestMenuWithAnswers(form, "http://10.30.0.10:8080", []WindowsProfile{p}, []WindowsAnswerVariant{answer}); err == nil {
		t.Fatal("accepted answer folder that iPXE cannot display")
	}
}

func TestWindowsMenuPagePrefillsActiveAnswer(t *testing.T) {
	p := WindowsProfile{ID: 17, WindowsProfileSource: WindowsProfileSource{Title: "Windows 11", Folder: "Windows"}, State: "ready", InstallFile: "install.wim"}
	a := WindowsAnswerVariant{ID: 31, Title: "Lab", Folder: "Windows", SHA256: strings.Repeat("a", 64), Size: 100, State: "ready"}
	form := url.Values{"title": {"PXE"}, "label": {"Diagnostics"}, "message": {"Ready"}, "install_2031": {"on"}, "folder_install_2031": {"Windows/Tests"}}
	script, err := buildTestMenuWithAnswers(form, "http://10.30.0.10:8080", []WindowsProfile{p}, []WindowsAnswerVariant{a})
	if err != nil {
		t.Fatal(err)
	}
	store := windowsMenuPageStore{menuPageStore: menuPageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}, versions: []MenuVersion{{Active: true, Script: string(script), Title: "PXE", Label: "Diagnostics", Message: "Ready", Items: []MenuVersionItem{{InstallationID: 2031, Title: a.Title, Folder: "Windows/Tests"}}}}}, profiles: []WindowsProfile{p}, answers: []WindowsAnswerVariant{a}}
	s := &Server{Store: store}
	req := httptest.NewRequest(http.MethodGet, "/menu?item=install_2031", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "install_2031") || !strings.Contains(res.Body.String(), `value="Windows/Tests"`) || strings.Contains(res.Body.String(), "install_1017") {
		t.Fatalf("answer selection not restored: %d %s", res.Code, res.Body.String())
	}
}

func TestWindowsMenuRejectsAnswerItemMissingFromSnapshotMetadata(t *testing.T) {
	modified := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	source := WindowsProfileSource{StorageID: 7, StorageRoot: "/mnt/omb-smb/key7", Path: "windows.iso", Size: 123, Modified: modified, Title: "Windows 11", Folder: "Windows"}
	p := WindowsProfile{ID: 17, WindowsProfileSource: source, State: "ready", InstallFile: "install.wim"}
	answers := []WindowsAnswerVariant{
		{ID: 31, Title: "Lab A", Folder: "Windows", SHA256: strings.Repeat("a", 64), Size: 100, State: "ready"},
		{ID: 32, Title: "Lab B", Folder: "Windows", SHA256: strings.Repeat("b", 64), Size: 100, State: "ready"},
	}
	form := url.Values{"title": {"PXE"}, "label": {"Diagnostics"}, "message": {"Ready"}, "install_2031": {"on"}, "install_2032": {"on"}}
	script, err := buildTestMenuWithAnswers(form, "http://10.30.0.10:8080", []WindowsProfile{p}, answers)
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

func (s windowsMenuPageStore) Installations(context.Context) ([]Installation, error) {
	return testInstallationSources(s.profiles, s.answers, nil).Installations, nil
}
func (s windowsMenuPageStore) BootProfiles(context.Context) ([]BootProfile, error) {
	return testInstallationSources(s.profiles, s.answers, nil).BootProfiles, nil
}
