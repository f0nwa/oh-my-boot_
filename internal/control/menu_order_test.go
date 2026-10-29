package control

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestMenuOrderSavesNewOrder(t *testing.T) {
	store := newDraftStore(t, "operator", nil)
	store.draft = &MenuDraft{Title: "oh my boot_", Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{Key: "winiso_17", Title: "A"}, {Key: "winanswer_31", Title: "B"}, {Key: "windriver_1", Title: "C", Folder: "Lab"}}}
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
	store.draft = &MenuDraft{Title: "oh my boot_", Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{Key: "winiso_17"}, {Key: "winanswer_31"}}}
	s, _ := draftServer(t, store)
	for _, keys := range [][]string{{"winiso_17"}, {"winiso_17", "winiso_17"}, {"winiso_17", "windriver_1"}} {
		res := menuPost(t, s, "/menu/order", url.Values{"key": keys})
		if res.Code < 400 || store.draft.Items[0].Key != "winiso_17" {
			t.Fatalf("%v accepted: %d", keys, res.Code)
		}
	}
}

func TestMenuStructureIsDraggableAndInspectorEdits(t *testing.T) {
	body := renderPage("menu", samplePageData(langRU))
	for _, want := range []string{`id="menu-items"`, `data-key="winiso_2" data-parent="folder_1"`, `class="drag-handle"`, `/assets/menu-order.js`, `action="/menu/items/winiso_2/title"`, `name="title" value="Windows 11"`, `name="folder"`, `action="/menu/items/winiso_2/move"`, `class="screen-row selected"`, "!! WIPES DISK", `<small class="mono">winanswer_3</small>`} {
		if !strings.Contains(body, want) {
			t.Fatalf("menu lacks %q: %s", want, body)
		}
	}
	if strings.Contains(body, `data-key="diagnostics"`) {
		t.Fatal("built-in items must not be draggable")
	}
}

func TestObserverMenuRowsAreReadOnly(t *testing.T) {
	data := samplePageData(langRU)
	data.Info.User.Role = "observer"
	body := renderPage("menu", data)
	if strings.Contains(body, "drag-handle") || strings.Contains(body, `name="title"`) || strings.Contains(body, "/move") {
		t.Fatalf("observer can edit: %s", body)
	}
}

func TestRestoreFromMenuPageStaysOnMenu(t *testing.T) {
	store := newDraftStore(t, "operator", []MenuDraftItem{{Key: "winiso_17", Title: "Windows 11"}})
	store.draft = &MenuDraft{Title: "oh my boot_", Label: "Diagnostics", Message: "Ready"}
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
