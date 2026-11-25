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

func TestDiagnosticsFormUsesLiveASCIIValidation(t *testing.T) {
	store := newDraftStore(t, "operator", []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11", Folder: "Windows"}})
	store.draft = &MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11", Folder: "Windows"}}}
	s, _ := draftServer(t, store)

	// Diagnostics form: live validation hooks, no permanent Cyrillic hint.
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, accountRequest(http.MethodGet, "/menu?item=diagnostics", nil))
	body := res.Body.String()
	if res.Code != http.StatusOK {
		t.Fatalf("GET /menu?item=diagnostics = %d", res.Code)
	}
	if strings.Contains(body, "Консоль загрузки не показывает кириллицу.") {
		t.Fatalf("Diagnostics form should not show permanent Cyrillic hint")
	}
	for _, want := range []string{`id="diagnostics-label"`, `data-ascii-field="title"`, `id="diagnostics-label-error"`, `id="diagnostics-message-error"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("Diagnostics form missing %q", want)
		}
	}

	// Item inspector form: clean labels, placeholder, datalist, live validation hooks.
	res = httptest.NewRecorder()
	s.Handler().ServeHTTP(res, accountRequest(http.MethodGet, "/menu?item=install_1017", nil))
	body = res.Body.String()
	if res.Code != http.StatusOK {
		t.Fatalf("GET /menu?item=install_1017 = %d", res.Code)
	}
	if strings.Contains(body, "Консоль загрузки не показывает кириллицу.") || strings.Contains(body, "Латиницей, до 80 символов.") {
		t.Fatalf("Item inspector should not show permanent Cyrillic/Latin hints")
	}
	for _, want := range []string{
		`id="inspector-title"`, `data-ascii-field="title"`, `id="inspector-title-error"`,
		`id="inspector-folder"`, `placeholder="Главное меню"`, `list="menu-folders-list"`,
		`data-ascii-field="folder"`, `id="inspector-folder-error"`,
		`<datalist id="menu-folders-list">`,
		`<option value="Windows"></option>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("Item inspector missing %q", want)
		}
	}
}

func TestInstallationNewFormEmptyDefaultFolderAndLiveValidation(t *testing.T) {
	s, store := newInstallServer(t)
	store.installations = []Installation{
		{ID: 100, BootProfileID: 50, Title: "Win 10", Folder: "Windows"},
		{ID: 101, BootProfileID: 52, Title: "Ubuntu 24", Folder: "Linux/Ubuntu"},
		{ID: 102, BootProfileID: 51, Title: "Win 11", Folder: "Windows"},
	}
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, accountRequest(http.MethodGet, "/installations/new", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("GET /installations/new = %d", res.Code)
	}
	body := res.Body.String()

	// Folder input must default to empty (not "Windows"), with placeholder and datalist.
	if strings.Contains(body, `name="folder" value="Windows"`) || strings.Contains(body, `name="folder" value="Linux"`) || strings.Contains(body, `name="folder" value="Tools"`) {
		t.Fatalf("folder input must not prefill a default folder on new installation")
	}
	for _, forbidden := range []string{"Название, латиницей", "Консоль загрузки не показывает кириллицу.", "Пусто — главное меню."} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("installation_new form should not contain %q", forbidden)
		}
	}
	for _, want := range []string{
		`<label for="i-title">Название</label>`,
		`data-ascii-field="title"`,
		`id="i-title-error"`,
		`<label for="i-folder">Каталог</label>`,
		`name="folder" value="" placeholder="Главное меню" list="menu-folders-list" data-ascii-field="folder"`,
		`id="i-folder-error"`,
		`<datalist id="menu-folders-list"><option value="Linux/Ubuntu"></option><option value="Windows"></option></datalist>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("installation_new form missing %q", want)
		}
	}
}
