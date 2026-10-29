package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"pxe-platform/internal/drivers"
)

type newInstallStore struct {
	*draftStore
	packages []DriverPackage
	created  []DriverConfig
}

func (s *newInstallStore) DriverPackages(context.Context) ([]DriverPackage, error) {
	return s.packages, nil
}
func (s *newInstallStore) CreateDriverConfig(_ context.Context, c DriverConfig) (DriverConfig, error) {
	c.ID = 40 + int64(len(s.created))
	s.created = append(s.created, c)
	return c, nil
}

func newInstallServer(t *testing.T) (*Server, *newInstallStore) {
	t.Helper()
	base := newDraftStore(t, "operator", nil)
	base.profiles = append(base.profiles, WindowsProfile{ID: 1, WindowsProfileSource: WindowsProfileSource{Title: "Windows 10 x64"}, State: "ready", InstallFile: "install.wim"})
	store := &newInstallStore{draftStore: base, packages: []DriverPackage{{SHA256: strings.Repeat("a", 64), Inventory: drivers.Inventory{INFs: []drivers.INF{
		{Path: "NetKVM/netkvm.inf", Architecture: "amd64"}, {Path: "vioscsi/vioscsi.inf", Architecture: "amd64"}, {Path: "x86/old.inf", Architecture: "x86"},
	}}}}}
	s, _ := draftServer(t, store.draftStore)
	s.Store = store
	return s, store
}

func lastDraftKey(store *newInstallStore) string {
	if store.draft == nil || len(store.draft.Items) == 0 {
		return ""
	}
	return store.draft.Items[len(store.draft.Items)-1].Key
}

func TestNewInstallationImageOnly(t *testing.T) {
	s, store := newInstallServer(t)
	res := menuPost(t, s, "/installations", url.Values{"profile_id": {"1"}, "title": {"Windows 10 x64"}})
	if res.Code != http.StatusSeeOther || lastDraftKey(store) != "winiso_1" {
		t.Fatalf("status=%d draft=%+v body=%s", res.Code, store.draft, res.Body.String())
	}
}

func TestNewInstallationWithAnswer(t *testing.T) {
	s, store := newInstallServer(t)
	res := menuPost(t, s, "/installations", url.Values{"profile_id": {"17"}, "answer_id": {"31"}, "title": {"Win11 Lab"}})
	if res.Code != http.StatusSeeOther || lastDraftKey(store) != "winanswer_31" || store.draft.Items[0].Title != "Win11 Lab" {
		t.Fatalf("status=%d draft=%+v body=%s", res.Code, store.draft, res.Body.String())
	}
}

func TestNewInstallationRejectsForeignAnswer(t *testing.T) {
	s, store := newInstallServer(t)
	res := menuPost(t, s, "/installations", url.Values{"profile_id": {"1"}, "answer_id": {"31"}, "title": {"Mixed"}})
	if res.Code < 400 || !strings.Contains(res.Body.String(), "другому образу") || store.draft != nil {
		t.Fatalf("status=%d draft=%+v body=%s", res.Code, store.draft, res.Body.String())
	}
}

func TestNewInstallationWithPackageCreatesConfig(t *testing.T) {
	s, store := newInstallServer(t)
	res := menuPost(t, s, "/installations", url.Values{"profile_id": {"17"}, "package": {strings.Repeat("a", 64)}, "title": {"Win11 VirtIO"}})
	if res.Code != http.StatusSeeOther || len(store.created) != 1 || len(store.created[0].PackageINFs) != 2 || lastDraftKey(store) != "windriver_40" {
		t.Fatalf("status=%d created=%+v draft=%+v body=%s", res.Code, store.created, store.draft, res.Body.String())
	}
}

func TestNewInstallationDuplicateIsRejected(t *testing.T) {
	s, store := newInstallServer(t)
	store.draft = &MenuDraft{Title: "oh my boot_", Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{Key: "winiso_1"}}}
	saved := len(store.saved)
	res := menuPost(t, s, "/installations", url.Values{"profile_id": {"1"}, "title": {"Again"}})
	if res.Code < 400 || !strings.Contains(res.Body.String(), "Такая установка уже в меню") || len(store.saved) != saved {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestNewInstallationFormListsChoices(t *testing.T) {
	s, _ := newInstallServer(t)
	body := libraryGet(t, s, "/installations/new")
	for _, want := range []string{`name="profile_id"`, `value="17"`, `data-profile="17"`, "netkvm", `name="package"`, `/assets/installation-form.js`} {
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
