package control

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestAddThenPublishLeavesNoPendingChanges(t *testing.T) {
	store := newDraftStore(t, "operator", []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11"}})
	s, _ := draftServer(t, store)
	if res := menuPost(t, s, "/installations/install_2031/add", url.Values{}); res.Code != http.StatusSeeOther {
		t.Fatalf("add: %d %s", res.Code, res.Body.String())
	}
	if res := menuPost(t, s, "/menu/publish", url.Values{}); res.Code != http.StatusSeeOther {
		t.Fatalf("publish: %d %s", res.Code, res.Body.String())
	}
	draft, active, err := s.currentDraft(context.Background())
	if err != nil || len(diffMenuDraft(active, draft)) != 0 {
		t.Fatalf("pending after publish: %v %+v", err, diffMenuDraft(active, draft))
	}
}

func TestReaddingRemovedItemRestoresItsTitleAndFolder(t *testing.T) {
	store := newDraftStore(t, "operator", []MenuDraftItem{{InstallationID: 1017, Title: "Custom", Folder: "Lab"}})
	store.draft = &MenuDraft{Title: "oh my boot_", Label: "Diagnostics", Message: "Ready"}
	s, _ := draftServer(t, store)
	menuPost(t, s, "/installations/install_1017/add", url.Values{})
	draft, active, _ := s.currentDraft(context.Background())
	if changes := diffMenuDraft(active, draft); len(changes) != 0 {
		t.Fatalf("re-add left changes %+v draft=%+v", changes, draft)
	}
}

func TestNewInstallationWithAnswerDefaultsToAnswerTitle(t *testing.T) {
	s, store := newInstallServer(t)
	res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"51"}, "answer_id": {"31"}})
	if res.Code != http.StatusSeeOther || len(store.installations) != 1 || store.installations[0].Title != "Lab" {
		t.Fatalf("status=%d installations=%+v", res.Code, store.installations)
	}
}

func TestFailedNewInstallationKeepsChoices(t *testing.T) {
	s, _ := newInstallServer(t)
	res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"51"}, "answer_id": {"31"}, "package": {strings.Repeat("a", 64)}, "title": {"Ключ"}})
	body := res.Body.String()
	if res.Code < 400 || !strings.Contains(body, `name="answer_id" value="31"`) || !strings.Contains(body, `value="`+strings.Repeat("a", 64)+`" checked`) || !strings.Contains(body, `value="Ключ"`) {
		t.Fatalf("status=%d body=%s", res.Code, body)
	}
	if !strings.Contains(body, `value="31" data-title="Lab" data-sha="`+strings.Repeat("d", 64)+`" data-wipes="false" checked`) {
		t.Fatalf("answer not reselected: %s", body)
	}
}

func TestAnswerWipesDiskUnknownWhenUnparsed(t *testing.T) {
	if _, known := AnswerWipesDisk([]byte(`<?xml version="1.0" encoding="utf-16"?><unattend/>`)); known {
		t.Fatal("unparsed XML reported as known")
	}
	if wipes, known := AnswerWipesDisk([]byte(`<unattend/>`)); !known || wipes {
		t.Fatal("plain XML not classified as safe")
	}
}

func TestInstallationsSearchFiltersByTitle(t *testing.T) {
	store := newDraftStore(t, "operator", []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11"}})
	s, _ := draftServer(t, store)
	body := libraryGet(t, s, "/installations?q=lab")
	if !strings.Contains(body, "install_2031") || strings.Contains(body, "install_1017") || !strings.Contains(body, `name="q" value="lab"`) {
		t.Fatalf("search page: %s", body)
	}
}

func TestPendingAddCanBeDroppedFromChanges(t *testing.T) {
	store := newDraftStore(t, "operator", []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11"}})
	store.draft = &MenuDraft{Title: "oh my boot_", Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11"}, {InstallationID: 2031, Title: "Lab"}}}
	s, _ := draftServer(t, store)
	body := libraryGet(t, s, "/menu")
	i := strings.Index(body, `id="menu-changes"`)
	j := i + strings.Index(body[max(i, 0):], "</ul></div>")
	if i < 0 || j < i || !strings.Contains(body[i:j], `action="/menu/items/install_2031/remove"`) {
		t.Fatalf("pending block lacks per-row remove: %s", body)
	}
}
