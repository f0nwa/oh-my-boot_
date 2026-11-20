package control

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"omb/internal/drivers"
)

type driverPageStore struct {
	authOnlyStore
	packages []DriverPackage
}

func (s *driverPageStore) CreateDriverPackage(_ context.Context, inventory drivers.Inventory) (bool, error) {
	for _, existing := range s.packages {
		if existing.SHA256 == inventory.SHA256 {
			return false, nil
		}
	}
	s.packages = append(s.packages, DriverPackage{SHA256: inventory.SHA256, Inventory: inventory})
	return true, nil
}
func (s *driverPageStore) DriverPackages(context.Context) ([]DriverPackage, error) {
	return s.packages, nil
}
func (s *driverPageStore) DriverConfigs(context.Context) ([]DriverConfig, error) {
	return []DriverConfig{}, nil
}

type fakeDriverAgent struct{ imports []drivers.Inventory }

func (a *fakeDriverAgent) ImportDriverPackage(_ context.Context, body io.Reader, size int64) (drivers.Inventory, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return drivers.Inventory{}, err
	}
	if int64(len(data)) != size {
		return drivers.Inventory{}, fmt.Errorf("length differs")
	}
	inventory, err := drivers.InspectZIP(bytes.NewReader(data), size)
	if err == nil {
		a.imports = append(a.imports, inventory)
	}
	return inventory, err
}
func (a *fakeDriverAgent) CheckDriverPackage(context.Context, string) error { return nil }

func testDriverUpload(t *testing.T, kind, csrf, filename string, content []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField("csrf", csrf); err != nil {
		t.Fatal(err)
	}
	if kind == "folder" {
		if err := w.WriteField("paths[]", filename); err != nil {
			t.Fatal(err)
		}
		filename = "driver.inf"
		kind = "files[]"
	}
	f, err := w.CreateFormFile(kind, filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/drivers/import", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	return req
}

func oneDriverZIP(t *testing.T) []byte {
	t.Helper()
	var body bytes.Buffer
	w := zip.NewWriter(&body)
	f, err := w.Create("vendor/driver.inf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("[Version]\nClass=Net\n[Models.NTamd64]\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func TestDriverImportFolderMatchesZIP(t *testing.T) {
	store := &driverPageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}}
	api := &fakeDriverAgent{}
	s := &Server{Store: store, DriverPackages: api}
	for _, input := range []struct {
		kind, name string
		data       []byte
	}{
		{"zip", "driver.zip", oneDriverZIP(t)},
		{"folder", "vendor/driver.inf", []byte("[Version]\nClass=Net\n[Models.NTamd64]\n")},
	} {
		res := httptest.NewRecorder()
		s.Handler().ServeHTTP(res, testDriverUpload(t, input.kind, CSRFToken("test-token"), input.name, input.data))
		if res.Code != http.StatusSeeOther {
			t.Fatalf("%s: %d %s", input.kind, res.Code, res.Body.String())
		}
	}
	if len(api.imports) != 2 || api.imports[0].SHA256 != api.imports[1].SHA256 {
		t.Fatalf("folder and ZIP differ: %+v", api.imports)
	}
}

func TestDriverImportRejectsCSRFAndTraversal(t *testing.T) {
	for _, tc := range []struct {
		name, csrf, path string
		want             int
	}{
		{"csrf", "wrong", "vendor/driver.inf", http.StatusForbidden},
		{"traversal", CSRFToken("test-token"), "../driver.inf", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &driverPageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}}
			api := &fakeDriverAgent{}
			res := httptest.NewRecorder()
			(&Server{Store: store, DriverPackages: api}).Handler().ServeHTTP(res, testDriverUpload(t, "folder", tc.csrf, tc.path, []byte("[Version]\n")))
			if res.Code != tc.want || len(api.imports) != 0 {
				t.Fatalf("status=%d imports=%d", res.Code, len(api.imports))
			}
		})
	}
}

func TestDriversPageObserverIsReadOnly(t *testing.T) {
	store := &driverPageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "observer", Enabled: true}}}
	s := &Server{Store: store}
	req := httptest.NewRequest(http.MethodGet, "/drivers", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK || strings.Contains(res.Body.String(), `action="/drivers/import"`) {
		t.Fatalf("observer page: %d %s", res.Code, res.Body.String())
	}
}

func TestDriverImportRejectsObserver(t *testing.T) {
	store := &driverPageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "observer", Enabled: true}}}
	api := &fakeDriverAgent{}
	res := httptest.NewRecorder()
	(&Server{Store: store, DriverPackages: api}).Handler().ServeHTTP(res, testDriverUpload(t, "zip", CSRFToken("test-token"), "driver.zip", oneDriverZIP(t)))
	if res.Code != http.StatusForbidden || len(api.imports) != 0 {
		t.Fatalf("observer upload: %d", res.Code)
	}
}

func TestDriversPageOperatorShowsBothImportMethods(t *testing.T) {
	store := &driverPageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}}
	req := httptest.NewRequest(http.MethodGet, "/drivers", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	(&Server{Store: store}).Handler().ServeHTTP(res, req)
	for _, required := range []string{`webkitdirectory`, `name="zip"`, `name="csrf"`, `/assets/driver-upload.js`, `<button type="submit" disabled>`} {
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), required) {
			t.Fatalf("missing %q in page: %d", required, res.Code)
		}
	}
}

func TestDriverImportReportsExistingPackage(t *testing.T) {
	store := &driverPageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}}
	s := &Server{Store: store, DriverPackages: &fakeDriverAgent{}}
	var messages []string
	for i := 0; i < 2; i++ {
		res := httptest.NewRecorder()
		s.Handler().ServeHTTP(res, testDriverUpload(t, "zip", CSRFToken("test-token"), "driver.zip", oneDriverZIP(t)))
		location, _ := url.QueryUnescape(res.Header().Get("Location"))
		if res.Code != http.StatusSeeOther {
			t.Fatalf("import %d: %d %s", i, res.Code, res.Body.String())
		}
		messages = append(messages, location)
	}
	if messages[0] != "/drivers?message=Пакет драйверов «driver.zip» добавлен" || messages[1] != "/drivers?message=Пакет «driver.zip» уже есть в каталоге" || len(store.packages) != 1 {
		t.Fatalf("messages=%q packages=%d", messages, len(store.packages))
	}
}

func TestFilterAndSortDriverConfigs(t *testing.T) {
	configs := []DriverConfig{
		{ID: 1, Title: "Dell Precision 3581", Author: "vlad", SizeBytes: 500000000},
		{ID: 2, Title: "Lenovo ThinkPad P16", Author: "admin", SizeBytes: 400000000},
		{ID: 3, Title: "HP EliteBook 840", Author: "alex", SizeBytes: 600000000},
	}
	usage := driverUsage{
		Configs: map[int64][]driverUse{
			1: {{ID: 10, Title: "Install 1"}},
			2: {{ID: 11, Title: "Install 2"}, {ID: 12, Title: "Install 3"}},
		},
	}

	// Filter by title
	f1 := filterDriverConfigs(configs, usage, "dell")
	if len(f1) != 1 || f1[0].ID != 1 {
		t.Fatalf("unexpected filter by dell: %+v", f1)
	}

	// Sort config asc
	s1 := append([]DriverConfig(nil), configs...)
	sortDriverConfigs(s1, usage, "config", "asc")
	if s1[0].Title != "Dell Precision 3581" || s1[1].Title != "HP EliteBook 840" || s1[2].Title != "Lenovo ThinkPad P16" {
		t.Fatalf("unexpected sort config asc: %+v", s1)
	}

	// Sort size desc
	s2 := append([]DriverConfig(nil), configs...)
	sortDriverConfigs(s2, usage, "size", "desc")
	if s2[0].SizeBytes != 600000000 || s2[1].SizeBytes != 500000000 || s2[2].SizeBytes != 400000000 {
		t.Fatalf("unexpected sort size desc: %+v", s2)
	}

	// Sort uses desc
	s3 := append([]DriverConfig(nil), configs...)
	sortDriverConfigs(s3, usage, "uses", "desc")
	if s3[0].ID != 2 || s3[1].ID != 1 || s3[2].ID != 3 {
		t.Fatalf("unexpected sort uses desc: %+v", s3)
	}
}

func TestDriverUploadSubmitGate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	scriptBytes, err := os.ReadFile("driver_upload.js")
	if err != nil {
		t.Fatalf("failed to read driver_upload.js: %v", err)
	}

	jsTest := fmt.Sprintf(`
const vm = require('node:vm');
const assert = require('node:assert/strict');

const code = %q;

let docListeners = {};

// Folder form elements
let folderFormListeners = {};
let folderInputListeners = {};
const folderFileInput = {
  files: [],
  disabled: false,
  addEventListener(type, fn) { folderInputListeners[type] = fn; },
};
const folderSubmitBtn = { disabled: true };
const folderCancelBtn = { hidden: true, addEventListener(type, fn) {} };
const folderProgress = { parentElement: { hidden: true }, value: 0 };
const folderStatus = { textContent: '' };
const folderForm = {
  dataset: { driverUpload: 'folder', csrf: 'test-csrf' },
  reset() { folderFileInput.files = []; },
  addEventListener(type, fn) { folderFormListeners[type] = fn; },
  querySelector(sel) {
    if (sel === 'input[type="file"]') return folderFileInput;
    if (sel === '[type="submit"]') return folderSubmitBtn;
    if (sel === '[data-cancel]') return folderCancelBtn;
    if (sel === 'progress') return folderProgress;
    if (sel === '[role="status"]') return folderStatus;
    return null;
  },
};

// Zip form elements
let zipFormListeners = {};
let zipInputListeners = {};
const zipFileInput = {
  files: [],
  disabled: false,
  addEventListener(type, fn) { zipInputListeners[type] = fn; },
};
const zipSubmitBtn = { disabled: true };
const zipCancelBtn = { hidden: true, addEventListener(type, fn) {} };
const zipProgress = { parentElement: { hidden: true }, value: 0 };
const zipStatus = { textContent: '' };
const zipForm = {
  dataset: { driverUpload: 'zip', csrf: 'test-csrf' },
  reset() { zipFileInput.files = []; },
  addEventListener(type, fn) { zipFormListeners[type] = fn; },
  querySelector(sel) {
    if (sel === 'input[type="file"]') return zipFileInput;
    if (sel === '[type="submit"]') return zipSubmitBtn;
    if (sel === '[data-cancel]') return zipCancelBtn;
    if (sel === 'progress') return zipProgress;
    if (sel === '[role="status"]') return zipStatus;
    return null;
  },
};

const forms = [folderForm, zipForm];

let dialogShown = false;
const uploadDialog = {
  shown: false,
  showModal() { this.shown = true; dialogShown = true; },
  close() { this.shown = false; dialogShown = false; },
  querySelector(sel) { return null; },
};

const sandbox = {
  document: {
    documentElement: { lang: 'ru' },
    addEventListener(type, fn) { docListeners[type] = fn; },
    querySelectorAll(sel) {
      if (sel === '[data-driver-upload]') return forms;
      if (sel === '[data-driver-config-form]') return [];
      if (typeof sel === 'string' && sel.includes('.driver-config-table')) return [];
      return [];
    },
    getElementById(id) {
      if (id === 'driver-upload-dialog') return uploadDialog;
      if (id === 'driver-q') return null;
      return null;
    },
  },
  console,
  setTimeout,
  clearTimeout,
  URL,
};

vm.createContext(sandbox);
vm.runInContext(code, sandbox);

// 1. Initial state: both buttons disabled
assert.equal(folderSubmitBtn.disabled, true, 'folder submit should be initially disabled');
assert.equal(zipSubmitBtn.disabled, true, 'zip submit should be initially disabled');

// 2. Select folder
folderFileInput.files = [{ name: 'driver.inf' }];
assert.ok(folderInputListeners['change'], 'change listener on folder input');
folderInputListeners['change']();
assert.equal(folderSubmitBtn.disabled, false, 'folder submit should be enabled after files selected');
assert.equal(zipSubmitBtn.disabled, true, 'zip submit should still be disabled');

// 3. Clear folder files
folderFileInput.files = [];
folderInputListeners['change']();
assert.equal(folderSubmitBtn.disabled, true, 'folder submit should be disabled when empty');

// 4. Select ZIP file
zipFileInput.files = [{ name: 'network.zip' }];
assert.ok(zipInputListeners['change'], 'change listener on zip input');
zipInputListeners['change']();
assert.equal(zipSubmitBtn.disabled, false, 'zip submit should be enabled after zip selected');
assert.equal(folderSubmitBtn.disabled, true, 'folder submit should still be disabled');

// 5. Submit event when disabled prevents default
let prevented = false;
folderFileInput.files = [];
folderInputListeners['input']();
folderFormListeners['submit']({
  preventDefault() { prevented = true; },
});
assert.equal(prevented, true, 'empty folder submission should be prevented');

// 6. Opening upload dialog resets forms and disables buttons
zipFileInput.files = [{ name: 'test.zip' }];
zipInputListeners['change']();
assert.equal(zipSubmitBtn.disabled, false);

assert.ok(docListeners['click'], 'document click listener should exist');
const uploadBtnEl = {
  closest(sel) {
    if (sel === '[data-driver-upload-open]') return this;
    return null;
  },
};
docListeners['click']({ target: uploadBtnEl });
assert.equal(uploadDialog.shown, true, 'upload dialog should be opened');
assert.equal(zipSubmitBtn.disabled, true, 'zip submit should be disabled on dialog open');
assert.equal(folderSubmitBtn.disabled, true, 'folder submit should be disabled on dialog open');
`, scriptBytes)

	cmd := exec.CommandContext(ctx, "node", "-e", jsTest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Node.js test failed: %v\nOutput: %s", err, string(out))
	}
}
