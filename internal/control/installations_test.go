package control

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omb/internal/boot"
)

func rowByKey(rows []installationRow, key string) (installationRow, int) {
	for i, row := range rows {
		if row.Key == key {
			return row, i
		}
	}
	return installationRow{}, -1
}

func TestInstallationRowsOrderAndStatus(t *testing.T) {
	profiles, answers, configs := draftFixture()
	profiles = append(profiles, WindowsProfile{ID: 18, WindowsProfileSource: WindowsProfileSource{Title: "Windows 10"}, State: "ready", InstallFile: "install.wim"})
	draft := MenuDraft{Items: []MenuDraftItem{{InstallationID: 3001}}}
	active := MenuDraft{Items: []MenuDraftItem{{InstallationID: 1017}}}
	rows := installationRows(profiles, answers, configs, draft, active, testInstallationSources(profiles, answers, configs))
	first, i := rowByKey(rows, "install_3001")
	if i != 0 || first.Status != "pending_add" || first.Image != "Windows 11" || first.Drivers != "net" {
		t.Fatalf("first row %+v at %d", first, i)
	}
	if row, _ := rowByKey(rows, "install_1017"); row.Status != "pending_remove" {
		t.Fatalf("removed row %+v", row)
	}
	if row, _ := rowByKey(rows, "install_1018"); row.Status != "not_in_menu" {
		t.Fatalf("other row %+v", row)
	}
	if row, _ := rowByKey(rows, "install_2031"); row.Answer != "Lab" || row.Image != "Windows 11" {
		t.Fatalf("answer row %+v", row)
	}
}

func TestInstallationRowNeedsBuild(t *testing.T) {
	profiles, answers, configs := draftFixture()
	configs[0].State = "pending"
	rows := installationRows(profiles, answers, configs, MenuDraft{}, MenuDraft{}, testInstallationSources(profiles, answers, configs))
	if row, _ := rowByKey(rows, "install_3001"); row.Status != "needs_build" || row.ConfigID != 1 {
		t.Fatalf("row %+v", row)
	}
}

func TestInstallationsPageShowsPendingNotice(t *testing.T) {
	store := newDraftStore(t, "operator", []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11"}})
	s, _ := draftServer(t, store)
	if body := libraryGet(t, s, "/installations"); strings.Contains(body, `class="draftbar"`) || !strings.Contains(body, "Windows 11") {
		t.Fatalf("clean page: %s", body)
	}
	store.draft = &MenuDraft{Title: "oh my boot_", Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11"}, {InstallationID: 2031}}}
	body := libraryGet(t, s, "/installations")
	if !strings.Contains(body, `class="draftbar"`) || !strings.Contains(body, `<span class="change-add">+1</span>`) || !strings.Contains(body, `action="/menu/publish"`) {
		t.Fatalf("pending page: %s", body)
	}
	if body := libraryGet(t, s, "/menu"); !strings.Contains(body, `class="draftbar"`) || !strings.Contains(body, `<span class="change-add">+1</span>`) {
		t.Fatalf("menu page lacks the draft bar: %s", body)
	}
}

func TestAddInstallationToDraft(t *testing.T) {
	store := newDraftStore(t, "operator", nil)
	s, _ := draftServer(t, store)
	res := menuPost(t, s, "/installations/install_2031/add", url.Values{})
	if res.Code != http.StatusSeeOther || store.draft == nil || len(store.draft.Items) != 1 || menuDraftItemKey(store.draft.Items[0]) != "install_2031" {
		t.Fatalf("status=%d draft=%+v body=%s", res.Code, store.draft, res.Body.String())
	}
}

func TestObserverSeesNoInstallationButtons(t *testing.T) {
	store := newDraftStore(t, "observer", []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11"}})
	s, _ := draftServer(t, store)
	body := libraryGet(t, s, "/installations")
	if strings.Contains(body, "Новая загрузка") || strings.Contains(body, `action="/installations/`) || strings.Contains(body, `action="/drivers/configs/`) {
		t.Fatalf("observer page: %s", body)
	}
}

func TestDownloadDeletionAppearsOnlyOutsidePublishedAndDraftMenus(t *testing.T) {
	for _, state := range []string{"ready", "error"} {
		t.Run(state, func(t *testing.T) {
			s, store := newInstallServer(t)
			store.user.Role = "admin"
			for i := range store.profiles {
				if store.profiles[i].ID == 17 {
					store.profiles[i].State = state
				}
			}
			store.installations = []Installation{
				{ID: 100, BootProfileID: 51, Title: "Published"},
				{ID: 101, BootProfileID: 51, Title: "Draft only"},
				{ID: 102, BootProfileID: 51, Title: "Pending removal"},
				{ID: 103, BootProfileID: 51, Title: "Outside menu"},
			}
			store.draft = &MenuDraft{Title: "PXE", Items: []MenuDraftItem{{InstallationID: 100}, {InstallationID: 101}}}
			store.versions = []MenuVersion{{Active: true, Title: "PXE", Items: []MenuVersionItem{{InstallationID: 100}, {InstallationID: 102}}}}
			body := libraryGet(t, s, "/installations")
			for _, id := range []string{"100", "101", "102"} {
				if strings.Contains(body, `action="/installations/`+id+`/delete"`) {
					t.Fatalf("entry %s can be deleted while in a menu", id)
				}
				if !strings.Contains(body, `action="/installations/`+id+`/remove-delete"`) || !strings.Contains(body, `popovertarget="download-menu-install_`+id+`"`) || !strings.Contains(body, `data-download-delete-open="download-delete-install_`+id+`"`) {
					t.Fatalf("entry %s lacks combined removal in its actions menu", id)
				}
			}
			if !strings.Contains(body, `action="/installations/103/delete"`) {
				t.Fatal("entry outside both menus cannot be deleted")
			}
			if state == "ready" && !strings.Contains(body, "Убрать из меню") {
				t.Fatal("published entry lacks a clear remove-from-menu action")
			}
			for _, role := range []string{"operator", "observer"} {
				store.user.Role = role
				body = libraryGet(t, s, "/installations")
				if strings.Contains(body, "/remove-delete") || strings.Contains(body, "data-download-delete-open") {
					t.Fatalf("role %s sees administrative deletion", role)
				}
			}
		})
	}
}

func TestInstallationUsageCountsChoicesPerItem(t *testing.T) {
	t0 := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	events := []boot.Event{
		{At: t0, Kind: "choice", Path: "/boot/choice?id=4", RemoteAddr: "10.30.0.19:1"},
		{At: t0.Add(5 * time.Second), Kind: "asset", Path: "/assets/windows-driver-configs/4/winpeshl.ini", RemoteAddr: "10.30.0.19:1"},
		{At: t0.Add(time.Hour), Kind: "choice", Path: "/boot/choice?id=4", RemoteAddr: "10.30.0.19:1"},
		{At: t0.Add(time.Minute), Kind: "choice", Path: "/boot/choice?id=4", RemoteAddr: "10.30.0.20:1"},
		{At: t0, Kind: "menu", Path: "/boot/menu.ipxe", RemoteAddr: "10.30.0.19:1"},
		{At: t0.Add(2 * time.Hour), Kind: "choice", Path: "/boot/choice?id=4", RemoteAddr: "10.30.0.10:1"},
	}
	use := installationUsage(events, map[string]bool{"10.30.0.10": true})
	if use["install_4"].Count != 3 || !use["install_4"].Last.Equal(t0.Add(time.Hour)) || len(use) != 1 {
		t.Fatalf("use=%+v", use)
	}
	if (installationRow{}).UsedLabel() != "не выбирали" || !strings.HasPrefix((installationRow{Used: 3, LastUsed: time.Now()}).UsedLabel(), "выборов: 3 · ") {
		t.Fatal("used label")
	}
}

func TestInstallationsFilterAndRemoveReturns(t *testing.T) {
	store := newDraftStore(t, "operator", []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11"}})
	s, _ := draftServer(t, store)
	body := libraryGet(t, s, "/installations?show=off")
	if strings.Contains(body, "install_1017") || !strings.Contains(body, `href="/installations?show=off" class="active"`) {
		t.Fatalf("off filter: %s", body)
	}
	if body := libraryGet(t, s, "/installations?show=menu"); !strings.Contains(body, "install_1017") || !strings.Contains(body, `name="return" value="/installations"`) {
		t.Fatalf("menu filter: %s", body)
	}
	res := menuPost(t, s, "/menu/items/install_1017/remove", url.Values{"return": {"/installations"}})
	if res.Code != http.StatusSeeOther || !strings.HasPrefix(res.Header().Get("Location"), "/installations?message=") {
		t.Fatalf("status=%d location=%q", res.Code, res.Header().Get("Location"))
	}
}

func TestUnifiedInstallationsPageShowsOnlyPersistentChoices(t *testing.T) {
	s, store := newInstallServer(t)
	store.installations = []Installation{{ID: 100, BootProfileID: 52, Title: "Ubuntu Lab", Folder: "Linux"}, {ID: 101, BootProfileID: 52, Title: "Ubuntu Rescue", Folder: "Recovery"}, {ID: 102, BootProfileID: 51, Title: "Windows XML", AnswerID: 31}}
	store.draft = &MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{InstallationID: 100, Title: "Ubuntu Lab", Folder: "Linux"}}}
	body := libraryGet(t, s, "/installations")
	for _, want := range []string{"Ubuntu Lab", "Ubuntu Rescue", "Ubuntu Desktop", `action="/installations/101/add"`, `action="/menu/items/install_100/remove"`, "Windows XML", "не выбирали"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s", want)
		}
	}
	if strings.Contains(body, "winiso_") || strings.Contains(body, "winanswer_") {
		t.Fatal("page invents source-derived installations")
	}
}

func TestUnifiedInstallationReAddRestoresPublishedPlacement(t *testing.T) {
	s, store := newInstallServer(t)
	boot, published := fakeBoot(t)
	s.Boot = boot
	store.installations = []Installation{{ID: 100, BootProfileID: 52, Title: "Ubuntu", Folder: "Linux"}, {ID: 101, BootProfileID: 52, Title: "Recovery"}}
	store.versions = []MenuVersion{{ID: 9, Active: true, Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", Items: []MenuVersionItem{{InstallationID: 100, Position: 0, Title: "Published Ubuntu", Folder: "Lab"}, {InstallationID: 101, Position: 1, Title: "Recovery"}}}}
	store.draft = &MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{InstallationID: 101, Title: "Recovery"}}}
	for i := 0; i < 2; i++ {
		res := menuPost(t, s, "/installations/100/add", url.Values{})
		if res.Code != http.StatusSeeOther {
			t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
		}
	}
	if len(store.draft.Items) != 2 || store.draft.Items[0].InstallationID != 100 || store.draft.Items[0].Title != "Published Ubuntu" || store.draft.Items[0].Folder != "Lab" || len(*published) != 0 {
		t.Fatalf("draft=%+v published=%v", store.draft, *published)
	}
}

func TestUnifiedUbuntuInspectorShowsSourceWithoutWindowsComponents(t *testing.T) {
	s, store := newInstallServer(t)
	store.installations = []Installation{{ID: 100, BootProfileID: 52, Title: "Ubuntu Lab", Folder: "Linux"}}
	store.draft = &MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{InstallationID: 100, Title: "Ubuntu Lab", Folder: "Linux"}}}
	body := libraryGet(t, s, "/menu?item=install_100")
	if !strings.Contains(body, "Ubuntu Desktop") || !strings.Contains(body, "<dt>Файл ответов</dt>") || strings.Contains(body, "<dt>Драйверы</dt>") {
		t.Fatal("Ubuntu inspector shows Windows components")
	}
}

func TestUnifiedInstallationPagesTranslateUbuntuSource(t *testing.T) {
	s, store := newInstallServer(t)
	store.user.Language = langEN
	store.installations = []Installation{{ID: 100, BootProfileID: 52, Title: "Ubuntu Lab", Folder: "Linux"}}
	store.draft = &MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{InstallationID: 100, Title: "Ubuntu Lab", Folder: "Linux"}}}
	for _, path := range []string{"/installations", "/installations/new?boot_profile_id=52", "/menu?item=install_100"} {
		body := libraryGet(t, s, path)
		if !strings.Contains(body, "Ubuntu Desktop") || strings.Contains(body, "Системный источник Ubuntu") {
			t.Errorf("%s has untranslated Ubuntu source", path)
		}
	}
}

func TestInstallationUsageCountsExplicitChoicesSharingUbuntuAssets(t *testing.T) {
	at := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	events := []boot.Event{
		{Kind: "choice", Path: "/boot/choice?id=101", At: at.Add(time.Minute), RemoteAddr: "10.30.0.19:2"},
		{Kind: "asset", Path: "/assets/ubuntu/vmlinuz", At: at.Add(2 * time.Second), RemoteAddr: "10.30.0.19:1"},
		{Kind: "choice", Path: "/boot/choice?id=101", At: at, RemoteAddr: "10.30.0.19:1"},
		{Kind: "asset", Path: "/assets/ubuntu/initrd", At: at.Add(3 * time.Second), RemoteAddr: "10.30.0.20:1"},
		{Kind: "iso", Path: "/images/ubuntu-desktop.iso", At: at.Add(4 * time.Second), RemoteAddr: "10.30.0.20:1"},
		{Kind: "choice", Path: "/boot/choice?id=100", At: at.Add(5 * time.Second), RemoteAddr: "10.30.0.10:1"},
	}
	use := installationUsage(events, map[string]bool{"10.30.0.10": true})
	if len(use) != 1 || use["install_101"].Count != 2 || !use["install_101"].Last.Equal(at.Add(time.Minute)) || use["install_100"].Count != 0 {
		t.Fatalf("usage=%+v", use)
	}
}

func TestInstallationUsageIgnoresInvalidChoiceEvents(t *testing.T) {
	for _, event := range []boot.Event{
		{Kind: "choice", Path: "/boot/choice?id=0"},
		{Kind: "choice", Path: "/boot/choice?id=-1"},
		{Kind: "choice", Path: "/boot/choice?id=01"},
		{Kind: "choice", Path: "/boot/choice?id=9223372036854775808"},
		{Kind: "choice", Path: "/boot/choice?id=100&id=101"},
		{Kind: "choice", Path: "/assets/ubuntu/vmlinuz"},
		{Kind: "asset", Path: "/boot/choice?id=100"},
	} {
		if use := installationUsage([]boot.Event{event}, nil); len(use) != 0 {
			t.Errorf("event=%+v use=%+v", event, use)
		}
	}
}

func TestInstallationsPageShowsChosenCount(t *testing.T) {
	s, store := newInstallServer(t)
	dir, err := os.MkdirTemp("", "boot")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "b.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	events := []boot.Event{
		{At: time.Now().Add(-10 * time.Minute), Kind: "choice", Path: "/boot/choice?id=100", RemoteAddr: "10.30.0.19:1"},
		{At: time.Now().Add(-5 * time.Minute), Kind: "choice", Path: "/boot/choice?id=100", RemoteAddr: "10.30.0.20:1"},
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/events" {
			_ = json.NewEncoder(w).Encode(events)
			return
		}
		w.WriteHeader(http.StatusOK)
	})}
	go func() { _ = server.Serve(l) }()
	t.Cleanup(func() { _ = server.Close() })
	s.Boot = NewBootClient(path)

	store.installations = []Installation{
		{ID: 100, BootProfileID: 52, Title: "Ubuntu Lab", Folder: "Linux"},
		{ID: 101, BootProfileID: 52, Title: "Ubuntu Rescue", Folder: "Recovery"},
	}
	body := libraryGet(t, s, "/installations")

	// ID 100 has 2 choices -> should have mono boots-count with "2"
	if !strings.Contains(body, `<span class="mono boots-count" title="выборов: 2 · `) || !strings.Contains(body, `>2</span>`) {
		t.Fatalf("expected boots-count 2 for install_100, got body:\n%s", body)
	}
	// ID 101 has 0 choices -> should have mono muted with "0" and title="не выбирали"
	if !strings.Contains(body, `<span class="mono muted" title="не выбирали">0</span>`) {
		t.Fatalf("expected mono muted 0 for install_101, got body:\n%s", body)
	}
}

func TestSortInstallations(t *testing.T) {
	rows := []installationRow{
		{ID: 1, Title: "Windows 11", Folder: "Windows", Status: "in_menu", Used: 5},
		{ID: 2, Title: "Astra Linux", Folder: "Linux", Status: "not_in_menu", Used: 0},
		{ID: 3, Title: "Ubuntu 24.04", Folder: "Linux", Status: "pending_add", Used: 12},
	}

	// Sort by title asc
	r1 := append([]installationRow(nil), rows...)
	sortInstallations(r1, "title", "asc")
	if r1[0].Title != "Astra Linux" || r1[1].Title != "Ubuntu 24.04" || r1[2].Title != "Windows 11" {
		t.Fatalf("unexpected title asc sort: %+v", r1)
	}

	// Sort by title desc
	r2 := append([]installationRow(nil), rows...)
	sortInstallations(r2, "title", "desc")
	if r2[0].Title != "Windows 11" || r2[1].Title != "Ubuntu 24.04" || r2[2].Title != "Astra Linux" {
		t.Fatalf("unexpected title desc sort: %+v", r2)
	}

	// Sort by used desc
	r3 := append([]installationRow(nil), rows...)
	sortInstallations(r3, "used", "desc")
	if r3[0].Used != 12 || r3[1].Used != 5 || r3[2].Used != 0 {
		t.Fatalf("unexpected used desc sort: %+v", r3)
	}

	// Sort by used asc
	r4 := append([]installationRow(nil), rows...)
	sortInstallations(r4, "used", "asc")
	if r4[0].Used != 0 || r4[1].Used != 5 || r4[2].Used != 12 {
		t.Fatalf("unexpected used asc sort: %+v", r4)
	}

	// Test HTML rendering with sort
	body := renderPage("installations", pageData{
		Installations: rows,
		Sort:          "used",
		Dir:           "desc",
		Info:          requestContext{Path: "/installations"},
	})
	if !strings.Contains(body, `aria-sort="descending"`) || !strings.Contains(body, `class="sorted"`) {
		t.Fatalf("expected sorted table header, got:\n%s", body)
	}
}
