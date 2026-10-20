package control

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"pxe-platform/internal/agent"
	"pxe-platform/internal/drivers"
	"pxe-platform/internal/images"
)

func TestImagesPageIsOneTableWithUsage(t *testing.T) {
	store := newDraftStore(t, "admin", []MenuDraftItem{{Key: "winiso_17", Title: "Windows 11"}, {Key: "winanswer_31", Title: "Lab"}})
	s, _ := draftServer(t, store)
	api := s.Images.(*fakeImagesAPI)
	api.catalog.Images = append(api.catalog.Images, images.Image{StorageID: store.profiles[0].StorageID, Path: "new.iso", Present: true})
	body := libraryGet(t, s, "/images")
	if strings.Count(body, "<table") != 1 || !strings.Contains(body, "new.iso") || !strings.Contains(body, "Не подготовлен") || !strings.Contains(body, "В меню: 2") || !strings.Contains(body, `href="/installations?q=Windows%2011"`) {
		t.Fatalf("images page: %s", body)
	}
}

func TestAnswersPageShowsUsage(t *testing.T) {
	store := newDraftStore(t, "admin", []MenuDraftItem{{Key: "winanswer_31", Title: "Lab"}, {Key: "windriver_2"}})
	s, _ := draftServer(t, store)
	body := libraryGet(t, s, "/answers")
	if !strings.Contains(body, "В меню: 2") {
		t.Fatalf("answers page: %s", body)
	}
}

func TestDeletedDraftObjectStaysVisibleOnInstallations(t *testing.T) {
	store := newDraftStore(t, "operator", nil)
	store.draft = &MenuDraft{Title: "PXE Platform", Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{Key: "winanswer_99", Title: "Gone"}}}
	s, _ := draftServer(t, store)
	body := libraryGet(t, s, "/installations")
	if !strings.Contains(body, "winanswer_99") || !strings.Contains(body, "Gone") {
		t.Fatalf("deleted object hidden: %s", body)
	}
}

func TestPublishButtonHasNoGuessedVersionNumber(t *testing.T) {
	data := samplePageData(langRU)
	data.Info.Draft = draftSummary{Active: 2, Add: 1}
	body := renderPage("menu", data)
	if strings.Contains(body, "Опубликовать версию") || !strings.Contains(body, "Опубликовать меню") {
		t.Fatalf("publish button: %s", body)
	}
}

func TestNewInstallationWithSameDriversIsRejected(t *testing.T) {
	s, store := newInstallServer(t)
	form := url.Values{"profile_id": {"17"}, "package": {strings.Repeat("a", 64)}, "title": {"Win11 VirtIO"}}
	if res := menuPost(t, s, "/installations", form); res.Code != http.StatusSeeOther {
		t.Fatalf("first: %d %s", res.Code, res.Body.String())
	}
	store.configs = append(store.configs, store.created[0])
	res := menuPost(t, s, "/installations", form)
	if res.Code < 400 || !strings.Contains(res.Body.String(), "уже есть") || len(store.created) != 1 {
		t.Fatalf("second: %d created=%d body=%s", res.Code, len(store.created), res.Body.String())
	}
}

func TestStylesheetMakesOnlyMarkedButtonsPrimary(t *testing.T) {
	css, _ := templateFS.ReadFile("templates/style.css")
	if strings.Contains(string(css), "form > button") || strings.Contains(string(css), "form>button") {
		t.Fatal("stylesheet styles buttons as primary by position")
	}
	for _, name := range []string{"login", "installation_new", "answers", "users", "storages", "images"} {
		data := samplePageData(langRU)
		if name == "login" {
			data.Info = requestContext{}
		}
		data.ImageError = ""
		if n := strings.Count(renderPage(name, data), `btn-primary`); n != 1 {
			t.Errorf("%s has %d primary buttons", name, n)
		}
	}
}

func TestSidebarCollapsesOnNarrowScreens(t *testing.T) {
	body := renderPage("installations", samplePageData(langRU))
	css, _ := templateFS.ReadFile("templates/style.css")
	if !strings.Contains(body, `id="nav-toggle"`) || !strings.Contains(string(css), ".nav-check:checked") {
		t.Fatal("no collapsible navigation")
	}
}

func TestPrepareReturnsToDriversPage(t *testing.T) {
	store := configFixture()
	store.configs = []DriverConfig{{ID: 1, ProfileID: 17, AnswerID: 4, Title: "Laptop A", Folder: "Windows", State: "pending", PackageINFs: []agent.SelectedINF{{PackageSHA256: strings.Repeat("a", 64), INFPath: "net.inf"}}}}
	s := &Server{Store: store, DriverConfigs: &fakeDriverBuilder{}}
	res := menuPost(t, s, "/drivers/configs/1/prepare", url.Values{"return": {"/drivers"}})
	if res.Code != http.StatusSeeOther || !strings.HasPrefix(res.Header().Get("Location"), "/drivers?message=") {
		t.Fatalf("status=%d location=%q body=%s", res.Code, res.Header().Get("Location"), res.Body.String())
	}
}

func TestObserverCannotPostToNewRoutes(t *testing.T) {
	store := newDraftStore(t, "observer", []MenuDraftItem{{Key: "winiso_17", Title: "Windows 11"}})
	s, _ := draftServer(t, store)
	for _, path := range []string{"/menu/settings", "/menu/items/winiso_17/move", "/menu/items/winiso_17/remove", "/menu/items/winiso_17/title", "/installations", "/installations/winiso_17/add", "/answers/3/inspect", "/answers/upload"} {
		res := menuPost(t, s, path, url.Values{})
		if res.Code != http.StatusForbidden {
			t.Errorf("%s: status %d", path, res.Code)
		}
	}
}

var _ = httptest.NewRecorder
var _ = drivers.Inventory{}

func TestCheckedAnswerCanBeCheckedAgain(t *testing.T) {
	data := samplePageData(langRU)
	body := renderPage("answers", data)
	if !strings.Contains(body, `action="/answers/5/inspect"`) {
		t.Fatalf("checked answer cannot be re-checked: %s", body)
	}
}
