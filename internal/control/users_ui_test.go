package control

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestUsersScriptEndpoint(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/assets/users.js", nil)
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	contentType := rec.Header().Get("Content-Type")
	if contentType != "text/javascript; charset=utf-8" {
		t.Fatalf("expected Content-Type text/javascript; charset=utf-8, got %q", contentType)
	}

	body := rec.Body.String()
	for _, expected := range []string{
		"user-search",
		"user-password-dialog",
		"updateRowDirty",
		"saveBtn.disabled = !isDirty",
		"updateCreateSubmit",
		"updatePwSubmit",
		"createSubmitBtn.disabled",
		"pwSubmitBtn.disabled",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("users.js does not contain expected identifier %q", expected)
		}
	}
}

func TestUsersPageNoDetailsOrSummary(t *testing.T) {
	for _, lang := range []string{langRU, langEN} {
		rec := httptest.NewRecorder()
		render(rec, "users", samplePageData(lang))
		if rec.Code != http.StatusOK {
			t.Fatalf("render users (%s) failed with status %d", lang, rec.Code)
		}
		body := rec.Body.String()
		if strings.Contains(body, "<details") {
			t.Errorf("users page (%s) must not contain <details>", lang)
		}
		if strings.Contains(body, "<summary") {
			t.Errorf("users page (%s) must not contain <summary>", lang)
		}
	}
}

func TestUsersPageToolbarAndTable(t *testing.T) {
	rec := httptest.NewRecorder()
	render(rec, "users", samplePageData(langRU))
	if rec.Code != http.StatusOK {
		t.Fatalf("render users failed: %d", rec.Code)
	}
	body := rec.Body.String()

	requiredSelectors := []string{
		`class="toolbar"`,
		`id="user-search"`,
		`id="user-count"`,
		`class="user-table"`,
		`data-user-row`,
		`data-user-pw-id="1"`,
		`data-user-pw-name="admin"`,
		`data-user-add`,
		`/users/change`,
		`action="/users/create"`,
		`action="/users/reset-password"`,
		`action="/users/delete"`,
		`action="/users/password-policy"`,
		`action="/users/user-password-policy"`,
		`data-password-policy-open`,
		`data-user-expiry-id="1"`,
		`data-user-expiry-id="2"`,
		`popovertarget="user-menu-2"`,
		`data-user-delete-id="2"`,
		`data-user-delete-name="viewer"`,
		`class="pw-cell"`,
	}
	for _, sel := range requiredSelectors {
		if !strings.Contains(body, sel) {
			t.Errorf("users page missing required markup element: %s", sel)
		}
	}
	if strings.Contains(body, "policy-summary-pill") {
		t.Errorf("users toolbar must not show policy-summary-pill")
	}
}

func TestUsersPageModals(t *testing.T) {
	rec := httptest.NewRecorder()
	render(rec, "users", samplePageData(langRU))
	if rec.Code != http.StatusOK {
		t.Fatalf("render users failed: %d", rec.Code)
	}
	body := rec.Body.String()

	modalRequirements := []string{
		`id="user-create-dialog"`,
		`id="user-password-dialog"`,
		`id="user-delete-dialog"`,
		`id="password-policy-dialog"`,
		`id="user-expiry-dialog"`,
		`id="delete-modal-user"`,
		`id="delete-modal-id"`,
		`name="must_change_password" checked`,
		`class="user-dialog"`,
		`data-close-dialog`,
		`src="/assets/users.js"`,
		`src="/assets/password.js"`,
	}
	for _, req := range modalRequirements {
		if !strings.Contains(body, req) {
			t.Errorf("users page missing modal requirement: %s", req)
		}
	}

	pwGenCount := strings.Count(body, "data-password-generator")
	if pwGenCount != 2 {
		t.Errorf("expected 2 data-password-generator inputs, got %d", pwGenCount)
	}

	if !strings.Contains(body, `<button class="btn btn-primary" disabled>Создать</button>`) {
		t.Errorf("user-create-dialog submit button must have disabled attribute")
	}
	if !strings.Contains(body, `<button class="btn btn-primary" disabled>Сменить пароль</button>`) {
		t.Errorf("user-password-dialog submit button must have disabled attribute")
	}

	if !strings.Contains(body, `name="username" pattern="^[A-Za-z0-9_.-]{3,32}$" minlength="3" maxlength="32"`) {
		t.Errorf("user-create-dialog username input must have pattern and length constraints")
	}
	if !strings.Contains(body, `От 3 до 32 символов: латиница, цифры, дефис, точка и подчёркивание.`) {
		t.Errorf("user-create-dialog missing RU username format hint")
	}

	recEN := httptest.NewRecorder()
	render(recEN, "users", samplePageData(langEN))
	bodyEN := recEN.Body.String()
	if !strings.Contains(bodyEN, `3 to 32 characters: Latin letters, digits, hyphen, dot, and underscore.`) {
		t.Errorf("user-create-dialog missing EN username format hint: %s", bodyEN)
	}
}

type userChangeMockStore struct {
	authOnlyStore
	users       []User
	called      bool
	deletedID   int64
	deletedName string
	lastErr     error
	audits      []string
}

func (s *userChangeMockStore) Users(context.Context) ([]User, error) {
	return s.users, nil
}

func (s *userChangeMockStore) ChangeUser(context.Context, int64, int64, string, bool) error {
	s.called = true
	return s.lastErr
}

func (s *userChangeMockStore) DeleteUser(_ context.Context, id, _ int64) (string, error) {
	s.called = true
	s.deletedID = id
	if s.lastErr != nil {
		return "", s.lastErr
	}
	return s.deletedName, nil
}

func (s *userChangeMockStore) Audit(_ context.Context, _ int64, action, result, detail string) error {
	s.audits = append(s.audits, action+"|"+result+"|"+detail)
	return nil
}

func TestUsersPageProtectsCurrentUserRow(t *testing.T) {
	for _, tc := range []struct {
		lang, you string
	}{
		{langRU, ">вы<"},
		{langEN, ">you<"},
	} {
		rec := httptest.NewRecorder()
		render(rec, "users", samplePageData(tc.lang))
		if rec.Code != http.StatusOK {
			t.Fatalf("render users (%s): %d", tc.lang, rec.Code)
		}
		body := rec.Body.String()
		for _, want := range []string{
			tc.you,
			`data-user-pw-id="1"`,
			`id="user-change-2"`,
			`data-user-save="2"`,
			`<button form="user-change-2" class="btn" data-user-save="2" disabled>`,
			`popovertarget="user-menu-1"`,
			`popovertarget="user-menu-2"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("users (%s) lacks %q", tc.lang, want)
			}
		}
		if strings.Contains(body, `id="user-change-1"`) || strings.Contains(body, `data-user-save="1"`) || strings.Contains(body, `data-user-delete-id="1"`) {
			t.Errorf("users (%s) must not render change form, Save button or delete action for current user", tc.lang)
		}
		selfStart := strings.Index(body, `data-user-id="1"`)
		selfEnd := strings.Index(body, `data-user-id="2"`)
		if selfStart < 0 || selfEnd <= selfStart {
			t.Fatalf("unexpected row order in users (%s)", tc.lang)
		}
		selfRow := body[selfStart:selfEnd]
		if !strings.Contains(selfRow, `<select name="role" disabled`) || !strings.Contains(selfRow, `type="checkbox" name="enabled"`) || !strings.Contains(selfRow, `disabled`) {
			t.Errorf("current user row (%s) must disable role and enabled controls: %s", tc.lang, selfRow)
		}
	}

	css, _ := templateFS.ReadFile("templates/style.css")
	if !strings.Contains(string(css), ".user-table td small{display:inline") {
		t.Errorf("stylesheet must keep user id and badge inline in .user-table")
	}
	if !strings.Contains(string(css), ".user-row-actions button:disabled") {
		t.Errorf("stylesheet must define disabled state for .user-row-actions buttons")
	}
}

func TestUserChangeBlocksSelfDemotionAndDisable(t *testing.T) {
	admin := User{ID: 1, Username: "vlad", Role: "admin", Enabled: true, Language: langRU}
	other := User{ID: 2, Username: "op", Role: "operator", Enabled: true}

	for _, tc := range []struct {
		name string
		form url.Values
	}{
		{"demote self", url.Values{"id": {"1"}, "role": {"operator"}, "enabled": {"on"}}},
		{"disable self", url.Values{"id": {"1"}, "role": {"admin"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &userChangeMockStore{
				authOnlyStore: authOnlyStore{user: admin},
				users:         []User{admin, other},
			}
			res := httptest.NewRecorder()
			(&Server{Store: store}).Handler().ServeHTTP(res, accountRequest(http.MethodPost, "/users/change", tc.form))
			if res.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", res.Code)
			}
			if store.called {
				t.Fatalf("ChangeUser in store must not be called for self demote/disable")
			}
			body := res.Body.String()
			if !strings.Contains(body, `role="alert"`) || !strings.Contains(body, "Нельзя отключить свою учётную запись или понизить свою роль администратора") {
				t.Fatalf("missing translated self-protection alert: %s", body)
			}
			if len(store.audits) == 0 || !strings.HasPrefix(store.audits[0], "user.change|error|") {
				t.Fatalf("audits = %v", store.audits)
			}
		})
	}

	t.Run("last admin error is translated", func(t *testing.T) {
		enAdmin := admin
		enAdmin.Language = langEN
		store := &userChangeMockStore{
			authOnlyStore: authOnlyStore{user: enAdmin},
			users:         []User{enAdmin, other},
			lastErr:       errLastAdmin,
		}
		res := httptest.NewRecorder()
		(&Server{Store: store}).Handler().ServeHTTP(res, accountRequest(http.MethodPost, "/users/change", url.Values{"id": {"2"}, "role": {"operator"}, "enabled": {"on"}}))
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "You cannot disable or demote the last administrator") {
			t.Fatalf("last admin alert: %d %s", res.Code, res.Body.String())
		}
	})
}

func TestUserDeleteHandler(t *testing.T) {
	admin := User{ID: 1, Username: "vlad", Role: "admin", Enabled: true, Language: langRU}
	other := User{ID: 2, Username: "op", Role: "operator", Enabled: true}

	t.Run("success", func(t *testing.T) {
		store := &userChangeMockStore{
			authOnlyStore: authOnlyStore{user: admin},
			users:         []User{admin, other},
			deletedName:   "op",
		}
		res := httptest.NewRecorder()
		(&Server{Store: store}).Handler().ServeHTTP(res, accountRequest(http.MethodPost, "/users/delete", url.Values{"id": {"2"}}))
		if res.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303: %s", res.Code, res.Body.String())
		}
		loc, _ := url.QueryUnescape(res.Header().Get("Location"))
		if loc != "/users?message=Пользователь «op» удалён" {
			t.Fatalf("location = %q", loc)
		}
		if store.deletedID != 2 {
			t.Fatalf("deletedID = %d, want 2", store.deletedID)
		}
		if len(store.audits) == 0 || store.audits[0] != "user.delete|ok|id=2 username=op" {
			t.Fatalf("audits = %v", store.audits)
		}
	})

	t.Run("blocks self deletion", func(t *testing.T) {
		store := &userChangeMockStore{
			authOnlyStore: authOnlyStore{user: admin},
			users:         []User{admin, other},
		}
		res := httptest.NewRecorder()
		(&Server{Store: store}).Handler().ServeHTTP(res, accountRequest(http.MethodPost, "/users/delete", url.Values{"id": {"1"}}))
		if res.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", res.Code)
		}
		if store.called {
			t.Fatalf("DeleteUser in store must not be called for self deletion")
		}
		if !strings.Contains(res.Body.String(), "Нельзя удалить свою учётную запись") {
			t.Fatalf("missing self-delete error message: %s", res.Body.String())
		}
	})

	t.Run("last admin error in English", func(t *testing.T) {
		enAdmin := admin
		enAdmin.Language = langEN
		store := &userChangeMockStore{
			authOnlyStore: authOnlyStore{user: enAdmin},
			users:         []User{enAdmin, other},
			lastErr:       errLastAdmin,
		}
		res := httptest.NewRecorder()
		(&Server{Store: store}).Handler().ServeHTTP(res, accountRequest(http.MethodPost, "/users/delete", url.Values{"id": {"2"}}))
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "You cannot delete the last administrator") {
			t.Fatalf("last admin delete alert: %d %s", res.Code, res.Body.String())
		}
	})

	t.Run("user not found", func(t *testing.T) {
		store := &userChangeMockStore{
			authOnlyStore: authOnlyStore{user: admin},
			users:         []User{admin},
			lastErr:       errUserNotFound,
		}
		res := httptest.NewRecorder()
		(&Server{Store: store}).Handler().ServeHTTP(res, accountRequest(http.MethodPost, "/users/delete", url.Values{"id": {"99"}}))
		if res.Code != http.StatusNotFound || !strings.Contains(res.Body.String(), "Пользователь не найден") {
			t.Fatalf("not found alert: %d %s", res.Code, res.Body.String())
		}
	})

	t.Run("operator forbidden", func(t *testing.T) {
		store := &userChangeMockStore{
			authOnlyStore: authOnlyStore{user: other},
		}
		res := httptest.NewRecorder()
		(&Server{Store: store}).Handler().ServeHTTP(res, accountRequest(http.MethodPost, "/users/delete", url.Values{"id": {"1"}}))
		if res.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", res.Code)
		}
	})
}

func TestUserDialogSubmitButtonsGate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	scriptBytes, err := os.ReadFile("users.js")
	if err != nil {
		t.Fatalf("failed to read users.js: %v", err)
	}

	jsTest := fmt.Sprintf(`
const vm = require('node:vm');
const assert = require('node:assert/strict');

const code = %q;

let docListeners = {};
const createSubmitBtn = { disabled: true, classList: { add() {}, remove() {} } };
const createUserInput = { value: '' };
const createPwInput = {
  value: '',
  listeners: {},
  addEventListener(type, fn) { this.listeners[type] = fn; },
  dispatchEvent(event) { if (this.listeners[event.type]) this.listeners[event.type](event); },
};

let createFormListeners = {};
const createFormEl = {
  addEventListener(type, fn) { createFormListeners[type] = fn; },
  querySelector(sel) {
    if (sel === 'input[name="username"]') return createUserInput;
    if (sel === 'input[name="password"]') return createPwInput;
    if (sel === 'button.btn-primary') return createSubmitBtn;
    return null;
  },
};

const createDialogEl = {
  shown: false,
  showModal() { this.shown = true; },
  close() { this.shown = false; },
  querySelector(sel) {
    if (sel === 'form') return createFormEl;
    if (sel === 'input[name="username"]') return createUserInput;
    if (sel === 'input[name="password"]') return createPwInput;
    if (sel === 'button.btn-primary') return createSubmitBtn;
    return null;
  },
  querySelectorAll() { return []; },
  addEventListener() {},
};

const pwSubmitBtn = { disabled: true, classList: { add() {}, remove() {} } };
const pwInput = {
  value: '',
  listeners: {},
  addEventListener(type, fn) { this.listeners[type] = fn; },
  dispatchEvent(event) { if (this.listeners[event.type]) this.listeners[event.type](event); },
};

let pwFormListeners = {};
const pwFormEl = {
  addEventListener(type, fn) { pwFormListeners[type] = fn; },
  querySelector(sel) {
    if (sel === 'input[name="password"]') return pwInput;
    if (sel === 'button.btn-primary') return pwSubmitBtn;
    return null;
  },
};

const pwDialogEl = {
  shown: false,
  showModal() { this.shown = true; },
  close() { this.shown = false; },
  querySelector(sel) {
    if (sel === 'form') return pwFormEl;
    if (sel === 'input[name="password"]') return pwInput;
    if (sel === 'button.btn-primary') return pwSubmitBtn;
    return null;
  },
  querySelectorAll() { return []; },
  addEventListener() {},
};

const document = {
  documentElement: { lang: 'ru' },
  querySelectorAll: () => [],
  querySelector: () => null,
  getElementById: (id) => {
    if (id === 'user-create-dialog') return createDialogEl;
    if (id === 'user-password-dialog') return pwDialogEl;
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

// 1. Initial state: both submit buttons must be disabled
assert.equal(createSubmitBtn.disabled, true, 'createSubmitBtn must be disabled initially');
assert.equal(pwSubmitBtn.disabled, true, 'pwSubmitBtn must be disabled initially');

// 2. User Create Form validation
// Only username: still disabled
createUserInput.value = 'testuser';
createFormListeners['input']();
assert.equal(createSubmitBtn.disabled, true, 'createSubmitBtn must be disabled without password');

// Short password (<12): still disabled
createPwInput.value = 'short123';
createFormListeners['input']();
assert.equal(createSubmitBtn.disabled, true, 'createSubmitBtn must be disabled with short password');

// Valid password (>=12): becomes enabled
createPwInput.value = 'SecretPass1234!';
createFormListeners['input']();
assert.equal(createSubmitBtn.disabled, false, 'createSubmitBtn must be enabled with valid username and 12+ chars password');

// Clear username: becomes disabled again
createUserInput.value = '   ';
createFormListeners['input']();
assert.equal(createSubmitBtn.disabled, true, 'createSubmitBtn must be disabled when username is whitespace');

// Invalid username checks (forbidden characters, Cyrillic, lengths):
createUserInput.value = 'админ'; // Cyrillic
createFormListeners['input']();
assert.equal(createSubmitBtn.disabled, true, 'createSubmitBtn must be disabled for Cyrillic username');

createUserInput.value = 'user name'; // space
createFormListeners['input']();
assert.equal(createSubmitBtn.disabled, true, 'createSubmitBtn must be disabled for username with space');

createUserInput.value = 'user@domain'; // @ forbidden
createFormListeners['input']();
assert.equal(createSubmitBtn.disabled, true, 'createSubmitBtn must be disabled for username with @');

createUserInput.value = 'user/admin'; // slash forbidden
createFormListeners['input']();
assert.equal(createSubmitBtn.disabled, true, 'createSubmitBtn must be disabled for username with slash');

createUserInput.value = 'ab'; // too short (< 3)
createFormListeners['input']();
assert.equal(createSubmitBtn.disabled, true, 'createSubmitBtn must be disabled for username shorter than 3 chars');

createUserInput.value = 'a'.repeat(33); // too long (> 32)
createFormListeners['input']();
assert.equal(createSubmitBtn.disabled, true, 'createSubmitBtn must be disabled for username longer than 32 chars');

createUserInput.value = 'a'.repeat(32); // exactly 32 chars: valid
createFormListeners['input']();
assert.equal(createSubmitBtn.disabled, false, 'createSubmitBtn must be enabled for 32 chars valid username');

// Restore username: becomes enabled
createUserInput.value = 'ivan-1.dev_';
createFormListeners['input']();
assert.equal(createSubmitBtn.disabled, false, 'createSubmitBtn must be enabled when username restored');

// Re-opening create modal resets values and disables button
const addBtn = { closest: (sel) => (sel === '[data-user-add]' ? addBtn : null) };
docListeners['click']({ target: addBtn, preventDefault() {} });
assert.equal(createUserInput.value, '', 'opening create dialog must reset username');
assert.equal(createPwInput.value, '', 'opening create dialog must reset password');
assert.equal(createSubmitBtn.disabled, true, 'createSubmitBtn must be disabled after opening dialog');

// 3. User Password Dialog validation
// Empty password: disabled
pwInput.value = '';
pwFormListeners['input']();
assert.equal(pwSubmitBtn.disabled, true, 'pwSubmitBtn must be disabled for empty password');

// Short password: disabled
pwInput.value = '12345678901';
pwFormListeners['input']();
assert.equal(pwSubmitBtn.disabled, true, 'pwSubmitBtn must be disabled for 11 chars');

// Valid password (12 chars): enabled
pwInput.value = '123456789012';
pwFormListeners['input']();
assert.equal(pwSubmitBtn.disabled, false, 'pwSubmitBtn must be enabled for 12 chars');

// Re-opening password dialog resets password and disables button
const pwOpenBtn = {
  getAttribute: (name) => (name === 'data-user-pw-id' ? '2' : 'operator'),
  closest: (sel) => (sel === '[data-user-pw-id]' ? pwOpenBtn : null),
};
docListeners['click']({ target: pwOpenBtn, preventDefault() {} });
assert.equal(pwInput.value, '', 'opening pw dialog must reset password');
assert.equal(pwSubmitBtn.disabled, true, 'pwSubmitBtn must be disabled after opening dialog');

// 4. Form submit gate prevention when disabled
let prevented = false;
docListeners['submit']({ target: createFormEl, preventDefault() { prevented = true; } });
assert.equal(prevented, true, 'submit of create form must be prevented when button is disabled');

let pwPrevented = false;
docListeners['submit']({ target: pwFormEl, preventDefault() { pwPrevented = true; } });
assert.equal(pwPrevented, true, 'submit of pw form must be prevented when button is disabled');

console.log('USER_DIALOG_BUTTONS_GATE_PASS');
`, string(scriptBytes))

	cmd := exec.CommandContext(ctx, "node", "-e", jsTest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node users.js test failed: %v\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "USER_DIALOG_BUTTONS_GATE_PASS") {
		t.Fatalf("missing pass marker: %s", string(out))
	}
}

func TestUserNamePatternValidation(t *testing.T) {
	cases := []struct {
		username string
		valid    bool
	}{
		{"", false},
		{"ab", false},
		{"abc", true},
		{"admin", true},
		{"john.doe", true},
		{"ops-team_1", true},
		{strings.Repeat("a", 32), true},
		{strings.Repeat("a", 33), false},
		{"админ", false},
		{"user name", false},
		{"user@domain", false},
		{"user/path", false},
		{"user\\domain", false},
		{"user:1", false},
		{"user*name", false},
		{"user?name", false},
		{"user#1", false},
		{"user$1", false},
	}

	for _, tc := range cases {
		got := userNamePattern.MatchString(tc.username)
		if got != tc.valid {
			t.Errorf("userNamePattern.MatchString(%q) = %v; want %v", tc.username, got, tc.valid)
		}
	}
}

func TestUserPasswordPolicyDialogsJS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	scriptBytes, err := os.ReadFile("users.js")
	if err != nil {
		t.Fatalf("failed to read users.js: %v", err)
	}

	jsTest := fmt.Sprintf(`
const vm = require('node:vm');
const assert = require('node:assert/strict');

const code = %q;
let docListeners = {};

const policyMaxInput = { value: '90', addEventListener() {} };
const policyWarnInput = { value: '7', disabled: false };
const policyWarnWrap = { style: {} };
const policyPresets = [
  { getAttribute: (n) => n === 'data-policy-preset' ? '0' : null, classList: { toggle() {} } },
  { getAttribute: (n) => n === 'data-policy-preset' ? '90' : null, classList: { toggle() {} } },
];
const policyDialog = {
  shown: false,
  showModal() { this.shown = true; },
  close() { this.shown = false; },
  querySelector(sel) {
    if (sel === 'input[name="max_age_days"]') return policyMaxInput;
    if (sel === 'input[name="warn_days"]') return policyWarnInput;
    if (sel === '[data-policy-warn-wrap]') return policyWarnWrap;
    return null;
  },
  querySelectorAll(sel) {
    if (sel === '[data-policy-preset]') return policyPresets;
    return [];
  },
  addEventListener() {},
};

const expiryIdInput = { value: '' };
const expiryUserSpan = { textContent: '' };
const expiryChangedSpan = { textContent: '' };
const expiryDaysInput = { value: '30', required: false };
const expiryCustomWrap = { hidden: true };
const expiryMustWrap = { hidden: false };
const expiryMustCheck = { checked: false };
const radioPolicy = { name: 'password_policy_mode', value: 'policy', checked: false, closest() { return { classList: { toggle() {} } }; } };
const radioNever = { name: 'password_policy_mode', value: 'never', checked: false, closest() { return { classList: { toggle() {} } }; } };
const radioCustom = { name: 'password_policy_mode', value: 'custom', checked: false, closest() { return { classList: { toggle() {} } }; } };

const expiryDialog = {
  shown: false,
  showModal() { this.shown = true; },
  close() { this.shown = false; },
  querySelector(sel) {
    if (sel === 'input[name="id"]') return expiryIdInput;
    if (sel === '#expiry-modal-user') return expiryUserSpan;
    if (sel === '#expiry-modal-changed') return expiryChangedSpan;
    if (sel === 'input[name="password_max_age_days"]') return expiryDaysInput;
    if (sel === '[data-expiry-custom-wrap]') return expiryCustomWrap;
    if (sel === '[data-expiry-must-change-wrap]') return expiryMustWrap;
    if (sel === 'input[name="must_change_password"]') return expiryMustCheck;
    return null;
  },
  querySelectorAll(sel) {
    if (sel === 'input[name="password_policy_mode"]') return [radioPolicy, radioNever, radioCustom];
    return [];
  },
  addEventListener() {},
};

const document = {
  documentElement: { lang: 'ru' },
  querySelectorAll: () => [],
  querySelector: () => null,
  getElementById: (id) => {
    if (id === 'password-policy-dialog') return policyDialog;
    if (id === 'user-expiry-dialog') return expiryDialog;
    return null;
  },
  addEventListener: (type, fn) => { docListeners[type] = fn; },
};

vm.runInNewContext(code, { document, window: {}, Event: class { constructor(t) { this.type = t; } } });

// Open policy dialog and click preset 0
const polOpen = { closest: (s) => (s === '[data-password-policy-open]' ? polOpen : null) };
docListeners['click']({ target: polOpen, preventDefault() {} });
assert.equal(policyDialog.shown, true, 'policyDialog must open');

const presetZero = {
  getAttribute: (n) => (n === 'data-policy-preset' ? '0' : null),
  closest: (s) => (s === '[data-policy-preset]' ? presetZero : null),
};
docListeners['click']({ target: presetZero, preventDefault() {} });
assert.equal(policyMaxInput.value, '0');
assert.equal(policyWarnInput.disabled, true, 'warn_days disabled when max_age_days=0');

// Open user-expiry dialog for other user in custom mode
const expBtn = {
  getAttribute: (n) => ({
    'data-user-expiry-id': '2',
    'data-user-expiry-name': 'operator1',
    'data-user-expiry-mode': 'custom',
    'data-user-expiry-days': '45',
    'data-user-expiry-must-change': 'true',
    'data-user-expiry-changed': '24.08.2026',
    'data-user-self': 'false',
  }[n] || ''),
  closest: (s) => (s === '[data-user-expiry-id]' ? expBtn : null),
};
docListeners['click']({ target: expBtn, preventDefault() {} });
assert.equal(expiryDialog.shown, true, 'expiryDialog must open');
assert.equal(expiryIdInput.value, '2');
assert.equal(radioCustom.checked, true);
assert.equal(expiryCustomWrap.hidden, false);
assert.equal(expiryDaysInput.value, '45');
assert.equal(expiryDaysInput.required, true);
assert.equal(expiryMustWrap.hidden, false);
assert.equal(expiryMustCheck.checked, true);

// Switch mode to never -> custom input hidden and not required
radioCustom.checked = false;
radioNever.checked = true;
docListeners['change']({ target: radioNever });
assert.equal(expiryCustomWrap.hidden, true);
assert.equal(expiryDaysInput.required, false);

// Open for self -> must_change_password hidden and unchecked
const selfExpBtn = {
  getAttribute: (n) => ({
    'data-user-expiry-id': '1',
    'data-user-expiry-name': 'admin',
    'data-user-expiry-mode': 'never',
    'data-user-expiry-days': '30',
    'data-user-expiry-must-change': 'false',
    'data-user-expiry-changed': '18.11.2026',
    'data-user-self': 'true',
  }[n] || ''),
  closest: (s) => (s === '[data-user-expiry-id]' ? selfExpBtn : null),
};
docListeners['click']({ target: selfExpBtn, preventDefault() {} });
assert.equal(expiryMustWrap.hidden, true);
assert.equal(expiryMustCheck.checked, false);

console.log('USER_EXPIRY_DIALOG_PASS');
`, string(scriptBytes))

	out, err := exec.CommandContext(ctx, "node", "-e", jsTest).CombinedOutput()
	if err != nil {
		t.Fatalf("node users.js expiry test failed: %v\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "USER_EXPIRY_DIALOG_PASS") {
		t.Fatalf("missing marker: %s", string(out))
	}
}
