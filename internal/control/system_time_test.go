package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"omb/internal/agent"
)

type fakeSystemTimeAPI struct {
	state     agent.SystemTimeState
	getErr    error
	setErr    error
	lastReq   agent.SystemTimeUpdate
	setCalled int
}

func (f *fakeSystemTimeAPI) SystemTime(context.Context) (agent.SystemTimeState, error) {
	if f.getErr != nil {
		return agent.SystemTimeState{}, f.getErr
	}
	return f.state, nil
}

func (f *fakeSystemTimeAPI) SetSystemTime(_ context.Context, req agent.SystemTimeUpdate) (agent.SystemTimeState, error) {
	f.setCalled++
	f.lastReq = req
	if f.setErr != nil {
		return agent.SystemTimeState{}, f.setErr
	}
	f.state.Timezone = req.Timezone
	f.state.NTPEnabled = req.NTPEnabled
	f.state.NTPServers = req.NTPServers
	return f.state, nil
}

type systemTimeAuditStore struct {
	authOnlyStore
	audits []string
}

func (s *systemTimeAuditStore) Audit(_ context.Context, _ int64, action, result, detail string) error {
	s.audits = append(s.audits, action+"|"+result+"|"+detail)
	return nil
}

func TestAgentClientSystemTimeRoundTrip(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "agent.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/system/time":
			_ = json.NewEncoder(w).Encode(agent.SystemTimeState{
				Timezone:        "Europe/Moscow",
				NTPEnabled:      true,
				NTPSynchronized: true,
				NTPBackend:      "chrony",
				NTPServers:      []string{"ntp.corp.local"},
				Timezones:       []string{"UTC", "Europe/Moscow"},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/system/time":
			var req agent.SystemTimeUpdate
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "bad json", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(agent.SystemTimeState{
				Timezone:   req.Timezone,
				NTPEnabled: req.NTPEnabled,
				NTPServers: req.NTPServers,
				NTPBackend: "chrony",
			})
		default:
			http.NotFound(w, r)
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	client := NewAgentClient(sock)
	st, err := client.SystemTime(context.Background())
	if err != nil {
		t.Fatalf("SystemTime() error = %v", err)
	}
	if st.Timezone != "Europe/Moscow" || st.NTPBackend != "chrony" || !reflect.DeepEqual(st.NTPServers, []string{"ntp.corp.local"}) {
		t.Fatalf("unexpected state: %+v", st)
	}

	updated, err := client.SetSystemTime(context.Background(), agent.SystemTimeUpdate{
		Timezone:   "UTC",
		NTPEnabled: true,
		NTPServers: []string{"192.168.10.1"},
	})
	if err != nil {
		t.Fatalf("SetSystemTime() error = %v", err)
	}
	if updated.Timezone != "UTC" || !reflect.DeepEqual(updated.NTPServers, []string{"192.168.10.1"}) {
		t.Fatalf("unexpected updated state: %+v", updated)
	}
}

func TestServerLocationUpdatesDynamically(t *testing.T) {
	t.Cleanup(func() { SetServerTimezone("") })

	SetServerTimezone("Asia/Vladivostok")
	if got := serverLocation().String(); got != "Asia/Vladivostok" {
		t.Fatalf("serverLocation() = %q, want Asia/Vladivostok", got)
	}
	ref := time.Date(2026, 11, 19, 0, 15, 0, 0, time.UTC)
	if got := ref.In(serverLocation()).Format("02.01.2006 15:04"); got != "19.11.2026 10:15" {
		t.Fatalf("formatted Vladivostok time = %q", got)
	}

	SetServerTimezone("UTC")
	if got := serverLocation().String(); got != "UTC" {
		t.Fatalf("serverLocation() = %q, want UTC", got)
	}
}

func TestSystemTimeUpdateHandler(t *testing.T) {
	t.Cleanup(func() { SetServerTimezone("") })

	api := &fakeSystemTimeAPI{
		state: agent.SystemTimeState{
			Timezone:        "Europe/Moscow",
			NTPEnabled:      true,
			NTPSynchronized: true,
			NTPBackend:      "chrony",
			Timezones:       []string{"UTC", "Europe/Moscow", "Asia/Yekaterinburg"},
		},
	}
	store := &systemTimeAuditStore{
		authOnlyStore: authOnlyStore{user: User{ID: 1, Username: "admin", Role: "admin", Enabled: true}},
	}
	s := &Server{Store: store, SystemTime: api}
	h := s.Handler()
	csrf := CSRFToken("test-token")

	// 1. Admin sets corporate NTP server + timezone
	form := url.Values{
		"csrf":        {csrf},
		"timezone":    {"Asia/Yekaterinburg"},
		"mode":        {"ntp"},
		"ntp_servers": {"ntp.corp.local, 192.168.10.1"},
	}
	req := httptest.NewRequest(http.MethodPost, "/system/time", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("POST /system/time = %d, want 303 (%s)", res.Code, res.Body.String())
	}
	loc, _ := url.QueryUnescape(res.Header().Get("Location"))
	if !strings.Contains(loc, "/system?message=") || !strings.Contains(loc, "Asia/Yekaterinburg") || !strings.Contains(loc, "ntp.corp.local 192.168.10.1") {
		t.Fatalf("unexpected redirect location: %q", loc)
	}
	if api.lastReq.Timezone != "Asia/Yekaterinburg" || !api.lastReq.NTPEnabled || !reflect.DeepEqual(api.lastReq.NTPServers, []string{"ntp.corp.local", "192.168.10.1"}) {
		t.Fatalf("unexpected agent request: %+v", api.lastReq)
	}
	if serverLocation().String() != "Asia/Yekaterinburg" {
		t.Fatalf("serverLocation not updated: %q", serverLocation().String())
	}
	if len(store.audits) != 1 || !strings.HasPrefix(store.audits[0], "system.time|ok|") {
		t.Fatalf("unexpected audits: %v", store.audits)
	}

	// 2. Admin switches to manual mode with explicit date/time
	formManual := url.Values{
		"csrf":        {csrf},
		"timezone":    {"UTC"},
		"mode":        {"manual"},
		"manual_time": {"2026-11-19T05:15"},
	}
	req = httptest.NewRequest(http.MethodPost, "/system/time", strings.NewReader(formManual.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("POST manual = %d, want 303 (%s)", res.Code, res.Body.String())
	}
	if api.lastReq.NTPEnabled || api.lastReq.ManualTime != "2026-11-19 05:15:00" {
		t.Fatalf("unexpected manual agent request: %+v", api.lastReq)
	}

	// 3. Invalid NTP host -> 400 re-render (no agent call)
	prevCalls := api.setCalled
	formBad := url.Values{
		"csrf":        {csrf},
		"timezone":    {"UTC"},
		"mode":        {"ntp"},
		"ntp_servers": {"bad;host"},
	}
	req = httptest.NewRequest(http.MethodPost, "/system/time", strings.NewReader(formBad.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("invalid NTP = %d, want 400", res.Code)
	}
	if api.setCalled != prevCalls {
		t.Fatalf("agent should not be called on invalid NTP")
	}

	// 4. Operator -> 403 Forbidden
	sOp := &Server{
		Store:      authOnlyStore{user: User{ID: 2, Username: "op", Role: "operator", Enabled: true}},
		SystemTime: api,
	}
	req = httptest.NewRequest(http.MethodPost, "/system/time", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res = httptest.NewRecorder()
	sOp.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("operator POST /system/time = %d, want 403", res.Code)
	}

	// 5. Missing CSRF -> 403
	formNoCSRF := url.Values{"timezone": {"UTC"}, "mode": {"ntp"}}
	req = httptest.NewRequest(http.MethodPost, "/system/time", strings.NewReader(formNoCSRF.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF = %d, want 403", res.Code)
	}

	// 6. Agent error -> 503 + error audit
	api.setErr = errors.New("timedatectl failed")
	req = httptest.NewRequest(http.MethodPost, "/system/time", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("agent error = %d, want 503", res.Code)
	}
	if !strings.HasPrefix(store.audits[len(store.audits)-1], "system.time|error|") {
		t.Fatalf("expected error audit entry, got %v", store.audits)
	}
}

type systemUsersStore struct {
	authOnlyStore
	users []User
}

func (s systemUsersStore) Users(context.Context) ([]User, error) {
	return s.users, nil
}

func TestSystemPageShowsTimeCardAndDialogForAdmin(t *testing.T) {
	t.Cleanup(func() { SetServerTimezone("") })

	api := &fakeSystemTimeAPI{
		state: agent.SystemTimeState{
			NowRFC3339:      "2026-11-19T03:53:12+03:00",
			Timezone:        "Europe/Moscow",
			NTPEnabled:      true,
			NTPSynchronized: true,
			CanNTP:          true,
			NTPBackend:      "chrony",
			NTPServers:      []string{"ntp.corp.local"},
			Timezones:       []string{"UTC", "Europe/Moscow"},
		},
	}
	s := &Server{
		Store: systemUsersStore{
			authOnlyStore: authOnlyStore{user: User{ID: 1, Username: "admin", Role: "admin", Enabled: true}},
			users:         []User{{ID: 1, Username: "admin", Role: "admin", Enabled: true}},
		},
		SystemTime: api,
	}
	req := httptest.NewRequest(http.MethodGet, "/system", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("GET /system = %d, want 200", res.Code)
	}
	body := res.Body.String()
	for _, want := range []string{
		`id="server-time-card"`,
		"Время сервера",
		`data-system-time-open`,
		"Europe/Moscow",
		"ntp.corp.local",
		`id="system-time-dialog"`,
		`action="/system/time"`,
		`name="timezone"`,
		`name="mode" value="ntp"`,
		`name="mode" value="manual"`,
		`name="ntp_servers"`,
		`name="manual_time"`,
		`data-time-browser-now`,
		`class="btn btn-primary" disabled`,
		`<section class="card card-wide"><div class="page-head"><h2>Пользователи</h2>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /system (admin) missing %q", want)
		}
	}
	_, mainBody, _ := strings.Cut(body, "<main")
	if strings.Contains(mainBody, "<details") || strings.Contains(mainBody, "<summary") {
		t.Fatalf("GET /system <main> must not contain <details> or <summary>")
	}

	// Verify EN rendering of time card and dialog
	sEN := &Server{
		Store: systemUsersStore{
			authOnlyStore: authOnlyStore{user: User{ID: 1, Username: "admin", Role: "admin", Enabled: true, Language: langEN}},
			users:         []User{{ID: 1, Username: "admin", Role: "admin", Enabled: true}},
		},
		SystemTime: api,
	}
	reqEN := httptest.NewRequest(http.MethodGet, "/system", nil)
	reqEN.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	resEN := httptest.NewRecorder()
	sEN.Handler().ServeHTTP(resEN, reqEN)
	if resEN.Code != http.StatusOK {
		t.Fatalf("GET /system (EN) = %d", resEN.Code)
	}
	bodyEN := resEN.Body.String()
	for _, want := range []string{"Server time", "Server time and NTP", "Corporate NTP server", "Use browser time"} {
		if !strings.Contains(bodyEN, want) {
			t.Fatalf("GET /system (EN) missing %q", want)
		}
	}
}

func TestSystemPageTimeCardReadOnlyForNonAdmin(t *testing.T) {
	t.Cleanup(func() { SetServerTimezone("") })

	api := &fakeSystemTimeAPI{
		state: agent.SystemTimeState{
			NowRFC3339:      "2026-11-19T03:53:12+03:00",
			Timezone:        "Europe/Moscow",
			NTPEnabled:      true,
			NTPSynchronized: true,
			NTPBackend:      "chrony",
		},
	}
	for _, role := range []string{"operator", "observer"} {
		s := &Server{
			Store:      authOnlyStore{user: User{ID: 2, Username: role, Role: role, Enabled: true}},
			SystemTime: api,
		}
		req := httptest.NewRequest(http.MethodGet, "/system", nil)
		req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
		res := httptest.NewRecorder()
		s.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("GET /system (%s) = %d", role, res.Code)
		}
		body := res.Body.String()
		if !strings.Contains(body, `id="server-time-card"`) || !strings.Contains(body, "Europe/Moscow") {
			t.Fatalf("GET /system (%s) missing read-only time card", role)
		}
		if strings.Contains(body, `data-system-time-open`) || strings.Contains(body, `id="system-time-dialog"`) {
			t.Fatalf("GET /system (%s) must not render edit controls or dialog", role)
		}
	}
}

func TestSystemPageTimeCardDegradesGracefully(t *testing.T) {
	api := &fakeSystemTimeAPI{getErr: errors.New("socket unreachable")}
	s := &Server{
		Store:      authOnlyStore{user: User{ID: 1, Username: "admin", Role: "admin", Enabled: true}},
		SystemTime: api,
	}
	req := httptest.NewRequest(http.MethodGet, "/system", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("GET /system with agent error = %d, want 200", res.Code)
	}
	if !strings.Contains(res.Body.String(), "Служба времени недоступна") {
		t.Fatalf("expected graceful degradation message on /system")
	}
}

func TestSystemTimeDialogJS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	scriptBytes, err := os.ReadFile("system.js")
	if err != nil {
		t.Fatalf("read system.js: %v", err)
	}

	jsTest := fmt.Sprintf(`
const vm = require('node:vm');
const assert = require('node:assert/strict');

const code = %q;
let formListeners = {};
let docListeners = {};

const tzSelect = { value: 'Europe/Moscow', addEventListener(ev, fn) { this._fn = fn; } };
const radioNtp = { value: 'ntp', checked: true, addEventListener(ev, fn) { this._fn = fn; } };
const radioManual = { value: 'manual', checked: false, addEventListener(ev, fn) { this._fn = fn; } };
const ntpInput = { value: 'ntp.corp.local', disabled: false, addEventListener(ev, fn) { this._fn = fn; } };
const manualInput = { value: '2026-11-19T03:53', disabled: true, addEventListener(ev, fn) { this._fn = fn; } };
const browserBtn = { disabled: true, addEventListener(ev, fn) { this._click = fn; } };
const submitBtn = { disabled: true };
const cancelBtn = { addEventListener(ev, fn) { this._click = fn; } };
const cardNtp = {
  getAttribute: (n) => n === 'data-time-mode-card' ? 'ntp' : null,
  classList: { _s: new Set(['is-selected']), toggle(c, on) { on ? this._s.add(c) : this._s.delete(c); } },
  addEventListener() {},
};
const cardManual = {
  getAttribute: (n) => n === 'data-time-mode-card' ? 'manual' : null,
  classList: { _s: new Set(), toggle(c, on) { on ? this._s.add(c) : this._s.delete(c); } },
  addEventListener() {},
};

const formAttrs = {
  'data-initial-tz': 'Europe/Moscow',
  'data-initial-mode': 'ntp',
  'data-initial-ntp': 'ntp.corp.local',
  'data-initial-manual': '2026-11-19T03:53',
};
const formEl = {
  getAttribute: (n) => formAttrs[n] ?? null,
  addEventListener(type, fn) { formListeners[type] = fn; },
};

const dialog = {
  shown: false,
  showModal() { this.shown = true; },
  close() { this.shown = false; },
  querySelector(sel) {
    if (sel === 'form') return formEl;
    if (sel === 'select[name="timezone"]') return tzSelect;
    if (sel === 'input[name="mode"][value="ntp"]') return radioNtp;
    if (sel === 'input[name="mode"][value="manual"]') return radioManual;
    if (sel === 'input[name="ntp_servers"]') return ntpInput;
    if (sel === 'input[name="manual_time"]') return manualInput;
    if (sel === '[data-time-browser-now]') return browserBtn;
    if (sel === 'button.btn-primary') return submitBtn;
    if (sel === '[data-dialog-cancel]') return cancelBtn;
    return null;
  },
  querySelectorAll(sel) {
    if (sel === '[data-time-mode-card]') return [cardNtp, cardManual];
    if (sel === 'input[name="mode"]') return [radioNtp, radioManual];
    return [];
  },
  addEventListener() {},
};

const openBtn = { addEventListener(ev, fn) { this._click = fn; } };

const document = {
  hidden: true,
  getElementById(id) {
    if (id === 'system-time-dialog') return dialog;
    return null;
  },
  querySelector(sel) {
    if (sel === '[data-system-time-open]') return openBtn;
    return null;
  },
  querySelectorAll(sel) {
    if (sel === '[data-system-time-open]') return [openBtn];
    return [];
  },
  addEventListener(ev, fn) { docListeners[ev] = fn; },
};

vm.runInNewContext(code, {
  document,
  window: {},
  Date,
  Math,
  String,
  setInterval() { return 1; },
  clearInterval() {},
  fetch() { return Promise.resolve({ ok: false }); },
});

// Open dialog -> resets state, Save disabled
openBtn._click();
assert.equal(dialog.shown, true);
assert.equal(submitBtn.disabled, true);
assert.equal(ntpInput.disabled, false);
assert.equal(manualInput.disabled, true);

// Submit while disabled -> prevented
let prevented = false;
formListeners.submit({ preventDefault() { prevented = true; } });
assert.equal(prevented, true);

// Change timezone -> Save enabled
tzSelect.value = 'UTC';
formListeners.change();
assert.equal(submitBtn.disabled, false);

// Revert timezone -> Save disabled again
tzSelect.value = 'Europe/Moscow';
formListeners.change();
assert.equal(submitBtn.disabled, true);

// Switch to manual -> Save enabled, manual input unlocked, browser button fills value
radioNtp.checked = false;
radioManual.checked = true;
formListeners.change();
assert.equal(submitBtn.disabled, false);
assert.equal(ntpInput.disabled, true);
assert.equal(manualInput.disabled, false);
assert.equal(browserBtn.disabled, false);

manualInput.value = '';
browserBtn._click();
assert.match(manualInput.value, /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/);
`, string(scriptBytes))

	cmd := exec.CommandContext(ctx, "node", "-e", jsTest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node JS test failed: %v\n%s", err, string(out))
	}
}

func TestSystemMetricsAPIIncludesTimeStatus(t *testing.T) {
	api := &fakeSystemTimeAPI{
		state: agent.SystemTimeState{
			Timezone:        "Europe/Moscow",
			NTPEnabled:      true,
			NTPSynchronized: false,
			NTPServers:      []string{"0.ru.pool.ntp.org"},
		},
	}
	s := &Server{
		Store:      authOnlyStore{user: User{ID: 1, Username: "admin", Role: "admin", Enabled: true}},
		SystemTime: api,
	}
	h := s.Handler()

	// 1. Unsynced NTP (RU)
	req := httptest.NewRequest(http.MethodGet, "/api/system/metrics", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("GET /api/system/metrics = %d", res.Code)
	}
	var snap SystemSnapshot
	if err := json.Unmarshal(res.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if snap.TimeStatus == nil {
		t.Fatalf("expected non-nil TimeStatus")
	}
	if snap.TimeStatus.NTPSynchronized || snap.TimeStatus.StatusLabel != "NTP (ожидание синхронизации)" || snap.TimeStatus.ServersText != "0.ru.pool.ntp.org" {
		t.Fatalf("unexpected unsynced TimeStatus: %+v", snap.TimeStatus)
	}

	// 2. Synced NTP (EN)
	api.state.NTPSynchronized = true
	sEN := &Server{
		Store:      authOnlyStore{user: User{ID: 1, Username: "admin", Role: "admin", Enabled: true, Language: langEN}},
		SystemTime: api,
	}
	reqEN := httptest.NewRequest(http.MethodGet, "/api/system/metrics", nil)
	reqEN.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	resEN := httptest.NewRecorder()
	sEN.Handler().ServeHTTP(resEN, reqEN)
	var snapEN SystemSnapshot
	if err := json.Unmarshal(resEN.Body.Bytes(), &snapEN); err != nil {
		t.Fatalf("decode EN: %v", err)
	}
	if snapEN.TimeStatus == nil || !snapEN.TimeStatus.NTPSynchronized || snapEN.TimeStatus.StatusLabel != "NTP" || snapEN.TimeStatus.DefaultLabel != "OS default" {
		t.Fatalf("unexpected synced EN TimeStatus: %+v", snapEN.TimeStatus)
	}
}

func TestSystemTimeLiveSyncUpdateJS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	scriptBytes, err := os.ReadFile("system.js")
	if err != nil {
		t.Fatalf("read system.js: %v", err)
	}

	jsTest := fmt.Sprintf(`
const vm = require('node:vm');
const assert = require('node:assert/strict');

const code = %q;
let visibilityHandler = null;
const sysTimeNow = { textContent: '' };
const sysTimeTz = { textContent: 'UTC' };
function makeEl(tag) {
  return { tagName: tag.toUpperCase(), className: '', textContent: '', children: [], appendChild(c) { this.children.push(c); return c; } };
}
const sysTimeSync = {
  children: [],
  replaceChildren(...nodes) { this.children = nodes; },
};

const document = {
  hidden: false,
  getElementById(id) {
    if (id === 'server-resources-card') return {};
    if (id === 'sys-time-now') return sysTimeNow;
    if (id === 'sys-time-tz') return sysTimeTz;
    if (id === 'sys-time-sync') return sysTimeSync;
    return null;
  },
  querySelectorAll() { return []; },
  createElement(tag) { return makeEl(tag); },
  createTextNode(t) { return { nodeType: 3, textContent: t }; },
  addEventListener(ev, fn) { if (ev === 'visibilitychange') visibilityHandler = fn; },
};

let pendingFetch = null;
vm.runInNewContext(code, {
  document,
  window: {},
  Date,
  Math,
  String,
  setInterval() { return 1; },
  clearInterval() {},
  fetch() {
    pendingFetch = Promise.resolve({
      ok: true,
      json: () => Promise.resolve({
        available: true,
        server_time: '19.11.2026 04:20:00',
        time_status: {
          timezone: 'Europe/Moscow',
          ntp_enabled: true,
          ntp_synchronized: true,
          status_label: 'NTP',
          servers_text: '0.ru.pool.ntp.org',
          default_label: 'по умолчанию ОС',
        },
      }),
    });
    return pendingFetch;
  },
});

(async () => {
  visibilityHandler();
  await pendingFetch;
  await new Promise((r) => setTimeout(r, 0));
  assert.equal(sysTimeNow.textContent, '19.11.2026 04:20:00');
  assert.equal(sysTimeTz.textContent, 'Europe/Moscow');
  assert.equal(sysTimeSync.children.length, 3);
  assert.equal(sysTimeSync.children[0].className, 'dot dot-ok');
  assert.equal(sysTimeSync.children[1].textContent, 'NTP · ');
  assert.equal(sysTimeSync.children[2].textContent, '0.ru.pool.ntp.org');
})();
`, string(scriptBytes))

	cmd := exec.CommandContext(ctx, "node", "-e", jsTest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node live sync JS test failed: %v\n%s", err, string(out))
	}
}

