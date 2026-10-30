package control

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestMenuOrderSavesNewOrder(t *testing.T) {
	store := newDraftStore(t, "operator", nil)
	store.draft = &MenuDraft{Title: "oh my boot_", Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{InstallationID: 1017, Title: "A"}, {InstallationID: 2031, Title: "B"}, {InstallationID: 3001, Title: "C", Folder: "Lab"}}}
	s, published := draftServer(t, store)
	res := menuPost(t, s, "/menu/order", url.Values{"key": {"install_3001", "install_1017", "install_2031"}})
	got := []string{}
	for _, item := range store.draft.Items {
		got = append(got, menuDraftItemKey(item)+"|"+item.Title+"|"+item.Folder)
	}
	if res.Code != http.StatusSeeOther || strings.Join(got, ",") != "install_3001|C|Lab,install_1017|A|,install_2031|B|" || len(*published) != 0 {
		t.Fatalf("status=%d order=%v body=%s", res.Code, got, res.Body.String())
	}
}

func TestMenuOrderRejectsDifferentItems(t *testing.T) {
	store := newDraftStore(t, "operator", nil)
	store.draft = &MenuDraft{Title: "oh my boot_", Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{InstallationID: 1017}, {InstallationID: 2031}}}
	s, _ := draftServer(t, store)
	for _, keys := range [][]string{{"install_1017"}, {"install_1017", "install_1017"}, {"install_1017", "install_3001"}} {
		res := menuPost(t, s, "/menu/order", url.Values{"key": keys})
		if res.Code < 400 || menuDraftItemKey(store.draft.Items[0]) != "install_1017" {
			t.Fatalf("%v accepted: %d", keys, res.Code)
		}
	}
}

func TestMenuStructureIsDraggableAndInspectorEdits(t *testing.T) {
	body := renderPage("menu", samplePageData(langRU))
	for _, want := range []string{`id="menu-items"`, `data-key="install_1002" data-parent="folder_1"`, `class="drag-handle"`, `/assets/menu-order.js`, `action="/menu/items/install_1002/title"`, `name="title" value="Windows 11"`, `name="folder"`, `action="/menu/items/install_1002/move"`, `class="screen-row selected"`, "!! WIPES DISK", `<small class="mono">install_2003</small>`} {
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
	store := newDraftStore(t, "operator", []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11"}})
	store.draft = &MenuDraft{Title: "oh my boot_", Label: "Diagnostics", Message: "Ready"}
	s, _ := draftServer(t, store)
	body := libraryGet(t, s, "/menu")
	if !strings.Contains(body, `action="/menu/items/install_1017/restore"`) {
		t.Fatalf("restore form missing: %s", body)
	}
	res := menuPost(t, s, "/menu/items/install_1017/restore", url.Values{})
	if res.Code != http.StatusSeeOther || !strings.HasPrefix(res.Header().Get("Location"), "/menu?") || len(store.draft.Items) != 1 {
		t.Fatalf("status=%d location=%q draft=%+v", res.Code, res.Header().Get("Location"), store.draft)
	}
}
