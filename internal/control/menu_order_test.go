package control

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestMenuOrderSavesNewOrder(t *testing.T) {
	store := newDraftStore(t, "operator", nil)
	store.draft = &MenuDraft{Title: "PXE Platform", Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{Key: "winiso_17", Title: "A"}, {Key: "winanswer_31", Title: "B"}, {Key: "windriver_1", Title: "C", Folder: "Lab"}}}
	s, published := draftServer(t, store)
	res := menuPost(t, s, "/menu/order", url.Values{"key": {"windriver_1", "winiso_17", "winanswer_31"}})
	got := []string{}
	for _, item := range store.draft.Items {
		got = append(got, item.Key+"|"+item.Title+"|"+item.Folder)
	}
	if res.Code != http.StatusSeeOther || strings.Join(got, ",") != "windriver_1|C|Lab,winiso_17|A|,winanswer_31|B|" || len(*published) != 0 {
		t.Fatalf("status=%d order=%v body=%s", res.Code, got, res.Body.String())
	}
}

func TestMenuOrderRejectsDifferentItems(t *testing.T) {
	store := newDraftStore(t, "operator", nil)
	store.draft = &MenuDraft{Title: "PXE Platform", Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{Key: "winiso_17"}, {Key: "winanswer_31"}}}
	s, _ := draftServer(t, store)
	for _, keys := range [][]string{{"winiso_17"}, {"winiso_17", "winiso_17"}, {"winiso_17", "windriver_1"}} {
		res := menuPost(t, s, "/menu/order", url.Values{"key": keys})
		if res.Code < 400 || store.draft.Items[0].Key != "winiso_17" {
			t.Fatalf("%v accepted: %d", keys, res.Code)
		}
	}
}

func TestMenuRowsAreEditableInPlaceAndDraggable(t *testing.T) {
	body := renderPage("menu", samplePageData(langRU))
	for _, want := range []string{`data-key="winiso_2"`, `class="drag-handle"`, `form="item-winiso_2"`, `name="title" value="Windows 11"`, `name="folder"`, `/assets/menu-order.js`} {
		if !strings.Contains(body, want) {
			t.Fatalf("menu lacks %q: %s", want, body)
		}
	}
	for _, gone := range []string{"/move\"", "<summary>Изменить</summary>"} {
		if strings.Contains(body, gone) {
			t.Fatalf("menu still has %q", gone)
		}
	}
}

func TestObserverMenuRowsAreReadOnly(t *testing.T) {
	data := samplePageData(langRU)
	data.Info.User.Role = "observer"
	body := renderPage("menu", data)
	if strings.Contains(body, "drag-handle") || strings.Contains(body, `name="title"`) {
		t.Fatalf("observer can edit: %s", body)
	}
}

func TestRestoreFromMenuPageStaysOnMenu(t *testing.T) {
	store := newDraftStore(t, "operator", []MenuDraftItem{{Key: "winiso_17", Title: "Windows 11"}})
	store.draft = &MenuDraft{Title: "PXE Platform", Label: "Diagnostics", Message: "Ready"}
	s, _ := draftServer(t, store)
	body := libraryGet(t, s, "/menu")
	if !strings.Contains(body, `action="/menu/items/winiso_17/restore"`) {
		t.Fatalf("restore form missing: %s", body)
	}
	res := menuPost(t, s, "/menu/items/winiso_17/restore", url.Values{})
	if res.Code != http.StatusSeeOther || !strings.HasPrefix(res.Header().Get("Location"), "/menu?") || len(store.draft.Items) != 1 {
		t.Fatalf("status=%d location=%q draft=%+v", res.Code, res.Header().Get("Location"), store.draft)
	}
}
