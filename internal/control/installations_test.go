package control

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
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
	if body := libraryGet(t, s, "/"); strings.Contains(body, "не опубликован") || !strings.Contains(body, "Windows 11") {
		t.Fatalf("clean page: %s", body)
	}
	store.draft = &MenuDraft{Title: "PXE Platform", Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{Key: "winiso_17", Title: "Windows 11"}, {Key: "winanswer_31"}}}
	body := libraryGet(t, s, "/")
	if !strings.Contains(body, `href="/menu"`) || !strings.Contains(body, "Изменений, не опубликованных в меню: 1") {
		t.Fatalf("pending page: %s", body)
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
	body := libraryGet(t, s, "/")
	if strings.Contains(body, "Новая установка") || strings.Contains(body, `action="/installations/`) || strings.Contains(body, `action="/drivers/configs/`) {
		t.Fatalf("observer page: %s", body)
	}
}
