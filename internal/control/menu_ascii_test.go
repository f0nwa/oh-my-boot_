package control

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestMenuFormsRejectCyrillicText(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		form       url.Values
	}{
		{"diagnostics label", "/menu/diagnostics", url.Values{"label": {"Диагностика"}, "message": {"Ready"}}},
		{"diagnostics message", "/menu/diagnostics", url.Values{"label": {"Diagnostics"}, "message": {"Готово"}}},
		{"Windows folder", "/menu/items/install_1017/title", url.Values{"title": {"Windows 11"}, "folder": {"Windows/Установщики"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newDraftStore(t, "operator", []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11"}})
			store.draft = &MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11", Folder: "Windows"}}}
			before := *store.draft
			s, _ := draftServer(t, store)
			res := menuPost(t, s, tc.path, tc.form)
			if res.Code != http.StatusBadRequest || !reflect.DeepEqual(*store.draft, before) {
				t.Fatalf("accepted Cyrillic: status=%d draft=%+v", res.Code, store.draft)
			}
		})
	}
}

func TestDiagnosticsFormExplainsLatinOnlyInput(t *testing.T) {
	store := newDraftStore(t, "operator", nil)
	store.draft = &MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready"}
	s, _ := draftServer(t, store)
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, accountRequest(http.MethodGet, "/menu?item=diagnostics", nil))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "Консоль загрузки не показывает кириллицу.") {
		t.Fatalf("Diagnostics form does not explain the restriction: status=%d", res.Code)
	}
}
