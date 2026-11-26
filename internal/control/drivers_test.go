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

func (s *driverPageStore) CreateDriverPackage(_ context.Context, inventory drivers.Inventory, title, description string) (bool, error) {
	for _, existing := range s.packages {
		if existing.SHA256 == inventory.SHA256 {
			return false, nil
		}
	}
	s.packages = append(s.packages, DriverPackage{SHA256: inventory.SHA256, Title: title, Description: description, Inventory: inventory})
	return true, nil
}
func (s *driverPageStore) UpdateDriverPackageMetadata(_ context.Context, hash, title, description string) error {
	for i := range s.packages {
		if s.packages[i].SHA256 == hash {
			s.packages[i].Title = title
			s.packages[i].Description = description
			return nil
		}
	}
	return os.ErrNotExist
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

func testDriverUploadWithMeta(t *testing.T, csrf, filename, title, description string, content []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for k, v := range map[string]string{
		"csrf":        csrf,
		"title":       title,
		"description": description,
	} {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	f, err := w.CreateFormFile("zip", filename)
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

func TestDriverPackageMetadataUploadAndEdit(t *testing.T) {
	store := &driverPageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Username: "admin", Role: "admin", Enabled: true}}}
	agent := &fakeDriverAgent{}
	server := &Server{Store: store, DriverPackages: agent}
	handler := server.Handler()

	csrf := CSRFToken("test-token")
	zipBytes := oneDriverZIP(t)

	// 1. Upload with title and description
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, testDriverUploadWithMeta(t, csrf, "dell.zip", "Dell Latitude 5440", "Сетевая карта Intel I219-LM\nWinPE x64", zipBytes))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("import with metadata status = %d, want 303 (%s)", rec.Code, rec.Body.String())
	}
	if len(store.packages) != 1 {
		t.Fatalf("stored packages = %d, want 1", len(store.packages))
	}
	pkg := store.packages[0]
	if pkg.Title != "Dell Latitude 5440" || pkg.Description != "Сетевая карта Intel I219-LM\nWinPE x64" {
		t.Fatalf("stored metadata = (%q, %q)", pkg.Title, pkg.Description)
	}

	// 2. Duplicate upload must not overwrite existing metadata
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, testDriverUploadWithMeta(t, csrf, "other.zip", "Другое имя", "Другое описание", zipBytes))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("duplicate import status = %d, want 303", rec.Code)
	}
	if store.packages[0].Title != "Dell Latitude 5440" || store.packages[0].Description != "Сетевая карта Intel I219-LM\nWinPE x64" {
		t.Fatalf("duplicate import overwrote metadata: (%q, %q)", store.packages[0].Title, store.packages[0].Description)
	}

	// 3. Rendered /drivers shows title, description and edit form for admin
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/drivers", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /drivers status = %d", rec.Code)
	}
	page := rec.Body.String()
	for _, want := range []string{"Dell Latitude 5440", "Сетевая карта Intel I219-LM", "data-driver-package-form", "/drivers/packages/" + pkg.SHA256 + "/metadata"} {
		if !strings.Contains(page, want) {
			t.Fatalf("drivers page missing %q", want)
		}
	}

	// 4. Operator can edit metadata
	store.user = User{ID: 2, Username: "op", Role: "operator", Enabled: true}
	opCSRF := CSRFToken("test-token")
	form := url.Values{"csrf": {opCSRF}, "title": {"Intel I219-LM (обновлённый)"}, "description": {"Для Latitude 5440 и 5540"}}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/drivers/packages/"+pkg.SHA256+"/metadata", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("operator metadata update status = %d, want 303 (%s)", rec.Code, rec.Body.String())
	}
	if store.packages[0].Title != "Intel I219-LM (обновлённый)" || store.packages[0].Description != "Для Latitude 5440 и 5540" {
		t.Fatalf("updated metadata = (%q, %q)", store.packages[0].Title, store.packages[0].Description)
	}

	// 5. Validation rejects too-long title or control characters
	badForm := url.Values{"csrf": {opCSRF}, "title": {strings.Repeat("a", 101)}, "description": {"ok"}}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/drivers/packages/"+pkg.SHA256+"/metadata", strings.NewReader(badForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("long title status = %d, want 400", rec.Code)
	}

	// 6. Unknown package returns 404
	missingForm := url.Values{"csrf": {opCSRF}, "title": {"ok"}, "description": {""}}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/drivers/packages/"+strings.Repeat("f", 64)+"/metadata", strings.NewReader(missingForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown package status = %d, want 404", rec.Code)
	}

	// 7. Observer gets 403 on update and read-only view on /drivers
	store.user = User{ID: 3, Username: "obs", Role: "observer", Enabled: true}
	obsCSRF := CSRFToken("test-token")
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/drivers/packages/"+pkg.SHA256+"/metadata", strings.NewReader(url.Values{"csrf": {obsCSRF}, "title": {"hack"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("observer metadata update status = %d, want 403", rec.Code)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/drivers", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	handler.ServeHTTP(rec, req)
	obsPage := rec.Body.String()
	if !strings.Contains(obsPage, "Intel I219-LM (обновлённый)") || strings.Contains(obsPage, "data-driver-package-form") {
		t.Fatalf("observer page should show read-only metadata without edit form")
	}
}

func TestDriverPackageMetadataInInstallationFormAndSearch(t *testing.T) {
	sha1 := strings.Repeat("1", 64)
	sha2 := strings.Repeat("2", 64)
	sha3 := strings.Repeat("3", 64)
	packages := []DriverPackage{
		{
			SHA256:      sha1,
			Title:       "Realtek USB-C Ethernet",
			Description: "Для док-станций Dell WD19",
			Inventory: drivers.Inventory{
				SHA256: sha1,
				INFs:   []drivers.INF{{Path: "Net/rtux64w10.inf", Architecture: "amd64", Class: "Net"}},
			},
		},
		{
			SHA256:      sha2,
			Title:       "Intel VMD 20.0",
			Description: "Контроллеры NVMe 12-14 Gen",
			Inventory: drivers.Inventory{
				SHA256: sha2,
				INFs: []drivers.INF{
					{Path: "VMD/iaStorVD.inf", Architecture: "amd64", Class: "SCSIAdapter"},
					{Path: "AHCI/iaAHCIC.inf", Architecture: "amd64", Class: "hdc"},
				},
			},
		},
		{
			SHA256: sha3,
			Inventory: drivers.Inventory{
				SHA256: sha3,
				INFs:   []drivers.INF{{Path: "PRO1000/e1d68x64.inf", Architecture: "amd64", Class: "Net"}},
			},
		},
	}

	choices := packageChoices(packages)
	if len(choices) != 3 {
		t.Fatalf("choices = %d, want 3", len(choices))
	}
	if choices[0].Title != "Realtek USB-C Ethernet" || choices[0].Description != "Для док-станций Dell WD19" {
		t.Fatalf("choice[0] = %+v", choices[0])
	}
	if choices[2].Title != "" || choices[2].Label != "e1d68x64" {
		t.Fatalf("choice[2] fallback = %+v", choices[2])
	}

	// Render installation_new template directly for edit mode
	rec := httptest.NewRecorder()
	render(rec, "installation_new", pageData{
		Info:               requestContext{User: User{ID: 1, Role: "admin", Enabled: true}, CSRF: "tok"},
		EditInstallationID: 7,
		SelectedBootKind:   "windows_iso",
		PackageChoices:     choices,
		Form:               url.Values{"boot_profile_id": {"1"}, "inf": {sha1 + ":Net/rtux64w10.inf"}},
	})
	html := rec.Body.String()
	for _, want := range []string{
		`<span class="choice-title">Realtek USB-C Ethernet</span>`,
		`<small class="choice-desc">Для док-станций Dell WD19</small>`,
		`<span class="choice-title">Intel VMD 20.0</span>`,
		`<small class="choice-desc">Контроллеры NVMe 12-14 Gen</small>`,
		`<span class="choice-title mono">e1d68x64</span>`,
		`data-search="Realtek USB-C Ethernet Для док-станций Dell WD19 rtux64w10 Net/rtux64w10.inf · Net ` + sha1 + `"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("installation edit form missing %q", want)
		}
	}

	// Global search finds package by title and description
	store := &driverPageStore{
		authOnlyStore: authOnlyStore{user: User{ID: 1, Username: "admin", Role: "admin", Enabled: true}},
		packages:      packages,
	}
	server := &Server{Store: store}
	srec := httptest.NewRecorder()
	sreq := httptest.NewRequest(http.MethodGet, "/search?format=json&q=WD19", nil)
	sreq.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	server.Handler().ServeHTTP(srec, sreq)
	if srec.Code != http.StatusOK {
		t.Fatalf("search status = %d", srec.Code)
	}
	if !strings.Contains(srec.Body.String(), "Realtek USB-C Ethernet") {
		t.Fatalf("search response missing package title: %s", srec.Body.String())
	}
}
