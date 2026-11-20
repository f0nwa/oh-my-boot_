package control

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"omb/internal/images"
)

func TestStoragesScriptEndpoint(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/assets/storages.js", nil)
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
		t.Fatalf("unexpected Content-Type: %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"smb-create-dialog", "data-smb-add",
		"nfs-create-dialog", "data-nfs-add",
		"nfs-retry-dialog", "data-nfs-retry-id",
		"storage-rename-dialog", "data-storage-rename-id", "data-storage-rename-save",
		"storage-delete-dialog", "data-storage-delete-id",
		"data-close-dialog",
		"data-smb-submit", "data-nfs-submit", "data-smb-retry-submit",
		"updateNfsSubmit", "updateSmbSubmit", "updateSmbRetrySubmit",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("storages.js lacks %q", want)
		}
	}
}

func TestStoragesPageConnectStorageMenuAndModals(t *testing.T) {
	for _, lang := range []string{langRU, langEN} {
		s := sampleSpaceTestServer("admin", lang, fakeDiskUsageAPI{})
		body := libraryGet(t, s, "/storages")

		for _, want := range []string{
			`popovertarget="storage-connect-menu"`,
			`id="storage-connect-menu"`,
			`data-smb-add`,
			`data-nfs-add`,
			`id="smb-create-dialog"`,
			`action="/images/smb"`,
			`id="nfs-create-dialog"`,
			`action="/images/nfs"`,
			`name="server"`,
			`name="export_path"`,
			`name="version"`,
			`data-close-dialog`,
			`src="/assets/storages.js"`,
			`data-storage-rename-id=`,
			`id="storage-rename-dialog"`,
			`action="/images/storages/0/rename"`,
			`id="storage-rename-name"`,
			`data-storage-rename-save`,
			`disabled`,
			`placeholder="//nas.example.com/share/iso"`,
			`placeholder="nas.example.com"`,
			`data-smb-submit`,
			`data-nfs-submit`,
			`data-smb-retry-submit`,
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("/storages (%s) missing %q", lang, want)
			}
		}
		for _, forbid := range []string{
			`placeholder="//192.168.`,
			`placeholder="192.168.`,
		} {
			if strings.Contains(body, forbid) {
				t.Fatalf("/storages (%s) contains local IP placeholder %q", lang, forbid)
			}
		}
		if lang == langRU {
			for _, want := range []string{"Подключить хранилище", "Подключить SMB", "Подключить NFS", "Версия протокола", "Переименовать…", "Переименовать хранилище", "Название хранилища"} {
				if !strings.Contains(body, want) {
					t.Fatalf("/storages (ru) missing %q", want)
				}
			}
		}
		if lang == langEN {
			for _, want := range []string{"Connect storage", "Connect SMB", "Connect NFS", "Protocol version", "Rename…", "Rename storage", "Storage name"} {
				if !strings.Contains(body, want) {
					t.Fatalf("/storages (en) missing %q", want)
				}
			}
		}

		if mainStart := strings.Index(body, `<main class="content">`); mainStart < 0 {
			t.Fatalf("/storages (%s) missing <main>", lang)
		} else if mainBody := body[mainStart:]; strings.Contains(mainBody, "<details") || strings.Contains(mainBody, "<summary") {
			t.Fatalf("/storages (%s) must not contain any <details> or <summary> inside <main>", lang)
		}
	}

	obs := sampleSpaceTestServer("observer", langRU, fakeDiskUsageAPI{})
	obsBody := libraryGet(t, obs, "/storages")
	for _, forbidden := range []string{`data-smb-add`, `data-nfs-add`, `id="smb-create-dialog"`, `id="nfs-create-dialog"`, `popovertarget="storage-connect-menu"`} {
		if strings.Contains(obsBody, forbidden) {
			t.Fatalf("observer should not see %q", forbidden)
		}
	}
}

func TestRowMenuScriptEndpoint(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/assets/row-menu.js", nil)
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
		t.Fatalf("unexpected Content-Type: %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{"data-row-menu", "popover-open", "data-close-popover"} {
		if !strings.Contains(body, want) {
			t.Fatalf("row-menu.js lacks %q", want)
		}
	}
}

func TestStoragesPageUsesRowMenuAndRetryModalWithNoDetails(t *testing.T) {
	for _, lang := range []string{langRU, langEN} {
		s := sampleSpaceTestServer("admin", lang, fakeDiskUsageAPI{})
		body := libraryGet(t, s, "/storages")

		for _, want := range []string{
			`data-row-menu`,
			`popovertarget="storage-menu-5"`,
			`id="storage-menu-5"`,
			`popovertarget="storage-menu-6"`,
			`id="storage-menu-6"`,
			`>☰<`,
			`data-smb-retry-id="5"`,
			`data-nfs-retry-id="6"`,
			`data-nfs-retry-version="4.1"`,
			`data-nfs-retry-location="192.168.10.20:/volume1/iso"`,
			`data-storage-delete-id="5"`,
			`data-storage-delete-id="6"`,
			`data-storage-delete-kind="smb"`,
			`data-storage-delete-kind="nfs"`,
			`id="smb-retry-dialog"`,
			`id="nfs-retry-dialog"`,
			`id="storage-delete-dialog"`,
			`id="storage-delete-form"`,
			`class="storage-delete-warning"`,
			`192.168.10.20:/volume1/iso`,
			`src="/assets/row-menu.js"`,
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("/storages (%s) missing %q", lang, want)
			}
		}
		if lang == langRU && !strings.Contains(body, "SMB и NFS") {
			t.Fatalf("/storages (ru) recovery dialog missing 'SMB и NFS'")
		}
		if lang == langEN && !strings.Contains(body, "SMB and NFS") {
			t.Fatalf("/storages (en) recovery dialog missing 'SMB and NFS'")
		}
		if mainStart := strings.Index(body, `<main class="content">`); mainStart < 0 {
			t.Fatalf("/storages (%s) missing <main>", lang)
		} else if mainBody := body[mainStart:]; strings.Contains(mainBody, "<details") || strings.Contains(mainBody, "<summary") {
			t.Fatalf("/storages (%s) must not contain any <details> or <summary> inside <main>: %s", lang, mainBody)
		}
	}

	op := sampleSpaceTestServer("operator", langRU, fakeDiskUsageAPI{})
	opBody := libraryGet(t, op, "/storages")
	if !strings.Contains(opBody, `action="/images/scan"`) || !strings.Contains(opBody, `data-row-menu`) ||
		strings.Contains(opBody, `data-smb-retry-id=`) || strings.Contains(opBody, `data-nfs-retry-id=`) ||
		strings.Contains(opBody, `id="smb-retry-dialog"`) || strings.Contains(opBody, `id="nfs-retry-dialog"`) ||
		strings.Contains(opBody, `data-storage-rename-id=`) || strings.Contains(opBody, `id="storage-rename-dialog"`) ||
		strings.Contains(opBody, `data-storage-delete-id=`) || strings.Contains(opBody, `id="storage-delete-dialog"`) {
		t.Fatalf("operator permissions mismatch on /storages")
	}

	obs := sampleSpaceTestServer("observer", langRU, fakeDiskUsageAPI{})
	obsBody := libraryGet(t, obs, "/storages")
	if strings.Contains(obsBody, `action="/images/scan"`) || strings.Contains(obsBody, `data-row-menu`) ||
		strings.Contains(obsBody, `data-storage-rename-id=`) || strings.Contains(obsBody, `id="storage-rename-dialog"`) ||
		strings.Contains(obsBody, `data-storage-delete-id=`) || strings.Contains(obsBody, `id="storage-delete-dialog"`) {
		t.Fatalf("observer permissions mismatch on /storages")
	}
}

func TestStoragesPageShowsTypeAndAccessMode(t *testing.T) {
	for _, tc := range []struct {
		lang           string
		wantTypeSMB    string
		wantTypeNFSv4  string
		wantTypeNFSv3  string
		wantTypeLocal  string
		wantAccessRW   string
		wantAccessRO   string
		forbidOldLabel string
		forbidOldNote  string
	}{
		{
			lang:           langRU,
			wantTypeSMB:    "SMB",
			wantTypeNFSv4:  "NFSv4.1",
			wantTypeNFSv3:  "NFSv3",
			wantTypeLocal:  "Локальный каталог",
			wantAccessRW:   "<span class=\"status\"><span class=\"dot dot-ok\"></span>Доступно</span><br><small class=\"muted\">чтение и запись</small>",
			wantAccessRO:   "<span class=\"status\"><span class=\"dot dot-ok\"></span>Доступно</span><br><small class=\"muted\">только чтение</small>",
			forbidOldLabel: "SMB хранилище",
			forbidOldNote:  "можно загружать ISO",
		},
		{
			lang:           langEN,
			wantTypeSMB:    "SMB",
			wantTypeNFSv4:  "NFSv4.1",
			wantTypeNFSv3:  "NFSv3",
			wantTypeLocal:  "Local directory",
			wantAccessRW:   "<span class=\"status\"><span class=\"dot dot-ok\"></span>Available</span><br><small class=\"muted\">read and write</small>",
			wantAccessRO:   "<span class=\"status\"><span class=\"dot dot-ok\"></span>Available</span><br><small class=\"muted\">read-only</small>",
			forbidOldLabel: "SMB storage",
			forbidOldNote:  "ISO uploads allowed",
		},
	} {
		api := &fakeImagesAPI{
			catalog: images.Catalog{
				Storages: []images.Storage{
					{
						ID:      5,
						Name:    "NAS Synology",
						Kind:    "smb",
						Server:  "192.168.10.10",
						Share:   "Downloads",
						State:   "ready",
						Uploads: true,
						Total:   385 << 30,
						Free:    115 << 30,
					},
					{
						ID:         6,
						Name:       "NFS Share v4",
						Kind:       "nfs",
						Server:     "192.168.10.20",
						ExportPath: "/iso4",
						NFSVersion: "4.1",
						State:      "ready",
						Uploads:    false,
						Total:      200 << 30,
						Free:       100 << 30,
					},
					{
						ID:         7,
						Name:       "NFS Share v3",
						Kind:       "nfs",
						Server:     "192.168.10.21",
						ExportPath: "/iso3",
						NFSVersion: "3",
						State:      "ready",
						Uploads:    false,
						Total:      150 << 30,
						Free:       50 << 30,
					},
					{
						ID:      8,
						Name:    "Local Archive",
						Kind:    "local",
						Root:    "/var/lib/omb-images/iso",
						State:   "ready",
						Uploads: false,
						Total:   100 << 30,
						Free:    40 << 30,
					},
				},
			},
		}
		s := &Server{
			Store:     authOnlyStore{user: User{ID: 1, Role: "admin", Language: tc.lang, Enabled: true}},
			Images:    api,
			DiskUsage: fakeDiskUsageAPI{},
		}
		body := libraryGet(t, s, "/storages")

		for _, want := range []string{tc.wantTypeSMB, tc.wantTypeNFSv4, tc.wantTypeNFSv3, tc.wantTypeLocal, tc.wantAccessRW, tc.wantAccessRO} {
			if !strings.Contains(body, want) {
				t.Fatalf("/storages (%s) missing %q in body:\n%s", tc.lang, want, body)
			}
		}
		if strings.Contains(body, tc.forbidOldLabel) {
			t.Fatalf("/storages (%s) must not contain obsolete label %q", tc.lang, tc.forbidOldLabel)
		}
		if strings.Contains(body, tc.forbidOldNote) {
			t.Fatalf("/storages (%s) must not contain obsolete note %q", tc.lang, tc.forbidOldNote)
		}
	}
}

func TestStoragesDeleteConfirmationModal(t *testing.T) {
	for _, tc := range []struct {
		lang        string
		wantTitle   string
		wantWarn    string
		wantDesc    string
		wantHint    string
		wantConfirm string
		wantCancel  string
	}{
		{
			lang:        langRU,
			wantTitle:   "Удалить хранилище",
			wantWarn:    "Внимание",
			wantDesc:    "Связанные образы ISO будут удалены из библиотеки OMB. Если образы используются активными загрузками Windows, сервер заблокирует удаление.",
			wantHint:    "Исходные файлы на сервере останутся без изменений.",
			wantConfirm: "Подтвердить удаление",
			wantCancel:  "Отмена",
		},
		{
			lang:        langEN,
			wantTitle:   "Delete storage",
			wantWarn:    "Warning",
			wantDesc:    "Linked ISO images will be removed from the OMB library. If the images are used by active Windows boot entries, the server will block deletion.",
			wantHint:    "Original files on the server will remain unchanged.",
			wantConfirm: "Confirm deletion",
			wantCancel:  "Cancel",
		},
	} {
		s := sampleSpaceTestServer("admin", tc.lang, fakeDiskUsageAPI{})
		body := libraryGet(t, s, "/storages")

		for _, want := range []string{
			`id="storage-delete-dialog"`,
			`id="storage-delete-form"`,
			`id="storage-delete-name"`,
			`id="storage-delete-path"`,
			tc.wantTitle,
			tc.wantWarn,
			tc.wantDesc,
			tc.wantHint,
			tc.wantConfirm,
			tc.wantCancel,
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("/storages (%s) missing %q in delete dialog", tc.lang, want)
			}
		}
	}
}

func TestNFSStorageUIBrowser(t *testing.T) {
	if os.Getenv("OMB_UI_TEST") != "1" {
		t.Skip("set OMB_UI_TEST=1 and NODE_PATH to run browser check")
	}
	dir := t.TempDir()
	assets := regexp.MustCompile(`(?:src|href)="(/assets/[^"?]+)"`)
	for _, lang := range []string{langRU, langEN} {
		s := sampleSpaceTestServer("admin", lang, fakeDiskUsageAPI{})
		body := libraryGet(t, s, "/storages")
		if err := os.WriteFile(filepath.Join(dir, "admin-"+lang+".html"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		for _, match := range assets.FindAllStringSubmatch(body, -1) {
			res := httptest.NewRecorder()
			s.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, match[1], nil))
			assetPath := filepath.Join(dir, match[1])
			if err := os.MkdirAll(filepath.Dir(assetPath), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(assetPath, res.Body.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "../../scripts/test-nfs-ui.cjs", dir)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser test failed: %v\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "NFS_UI_BROWSER_TESTS_PASS") {
		t.Fatalf("browser check missing success marker: %s", string(out))
	}
}

func TestStorageConnectDialogsSubmitGate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	scriptBytes, err := os.ReadFile("storages.js")
	if err != nil {
		t.Fatalf("failed to read storages.js: %v", err)
	}

	jsTest := fmt.Sprintf(`
const vm = require('node:vm');
const assert = require('node:assert/strict');

const code = %q;

let docListeners = {};

// NFS elements
const nfsSubmitBtn = { disabled: true };
const nfsServerInput = { value: '', validity: { valid: true }, focused: false, focus() { this.focused = true; } };
const nfsExportPathInput = { value: '', validity: { valid: true } };
let nfsFormListeners = {};
const nfsFormEl = {
  reset() {
    nfsServerInput.value = '';
    nfsExportPathInput.value = '';
  },
  addEventListener(type, fn) { nfsFormListeners[type] = fn; },
  querySelector(sel) {
    if (sel === 'input[name="server"]') return nfsServerInput;
    if (sel === 'input[name="export_path"]') return nfsExportPathInput;
    if (sel === '[data-nfs-submit]' || sel === 'button.btn-primary') return nfsSubmitBtn;
    return null;
  },
};
const nfsDialogEl = {
  shown: false,
  showModal() { this.shown = true; },
  close() { this.shown = false; },
  querySelector(sel) {
    if (sel === 'form') return nfsFormEl;
    if (sel === 'input[name="server"]') return nfsServerInput;
    if (sel === 'input[name="export_path"]') return nfsExportPathInput;
    if (sel === '[data-nfs-submit]' || sel === 'button.btn-primary') return nfsSubmitBtn;
    return null;
  },
};

// SMB elements
const smbSubmitBtn = { disabled: true };
const smbPathInput = { value: '', validity: { valid: true }, focused: false, focus() { this.focused = true; } };
const smbUserInput = { value: '', validity: { valid: true } };
const smbPwInput = { value: '', validity: { valid: true } };
let smbFormListeners = {};
const smbFormEl = {
  reset() {
    smbPathInput.value = '';
    smbUserInput.value = '';
    smbPwInput.value = '';
  },
  addEventListener(type, fn) { smbFormListeners[type] = fn; },
  querySelector(sel) {
    if (sel === 'input[name="path"]') return smbPathInput;
    if (sel === 'input[name="username"]') return smbUserInput;
    if (sel === 'input[name="password"]') return smbPwInput;
    if (sel === '[data-smb-submit]' || sel === 'button.btn-primary') return smbSubmitBtn;
    return null;
  },
};
const smbDialogEl = {
  shown: false,
  showModal() { this.shown = true; },
  close() { this.shown = false; },
  querySelector(sel) {
    if (sel === 'form') return smbFormEl;
    if (sel === 'input[name="path"]') return smbPathInput;
    if (sel === 'input[name="username"]') return smbUserInput;
    if (sel === 'input[name="password"]') return smbPwInput;
    if (sel === '[data-smb-submit]' || sel === 'button.btn-primary') return smbSubmitBtn;
    return null;
  },
};

// SMB retry elements
const retrySubmitBtn = { disabled: true };
const retryUserInput = { value: '', validity: { valid: true }, focused: false, focus() { this.focused = true; } };
const retryPwInput = { value: '', validity: { valid: true } };
let retryFormListeners = {};
const retryFormEl = {
  action: '',
  reset() {
    retryUserInput.value = '';
    retryPwInput.value = '';
  },
  addEventListener(type, fn) { retryFormListeners[type] = fn; },
  querySelector(sel) {
    if (sel === 'input[name="username"]') return retryUserInput;
    if (sel === 'input[name="password"]') return retryPwInput;
    if (sel === '[data-smb-retry-submit]' || sel === 'button.btn-primary') return retrySubmitBtn;
    return null;
  },
};
const retryDialogEl = {
  shown: false,
  showModal() { this.shown = true; },
  close() { this.shown = false; },
  querySelector(sel) {
    if (sel === 'form') return retryFormEl;
    if (sel === 'input[name="username"]') return retryUserInput;
    if (sel === 'input[name="password"]') return retryPwInput;
    if (sel === '[data-smb-retry-submit]' || sel === 'button.btn-primary') return retrySubmitBtn;
    return null;
  },
};

const document = {
  documentElement: { lang: 'ru' },
  querySelectorAll: () => [],
  querySelector: () => null,
  getElementById: (id) => {
    if (id === 'nfs-create-dialog') return nfsDialogEl;
    if (id === 'smb-create-dialog') return smbDialogEl;
    if (id === 'smb-retry-dialog') return retryDialogEl;
    return null;
  },
  addEventListener: (type, fn) => { docListeners[type] = fn; },
};

const sandbox = {
  document,
  window: {},
  Event: class { constructor(type) { this.type = type; } },
};

vm.runInNewContext(code, sandbox);

// 1. Initial states: buttons must be disabled
assert.equal(nfsSubmitBtn.disabled, true, 'nfsSubmitBtn must be disabled initially');
assert.equal(smbSubmitBtn.disabled, true, 'smbSubmitBtn must be disabled initially');
assert.equal(retrySubmitBtn.disabled, true, 'retrySubmitBtn must be disabled initially');

// 2. NFS Validation
nfsServerInput.value = 'nas.example.com';
nfsFormListeners['input']();
assert.equal(nfsSubmitBtn.disabled, true, 'nfsSubmitBtn must remain disabled with server only');

nfsExportPathInput.value = '/volume1/iso';
nfsFormListeners['input']();
assert.equal(nfsSubmitBtn.disabled, false, 'nfsSubmitBtn must be enabled when server and export_path are set');

nfsServerInput.value = '   ';
nfsFormListeners['input']();
assert.equal(nfsSubmitBtn.disabled, true, 'nfsSubmitBtn must be disabled when server is blank');

// Prevent submit if disabled
let prevented = false;
nfsFormListeners['submit']({ preventDefault() { prevented = true; } });
assert.equal(prevented, true, 'nfsForm submission must be prevented when button is disabled');

// Trigger open via [data-nfs-add]
nfsServerInput.value = 'nas.example.com';
nfsExportPathInput.value = '/volume1/iso';
nfsSubmitBtn.disabled = false;
docListeners['click']({
  preventDefault() {},
  target: {
    closest(sel) {
      if (sel === '[data-nfs-add]') return { closest() { return null; } };
      return null;
    },
  },
});
assert.equal(nfsDialogEl.shown, true, 'nfsDialog should be shown');
assert.equal(nfsSubmitBtn.disabled, true, 'nfsSubmitBtn must be disabled after opening and reset');
assert.equal(nfsServerInput.focused, true, 'serverInput should receive focus');

// 3. SMB Validation
smbPathInput.value = '//nas.example.com/iso';
smbFormListeners['input']();
assert.equal(smbSubmitBtn.disabled, true, 'smbSubmitBtn must remain disabled with path only');

smbUserInput.value = 'user1';
smbFormListeners['input']();
assert.equal(smbSubmitBtn.disabled, true, 'smbSubmitBtn must remain disabled with path and user only');

smbPwInput.value = 'secret';
smbFormListeners['input']();
assert.equal(smbSubmitBtn.disabled, false, 'smbSubmitBtn must be enabled when path, user, and password are set');

smbUserInput.value = '   ';
smbFormListeners['input']();
assert.equal(smbSubmitBtn.disabled, true, 'smbSubmitBtn must be disabled when username is whitespace');

prevented = false;
smbFormListeners['submit']({ preventDefault() { prevented = true; } });
assert.equal(prevented, true, 'smbForm submission must be prevented when button is disabled');

// Trigger open via [data-smb-add]
smbPathInput.value = '//nas/iso';
smbUserInput.value = 'u';
smbPwInput.value = 'p';
smbSubmitBtn.disabled = false;
docListeners['click']({
  preventDefault() {},
  target: {
    closest(sel) {
      if (sel === '[data-smb-add]') return { closest() { return null; } };
      return null;
    },
  },
});
assert.equal(smbDialogEl.shown, true, 'smbDialog should be shown');
assert.equal(smbSubmitBtn.disabled, true, 'smbSubmitBtn must be disabled after opening and reset');
assert.equal(smbPathInput.focused, true, 'pathInput should receive focus');

// 4. SMB Retry Validation
retryUserInput.value = 'user1';
retryFormListeners['input']();
assert.equal(retrySubmitBtn.disabled, true, 'retrySubmitBtn must be disabled with user only');

retryPwInput.value = 'secret';
retryFormListeners['input']();
assert.equal(retrySubmitBtn.disabled, false, 'retrySubmitBtn must be enabled with user and password');

retryPwInput.value = '';
retryFormListeners['input']();
assert.equal(retrySubmitBtn.disabled, true, 'retrySubmitBtn must be disabled with empty password');

prevented = false;
retryFormListeners['submit']({ preventDefault() { prevented = true; } });
assert.equal(prevented, true, 'retryForm submission must be prevented when button is disabled');

// Trigger open via [data-smb-retry-id]
retryUserInput.value = 'u';
retryPwInput.value = 'p';
retrySubmitBtn.disabled = false;
docListeners['click']({
  preventDefault() {},
  target: {
    closest(sel) {
      if (sel === '[data-smb-retry-id]') {
        return {
          closest() { return null; },
          getAttribute(name) {
            if (name === 'data-smb-retry-id') return '5';
            if (name === 'data-smb-retry-name') return 'Storage5';
            if (name === 'data-smb-retry-path') return '//nas/iso';
            return '';
          },
        };
      }
      return null;
    },
  },
});
assert.equal(retryDialogEl.shown, true, 'retryDialog should be shown');
assert.equal(retrySubmitBtn.disabled, true, 'retrySubmitBtn must be disabled after opening and reset');
assert.equal(retryUserInput.focused, true, 'retryUserInput should receive focus');

console.log('STORAGE_SUBMIT_GATE_TESTS_PASS');
`, string(scriptBytes))

	cmd := exec.CommandContext(ctx, "node", "-e", jsTest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node test failed: %v\nOutput: %s", err, string(out))
	}
	if !strings.Contains(string(out), "STORAGE_SUBMIT_GATE_TESTS_PASS") {
		t.Fatalf("missing success marker: %s", string(out))
	}
}

func TestStoragesPageRendersLiveRefresh(t *testing.T) {
	s := sampleSpaceTestServer("admin", langRU, fakeDiskUsageAPI{})
	body := libraryGet(t, s, "/storages")
	if !strings.Contains(body, `data-live-refresh="15"`) {
		t.Errorf("expected data-live-refresh=\"15\" in /storages")
	}
	if !strings.Contains(body, `data-live-targets="#storages-live"`) {
		t.Errorf("expected data-live-targets=\"#storages-live\" in /storages")
	}
	if !strings.Contains(body, `<script defer src="/assets/live-refresh.js"></script>`) {
		t.Errorf("expected script /assets/live-refresh.js in /storages")
	}
}
