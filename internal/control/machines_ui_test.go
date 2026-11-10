package control

import (
	"net/url"
	"strings"
	"testing"
)

func TestMachinesPageHasNoDetails(t *testing.T) {
	for _, lang := range []string{langRU, langEN} {
		store := newRulesBook("admin")
		store.user.Language = lang
		s := &Server{Store: store}

		body := libraryGet(t, s, "/machines")
		mainStart := strings.Index(body, "<main")
		if mainStart < 0 {
			t.Fatalf("missing <main> in /machines (%s)", lang)
		}
		mainBody := body[mainStart:]
		if strings.Contains(mainBody, "<details") {
			t.Errorf("machines page (%s) must not contain <details> inside <main>", lang)
		}
		if strings.Contains(mainBody, "<summary") {
			t.Errorf("machines page (%s) must not contain <summary> inside <main>", lang)
		}
	}
}

func TestMachinesPageDialogsAndRoleRights(t *testing.T) {
	store := machinesFixture()
	s := &Server{Store: store}

	// 1. Observer: no add buttons, no edit dialogs, no delete buttons
	store.user.Role = "observer"
	obsBody := libraryGet(t, s, "/machines")
	for _, forbidden := range []string{
		"data-machine-add-open",
		"data-machine-import-open",
		"data-machine-edit-btn",
		"data-rule-add-open",
		"data-rule-edit-btn",
		"machine-create-dialog",
		"machine-import-dialog",
		"machine-edit-dialog",
		"rule-create-dialog",
		"rule-edit-dialog",
	} {
		if strings.Contains(obsBody, forbidden) {
			t.Errorf("observer should not see %q", forbidden)
		}
	}

	// 2. Operator: sees add buttons and edit dialogs, but not delete forms
	store.user.Role = "operator"
	opBody := libraryGet(t, s, "/machines")
	for _, required := range []string{
		"data-machine-add-open",
		"data-machine-import-open",
		"data-machine-edit-btn",
		"machine-create-dialog",
		"machine-import-dialog",
		"machine-edit-dialog",
		`data-mac="bc:24:11:52:02:37"`,
	} {
		if !strings.Contains(opBody, required) {
			t.Errorf("operator must see %q", required)
		}
	}
	if strings.Contains(opBody, `id="machine-delete-form"`) || strings.Contains(opBody, `id="rule-delete-form"`) || strings.Contains(opBody, "/delete") {
		t.Errorf("operator must not see delete controls")
	}

	// 3. Admin: sees delete forms in edit dialogs
	store.user.Role = "admin"
	adminBody := libraryGet(t, s, "/machines")
	if !strings.Contains(adminBody, `id="machine-delete-form"`) {
		t.Errorf("admin must see machine delete form")
	}
}

func TestMachinesImportPreviewErrorsHaveNoDetails(t *testing.T) {
	store := operatorBook()
	s := &Server{Store: store}

	res := csvUpload(t, s, "MAC;Имя\nbad-mac;test\n")
	body := res.Body.String()
	if strings.Contains(body, "<details") || strings.Contains(body, "<summary") {
		t.Fatalf("import preview with errors must not contain <details> or <summary>: %s", body)
	}
	if !strings.Contains(body, "Строки с ошибками — они будут пропущены") || !strings.Contains(body, "bad-mac") {
		t.Fatalf("import preview missing error block: %s", body)
	}
}

func TestMachinesDialogAutoOpenOnValidationError(t *testing.T) {
	store := machinesFixture()
	store.user.Role = "operator"
	s := &Server{Store: store}

	res := menuPost(t, s, "/machines", url.Values{
		"form": {"machine"},
		"mac":  {"invalid-mac"},
		"name": {"broken-box"},
	})
	body := res.Body.String()
	if !strings.Contains(body, `id="machine-create-dialog"`) || !strings.Contains(body, `data-auto-open="true"`) {
		t.Fatalf("expected machine-create-dialog to have data-auto-open on validation error:\n%s", body)
	}
}

func TestMachinesScriptEndpoint(t *testing.T) {
	store := machinesFixture()
	s := &Server{Store: store}
	js := libraryGet(t, s, "/assets/machines.js")
	for _, want := range []string{
		"machine-create-dialog",
		"machine-import-dialog",
		"machine-edit-dialog",
		"rule-create-dialog",
		"rule-edit-dialog",
		"data-machine-edit-btn",
		"data-rule-edit-btn",
		"showModal",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("/assets/machines.js missing %q", want)
		}
	}
}

func TestMachinesPageHeaderActionsAndVisualAccents(t *testing.T) {
	store := newRulesBook("admin")
	r := store.rules["bc:24:11"]
	r.NetworkDriver = "snp"
	store.rules["bc:24:11"] = r
	s := &Server{Store: store}

	body := libraryGet(t, s, "/machines")

	headEnd := strings.Index(body, `<div class="toolbar">`)
	if headEnd < 0 {
		t.Fatalf("missing toolbar in /machines")
	}
	headPart := body[:headEnd]
	toolbarAndRest := body[headEnd:]

	if !strings.Contains(headPart, `class="btn machine-export"`) {
		t.Errorf("expected Скачать CSV (.machine-export) inside .page-head actions")
	}
	if strings.Contains(toolbarAndRest, `machine-export`) {
		t.Errorf("expected Скачать CSV (.machine-export) not to be repeated in .toolbar")
	}
	for _, want := range []string{
		`<svg class="btn-icon"`,
		`class="machine-name-accent">test-vm-17</a>`,
		`<span class="chip">UEFI</span>`,
		`<span class="chip">BIOS</span>`,
		`<td class="mono rule-prefix">bc:24:11</td>`,
		`<span class="chip">прошивки (snponly)</span>`,
		`class="mono rule-count">2</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/machines missing visual accent %q", want)
		}
	}
}
