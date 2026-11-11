package control

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
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
		"machines-table-area",
		"runSearch",
		"AbortController",
		"setLoading",
		"is-loading",
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

	if !strings.Contains(body, `id="machines-table-area"`) {
		t.Errorf("expected #machines-table-area wrapper in /machines")
	}
	if !strings.Contains(body, `name="q" value="" placeholder="Имя, MAC, модель, серийный номер" autocomplete="off"`) {
		t.Errorf("expected autocomplete=off on machines search input")
	}
	if !strings.Contains(body, `<span class="search-spinner" aria-hidden="true"></span>`) {
		t.Errorf("expected search-spinner element inside machines search form")
	}

	css := libraryGet(t, s, "/assets/style.css")
	if !strings.Contains(css, `.toolbar .machine-search .search-spinner`) {
		t.Errorf("expected style.css to style .toolbar .machine-search .search-spinner")
	}
	if !strings.Contains(css, `.toolbar .machine-search.is-loading .search-spinner`) {
		t.Errorf("expected style.css to handle .toolbar .machine-search.is-loading .search-spinner")
	}
	if !strings.Contains(css, `#machines-table-area.is-loading`) {
		t.Errorf("expected style.css to handle #machines-table-area.is-loading")
	}

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

func TestMachinesPagePaginationAndPartialRows(t *testing.T) {
	t0 := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	store := &bookStore{
		authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}},
		machines:      make(map[string]MachineInfo, 120),
	}
	for i := 1; i <= 120; i++ {
		mac := fmt.Sprintf("bc:24:11:00:%02x:%02x", i/256, i%256)
		store.machines[mac] = MachineInfo{
			MAC:       mac,
			Name:      fmt.Sprintf("node-%03d", i),
			FirstSeen: t0.Add(time.Duration(121-i) * time.Minute),
			LastSeen:  t0.Add(time.Duration(121-i) * time.Minute),
		}
	}
	s := &Server{Store: store}

	// 1. Initial page GET /machines: should render first 50 machines and sentinel
	body := libraryGet(t, s, "/machines")
	if !strings.Contains(body, "node-001") {
		t.Errorf("GET /machines expected to contain node-001")
	}
	if !strings.Contains(body, "node-050") {
		t.Errorf("GET /machines expected to contain node-050")
	}
	if strings.Contains(body, "node-051") {
		t.Errorf("GET /machines should not contain node-051 (offset=0, limit=50)")
	}
	if !strings.Contains(body, `data-has-more="true"`) {
		t.Errorf("GET /machines expected data-has-more=\"true\"")
	}
	if !strings.Contains(body, `data-next-offset="50"`) {
		t.Errorf("GET /machines expected data-next-offset=\"50\"")
	}
	if !strings.Contains(body, `class="infinite-scroll-sentinel"`) {
		t.Errorf("GET /machines expected .infinite-scroll-sentinel container")
	}

	// 2. Partial page GET /machines?offset=50&partial=1: should render rows 51-100 and sentinel without full layout
	partial := libraryGet(t, s, "/machines?offset=50&partial=1")
	if strings.Contains(partial, "<!DOCTYPE") || strings.Contains(partial, "<html") || strings.Contains(partial, "<main") {
		t.Errorf("GET /machines?partial=1 should not render full page layout")
	}
	if strings.Contains(partial, "node-001") || strings.Contains(partial, "node-050") {
		t.Errorf("GET /machines?offset=50&partial=1 should not contain node-001..node-050")
	}
	if !strings.Contains(partial, "node-051") {
		t.Errorf("GET /machines?offset=50&partial=1 expected to contain node-051")
	}
	if !strings.Contains(partial, "node-100") {
		t.Errorf("GET /machines?offset=50&partial=1 expected to contain node-100")
	}
	if strings.Contains(partial, "node-101") {
		t.Errorf("GET /machines?offset=50&partial=1 should not contain node-101")
	}
	if !strings.Contains(partial, `data-has-more="true"`) {
		t.Errorf("GET /machines?offset=50&partial=1 expected data-has-more=\"true\"")
	}
	if !strings.Contains(partial, `data-next-offset="100"`) {
		t.Errorf("GET /machines?offset=50&partial=1 expected data-next-offset=\"100\"")
	}

	// 3. CSV export GET /machines.csv: MUST export all 120 matched machines
	csvData := libraryGet(t, s, "/machines.csv")
	for i := 1; i <= 120; i++ {
		name := fmt.Sprintf("node-%03d", i)
		if !strings.Contains(csvData, name) {
			t.Fatalf("CSV export missing machine %s", name)
		}
	}
}
