package control

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestDiagnosticsSettingsBelongToDiagnosticsItem(t *testing.T) {
	store := newDraftStore(t, "operator", nil)
	store.draft = &MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", LocalTimeout: 25}
	s, _ := draftServer(t, store)

	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, accountRequest(http.MethodGet, "/menu?item=diagnostics", nil))
	body := res.Body.String()
	if res.Code != http.StatusOK || !strings.Contains(body, `action="/menu/diagnostics"`) || !strings.Contains(body, `name="label" value="Diagnostics"`) || !strings.Contains(body, `name="message" value="Ready"`) {
		t.Fatalf("diagnostics properties are missing: status=%d", res.Code)
	}
	if strings.Contains(body, `action="/menu/settings"`) {
		t.Fatal("Ubuntu settings are shown with Diagnostics")
	}

	res = menuPost(t, s, "/menu/diagnostics", url.Values{"label": {"Health check"}, "message": {"All clear"}, "diagnostics_on": {"on"}})
	if res.Code != http.StatusSeeOther || !strings.Contains(res.Header().Get("Location"), "item=diagnostics") || store.draft.Label != "Health check" || store.draft.Message != "All clear" || store.draft.LocalTimeout != 25 {
		t.Fatalf("status=%d location=%q draft=%+v", res.Code, res.Header().Get("Location"), store.draft)
	}
	if res := menuPost(t, s, "/menu/diagnostics", url.Values{"label": {""}, "message": {"All clear"}}); res.Code != http.StatusBadRequest || store.draft.Label != "Health check" {
		t.Fatalf("invalid diagnostics accepted: status=%d draft=%+v", res.Code, store.draft)
	}

}

func TestDiagnosticsCanBeDisabledInDraftAndPublishedScript(t *testing.T) {
	base := MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", look: menuLook}
	onScript, err := buildTestMenuFromDraft(base, testBootURL, nil, nil, nil)
	if err != nil || !strings.Contains(string(onScript), "item diagnostics Diagnostics\n") {
		t.Fatalf("enabled diagnostics missing: err=%v script=%s", err, onScript)
	}
	onBack := draftFromVersion(MenuVersion{Title: base.Title, Label: base.Label, Message: base.Message, Script: string(onScript)})
	if !onBack.DiagnosticsOn() {
		t.Fatalf("enabled version read as disabled: %+v", onBack)
	}
	store := newDraftStore(t, "operator", nil)
	store.draft = &base
	s, _ := draftServer(t, store)
	if res := menuPost(t, s, "/menu/diagnostics", url.Values{"label": {"Diagnostics"}, "message": {"Ready"}}); res.Code != http.StatusSeeOther {
		t.Fatalf("status=%d", res.Code)
	}
	offScript, err := buildTestMenuFromDraft(*store.draft, testBootURL, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(offScript), "item diagnostics ") || strings.Contains(string(offScript), ":diagnostics\n") || !strings.Contains(string(offScript), "item local Boot local disk\n") {
		t.Fatalf("disabled diagnostics remains in client menu: %s", offScript)
	}
	back := draftFromVersion(MenuVersion{Title: base.Title, Label: base.Label, Message: base.Message, Script: string(offScript), NoDiagnostics: true, Items: []MenuVersionItem{}})
	backScript, err := buildTestMenuFromDraft(back, testBootURL, nil, nil, nil)
	if err != nil || strings.Contains(string(backScript), "item diagnostics ") {
		t.Fatalf("disabled version restored diagnostics: err=%v script=%s", err, backScript)
	}
	if changes := diffMenuDraft(base, *store.draft); len(changes) != 1 || changes[0].Kind != "settings" {
		t.Fatalf("disabling diagnostics is not a pending change: %+v", changes)
	}
	profiles, answers, configs := draftFixture()
	withItems := *store.draft
	withItems.Items = []MenuDraftItem{{InstallationID: 1017}}
	withItemsScript, err := buildTestMenuFromDraft(withItems, testBootURL, profiles, answers, configs)
	if err != nil || strings.Contains(string(withItemsScript), "item diagnostics ") || !strings.Contains(string(withItemsScript), "item install_1017 ") {
		t.Fatalf("disabled diagnostics remains in populated menu: err=%v script=%s", err, withItemsScript)
	}
}

func TestDiagnosticsToggleStaysAvailableWhenOff(t *testing.T) {
	store := newDraftStore(t, "operator", nil)
	store.draft = &MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready"}
	s, _ := draftServer(t, store)
	form := url.Values{"label": {"Diagnostics"}, "message": {"Ready"}}
	res := menuPost(t, s, "/menu/diagnostics", form)
	if res.Code != http.StatusSeeOther || !strings.Contains(res.Header().Get("Location"), "item=diagnostics") {
		t.Fatalf("diagnostics not disabled: status=%d location=%q draft=%+v", res.Code, res.Header().Get("Location"), store.draft)
	}
	page := httptest.NewRecorder()
	s.Handler().ServeHTTP(page, accountRequest(http.MethodGet, "/menu?item=diagnostics", nil))
	body := page.Body.String()
	if page.Code != http.StatusOK || !strings.Contains(body, `name="diagnostics_on"`) || !strings.Contains(body, `tree-off`) || !strings.Contains(body, `href="/menu?item=diagnostics"`) {
		t.Fatalf("disabled item cannot be selected or re-enabled: status=%d", page.Code)
	}
	screen := body[strings.Index(body, `<section class="screen-frame"`):]
	screen = screen[:strings.Index(screen, "</section>")]
	if strings.Contains(screen, `href="/menu?item=diagnostics"`) {
		t.Fatal("disabled diagnostics still appears on the machine screen")
	}
	form.Set("diagnostics_on", "on")
	if res := menuPost(t, s, "/menu/diagnostics", form); res.Code != http.StatusSeeOther {
		t.Fatalf("diagnostics not re-enabled: status=%d draft=%+v", res.Code, store.draft)
	}
	enabledScript, err := buildTestMenuFromDraft(*store.draft, testBootURL, nil, nil, nil)
	if err != nil || !strings.Contains(string(enabledScript), "item diagnostics Diagnostics\n") {
		t.Fatalf("diagnostics not restored on machine screen: err=%v script=%s", err, enabledScript)
	}
}
