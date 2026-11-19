package control

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"omb/internal/images"
)

func imageHTMLRow(t *testing.T, body, path string) string {
	t.Helper()
	needle := `data-path="` + path + `"`
	idx := strings.Index(body, needle)
	if idx < 0 {
		t.Fatalf("image row %q not found in body", path)
	}
	start := strings.LastIndex(body[:idx], "<tr")
	if start < 0 {
		t.Fatalf("start of row %q not found", path)
	}
	rest := body[start:]
	end := strings.Index(rest, "</tr>")
	if end < 0 {
		t.Fatalf("end of row %q not found", path)
	}
	return rest[:end]
}

func TestImagesPageStorageOutageVariantB(t *testing.T) {
	s, store := newInstallServer(t)
	store.user.Role = "admin"
	// Profile 10 sits on DSM (storage 7) with no installations using it, so it gets CleanupProfileID=10.
	store.profiles = []WindowsProfile{
		{ID: 10, WindowsProfileSource: WindowsProfileSource{StorageID: 7, Path: "win-cached.iso", Title: "Win Cached"}, State: "ready", InstallFile: "install.wim"},
	}
	store.installations = nil
	api := s.Images.(*fakeImagesAPI)
	api.catalog = images.Catalog{
		Storages: []images.Storage{
			{ID: 7, Name: "DSM", Kind: "smb", Root: "/mnt/omb-smb/dsm", State: "unavailable", Uploads: true, Free: 100 << 30, Total: 200 << 30},
			{ID: 8, Name: "Local", Kind: "local", Root: "/srv/omb/iso", State: "ready", Uploads: true, Free: 50 << 30, Total: 100 << 30},
		},
		Images: []images.Image{
			{StorageID: 7, Path: "win-cached.iso", Size: 4 << 30, Present: true},
			{StorageID: 7, Path: "ubuntu-dsm.iso", Size: 3 << 30, Present: true},
			{StorageID: 8, Path: "local-ok.iso", Size: 2 << 30, Present: true},
		},
	}

	// 1. Storage unavailable
	body := libraryGet(t, s, "/images")
	cachedRow := imageHTMLRow(t, body, "win-cached.iso")
	plainRow := imageHTMLRow(t, body, "ubuntu-dsm.iso")
	localRow := imageHTMLRow(t, body, "local-ok.iso")

	for name, row := range map[string]string{"win-cached.iso": cachedRow, "ubuntu-dsm.iso": plainRow} {
		if !strings.Contains(row, `<span class="dot dot-error"></span>Хранилище недоступно`) {
			t.Fatalf("%s must show unavailable storage status: %s", name, row)
		}
		if strings.Contains(row, "В библиотеке") || strings.Contains(row, "dot-ok") {
			t.Fatalf("%s must not show green in-library status: %s", name, row)
		}
		if strings.Contains(row, `action="/images/iso/inspect"`) || strings.Contains(row, `data-image-migrate-open=`) || strings.Contains(row, `data-image-delete-open="image-delete-`) {
			t.Fatalf("%s must hide ISO actions under Variant B: %s", name, row)
		}
	}
	// Variant B: row without CleanupProfileID hides the entire ☰ button; row with CleanupProfileID keeps only cleanup.
	if strings.Contains(plainRow, `class="secondary row-menu-btn"`) {
		t.Fatalf("ubuntu-dsm.iso without cleanup must hide row menu button: %s", plainRow)
	}
	if !strings.Contains(cachedRow, `class="secondary row-menu-btn"`) || !strings.Contains(cachedRow, "Удалить подготовленные файлы") {
		t.Fatalf("win-cached.iso with cleanup must keep cleanup action: %s", cachedRow)
	}
	if !strings.Contains(localRow, `<span class="dot dot-ok"></span>В библиотеке`) {
		t.Fatalf("local-ok.iso must stay healthy: %s", localRow)
	}
	if !strings.Contains(body, `show=attention">Требуют внимания <span class="mono">2</span>`) {
		t.Fatalf("attention counter must include unavailable storage images: %s", body)
	}
	attention := libraryGet(t, s, "/images?show=attention")
	if !strings.Contains(attention, "win-cached.iso") || !strings.Contains(attention, "ubuntu-dsm.iso") || strings.Contains(attention, "local-ok.iso") {
		t.Fatalf("attention filter mismatch: %s", attention)
	}

	// Dialogs: upload select and migrate select must disable unavailable DSM storage.
	if !strings.Contains(body, `<option value="7" data-free="107374182400" disabled>DSM · SMB · недоступно</option>`) {
		t.Fatalf("upload dialog must disable unavailable DSM storage: %s", body)
	}
	if !strings.Contains(body, `<option value="7" disabled>DSM · SMB · недоступно</option>`) {
		t.Fatalf("migrate dialog must disable unavailable DSM storage: %s", body)
	}

	// 2. Storage scanning
	api.catalog.Storages[0].State = "scanning"
	bodyScan := libraryGet(t, s, "/images")
	if row := imageHTMLRow(t, bodyScan, "ubuntu-dsm.iso"); !strings.Contains(row, `<span class="dot dot-accent"></span>Проверяется хранилище`) || strings.Contains(row, `action="/images/iso/inspect"`) {
		t.Fatalf("scanning storage status mismatch: %s", row)
	}
	if !strings.Contains(bodyScan, `<option value="7" disabled>DSM · SMB · сканирование</option>`) {
		t.Fatalf("migrate dialog must disable scanning DSM storage: %s", bodyScan)
	}

	// 3. Storage unknown
	api.catalog.Storages[0].State = "unknown"
	bodyUnknown := libraryGet(t, s, "/images")
	if row := imageHTMLRow(t, bodyUnknown, "ubuntu-dsm.iso"); !strings.Contains(row, `<span class="dot dot-accent"></span>Хранилище не проверено`) {
		t.Fatalf("unknown storage status mismatch: %s", row)
	}

	// 4. English localization
	api.catalog.Storages[0].State = "unavailable"
	store.user.Language = "en"
	bodyEN := libraryGet(t, s, "/images")
	if row := imageHTMLRow(t, bodyEN, "ubuntu-dsm.iso"); !strings.Contains(row, `<span class="dot dot-error"></span>Storage is unavailable`) {
		t.Fatalf("English unavailable status mismatch: %s", row)
	}
	if !strings.Contains(bodyEN, `<option value="7" disabled>DSM · SMB · unavailable</option>`) {
		t.Fatalf("English migrate dialog disabled option mismatch: %s", bodyEN)
	}

	// 5. Recovery restores normal state and actions
	store.user.Language = "ru"
	api.catalog.Storages[0].State = "ready"
	bodyOK := libraryGet(t, s, "/images")
	recovered := imageHTMLRow(t, bodyOK, "ubuntu-dsm.iso")
	if !strings.Contains(recovered, `<span class="dot dot-ok"></span>В библиотеке`) || !strings.Contains(recovered, `action="/images/iso/inspect"`) {
		t.Fatalf("recovered image must restore status and actions: %s", recovered)
	}
	if !strings.Contains(bodyOK, `show=attention">Требуют внимания <span class="mono">0</span>`) {
		t.Fatalf("attention counter must return to 0 after recovery: %s", bodyOK)
	}
}

func TestNewInstallationBlocksExistingProfileOnUnavailableStorage(t *testing.T) {
	s, store := newInstallServer(t)
	store.user.Role = "admin"
	store.bootProfiles = append(store.bootProfiles, BootProfile{
		ID:              55,
		Kind:            BootProfileUbuntuISO,
		Title:           "Ubuntu 24.04",
		UbuntuSourceID:  9,
		UbuntuStorageID: 7,
		UbuntuPath:      "ubuntu-24.04.iso",
		UbuntuSHA256:    strings.Repeat("a", 64),
		UbuntuState:     "ready",
	})
	api := s.Images.(*fakeImagesAPI)
	api.catalog.Storages = []images.Storage{
		{ID: 7, Name: "DSM", Kind: "smb", Root: "/mnt/omb-smb/dsm", State: "unavailable"},
	}

	choices, err := s.downloadSourceChoices(context.Background(), store.bootProfiles, store.profiles, true)
	if err != nil {
		t.Fatalf("downloadSourceChoices: %v", err)
	}
	foundWin, foundUbuntu := false, false
	for _, c := range choices {
		if c.ID == 51 || c.ID == 55 {
			if c.ID == 51 {
				foundWin = true
			} else {
				foundUbuntu = true
			}
			if c.Selectable() {
				t.Fatalf("existing profile %d on unavailable storage must not be selectable: %+v", c.ID, c)
			}
			if c.State != "unavailable" || c.Error != "Хранилище недоступно" {
				t.Fatalf("unexpected choice %d state/error: %+v", c.ID, c)
			}
		}
	}
	if !foundWin || !foundUbuntu {
		t.Fatalf("expected both profiles in choices: win=%v ubuntu=%v", foundWin, foundUbuntu)
	}

	res := menuPost(t, s, "/installations", url.Values{
		"boot_profile_id": {"51"},
		"title":           {"Win11 Blocked"},
	})
	if res.Code == http.StatusSeeOther {
		t.Fatalf("POST /installations must reject existing profile on unavailable storage, got redirect to %s", res.Header().Get("Location"))
	}
	if !strings.Contains(res.Body.String(), "Хранилище недоступно") {
		t.Fatalf("expected storage outage error in response, got %d: %s", res.Code, res.Body.String())
	}

	// Recovery makes it selectable again.
	api.catalog.Storages[0].State = "ready"
	choices, err = s.downloadSourceChoices(context.Background(), store.bootProfiles, store.profiles, true)
	if err != nil {
		t.Fatalf("downloadSourceChoices after recovery: %v", err)
	}
	for _, c := range choices {
		if c.ID == 51 && !c.Selectable() {
			t.Fatalf("profile 51 must be selectable after storage recovery: %+v", c)
		}
	}
}

func TestMenuPageMarksItemUnavailableOnStorageOutageAndDegradesSafely(t *testing.T) {
	s, store := newInstallServer(t)
	store.user.Role = "admin"
	store.installations = []Installation{
		{ID: 100, BootProfileID: 51, Title: "Active Windows"},
	}
	store.draft = &MenuDraft{Title: "PXE", Items: []MenuDraftItem{{InstallationID: 100}}}
	store.versions = []MenuVersion{{Active: true, Title: "PXE", Items: []MenuVersionItem{{InstallationID: 100}}}}
	api := s.Images.(*fakeImagesAPI)
	api.catalog.Storages = []images.Storage{
		{ID: 7, Name: "DSM", Kind: "smb", Root: "/mnt/omb-smb/dsm", State: "unavailable"},
	}

	body := libraryGet(t, s, "/menu?item=install_100")
	if !strings.Contains(body, `title="Недоступно"`) || !strings.Contains(body, "Хранилище ISO недоступно") {
		t.Fatalf("menu page must mark item unavailable on storage outage: %s", body)
	}
	if strings.Contains(body, `<span class="dot dot-ok"></span>Готово`) {
		t.Fatalf("menu inspector must not show green Ready during storage outage: %s", body)
	}

	// English translation in menu inspector
	store.user.Language = "en"
	bodyEN := libraryGet(t, s, "/menu?item=install_100")
	if !strings.Contains(bodyEN, "ISO storage is unavailable") {
		t.Fatalf("English menu inspector must translate storage outage error: %s", bodyEN)
	}

	// Recovery
	store.user.Language = "ru"
	api.catalog.Storages[0].State = "ready"
	bodyOK := libraryGet(t, s, "/menu?item=install_100")
	if !strings.Contains(bodyOK, `<span class="dot dot-ok"></span>Готово`) {
		t.Fatalf("menu inspector must show Ready after recovery: %s", bodyOK)
	}

	// Graceful degradation when omb-images itself is offline
	s.Images = offlineDownloadImages{ImagesAPI: s.Images}
	bodyOffline := libraryGet(t, s, "/menu?item=install_100")
	if !strings.Contains(bodyOffline, `<span class="dot dot-ok"></span>Готово`) {
		t.Fatalf("menu item must not turn broken when omb-images catalog is temporarily unreachable: %s", bodyOffline)
	}
}
