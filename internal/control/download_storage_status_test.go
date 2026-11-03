package control

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"omb/internal/images"
)

func downloadHTMLRow(t *testing.T, body, key string) string {
	t.Helper()
	for _, row := range strings.Split(body, "<tr") {
		if strings.Contains(row, ">"+key+"</small>") {
			return strings.SplitN(row, "</tr>", 2)[0]
		}
	}
	t.Fatalf("download %s is absent", key)
	return ""
}

func TestDownloadStorageWarningTracksOutageAndRecovery(t *testing.T) {
	s, store := newInstallServer(t)
	store.user.Role = "admin"
	store.profiles = append(store.profiles, WindowsProfile{ID: 99, WindowsProfileSource: WindowsProfileSource{StorageID: 8, Path: "other.iso", Title: "Other"}, State: "ready", InstallFile: "install.wim"})
	store.bootProfiles = append(store.bootProfiles, BootProfile{ID: 53, Kind: BootProfileWindowsISO, WindowsProfileID: 99, Title: "Other"})
	store.installations = []Installation{
		{ID: 100, BootProfileID: 51, Title: "Active Windows"},
		{ID: 101, BootProfileID: 50, Title: "Draft Windows"},
		{ID: 102, BootProfileID: 53, Title: "Healthy Windows"},
		{ID: 103, BootProfileID: 52, Title: "Ubuntu Test"},
	}
	store.draft = &MenuDraft{Title: "PXE", Items: []MenuDraftItem{{InstallationID: 100}, {InstallationID: 101}}}
	store.versions = []MenuVersion{{Active: true, Title: "PXE", Items: []MenuVersionItem{{InstallationID: 100}}}}
	api := s.Images.(*fakeImagesAPI)
	api.catalog.Storages = []images.Storage{{ID: 7, Name: "DSM", Kind: "smb", State: "unavailable"}, {ID: 8, Name: "Reserve", Kind: "local", State: "ready"}}

	body := libraryGet(t, s, "/installations")
	active := downloadHTMLRow(t, body, "install_100")
	if !strings.Contains(active, "Хранилище ISO недоступно") || !strings.Contains(active, "В меню") || !strings.Contains(active, "Убрать из меню") {
		t.Fatalf("active entry must retain menu state and show outage: %s", active)
	}
	if strings.Contains(active, `class="dot dot-ok"`) {
		t.Fatal("unavailable storage still displays a green status")
	}
	if row := downloadHTMLRow(t, body, "install_101"); !strings.Contains(row, "Хранилище ISO недоступно") || !strings.Contains(row, "Будет добавлена") {
		t.Fatalf("draft entry lost its state or warning: %s", row)
	}
	for _, key := range []string{"install_102", "install_103"} {
		if row := downloadHTMLRow(t, body, key); strings.Contains(row, "Хранилище ISO недоступно") {
			t.Fatalf("unrelated entry %s received warning: %s", key, row)
		}
	}
	attention := libraryGet(t, s, "/installations?show=attention")
	for _, title := range []string{"Active Windows", "Draft Windows"} {
		if !strings.Contains(attention, title) {
			t.Fatalf("attention filter misses %s", title)
		}
	}
	if strings.Contains(attention, "Healthy Windows") || strings.Contains(attention, "Ubuntu Test") {
		t.Fatal("attention filter includes unrelated downloads")
	}
	if menu := libraryGet(t, s, "/installations?show=menu"); !strings.Contains(menu, "Active Windows") {
		t.Fatal("outage removed the published entry from the menu filter")
	}
	if !strings.Contains(body, `show=attention">Требуют внимания <span class="mono">2</span>`) || !strings.Contains(body, `show=menu">В меню <span class="mono">1</span>`) {
		t.Fatal("menu and attention counters do not include affected entries")
	}
	store.profiles[0].State = "error"
	if body := libraryGet(t, s, "/installations"); !strings.Contains(body, `show=attention">Требуют внимания <span class="mono">2</span>`) {
		t.Fatal("an entry with both a preparation error and a storage warning was counted twice")
	}
	store.profiles[0].State = "ready"
	api.catalog.Storages = api.catalog.Storages[1:]
	if row := downloadHTMLRow(t, libraryGet(t, s, "/installations"), "install_100"); !strings.Contains(row, "Хранилище ISO не найдено") {
		t.Fatalf("missing storage was not reported: %s", row)
	}
	api.catalog.Storages = append([]images.Storage{{ID: 7, Name: "DSM", Kind: "smb", State: "unavailable"}}, api.catalog.Storages...)

	api.catalog.Storages[0].State = "scanning"
	if row := downloadHTMLRow(t, libraryGet(t, s, "/installations"), "install_100"); !strings.Contains(row, "Проверяется хранилище ISO") || strings.Contains(row, "Хранилище ISO недоступно") {
		t.Fatalf("rescan is not a confirmed outage: %s", row)
	}
	api.catalog.Storages[0].State = "ready"
	if body := libraryGet(t, s, "/installations"); strings.Contains(body, "Хранилище ISO недоступно") {
		t.Fatal("warning persisted after recovery")
	}
	api.catalog.Storages[0].State = "unavailable"
	store.user.Role = "observer"
	if body := libraryGet(t, s, "/installations"); !strings.Contains(downloadHTMLRow(t, body, "install_100"), "Хранилище ISO недоступно") {
		t.Fatal("observer cannot see the warning")
	}
	store.user.Role, store.user.Language = "admin", "en"
	if body := libraryGet(t, s, "/installations"); !strings.Contains(downloadHTMLRow(t, body, "install_100"), "ISO storage is unavailable") {
		t.Fatal("English warning is missing")
	}

	s.Images = offlineDownloadImages{ImagesAPI: s.Images}
	store.user.Language = "ru"
	body = libraryGet(t, s, "/installations")
	if row := downloadHTMLRow(t, body, "install_100"); !strings.Contains(row, "Не удалось проверить хранилище ISO") || strings.Contains(row, "Хранилище ISO недоступно") {
		t.Fatalf("catalog outage must be reported as unknown, not a confirmed storage outage: %s", row)
	}
	if res := menuPost(t, s, "/menu/items/install_100/remove", url.Values{}); res.Code != http.StatusSeeOther {
		t.Fatalf("menu action unavailable during catalog outage: %d", res.Code)
	}
}
