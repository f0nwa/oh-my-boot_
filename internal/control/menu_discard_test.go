package control

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestDiscardDraftReturnsToPublishedMenu(t *testing.T) {
	store := newDraftStore(t, "operator", []MenuDraftItem{{Key: "winiso_17", Title: "Windows 11"}})
	store.draft = &MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{Key: "winanswer_31"}}}
	s, published := draftServer(t, store)
	res := menuPost(t, s, "/menu/discard", url.Values{"confirm": {"yes"}})
	if res.Code != http.StatusSeeOther || !strings.HasPrefix(res.Header().Get("Location"), "/menu?message=") {
		t.Fatalf("status=%d location=%q body=%s", res.Code, res.Header().Get("Location"), res.Body.String())
	}
	if len(*published) != 0 {
		t.Fatal("discarding the draft published a menu")
	}
	draft, active, err := s.currentDraft(t.Context())
	if err != nil || len(diffMenuDraft(active, draft)) != 0 || len(draft.Items) != 1 || draft.Items[0].Key != "winiso_17" {
		t.Fatalf("err=%v draft=%+v changes=%v", err, draft, diffMenuDraft(active, draft))
	}
}

func TestDiscardDraftNeedsConfirmation(t *testing.T) {
	store := newDraftStore(t, "operator", []MenuDraftItem{{Key: "winiso_17", Title: "Windows 11"}})
	store.draft = &MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{Key: "winanswer_31"}}}
	s, _ := draftServer(t, store)
	res := menuPost(t, s, "/menu/discard", url.Values{})
	if res.Code == http.StatusSeeOther || len(store.draft.Items) != 1 || store.draft.Items[0].Key != "winanswer_31" {
		t.Fatalf("status=%d draft=%+v", res.Code, store.draft)
	}
}

func TestObserverCannotDiscardDraft(t *testing.T) {
	store := newDraftStore(t, "observer", []MenuDraftItem{{Key: "winiso_17", Title: "Windows 11"}})
	store.draft = &MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{Key: "winanswer_31"}}}
	s, _ := draftServer(t, store)
	res := menuPost(t, s, "/menu/discard", url.Values{"confirm": {"yes"}})
	if res.Code == http.StatusSeeOther || store.draft.Items[0].Key != "winanswer_31" {
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
