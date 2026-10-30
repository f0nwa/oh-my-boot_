package control

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMenuChangesNameTheEditedSettingsAndValues(t *testing.T) {
	active := MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", LocalTimeout: 10, Items: []MenuDraftItem{{InstallationID: 1001, Title: "Windows 10", Folder: "Windows"}, {InstallationID: 1002, Title: "Windows 11"}}}
	draft := MenuDraft{Title: productMenuTitle, Label: "Checks", Message: "Done", NoDiagnostics: true, NoLocalTimer: true, Items: []MenuDraftItem{{InstallationID: 1002, Title: "Windows 11"}, {InstallationID: 1001, Title: "Win 10", Folder: "Lab"}}}
	changes := diffMenuDraft(active, draft)
	want := map[string][2]string{
		"Пункт Diagnostics":            {"Показан", "Скрыт"},
		"Название Diagnostics":         {"Diagnostics", "Checks"},
		"Сообщение Diagnostics":        {"Ready", "Done"},
		"Автозагрузка с диска":         {"10 с", "Выключена"},
		"Название пункта install_1001": {"Windows 10", "Win 10"},
		"Каталог пункта install_1001":  {"Windows", "Lab"},
		"Порядок пунктов":              {"Windows 10 → Windows 11", "Windows 11 → Win 10"},
	}
	for _, change := range changes {
		key := change.Label
		if change.Key != "" {
			key += " " + change.Key
		}
		if expected, ok := want[key]; ok {
			if change.Before != expected[0] || change.After != expected[1] {
				t.Errorf("%s: %q → %q, want %q → %q", key, change.Before, change.After, expected[0], expected[1])
			}
			delete(want, key)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing specific changes: %+v; got %+v", want, changes)
	}
}

func TestDraftBarOpensChangesInsteadOfReloadingMenu(t *testing.T) {
	data := samplePageData(langRU)
	data.Info.Draft = draftSummary{Other: 1}
	data.Info.Path = "/installations"
	res := httptest.NewRecorder()
	render(res, "installations", data)
	if !strings.Contains(res.Body.String(), `href="/menu#menu-changes"`) {
		t.Fatal("draft bar does not link to the change list")
	}
	data.Info.Path = "/menu"
	res = httptest.NewRecorder()
	render(res, "menu", data)
	body := res.Body.String()
	if strings.Contains(body, `href="/menu#menu-changes"`) || !strings.Contains(body, `id="menu-changes" open`) {
		t.Fatal("menu page should show the expanded change list without a reload link")
	}
}

func TestEnglishMenuChangesTranslateValues(t *testing.T) {
	data := samplePageData(langEN)
	active := MenuDraft{Label: "Diagnostics", LocalTimeout: 10}
	draft := MenuDraft{Label: "Checks", NoDiagnostics: true, LocalTimeout: 20}
	data.Changes = diffMenuDraft(active, draft)
	data.Now = &nowView{}
	for _, page := range []string{"menu", "now"} {
		res := httptest.NewRecorder()
		render(res, page, data)
		body := res.Body.String()
		for _, want := range []string{"Diagnostics item", "Shown", "Hidden", "Automatic local disk boot", "10 s", "20 s"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: English change list lacks %q", page, want)
			}
		}
		if found := cyrillic.FindString(body); found != "" {
			t.Errorf("%s: English change list contains Russian text %q", page, found)
		}
	}
}
