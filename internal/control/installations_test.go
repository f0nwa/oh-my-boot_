package control

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"pxe-platform/internal/boot"
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
	draft := MenuDraft{Items: []MenuDraftItem{{Key: "windriver_1"}}}
	active := MenuDraft{Items: []MenuDraftItem{{Key: "winiso_17"}}}
	rows := installationRows(profiles, answers, configs, draft, active)
	first, i := rowByKey(rows, "windriver_1")
	if i != 0 || first.Status != "pending_add" || first.Image != "Windows 11" || first.Drivers != "net" {
		t.Fatalf("first row %+v at %d", first, i)
	}
	if row, _ := rowByKey(rows, "winiso_17"); row.Status != "pending_remove" {
		t.Fatalf("removed row %+v", row)
	}
	if row, _ := rowByKey(rows, "winiso_18"); row.Status != "not_in_menu" {
		t.Fatalf("other row %+v", row)
	}
	if row, _ := rowByKey(rows, "winanswer_31"); row.Answer != "Lab" || row.Image != "Windows 11" {
		t.Fatalf("answer row %+v", row)
	}
}

func TestInstallationRowNeedsBuild(t *testing.T) {
	profiles, answers, configs := draftFixture()
	configs[0].State = "pending"
	rows := installationRows(profiles, answers, configs, MenuDraft{}, MenuDraft{})
	if row, _ := rowByKey(rows, "windriver_1"); row.Status != "needs_build" || row.ConfigID != 1 {
		t.Fatalf("row %+v", row)
	}
}

func TestInstallationsPageShowsPendingNotice(t *testing.T) {
	store := newDraftStore(t, "operator", []MenuDraftItem{{Key: "winiso_17", Title: "Windows 11"}})
	s, _ := draftServer(t, store)
	if body := libraryGet(t, s, "/installations"); strings.Contains(body, `class="draftbar"`) || !strings.Contains(body, "Windows 11") {
		t.Fatalf("clean page: %s", body)
	}
	store.draft = &MenuDraft{Title: "oh my boot_", Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{Key: "winiso_17", Title: "Windows 11"}, {Key: "winanswer_31"}}}
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
	res := menuPost(t, s, "/installations/winanswer_31/add", url.Values{})
	if res.Code != http.StatusSeeOther || store.draft == nil || len(store.draft.Items) != 1 || store.draft.Items[0].Key != "winanswer_31" {
		t.Fatalf("status=%d draft=%+v body=%s", res.Code, store.draft, res.Body.String())
	}
}

func TestObserverSeesNoInstallationButtons(t *testing.T) {
	store := newDraftStore(t, "observer", []MenuDraftItem{{Key: "winiso_17", Title: "Windows 11"}})
	s, _ := draftServer(t, store)
	body := libraryGet(t, s, "/installations")
	if strings.Contains(body, "Новая установка") || strings.Contains(body, `action="/installations/`) || strings.Contains(body, `action="/drivers/configs/`) {
		t.Fatalf("observer page: %s", body)
	}
}

func TestInstallationUsageCountsBootsPerItem(t *testing.T) {
	t0 := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	events := []boot.Event{
		{At: t0, Kind: "asset", Path: "/assets/windows-driver-configs/4/pxe-setup.cmd", RemoteAddr: "10.30.0.19:1"},
		{At: t0.Add(5 * time.Second), Kind: "asset", Path: "/assets/windows-driver-configs/4/winpeshl.ini", RemoteAddr: "10.30.0.19:1"},
		{At: t0.Add(time.Hour), Kind: "asset", Path: "/assets/windows-driver-configs/4/pxe-setup.cmd", RemoteAddr: "10.30.0.19:1"},
		{At: t0.Add(time.Minute), Kind: "asset", Path: "/assets/windows-driver-configs/4/pxe-setup.cmd", RemoteAddr: "10.30.0.20:1"},
		{At: t0, Kind: "menu", Path: "/boot/menu.ipxe", RemoteAddr: "10.30.0.19:1"},
		{At: t0.Add(2 * time.Hour), Kind: "asset", Path: "/assets/windows-driver-configs/4/pxe-setup.cmd", RemoteAddr: "10.30.0.10:1"},
	}
	use := installationUsage(events, map[string]bool{"10.30.0.10": true})
	if use["windriver_4"].Count != 3 || !use["windriver_4"].Last.Equal(t0.Add(time.Hour)) || len(use) != 1 {
		t.Fatalf("use=%+v", use)
	}
	if (installationRow{}).UsedLabel() != "не выбирали" || !strings.HasPrefix((installationRow{Used: 3, LastUsed: time.Now()}).UsedLabel(), "загрузок: 3 · ") {
		t.Fatal("used label")
	}
}

func TestInstallationsFilterAndRemoveReturns(t *testing.T) {
	store := newDraftStore(t, "operator", []MenuDraftItem{{Key: "winiso_17", Title: "Windows 11"}})
	s, _ := draftServer(t, store)
	body := libraryGet(t, s, "/installations?show=off")
	if strings.Contains(body, "winiso_17") || !strings.Contains(body, `href="/installations?show=off" class="active"`) {
		t.Fatalf("off filter: %s", body)
	}
	if body := libraryGet(t, s, "/installations?show=menu"); !strings.Contains(body, "winiso_17") || !strings.Contains(body, `name="return" value="/installations"`) {
		t.Fatalf("menu filter: %s", body)
	}
	res := menuPost(t, s, "/menu/items/winiso_17/remove", url.Values{"return": {"/installations"}})
	if res.Code != http.StatusSeeOther || !strings.HasPrefix(res.Header().Get("Location"), "/installations?message=") {
		t.Fatalf("status=%d location=%q", res.Code, res.Header().Get("Location"))
	}
}
