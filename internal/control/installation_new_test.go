package control

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"omb/internal/agent"
	"omb/internal/drivers"
	"omb/internal/images"
)

type newInstallStore struct {
	*draftStore
	packages      []DriverPackage
	created       []DriverConfig
	bootProfiles  []BootProfile
	installations []Installation
}

func (s *newInstallStore) DriverPackages(context.Context) ([]DriverPackage, error) {
	return s.packages, nil
}
func (s *newInstallStore) CreateDriverConfig(_ context.Context, c DriverConfig) (DriverConfig, error) {
	c.ID = 40 + int64(len(s.created))
	s.created = append(s.created, c)
	s.configs = append(s.configs, c)
	return c, nil
}

func (s *newInstallStore) BootProfiles(context.Context) ([]BootProfile, error) {
	return s.bootProfiles, nil
}
func (s *newInstallStore) Installations(context.Context) ([]Installation, error) {
	return s.installations, nil
}
func (s *newInstallStore) CreateInstallation(_ context.Context, item Installation) (Installation, error) {
	item.ID = 100 + int64(len(s.installations))
	s.installations = append(s.installations, item)
	return item, nil
}

func newInstallServer(t *testing.T) (*Server, *newInstallStore) {
	t.Helper()
	base := newDraftStore(t, "operator", nil)
	base.profiles = append(base.profiles, WindowsProfile{ID: 1, WindowsProfileSource: WindowsProfileSource{StorageID: 7, StorageRoot: "/mnt/omb-smb/key7", Path: "win10.iso", Size: 12, Modified: base.profiles[0].Modified, Title: "Windows 10 x64"}, State: "ready", InstallFile: "install.wim"})
	store := &newInstallStore{draftStore: base, packages: []DriverPackage{{SHA256: strings.Repeat("a", 64), Inventory: drivers.Inventory{INFs: []drivers.INF{
		{Path: "NetKVM/netkvm.inf", Architecture: "amd64"}, {Path: "vioscsi/vioscsi.inf", Architecture: "amd64"}, {Path: "x86/old.inf", Architecture: "x86"},
	}}}}}
	s, _ := draftServer(t, store.draftStore)
	store.bootProfiles = []BootProfile{{ID: 50, Kind: BootProfileWindowsISO, WindowsProfileID: 1, Title: "Windows 10 x64"}, {ID: 51, Kind: BootProfileWindowsISO, WindowsProfileID: 17, Title: "Windows 11"}, {ID: 52, Kind: BootProfileUbuntuBuiltin, Title: "Ubuntu Desktop"}}
	s.Store = store
	for _, p := range store.profiles {
		s.Images.(*fakeImagesAPI).catalog.Images = append(s.Images.(*fakeImagesAPI).catalog.Images, images.Image{StorageID: p.StorageID, Path: p.Path, Size: p.Size, Modified: p.Modified, Present: true})
	}
	s.UbuntuReadiness = HTTPUbuntuReadiness{BaseURL: ubuntuBootResources(t, "")}
	return s, store
}

func lastDraftKey(store *newInstallStore) string {
	if store.draft == nil || len(store.draft.Items) == 0 {
		return ""
	}
	return menuDraftItemKey(store.draft.Items[len(store.draft.Items)-1])
}

func TestNewInstallationImageOnly(t *testing.T) {
	s, store := newInstallServer(t)
	res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"50"}, "title": {"Windows 10 x64"}})
	if res.Code != http.StatusSeeOther || len(store.installations) != 1 || lastDraftKey(store) != "" {
		t.Fatalf("status=%d draft=%+v body=%s", res.Code, store.draft, res.Body.String())
	}
}

func TestUbuntuISOChoicesShowVerifiedFileAndHideUnverified(t *testing.T) {
	s, _ := newInstallServer(t)
	profiles := []BootProfile{
		{ID: 61, Kind: BootProfileUbuntuISO, Title: "Ubuntu 24", UbuntuSourceID: 3, UbuntuStorageID: 7, UbuntuPath: "linux/ubuntu.iso", UbuntuSHA256: strings.Repeat("a", 64), UbuntuState: "verified"},
		{ID: 62, Kind: BootProfileUbuntuISO, Title: "Unchecked", UbuntuSourceID: 4, UbuntuStorageID: 7, UbuntuPath: "unchecked.iso", UbuntuSHA256: strings.Repeat("b", 64), UbuntuState: "unverified"},
	}
	choices := s.bootProfileChoices(context.Background(), profiles, nil)
	if len(choices) != 2 || !choices[0].Selectable() || choices[0].Path != "linux/ubuntu.iso" || choices[1].Selectable() {
		t.Fatalf("choices=%+v", choices)
	}
}

func TestNewInstallationWithAnswer(t *testing.T) {
	s, store := newInstallServer(t)
	res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"51"}, "answer_id": {"31"}, "title": {"Win11 Lab"}})
	if res.Code != http.StatusSeeOther || len(store.installations) != 1 || store.installations[0].Title != "Win11 Lab" || lastDraftKey(store) != "" {
		t.Fatalf("status=%d draft=%+v body=%s", res.Code, store.draft, res.Body.String())
	}
}

func TestNewInstallationRejectsForeignAnswer(t *testing.T) {
	s, store := newInstallServer(t)
	res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"50"}, "answer_id": {"31"}, "title": {"Mixed"}})
	if res.Code < 400 || !strings.Contains(res.Body.String(), "другому образу") || store.draft != nil {
		t.Fatalf("status=%d draft=%+v body=%s", res.Code, store.draft, res.Body.String())
	}
}

func TestNewInstallationWithPackageCreatesConfig(t *testing.T) {
	s, store := newInstallServer(t)
	res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"51"}, "package": {strings.Repeat("a", 64)}, "title": {"Win11 VirtIO"}})
	if res.Code != http.StatusSeeOther || len(store.created) != 1 || len(store.created[0].PackageINFs) != 2 || len(store.installations) != 1 || lastDraftKey(store) != "" {
		t.Fatalf("status=%d created=%+v draft=%+v body=%s", res.Code, store.created, store.draft, res.Body.String())
	}
}

func TestNewInstallationAllowsTwoUbuntuChoicesWithoutPublishing(t *testing.T) {
	s, store := newInstallServer(t)
	boot, published := fakeBoot(t)
	s.Boot = boot
	for _, title := range []string{"Ubuntu Lab", "Ubuntu Recovery"} {
		res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"52"}, "title": {title}, "folder": {"Linux/" + strings.TrimPrefix(title, "Ubuntu ")}})
		if res.Code != http.StatusSeeOther {
			t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
		}
	}
	if len(store.installations) != 2 || store.installations[0].BootProfileID != 52 || store.installations[1].BootProfileID != 52 || lastDraftKey(store) != "" || len(*published) != 0 {
		t.Fatalf("installations=%+v draft=%+v published=%v", store.installations, store.draft, *published)
	}
}

func TestNewInstallationWindowsXMLPersistsComponentsWithoutPublishing(t *testing.T) {
	s, store := newInstallServer(t)
	boot, published := fakeBoot(t)
	s.Boot = boot
	for _, title := range []string{"Windows Lab", "Windows Recovery"} {
		res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"51"}, "answer_id": {"31"}, "title": {title}})
		if res.Code != http.StatusSeeOther {
			t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
		}
	}
	if len(store.installations) != 2 || store.installations[0].AnswerID != 31 || store.installations[1].AnswerID != 31 || lastDraftKey(store) != "" || len(*published) != 0 {
		t.Fatalf("installations=%+v draft=%+v published=%v", store.installations, store.draft, *published)
	}
}

func TestNewInstallationReusesDriverSelection(t *testing.T) {
	s, store := newInstallServer(t)
	for _, title := range []string{"Windows Lab", "Windows Recovery"} {
		res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"51"}, "inf": {strings.Repeat("a", 64) + ":NetKVM/netkvm.inf"}, "title": {title}})
		if res.Code != http.StatusSeeOther {
			t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
		}
		store.configs = append(store.configs, store.created...)
	}
	if len(store.created) != 1 || len(store.installations) != 2 || store.installations[0].DriverConfigID != 40 || store.installations[1].DriverConfigID != 40 {
		t.Fatalf("configs=%+v installations=%+v", store.created, store.installations)
	}
}

func TestNewInstallationUbuntuRejectsWindowsComponents(t *testing.T) {
	for _, field := range []string{"answer_id", "package", "inf"} {
		t.Run(field, func(t *testing.T) {
			s, store := newInstallServer(t)
			res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"52"}, field: {"31"}, "title": {"Ubuntu"}})
			if res.Code != http.StatusBadRequest || len(store.installations) != 0 {
				t.Fatalf("status=%d installations=%+v", res.Code, store.installations)
			}
		})
	}
}

func TestNewInstallationFormListsChoices(t *testing.T) {
	s, _ := newInstallServer(t)
	body := libraryGet(t, s, "/installations/new")
	for _, want := range []string{`name="boot_profile_id"`, `value="51"`, `data-profile="17"`, "netkvm", `name="package"`, `/assets/installation-form.js`} {
		if !strings.Contains(body, want) {
			t.Fatalf("form lacks %q: %s", want, body)
		}
	}
}

func TestDriversPageHasNoConfigurationForm(t *testing.T) {
	res := renderPage("drivers", samplePageData(langRU))
	if strings.Contains(res, `action="/drivers/configs"`) {
		t.Fatal("drivers page still creates configurations")
	}
}

func renderPage(name string, data pageData) string {
	res := httptest.NewRecorder()
	render(res, name, data)
	return res.Body.String()
}

func TestUbuntuInstallationFormHidesWindowsOptions(t *testing.T) {
	s, _ := newInstallServer(t)
	body := libraryGet(t, s, "/installations/new?boot_profile_id=52")
	for _, want := range []string{`value="52"`, `data-kind="ubuntu_builtin"`, `id="windows-drivers" hidden disabled`, `id="windows-answers" hidden disabled`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s: %s", want, body)
		}
	}
}

func TestNewInstallationDoesNotReuseUnbootableDriverConfig(t *testing.T) {
	s, store := newInstallServer(t)
	store.configs = []DriverConfig{{ID: 9, ProfileID: 17, State: "ready", PackageINFs: []agent.SelectedINF{{PackageSHA256: strings.Repeat("a", 64), INFPath: "NetKVM/netkvm.inf"}}}}
	res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"51"}, "title": {"New drivers"}, "inf": {strings.Repeat("a", 64) + ":NetKVM/netkvm.inf"}})
	if res.Code != http.StatusSeeOther || len(store.created) != 1 || len(store.installations) != 1 || store.installations[0].DriverConfigID != 40 {
		t.Fatalf("status=%d configs=%+v installations=%+v", res.Code, store.created, store.installations)
	}
}

func TestNewInstallationRejectsUnavailableOrInvalidSelection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		form   url.Values
		mutate func(*newInstallStore)
	}{
		{name: "missing profile", form: url.Values{"boot_profile_id": {"999"}, "title": {"Lab"}}},
		{name: "unavailable Windows", form: url.Values{"boot_profile_id": {"51"}, "title": {"Lab"}}, mutate: func(s *newInstallStore) {
			s.profiles[0].State = "unavailable"
			s.profiles[0].Error = "Исходный ISO изменился или недоступен"
		}},
		{name: "non ASCII title", form: url.Values{"boot_profile_id": {"52"}, "title": {"Лаборатория"}}},
		{name: "non ASCII folder", form: url.Values{"boot_profile_id": {"52"}, "title": {"Lab"}, "folder": {"Лаборатория"}}},
		{name: "unsafe folder", form: url.Values{"boot_profile_id": {"52"}, "title": {"Lab"}, "folder": {"../Lab"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, store := newInstallServer(t)
			if tc.mutate != nil {
				tc.mutate(store)
			}
			res := menuPost(t, s, "/installations", tc.form)
			if res.Code != http.StatusBadRequest || len(store.installations) != 0 || store.draft != nil {
				t.Fatalf("status=%d installations=%+v draft=%+v", res.Code, store.installations, store.draft)
			}
		})
	}
}

func TestNewInstallationRequiresRoleAndCSRF(t *testing.T) {
	for _, tc := range []struct {
		name, role  string
		invalidCSRF bool
	}{{"observer", "observer", false}, {"csrf", "operator", true}} {
		t.Run(tc.name, func(t *testing.T) {
			s, store := newInstallServer(t)
			store.user.Role = tc.role
			form := url.Values{"boot_profile_id": {"52"}, "title": {"Lab"}}
			req := accountRequest(http.MethodPost, "/installations", form)
			if tc.invalidCSRF {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.Body = io.NopCloser(strings.NewReader("boot_profile_id=52&title=Lab&csrf=invalid"))
			}
			res := httptest.NewRecorder()
			s.Handler().ServeHTTP(res, req)
			if res.Code != http.StatusForbidden || len(store.installations) != 0 {
				t.Fatalf("status=%d installations=%+v", res.Code, store.installations)
			}
		})
	}
}

func (s *newInstallStore) SetDriverConfigResult(_ context.Context, id int64, state string, result agent.DriverBuildResult, problem string) error {
	for i := range s.configs {
		if s.configs[i].ID == id {
			s.configs[i].State, s.configs[i].Build, s.configs[i].Error = state, result, problem
		}
	}
	return nil
}

func (s *newInstallStore) SetWindowsProfileResult(_ context.Context, id int64, state, install, problem string) error {
	for i := range s.profiles {
		if s.profiles[i].ID == id {
			s.profiles[i].State, s.profiles[i].InstallFile, s.profiles[i].Error = state, install, problem
		}
	}
	return nil
}
