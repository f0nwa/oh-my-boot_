package control

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestDiscardDraftReturnsToPublishedMenu(t *testing.T) {
	store := newDraftStore(t, "operator", nil)
	store.versions = []MenuVersion{{ID: 32, Active: true, Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", Script: "#!ipxe\n# structure lives in rows\n", Look: menuLook, LocalTimeout: 10, Items: []MenuVersionItem{{VersionID: 32, InstallationID: 7, Position: 0, Title: "Ubuntu Lab", Folder: "Linux"}, {VersionID: 32, InstallationID: 8, Position: 1, Title: "Windows", Folder: "Windows"}}}}
	store.draft = &MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Edited", Items: []MenuDraftItem{{InstallationID: 8, Title: "Edited"}}}
	s, published := draftServer(t, store)
	res := menuPost(t, s, "/menu/discard", url.Values{"confirm": {"yes"}})
	if res.Code != http.StatusSeeOther || !strings.HasPrefix(res.Header().Get("Location"), "/menu?message=") {
		t.Fatalf("status=%d location=%q body=%s", res.Code, res.Header().Get("Location"), res.Body.String())
	}
	if len(*published) != 0 {
		t.Fatal("discarding the draft published a menu")
	}
	draft, active, err := s.currentDraft(t.Context())
	if err != nil || len(diffMenuDraft(active, draft)) != 0 || len(draft.Items) != 2 || draft.Items[0].InstallationID != 7 || draft.Items[0].Title != "Ubuntu Lab" || draft.Items[0].Folder != "Linux" || draft.Items[1].InstallationID != 8 {
		t.Fatalf("err=%v draft=%+v changes=%v", err, draft, diffMenuDraft(active, draft))
	}
}

func TestDiscardDraftNeedsConfirmation(t *testing.T) {
	store := newDraftStore(t, "operator", []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11"}})
	store.draft = &MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{InstallationID: 2031}}}
	s, _ := draftServer(t, store)
	res := menuPost(t, s, "/menu/discard", url.Values{})
	if res.Code == http.StatusSeeOther || len(store.draft.Items) != 1 || menuDraftItemKey(store.draft.Items[0]) != "install_2031" {
		t.Fatalf("status=%d draft=%+v", res.Code, store.draft)
	}
}

func TestObserverCannotDiscardDraft(t *testing.T) {
	store := newDraftStore(t, "observer", []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11"}})
	store.draft = &MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{InstallationID: 2031}}}
	s, _ := draftServer(t, store)
	res := menuPost(t, s, "/menu/discard", url.Values{"confirm": {"yes"}})
	if res.Code == http.StatusSeeOther || menuDraftItemKey(store.draft.Items[0]) != "install_2031" {
		t.Fatalf("observer discarded the draft: status=%d", res.Code)
	}
}

func TestDraftBarOffersDiscard(t *testing.T) {
	d := samplePageData(langEN)
	d.Info.Path = "/installations"
	d.Info.Draft = draftSummary{Active: 38, Add: 1}
	res := httptest.NewRecorder()
	render(res, "installations", d)
	body := res.Body.String()
	if !strings.Contains(body, `action="/menu/discard"`) || !strings.Contains(body, `name="confirm" value="yes"`) || !strings.Contains(body, "Discard changes") {
		t.Fatal("the draft bar has no way to discard the draft")
	}
}
