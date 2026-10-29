package control

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestUbuntuSettingsBelongToUbuntuItem(t *testing.T) {
	store := newDraftStore(t, "operator", nil)
	store.draft = &MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", Ubuntu: true, UbuntuFolder: "Linux"}
	s, _ := draftServer(t, store)

	page := httptest.NewRecorder()
	s.Handler().ServeHTTP(page, accountRequest(http.MethodGet, "/menu?item=ubuntu", nil))
	body := page.Body.String()
	if page.Code != http.StatusOK || !strings.Contains(body, `action="/menu/settings"`) || !strings.Contains(body, `name="ubuntu" checked`) || !strings.Contains(body, `name="folder_ubuntu" value="Linux"`) {
		t.Fatalf("Ubuntu properties are missing: status=%d", page.Code)
	}
	if strings.Contains(body, `<summary>Настройки меню</summary>`) {
		t.Fatal("old general settings block remains")
	}

	res := menuPost(t, s, "/menu/settings", url.Values{"folder_ubuntu": {"Lab"}})
	if res.Code != http.StatusSeeOther || !strings.Contains(res.Header().Get("Location"), "item=ubuntu") || store.draft.Ubuntu || store.draft.UbuntuFolder != "Lab" {
		t.Fatalf("Ubuntu was not disabled: status=%d location=%q draft=%+v", res.Code, res.Header().Get("Location"), store.draft)
	}
	page = httptest.NewRecorder()
	s.Handler().ServeHTTP(page, accountRequest(http.MethodGet, "/menu?item=ubuntu", nil))
	body = page.Body.String()
	if page.Code != http.StatusOK || !strings.Contains(body, `href="/menu?item=ubuntu"`) || !strings.Contains(body, `tree-off`) {
		t.Fatal("disabled Ubuntu cannot be selected in the editor")
	}
	screen := body[strings.Index(body, `<section class="screen-frame"`):]
	screen = screen[:strings.Index(screen, "</section>")]
	if strings.Contains(screen, `href="/menu?item=ubuntu"`) {
		t.Fatal("disabled Ubuntu appears on the machine screen")
	}
	res = menuPost(t, s, "/menu/settings", url.Values{"ubuntu": {"on"}, "folder_ubuntu": {"Lab"}})
	if res.Code != http.StatusSeeOther || !store.draft.Ubuntu {
		t.Fatalf("Ubuntu was not re-enabled: status=%d draft=%+v", res.Code, store.draft)
	}
}
