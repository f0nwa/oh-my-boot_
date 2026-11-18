package control

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"omb/internal/images"
)

func TestImageMigrateUI(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)

	// Create admin and observer users
	if err := store.CreateUser(ctx, "adminui", "AdminPass1234!", "admin"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateUser(ctx, "obsui", "ObserverPass123!", "observer"); err != nil {
		t.Fatal(err)
	}

	_, adminToken, err := store.Login(ctx, "adminui", "AdminPass1234!")
	if err != nil {
		t.Fatal(err)
	}
	_, obsToken, err := store.Login(ctx, "obsui", "ObserverPass123!")
	if err != nil {
		t.Fatal(err)
	}

	mockImg := &migrationMockImages{
		fakeImagesAPI: fakeImagesAPI{
			catalog: images.Catalog{
				Storages: []images.Storage{
					{ID: 1, Name: "Локальный диск", Kind: "local", Root: "/srv/omb/iso", State: "ready", Uploads: true, Free: 50 << 30},
					{ID: 2, Name: "Synology NFS", Kind: "nfs", Root: "/mnt/omb-nfs/nas", State: "ready", Uploads: true, Free: 100 << 30},
					{ID: 3, Name: "Архив RO", Kind: "nfs", Root: "/mnt/omb-nfs/ro", State: "ready", Uploads: false, Free: 100 << 30},
					{ID: 4, Name: "Переполненный SMB", Kind: "smb", Root: "/mnt/omb-smb/full", State: "ready", Uploads: true, Free: 100 << 20}, // 100 МБ
				},
				Images: []images.Image{
					{StorageID: 1, Path: "win11.iso", Size: 5 << 30, Present: true},
					{StorageID: 1, Path: "ubuntu.iso", Size: 3 << 30, Present: true},
				},
			},
		},
		migrations: map[string]*images.MigrationJob{
			"mig_active": {
				ID:              "mig_active",
				SourceStorageID: 1,
				Path:            "ubuntu.iso",
				TargetStorageID: 2,
				State:           "copying",
				Percent:         45,
				CopiedBytes:     1350 << 20,
				TotalBytes:      3000 << 20,
			},
		},
	}
	mockAgent := &migrationMockAgent{}

	s := &Server{
		Store:      store,
		Images:     mockImg,
		WindowsISO: mockAgent,
		UbuntuISO:  mockAgent,
	}

	// 1. Admin views /images
	reqAdmin := httptest.NewRequest(http.MethodGet, "/images", nil)
	reqAdmin.AddCookie(&http.Cookie{Name: "omb_session", Value: adminToken})
	recAdmin := httptest.NewRecorder()
	s.Handler().ServeHTTP(recAdmin, reqAdmin)

	if recAdmin.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", recAdmin.Code)
	}
	bodyAdmin := recAdmin.Body.String()

	// Check menu action present
	if !strings.Contains(bodyAdmin, "Переместить в другое хранилище…") {
		t.Fatalf("expected 'Переместить в другое хранилище…' in admin page")
	}

	// Check modal dialog rendered
	if !strings.Contains(bodyAdmin, "image-migrate-dialog") || !strings.Contains(bodyAdmin, "Перемещение образа") {
		t.Fatalf("expected image-migrate-dialog in admin page")
	}

	// Check options in target storage select
	if !strings.Contains(bodyAdmin, "Synology NFS") {
		t.Fatalf("expected Synology NFS option in select")
	}
	// Check RO option disabled
	if !strings.Contains(bodyAdmin, "Архив RO · NFS · только чтение") && !strings.Contains(bodyAdmin, "Архив RO") {
		t.Fatalf("expected Архив RO in select")
	}
	if !strings.Contains(bodyAdmin, "только чтение") {
		t.Fatalf("expected 'только чтение' note for RO storage")
	}
	// Check insufficient space option disabled
	if !strings.Contains(bodyAdmin, "недостаточно места") {
		t.Fatalf("expected 'недостаточно места' note for full storage")
	}

	// Check submit button is initially disabled
	if !strings.Contains(bodyAdmin, `<button type="submit" class="btn btn-primary" disabled>`) {
		t.Fatalf("expected submit button in image-migrate-dialog to be disabled initially")
	}

	// Check active migration indicator for ubuntu.iso is in "Загрузки и состояние" column, not in ISO file cell
	migRowIdx := strings.Index(bodyAdmin, `data-path="ubuntu.iso"`)
	if migRowIdx < 0 {
		t.Fatalf("expected ubuntu.iso row")
	}
	migRowEnd := strings.Index(bodyAdmin[migRowIdx:], "</tr>")
	if migRowEnd < 0 {
		t.Fatalf("expected closing </tr> for ubuntu.iso row")
	}
	migRowHTML := bodyAdmin[migRowIdx : migRowIdx+migRowEnd]
	pathCellEnd := strings.Index(migRowHTML, "</td>")
	if pathCellEnd < 0 {
		t.Fatalf("expected closing </td> for path-cell")
	}
	pathCellHTML := migRowHTML[:pathCellEnd]
	if strings.Contains(pathCellHTML, "image-migrating-status") || strings.Contains(pathCellHTML, "progress-bar-micro") {
		t.Fatalf("path-cell must not contain migration progress indicator: %s", pathCellHTML)
	}
	statusCellIdx := strings.Index(migRowHTML, `class="image-status-cell"`)
	if statusCellIdx < 0 {
		t.Fatalf("expected image-status-cell in migrating row")
	}
	statusCellHTML := migRowHTML[statusCellIdx:]
	for _, want := range []string{
		`class="status image-library-status" hidden`,
		`class="image-migrating-status"`,
		`class="status status-migrating"`,
		`class="dot dot-migrating"`,
		"Перемещается…",
		"45%",
		`class="progress-bar-micro"`,
		`width: 45%`,
	} {
		if !strings.Contains(statusCellHTML, want) {
			t.Fatalf("expected %q inside image-status-cell, got: %s", want, statusCellHTML)
		}
	}
	if !strings.Contains(bodyAdmin, `data-migration-id="mig_active"`) {
		t.Fatalf("expected active migration row to carry data-migration-id=\"mig_active\"")
	}

	// Check switching state label on rebinding
	mockImg.migrations["mig_active"].State = "rebinding"
	mockImg.migrations["mig_active"].Percent = 100
	recRebind := httptest.NewRecorder()
	reqRebind := httptest.NewRequest(http.MethodGet, "/images", nil)
	reqRebind.AddCookie(&http.Cookie{Name: "omb_session", Value: adminToken})
	s.Handler().ServeHTTP(recRebind, reqRebind)
	if recRebind.Code != http.StatusOK || !strings.Contains(recRebind.Body.String(), "Переключение…") {
		t.Fatalf("expected 'Переключение…' during rebinding")
	}

	// 2. Observer views /images
	reqObs := httptest.NewRequest(http.MethodGet, "/images", nil)
	reqObs.AddCookie(&http.Cookie{Name: "omb_session", Value: obsToken})
	recObs := httptest.NewRecorder()
	s.Handler().ServeHTTP(recObs, reqObs)

	if recObs.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for observer, got %d", recObs.Code)
	}
	bodyObs := recObs.Body.String()

	// Observer must not have migrate action or migrate dialogs
	if strings.Contains(bodyObs, "data-image-migrate-open") || strings.Contains(bodyObs, "image-migrate-dialog") {
		t.Fatalf("observer page must not contain migration dialog or trigger")
	}

	// 3. Script /assets/image-migrate.js is served
	reqJS := httptest.NewRequest(http.MethodGet, "/assets/image-migrate.js", nil)
	recJS := httptest.NewRecorder()
	s.Handler().ServeHTTP(recJS, reqJS)
	if recJS.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for /assets/image-migrate.js, got %d", recJS.Code)
	}
	if !strings.Contains(recJS.Body.String(), "data-image-migrate-open") {
		t.Fatalf("script does not contain expected code")
	}
}

func TestImageMigrateScriptIgnoresStaleCompletedJobs(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	jsTest := `
const fs = require('node:fs');
const assert = require('node:assert/strict');
const vm = require('node:vm');

const code = fs.readFileSync('image_migrate.js', 'utf8');

function makeClassList(initial = []) {
  const set = new Set(initial);
  return {
    add: (c) => set.add(c),
    remove: (c) => set.delete(c),
    contains: (c) => set.has(c),
  };
}

function createHarness(initialMigrationId) {
  let reloadCount = 0;
  let fetchCalls = 0;
  let queuedJobs = [];
  const listeners = {};
  const labelEl = { textContent: '' };
  const pctEl = { textContent: '0%' };
  const barEl = { style: { width: '0%' } };
  const libStatusEl = { hidden: false };
  const migStatusEl = { hidden: true };
  const menuBtnEl = { disabled: false, title: '' };
  const rowEl = {
    classList: makeClassList(initialMigrationId ? ['is-migrating'] : []),
    dataset: initialMigrationId ? { migrationId: initialMigrationId } : {},
    querySelector: (sel) => {
      switch (sel) {
        case '.image-library-status': return libStatusEl;
        case '.image-migrating-status': return migStatusEl;
        case '.migrating-label': return labelEl;
        case '.migrating-pct': return pctEl;
        case '.progress-bar-fill': return barEl;
        case '.row-menu-btn': return menuBtnEl;
        default: return null;
      }
    },
  };
  const cellEl = {
    dataset: { storageId: '6', path: 'win8.iso' },
    closest: (sel) => (sel === 'tr' ? rowEl : null),
  };
  const document = {
    documentElement: { lang: 'ru' },
    querySelectorAll: (sel) => {
      if (sel === '.path-cell') return [cellEl];
      if (sel === '.is-migrating') return rowEl.classList.contains('is-migrating') ? [rowEl] : [];
      return [];
    },
    querySelector: (sel) => {
      if (sel === '.is-migrating' && rowEl.classList.contains('is-migrating')) return rowEl;
      return null;
    },
    addEventListener: (type, fn) => { listeners[type] = fn; },
    getElementById: () => null,
  };
  let scheduledTimers = [];
  let nextTimerId = 1;
  const ctx = {
    document,
    window: {
      location: {
        reload: () => { reloadCount++; },
      },
    },
    alert: () => {},
    FormData: class { constructor() {} },
    URLSearchParams: class { constructor() {} },
    setInterval: (fn, ms) => {
      const id = nextTimerId++;
      scheduledTimers.push({ id, fn, ms, interval: true, cleared: false });
      return id;
    },
    clearInterval: (id) => {
      const t = scheduledTimers.find(x => x.id === id);
      if (t) t.cleared = true;
    },
    setTimeout: (fn, ms) => {
      const id = nextTimerId++;
      scheduledTimers.push({ id, fn, ms, interval: false, cleared: false });
      return id;
    },
    clearTimeout: (id) => {
      const t = scheduledTimers.find(x => x.id === id);
      if (t) t.cleared = true;
    },
    fetch: async () => {
      fetchCalls++;
      return { ok: true, json: async () => queuedJobs };
    },
  };
  vm.runInNewContext(code, ctx);
  return {
    rowEl, labelEl, pctEl, barEl,
    get reloadCount() { return reloadCount; },
    get fetchCalls() { return fetchCalls; },
    setJobs(j) { queuedJobs = j; },
    async tick() {
      const active = scheduledTimers.filter(t => !t.cleared);
      for (const t of active) {
        if (!t.interval) t.cleared = true;
        await t.fn();
      }
    },
    async flush() {
      await new Promise(r => setImmediate(r));
      await new Promise(r => setImmediate(r));
    },
  };
}

(async () => {
  const h = createHarness('mig_new');
  // Poll 1: stale completed job mig_old (same storage 6 -> 20) + active mig_new
  h.setJobs([
    { id: 'mig_old', source_storage_id: 6, path: 'win8.iso', target_storage_id: 20, state: 'completed', percent: 100 },
    { id: 'mig_new', source_storage_id: 6, path: 'win8.iso', target_storage_id: 20, state: 'copying', percent: 35 },
  ]);
  await h.flush();
  assert.equal(h.reloadCount, 0, 'stale completed job must NOT trigger reload while mig_new is active');
  assert.equal(h.pctEl.textContent, '35%');

  // Poll 2: mig_new finishes
  h.setJobs([
    { id: 'mig_old', source_storage_id: 6, path: 'win8.iso', target_storage_id: 20, state: 'completed', percent: 100 },
    { id: 'mig_new', source_storage_id: 6, path: 'win8.iso', target_storage_id: 20, state: 'completed', percent: 100 },
  ]);
  await h.tick();
  await h.flush();
  assert.equal(h.reloadCount, 1, 'completing mig_new must trigger exactly 1 reload');
  console.log('IMAGE_MIGRATE_JS_PASS');
})().catch((err) => { console.error(err); process.exit(1); });
`
	cmd := exec.CommandContext(ctx, "node", "-e", jsTest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node image_migrate.js test failed: %v\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "IMAGE_MIGRATE_JS_PASS") {
		t.Fatalf("missing pass marker: %s", string(out))
	}
}

func TestImageMigrateSubmitButtonGate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	scriptBytes, err := os.ReadFile("image_migrate.js")
	if err != nil {
		t.Fatalf("failed to read image_migrate.js: %v", err)
	}

	jsTest := fmt.Sprintf(`
const vm = require('node:vm');
const assert = require('node:assert/strict');

const code = %q;

let listeners = {};
const submitBtn = { disabled: true };
const targetSelect = { name: 'target_storage_id', value: '', closest: (sel) => (sel === '[data-image-migrate-form]' ? formEl : null) };
const formEl = {
  resetCalled: false,
  reset() {
    this.resetCalled = true;
    targetSelect.value = '';
  },
  querySelector(sel) {
    if (sel === '[name="target_storage_id"]') return targetSelect;
    if (sel === 'button[type="submit"]') return submitBtn;
    return null;
  },
  closest(sel) {
    if (sel === '[data-image-migrate-form]') return this;
    if (sel === 'dialog') return dialogEl;
    return null;
  },
};
const dialogEl = {
  shown: false,
  closed: false,
  showModal() { this.shown = true; },
  close() { this.closed = true; },
  querySelector(sel) {
    if (sel === 'form') return formEl;
    return null;
  },
};
const openBtn = {
  disabled: false,
  dataset: { imageMigrateOpen: 'dlg-1' },
  closest(sel) {
    if (sel === '[data-image-migrate-open]') return this;
    return null;
  },
};

const document = {
  documentElement: { lang: 'ru' },
  querySelectorAll: () => [],
  querySelector: () => null,
  getElementById: (id) => (id === 'dlg-1' ? dialogEl : null),
  addEventListener: (type, fn) => { listeners[type] = fn; },
};

const sandbox = {
  document,
  window: { location: { reload() {} } },
  alert: () => {},
  FormData: class { constructor() {} },
  URLSearchParams: class { constructor() {} },
  setTimeout: () => 1,
  clearTimeout: () => {},
  fetch: async () => ({ ok: true, json: async () => [] }),
};

vm.runInNewContext(code, sandbox);

// 1. Open dialog click: must reset form and keep submitBtn disabled
targetSelect.value = '6'; // Simulate leftover state from previous interaction
submitBtn.disabled = false;
listeners['click']({ target: openBtn });
assert.equal(formEl.resetCalled, true, 'opening dialog must reset form');
assert.equal(targetSelect.value, '', 'select value must be reset to empty');
assert.equal(submitBtn.disabled, true, 'submit button must be disabled upon opening');
assert.equal(dialogEl.shown, true, 'dialog must be shown');

// 2. Select target storage -> becomes enabled
targetSelect.value = '6';
listeners['change']({ target: targetSelect });
assert.equal(submitBtn.disabled, false, 'submit button must be enabled when target storage selected');

// 3. Reset target storage back to empty -> becomes disabled
targetSelect.value = '';
listeners['change']({ target: targetSelect });
assert.equal(submitBtn.disabled, true, 'submit button must be disabled when target storage cleared');

// 4. Input event also triggers enable
targetSelect.value = '20';
listeners['input']({ target: targetSelect });
assert.equal(submitBtn.disabled, false, 'submit button must be enabled on input event with storage');

console.log('BUTTON_GATE_JS_PASS');
`, string(scriptBytes))

	cmd := exec.CommandContext(ctx, "node", "-e", jsTest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node test failed: %v\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "BUTTON_GATE_JS_PASS") {
		t.Fatalf("missing pass marker: %s", string(out))
	}
}
