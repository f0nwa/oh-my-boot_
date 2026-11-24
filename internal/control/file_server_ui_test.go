package control

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func sampleFileServerPageData(role, lang string, smbOn, sftpOn bool) pageData {
	data := samplePageDataBase(lang)
	data.ImageError = ""
	data.Error = ""
	data.Info.User.Role = role
	data.FileServer = fileServerView{
		SMBEnabled:         smbOn,
		SFTPEnabled:        sftpOn,
		SFTPPort:           2022,
		SMBAddress:         `\\192.0.2.10\iso`,
		SFTPAddress:        "sftp://192.0.2.10:2022/",
		HostKeyFingerprint: "SHA256:4X7xMockSFTPHostKeyFingerprintBase64",
		ActiveUsersCount:   2,
		ROUsersCount:       1,
		RWUsersCount:       1,
	}
	return data
}

func TestFileServerUI_Storages(t *testing.T) {
	t.Run("admin with both enabled", func(t *testing.T) {
		data := sampleFileServerPageData("admin", langRU, true, true)
		rec := httptest.NewRecorder()
		render(rec, "storages", data)
		body := rec.Body.String()

		if strings.Contains(body, "<details") || strings.Contains(body, "<summary") {
			t.Fatalf("storages page must not contain <details> or <summary>")
		}

		// Header actions should not contain the file server button
		if strings.Contains(body, `class="storage-page-actions"`) {
			spaIdx := strings.Index(body, `class="storage-page-actions"`)
			spaEnd := strings.Index(body[spaIdx:], `</div>`)
			spaBody := body[spaIdx : spaIdx+spaEnd]
			if strings.Contains(spaBody, `data-file-server-open`) {
				t.Fatalf("storage-page-actions must not contain data-file-server-open: %s", spaBody)
			}
		}

		// Local row chips
		if !strings.Contains(body, `SMB · \\192.0.2.10\iso`) {
			t.Fatalf("missing SMB address chip in Local row: %s", body)
		}
		if !strings.Contains(body, `SFTP · sftp://192.0.2.10:2022/`) {
			t.Fatalf("missing SFTP address chip in Local row: %s", body)
		}

		// Row menu item in Local row (the only place where file server dialog is opened from)
		if !strings.Contains(body, "Файловый сервер (SMB / SFTP)…") {
			t.Fatalf("missing Local row menu item 'Файловый сервер (SMB / SFTP)…'")
		}
		if !strings.Contains(body, `data-file-server-open`) {
			t.Fatalf("missing data-file-server-open in Local row menu")
		}

		// Modal dialog #file-server-dialog
		if !strings.Contains(body, `id="file-server-dialog"`) {
			t.Fatalf("missing #file-server-dialog")
		}
		for _, want := range []string{
			`name="smb_enabled"`,
			`name="sftp_enabled"`,
			`name="sftp_port"`,
			`min="1024"`,
			`max="65535"`,
			`data-file-server-save`,
			`SHA256:4X7xMockSFTPHostKeyFingerprintBase64`,
			`Настроить доступы в «Пользователях» →`,
			`action="/images/file-server"`,
			`proto-status is-on`,
			`dot dot-ok`,
			`Активен`,
			`Стандартный порт 445.`,
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("dialog missing %q", want)
			}
		}

		// Ensure old bulky chips are removed from the dialog
		fsDlgIdx := strings.Index(body, `id="file-server-dialog"`)
		fsDlgEnd := strings.Index(body[fsDlgIdx:], `</dialog>`)
		fsDlgBody := body[fsDlgIdx : fsDlgIdx+fsDlgEnd]
		if strings.Contains(fsDlgBody, `chip-ok`) || strings.Contains(fsDlgBody, "Активен · порт") {
			t.Fatalf("dialog must not contain old chip-ok or 'Активен · порт': %s", fsDlgBody)
		}
	})

	t.Run("admin with both disabled", func(t *testing.T) {
		data := sampleFileServerPageData("admin", langRU, false, false)
		rec := httptest.NewRecorder()
		render(rec, "storages", data)
		body := rec.Body.String()

		if !strings.Contains(body, `dot-off`) {
			t.Fatalf("expected dot-off when file server is disabled")
		}
		if !strings.Contains(body, "Приём по SMB/SFTP выключен") {
			t.Fatalf("expected 'Приём по SMB/SFTP выключен' when disabled")
		}

		// Modal dialog rendered with disabled status
		if !strings.Contains(body, `proto-status is-off`) || !strings.Contains(body, "Выключен") {
			t.Fatalf("dialog should show disabled status when services off")
		}
	})

	t.Run("observer does not see admin configuration dialog", func(t *testing.T) {
		data := sampleFileServerPageData("observer", langRU, true, true)
		rec := httptest.NewRecorder()
		render(rec, "storages", data)
		body := rec.Body.String()

		if strings.Contains(body, `id="file-server-dialog"`) {
			t.Fatalf("observer must not see file-server-dialog")
		}
	})
}

func TestFileServerUI_Users(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	data := sampleFileServerPageData("admin", langRU, true, true)
	data.Users = []User{
		{
			ID:                  1,
			Username:            "admin",
			Role:                "admin",
			Enabled:             true,
			FSAccessMode:        "rw",
			HasFSPassword:       true,
			FSPasswordChangedAt: &now,
		},
		{
			ID:                  2,
			Username:            "engineer",
			Role:                "operator",
			Enabled:             true,
			FSAccessMode:        "ro",
			HasFSPassword:       true,
			FSPasswordChangedAt: &now,
		},
		{
			ID:           3,
			Username:     "auditor",
			Role:         "observer",
			Enabled:      true,
			FSAccessMode: "none",
		},
		{
			ID:           4,
			Username:     "disabled_op",
			Role:         "operator",
			Enabled:      false,
			FSAccessMode: "rw",
		},
	}

	rec := httptest.NewRecorder()
	render(rec, "users", data)
	body := rec.Body.String()

	if strings.Contains(body, "<details") || strings.Contains(body, "<summary") {
		t.Fatalf("users page must not contain <details> or <summary>")
	}

	// 6th column header
	if !strings.Contains(body, "<th>SMB / SFTP</th>") {
		t.Fatalf("users table missing <th>SMB / SFTP</th> column")
	}

	// Chips
	if !strings.Contains(body, `chip-ok`) || !strings.Contains(body, "Чтение и запись") {
		t.Fatalf("missing chip-ok 'Чтение и запись'")
	}
	if !strings.Contains(body, `chip-ro`) || !strings.Contains(body, "Только чтение") {
		t.Fatalf("missing chip-ro 'Только чтение'")
	}
	if !strings.Contains(body, `chip-off`) || !strings.Contains(body, "Отключён") {
		t.Fatalf("missing chip-off 'Отключён'")
	}
	if !strings.Contains(body, "приостановлен (аккаунт отключён)") {
		t.Fatalf("missing suspended mark for disabled user with fs access")
	}

	// Row menu item
	if !strings.Contains(body, "Доступ SMB / SFTP…") {
		t.Fatalf("missing row menu item 'Доступ SMB / SFTP…'")
	}

	// Modal dialog
	if !strings.Contains(body, `id="user-fs-access-dialog"`) {
		t.Fatalf("missing #user-fs-access-dialog")
	}
	for _, want := range []string{
		`action="/users/file-access"`,
		`name="mode" value="none"><span><strong>`,
		`name="mode" value="ro"><span><strong>`,
		`name="mode" value="rw"><span><strong>`,
		`data-user-fs-save`,
		`data-password-generator`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("user-fs-access-dialog missing %q", want)
		}
	}
}

func TestFileServerUI_Account(t *testing.T) {
	now := time.Date(2026, 11, 19, 14, 0, 0, 0, time.UTC)

	t.Run("user with rw mode shows fs section and password dialog", func(t *testing.T) {
		data := sampleFileServerPageData("operator", langRU, true, true)
		data.Info.User = User{
			ID:                  2,
			Username:            "engineer",
			Role:                "operator",
			FSAccessMode:        "rw",
			HasFSPassword:       true,
			FSPasswordChangedAt: &now,
		}

		rec := httptest.NewRecorder()
		render(rec, "account", data)
		body := rec.Body.String()

		if strings.Contains(body, "<details") || strings.Contains(body, "<summary") || strings.Contains(body, `<hr class="sep">`) {
			t.Fatalf("account page must not contain <details>, <summary> or <hr class=\"sep\">")
		}

		if !strings.Contains(body, `class="account-grid"`) {
			t.Fatalf("account page must contain .account-grid container")
		}
		if !strings.Contains(body, `class="card account-card account-fs-card"`) {
			t.Fatalf("account page must render separate .account-fs-card card")
		}
		if !strings.Contains(body, "Доступ к хранилищу ISO (SMB / SFTP)") {
			t.Fatalf("missing 'Доступ к хранилищу ISO (SMB / SFTP)' section in account page")
		}
		if !strings.Contains(body, "Чтение и запись") {
			t.Fatalf("missing mode label in account page")
		}
		if !strings.Contains(body, `\\192.0.2.10\iso`) {
			t.Fatalf("missing SMB address in account page")
		}
		if !strings.Contains(body, `sftp://engineer@192.0.2.10:2022/`) {
			t.Fatalf("missing user SFTP address in account page: %s", body)
		}
		if !strings.Contains(body, `data-account-fs-password-open`) {
			t.Fatalf("missing data-account-fs-password-open button")
		}
		if !strings.Contains(body, `id="account-fs-password-dialog"`) {
			t.Fatalf("missing #account-fs-password-dialog")
		}
		if !strings.Contains(body, `action="/account/file-password"`) {
			t.Fatalf("missing action='/account/file-password'")
		}
	})

	t.Run("user with none mode hides fs section and password dialog", func(t *testing.T) {
		data := sampleFileServerPageData("observer", langRU, true, true)
		data.Info.User = User{
			ID:           3,
			Username:     "auditor",
			Role:         "observer",
			FSAccessMode: "none",
		}

		rec := httptest.NewRecorder()
		render(rec, "account", data)
		body := rec.Body.String()

		if strings.Contains(body, "Доступ к хранилищу ISO (SMB / SFTP)") {
			t.Fatalf("account page must not show fs section when FSAccessMode is none")
		}
		if strings.Contains(body, `id="account-fs-password-dialog"`) {
			t.Fatalf("account page must not contain #account-fs-password-dialog when FSAccessMode is none")
		}
	})
}

func TestFileServerUI_StoragesNodeJS(t *testing.T) {
	scriptBytes, err := os.ReadFile("storages.js")
	if err != nil {
		t.Fatalf("read storages.js: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	jsTest := fmt.Sprintf(`
const assert = require('assert');

const docListeners = {};
const elements = {};

function createEl(id, tag, props = {}) {
  const listeners = {};
  const el = {
    id,
    tagName: (tag || 'DIV').toUpperCase(),
    value: '',
    checked: false,
    disabled: false,
    hidden: false,
    classList: {
      _classes: new Set(),
      add(c) { this._classes.add(c); },
      remove(c) { this._classes.delete(c); },
      toggle(c, force) {
        if (force === undefined) {
          if (this._classes.has(c)) this._classes.delete(c);
          else this._classes.add(c);
        } else if (force) {
          this._classes.add(c);
        } else {
          this._classes.delete(c);
        }
      },
      contains(c) { return this._classes.has(c); }
    },
    dataset: {},
    getAttribute(name) { return this[name] || null; },
    setAttribute(name, val) { this[name] = val; },
    addEventListener(evt, fn) {
      if (!listeners[evt]) listeners[evt] = [];
      listeners[evt].push(fn);
    },
    dispatchEvent(evt) {
      const list = listeners[evt.type || evt] || [];
      list.forEach(fn => fn(evt));
    },
    closest(sel) {
      if (sel === '#' + id || sel === tag) return el;
      return null;
    },
    querySelector(sel) {
      if (sel === '[data-file-server-save]') return elements['fs-save'];
      return null;
    },
    querySelectorAll(sel) { return []; },
    showModal() { this._open = true; },
    close() { this._open = false; },
    reset() {
      if (this.id === 'file-server-form') {
        elements['fs-smb-enabled'].checked = elements['fs-smb-enabled'].defaultChecked;
        elements['fs-sftp-enabled'].checked = elements['fs-sftp-enabled'].defaultChecked;
        elements['fs-sftp-port'].value = elements['fs-sftp-port'].defaultValue;
        elements['fs-sftp-port'].valueAsNumber = Number(elements['fs-sftp-port'].defaultValue);
      }
    },
    ...props
  };
  elements[id] = el;
  return el;
}

// Dialog elements
createEl('file-server-dialog', 'dialog');
createEl('file-server-form', 'form');
const fsSave = createEl('fs-save', 'button', { disabled: true });
const smbCheck = createEl('fs-smb-enabled', 'input', { checked: false, defaultChecked: false });
const sftpCheck = createEl('fs-sftp-enabled', 'input', { checked: false, defaultChecked: false });
const sftpPort = createEl('fs-sftp-port', 'input', { value: '2022', defaultValue: '2022', valueAsNumber: 2022 });
const protoCardSmb = createEl('proto-card-smb', 'div');
const protoCardSftp = createEl('proto-card-sftp', 'div');
const smbChip = createEl('fs-smb-chip', 'span');
const sftpChip = createEl('fs-sftp-chip', 'span');
const sftpAddr = createEl('fs-sftp-addr', 'span', { dataset: { baseHost: '192.0.2.10' } });

global.document = {
  documentElement: { lang: 'ru' },
  getElementById(id) { return elements[id] || null; },
  querySelector(sel) {
    if (sel === '#fs-sftp-addr') return sftpAddr;
    return null;
  },
  querySelectorAll() { return []; },
  addEventListener(evt, fn) {
    if (!docListeners[evt]) docListeners[evt] = [];
    docListeners[evt].push(fn);
  }
};

let copiedText = '';
try {
  Object.defineProperty(globalThis.navigator, 'clipboard', {
    value: {
      writeText(txt) {
        copiedText = txt;
        return Promise.resolve();
      }
    },
    configurable: true,
    writable: true
  });
} catch (_) {
  global.navigator = {
    clipboard: {
      writeText(txt) {
        copiedText = txt;
        return Promise.resolve();
      }
    }
  };
}

// Load script
%s

// 1. Initial state: save button must be disabled
assert.equal(fsSave.disabled, true, 'fsSave must initially be disabled');

// 2. Toggle SMB: becomes dirty and valid -> save button enabled
smbCheck.checked = true;
elements['file-server-form'].dispatchEvent({ type: 'change' });
assert.equal(fsSave.disabled, false, 'fsSave should be enabled after toggling smb_enabled');
assert.equal(protoCardSmb.classList.contains('is-on'), true, 'proto-card-smb should have is-on class');
assert.equal(smbChip.className, 'proto-status is-on', 'smbChip should have proto-status is-on class');
assert.equal(smbChip.innerHTML.includes('dot-ok'), true, 'smbChip should contain dot-ok');
assert.equal(smbChip.innerHTML.includes('Активен'), true, 'smbChip should contain Активен');

// 3. Revert SMB: save button disabled again
smbCheck.checked = false;
elements['file-server-form'].dispatchEvent({ type: 'change' });
assert.equal(fsSave.disabled, true, 'fsSave should be disabled when smb_enabled reverted to default');
assert.equal(smbChip.className, 'proto-status is-off', 'smbChip should have proto-status is-off class');
assert.equal(smbChip.innerHTML.includes('dot-off'), true, 'smbChip should contain dot-off');
assert.equal(smbChip.innerHTML.includes('Выключен'), true, 'smbChip should contain Выключен');

// 4. Toggle SFTP: becomes dirty and valid -> save button enabled
sftpCheck.checked = true;
elements['file-server-form'].dispatchEvent({ type: 'change' });
assert.equal(fsSave.disabled, false, 'fsSave should be enabled after toggling sftp_enabled');

// 5. Change SFTP port to invalid (< 1024): save button disabled
sftpPort.value = '80';
sftpPort.valueAsNumber = 80;
elements['file-server-form'].dispatchEvent({ type: 'input' });
assert.equal(fsSave.disabled, true, 'fsSave should be disabled when sftp_port < 1024');

// 6. Change SFTP port to valid (2025): save button enabled
sftpPort.value = '2025';
sftpPort.valueAsNumber = 2025;
elements['file-server-form'].dispatchEvent({ type: 'input' });
assert.equal(fsSave.disabled, false, 'fsSave should be enabled when sftp_port is valid (2025)');

// 7. Form submit prevented when save is disabled
sftpPort.value = '80';
sftpPort.valueAsNumber = 80;
elements['file-server-form'].dispatchEvent({ type: 'input' });
assert.equal(fsSave.disabled, true);
let submitPrevented = false;
elements['file-server-form'].dispatchEvent({
  type: 'submit',
  preventDefault() { submitPrevented = true; }
});
assert.equal(submitPrevented, true, 'submit of file-server-form must be prevented when save is disabled');

// 8. Copy button feedback
const copyBtn = {
  textContent: 'Скопировать',
  getAttribute(name) {
    if (name === 'data-copy') return '\\\\192.0.2.10\\iso';
    return null;
  },
  closest(sel) {
    if (sel.includes('[data-copy]')) return copyBtn;
    return null;
  }
};
docListeners['click'].forEach(fn => fn({
  target: copyBtn,
  preventDefault() {}
}));
assert.equal(copiedText, '\\\\192.0.2.10\\iso', 'copy button must copy data-copy address');
queueMicrotask(() => {
  assert.equal(copyBtn.textContent, 'Скопировано', 'copy button text should become "Скопировано"');
  console.log('STORAGES_JS_GATE_PASS');
});
`, string(scriptBytes))

	cmd := exec.CommandContext(ctx, "node", "-e", jsTest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node storages.js test failed: %v\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "STORAGES_JS_GATE_PASS") {
		t.Fatalf("missing pass marker: %s", string(out))
	}
}

func TestFileServerUI_UsersNodeJS(t *testing.T) {
	scriptBytes, err := os.ReadFile("users.js")
	if err != nil {
		t.Fatalf("read users.js: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	jsTest := fmt.Sprintf(`
const assert = require('assert');

const docListeners = {};
const elements = {};

function createEl(id, tag, props = {}) {
  const listeners = {};
  const el = {
    id,
    tagName: (tag || 'DIV').toUpperCase(),
    value: '',
    checked: false,
    disabled: false,
    hidden: false,
    classList: {
      _classes: new Set(),
      add(c) { this._classes.add(c); },
      remove(c) { this._classes.delete(c); },
      toggle(c, force) {
        if (force === undefined) {
          if (this._classes.has(c)) this._classes.delete(c);
          else this._classes.add(c);
        } else if (force) {
          this._classes.add(c);
        } else {
          this._classes.delete(c);
        }
      },
      contains(c) { return this._classes.has(c); }
    },
    dataset: {},
    getAttribute(name) { return this[name] || null; },
    setAttribute(name, val) { this[name] = val; },
    addEventListener(evt, fn) {
      if (!listeners[evt]) listeners[evt] = [];
      listeners[evt].push(fn);
    },
    dispatchEvent(evt) {
      const list = listeners[evt.type || evt] || [];
      list.forEach(fn => fn(evt));
    },
    closest(sel) {
      if (sel === '#' + id || sel === tag) return el;
      return null;
    },
    querySelector(sel) {
      if (sel === 'form') return elements['user-fs-form'];
      if (sel === '[data-user-fs-save]') return elements['user-fs-save'];
      if (sel === 'input[name="mode"]:checked') {
        const radios = [elements['mode-none'], elements['mode-ro'], elements['mode-rw']];
        return radios.find(r => r.checked) || null;
      }
      return null;
    },
    querySelectorAll(sel) {
      if (sel === 'input[name="mode"]') {
        return [elements['mode-none'], elements['mode-ro'], elements['mode-rw']];
      }
      if (sel.includes('.radio-card')) {
        return [elements['card-none'], elements['card-ro'], elements['card-rw']];
      }
      return [];
    },
    showModal() { this._open = true; },
    close() { this._open = false; },
    style: {},
    ...props
  };
  elements[id] = el;
  return el;
}

createEl('user-fs-access-dialog', 'dialog');
createEl('user-fs-form', 'form');
const fsSave = createEl('user-fs-save', 'button', { disabled: true });
const idInput = createEl('user-fs-modal-id', 'input');
const nameSpan = createEl('user-fs-modal-name', 'span');
const pwInput = createEl('user-fs-pw-input', 'input');
const pwBox = createEl('user-fs-pw-box', 'div');
const pwLabel = createEl('user-fs-pw-label', 'span');

const radioNone = createEl('mode-none', 'input', { value: 'none', checked: true });
const radioRo = createEl('mode-ro', 'input', { value: 'ro', checked: false });
const radioRw = createEl('mode-rw', 'input', { value: 'rw', checked: false });

const cardNone = createEl('card-none', 'label', { querySelector: () => radioNone });
const cardRo = createEl('card-ro', 'label', { querySelector: () => radioRo });
const cardRw = createEl('card-rw', 'label', { querySelector: () => radioRw });

global.document = {
  documentElement: { lang: 'ru' },
  getElementById(id) { return elements[id] || null; },
  querySelector() { return null; },
  querySelectorAll() { return []; },
  addEventListener(evt, fn) {
    if (!docListeners[evt]) docListeners[evt] = [];
    docListeners[evt].push(fn);
  }
};

// Load script
%s

// SCENARIO 1: User has NO password yet (mode = 'none', hasPw = false)
const triggerUser1 = {
  getAttribute(name) {
    if (name === 'data-user-fs-id') return '10';
    if (name === 'data-user-fs-name') return 'newbie';
    if (name === 'data-user-fs-mode') return 'none';
    if (name === 'data-user-fs-has-pw') return 'false';
    return null;
  },
  closest(sel) {
    if (sel.includes('[data-user-fs-id]')) return triggerUser1;
    return null;
  }
};

// Click trigger opens dialog
docListeners['click'].forEach(fn => fn({ target: triggerUser1, preventDefault() {} }));
assert.equal(fsSave.disabled, true, 'Save must be disabled on initial open for mode none');

// Select mode 'ro': still disabled because password is required for first access!
radioNone.checked = false;
radioRo.checked = true;
elements['user-fs-form'].dispatchEvent({ type: 'change' });
assert.equal(fsSave.disabled, true, 'Save must remain disabled when switching to ro without password');

// Enter password of only 8 chars (< 12): still disabled
pwInput.value = 'short8ch';
elements['user-fs-form'].dispatchEvent({ type: 'input' });
assert.equal(fsSave.disabled, true, 'Save must remain disabled when password < 12 chars');

// Enter password of 12 chars: becomes enabled!
pwInput.value = 'ValidPass1234!';
elements['user-fs-form'].dispatchEvent({ type: 'input' });
assert.equal(fsSave.disabled, false, 'Save must be enabled with valid 12+ char password');

// Switch back to 'none': password cleared/not needed, but initial was 'none' so not dirty -> disabled
radioRo.checked = false;
radioNone.checked = true;
pwInput.value = '';
elements['user-fs-form'].dispatchEvent({ type: 'change' });
assert.equal(fsSave.disabled, true, 'Save must be disabled if switched back to initial mode none');

// SCENARIO 2: User ALREADY has a password (mode = 'ro', hasPw = true)
const triggerUser2 = {
  getAttribute(name) {
    if (name === 'data-user-fs-id') return '2';
    if (name === 'data-user-fs-name') return 'engineer';
    if (name === 'data-user-fs-mode') return 'ro';
    if (name === 'data-user-fs-has-pw') return 'true';
    return null;
  },
  closest(sel) {
    if (sel.includes('[data-user-fs-id]')) return triggerUser2;
    return null;
  }
};

docListeners['click'].forEach(fn => fn({ target: triggerUser2, preventDefault() {} }));
assert.equal(fsSave.disabled, true, 'Save must be disabled on initial open for existing ro user');

// Switch mode to 'rw': password can stay empty! Save becomes enabled!
radioRo.checked = false;
radioRw.checked = true;
pwInput.value = '';
elements['user-fs-form'].dispatchEvent({ type: 'change' });
assert.equal(fsSave.disabled, false, 'Save must be enabled when changing mode ro -> rw without new password');

// Switch mode to 'none': save enabled (mode changed)
radioRw.checked = false;
radioNone.checked = true;
elements['user-fs-form'].dispatchEvent({ type: 'change' });
assert.equal(fsSave.disabled, false, 'Save must be enabled when changing mode ro -> none');

// Switch back to initial 'ro': save disabled
radioNone.checked = false;
radioRo.checked = true;
elements['user-fs-form'].dispatchEvent({ type: 'change' });
assert.equal(fsSave.disabled, true, 'Save must be disabled when mode reverted to initial ro and pw is empty');

// In initial 'ro', type a short password: invalid -> disabled
pwInput.value = 'short';
elements['user-fs-form'].dispatchEvent({ type: 'input' });
assert.equal(fsSave.disabled, true, 'Save must be disabled when new password is short');

// In initial 'ro', type a valid new password: dirty & valid -> enabled!
pwInput.value = 'NewPassword12345!';
elements['user-fs-form'].dispatchEvent({ type: 'input' });
assert.equal(fsSave.disabled, false, 'Save must be enabled when valid new password is typed');

// Form submit prevented when save is disabled
pwInput.value = 'short';
elements['user-fs-form'].dispatchEvent({ type: 'input' });
assert.equal(fsSave.disabled, true);
let submitPrevented = false;
elements['user-fs-form'].dispatchEvent({
  type: 'submit',
  preventDefault() { submitPrevented = true; }
});
assert.equal(submitPrevented, true, 'submit of user-fs-form must be prevented when save is disabled');

console.log('USERS_FS_JS_GATE_PASS');
`, string(scriptBytes))

	cmd := exec.CommandContext(ctx, "node", "-e", jsTest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node users.js test failed: %v\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "USERS_FS_JS_GATE_PASS") {
		t.Fatalf("missing pass marker: %s", string(out))
	}
}
