package control

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"omb/internal/agent"
	"omb/internal/drivers"
	"omb/internal/images"
)

func TestImagesPageIsOneTableWithUsage(t *testing.T) {
	store := newDraftStore(t, "admin", []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11"}, {InstallationID: 2031, Title: "Lab"}})
	s, _ := draftServer(t, store)
	api := s.Images.(*fakeImagesAPI)
	api.catalog.Images = append(api.catalog.Images, images.Image{StorageID: store.profiles[0].StorageID, Path: "new.iso", Present: true})
	body := libraryGet(t, s, "/images")
	if strings.Count(body, "<table") != 1 || !strings.Contains(body, "new.iso") || !strings.Contains(body, "В библиотеке") || !strings.Contains(body, `href="/installations/1017/edit"`) || !strings.Contains(body, `href="/installations/2031/edit"`) {
		t.Fatalf("images page: %s", body)
	}
}

func TestAnswersPageShowsUsage(t *testing.T) {
	store := newDraftStore(t, "admin", []MenuDraftItem{{InstallationID: 2031, Title: "Lab"}, {InstallationID: 3002}})
	s, _ := draftServer(t, store)
	body := libraryGet(t, s, "/answers")
	if !strings.Contains(body, "В меню: 2") {
		t.Fatalf("answers page: %s", body)
	}
}

func TestDeletedDraftObjectStaysVisibleOnInstallations(t *testing.T) {
	store := newDraftStore(t, "operator", nil)
	store.draft = &MenuDraft{Title: "oh my boot_", Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{InstallationID: 2099, Title: "Gone"}}}
	s, _ := draftServer(t, store)
	body := libraryGet(t, s, "/installations")
	if !strings.Contains(body, "install_2099") || !strings.Contains(body, "Gone") {
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

func TestNewInstallationWithSameDriversCreatesIndependentChoice(t *testing.T) {
	s, store := newInstallServer(t)
	form := url.Values{"boot_profile_id": {"51"}, "package": {strings.Repeat("a", 64)}, "title": {"Win11 VirtIO"}}
	if res := menuPost(t, s, "/installations", form); res.Code != http.StatusSeeOther {
		t.Fatalf("first: %d %s", res.Code, res.Body.String())
	}
	store.configs = append(store.configs, store.created[0])
	res := menuPost(t, s, "/installations", form)
	if res.Code != http.StatusSeeOther || len(store.created) != 1 || len(store.installations) != 2 {
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
		body := regexp.MustCompile(`(?s)<dialog\b[^>]*>.*?</dialog>`).ReplaceAllString(renderPage(name, data), "")
		if n := strings.Count(body, `btn-primary`); n != 1 {
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
	store := newDraftStore(t, "observer", []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11"}})
	s, _ := draftServer(t, store)
	for _, path := range []string{"/menu/items/install_1017/move", "/menu/items/install_1017/remove", "/menu/items/install_1017/title", "/installations", "/installations/install_1017/add", "/answers/3/inspect", "/answers/upload"} {
		res := menuPost(t, s, path, url.Values{})
		if res.Code != http.StatusForbidden {
			t.Errorf("%s: status %d", path, res.Code)
		}
	}
}

var _ = httptest.NewRecorder
var _ = drivers.Inventory{}

func TestAnswersPageWipesDiskBadge(t *testing.T) {
	data := samplePageData(langRU)
	body := renderPage("answers", data)
	if !strings.Contains(body, `Ready <span class="chip-bad">стирает диск</span>`) {
		t.Fatalf("wipes-disk answer variant missing danger chip: %s", body)
	}
	if strings.Contains(body, `action="/answers/5/inspect"`) {
		t.Fatalf("answers page should not contain legacy inspect form: %s", body)
	}
	if strings.Contains(body, `<th>Диск</th>`) {
		t.Fatalf("answers page should not have a separate Disk column: %s", body)
	}
}
