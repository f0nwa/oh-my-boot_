package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeSystemTimeRunner struct {
	calls     [][]string
	showOut   string
	tzOut     string
	errByCmd  map[string]error
	onCommand func(name string, args ...string)
}

func (f *fakeSystemTimeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := append([]string{name}, args...)
	f.calls = append(f.calls, call)
	key := strings.Join(call, " ")
	if f.errByCmd != nil {
		if err, ok := f.errByCmd[key]; ok {
			return nil, err
		}
	}
	if f.onCommand != nil {
		f.onCommand(name, args...)
	}
	if name == "timedatectl" && len(args) > 0 {
		if args[0] == "show" {
			return []byte(f.showOut), nil
		}
		if args[0] == "list-timezones" {
			return []byte(f.tzOut), nil
		}
	}
	return nil, nil
}

func TestSystemTimeStateParsesTimedatectlAndChronySources(t *testing.T) {
	chronyDir := filepath.Join(t.TempDir(), "sources.d")
	if err := os.MkdirAll(chronyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "# Managed by Oh My Boot\nserver ntp1.corp.local iburst prefer\nserver 192.168.10.1 iburst prefer\n"
	if err := os.WriteFile(filepath.Join(chronyDir, "omb.sources"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &fakeSystemTimeRunner{
		showOut: "Timezone=Europe/Moscow\nNTP=yes\nNTPSynchronized=yes\nCanNTP=yes\n",
		tzOut:   "UTC\nEurope/Moscow\nAsia/Vladivostok\n",
	}
	fixed := time.Date(2026, 11, 19, 3, 53, 12, 0, time.FixedZone("MSK", 3*3600))
	mgr := &SystemTimeManager{
		ChronySourcesDir: chronyDir,
		TimesyncdConfDir: filepath.Join(t.TempDir(), "missing-timesyncd"),
		Runner:           runner,
		Now:              func() time.Time { return fixed },
	}

	st, err := mgr.State(context.Background())
	if err != nil {
		t.Fatalf("State() error = %v", err)
	}
	if st.Timezone != "Europe/Moscow" || !st.NTPEnabled || !st.NTPSynchronized || !st.CanNTP {
		t.Fatalf("unexpected timedatectl fields: %+v", st)
	}
	if st.NTPBackend != "chrony" {
		t.Fatalf("NTPBackend = %q, want chrony", st.NTPBackend)
	}
	wantServers := []string{"ntp1.corp.local", "192.168.10.1"}
	if !reflect.DeepEqual(st.NTPServers, wantServers) {
		t.Fatalf("NTPServers = %v, want %v", st.NTPServers, wantServers)
	}
	if !reflect.DeepEqual(st.Timezones, []string{"UTC", "Europe/Moscow", "Asia/Vladivostok"}) {
		t.Fatalf("Timezones = %v", st.Timezones)
	}
	if st.NowRFC3339 != fixed.Format(time.RFC3339) {
		t.Fatalf("NowRFC3339 = %q, want %q", st.NowRFC3339, fixed.Format(time.RFC3339))
	}
}

func TestSystemTimeStateParsesTimesyncdDropIn(t *testing.T) {
	tsDir := filepath.Join(t.TempDir(), "timesyncd.conf.d")
	if err := os.MkdirAll(tsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tsDir, "omb.conf"), []byte("# Managed by Oh My Boot\n[Time]\nNTP=10.0.0.1 ntp.internal\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &fakeSystemTimeRunner{
		showOut: "Timezone=UTC\nNTP=yes\nNTPSynchronized=no\nCanNTP=yes\n",
		tzOut:   "UTC\n",
	}
	mgr := &SystemTimeManager{
		ChronySourcesDir: filepath.Join(t.TempDir(), "missing-chrony"),
		TimesyncdConfDir: tsDir,
		Runner:           runner,
	}

	st, err := mgr.State(context.Background())
	if err != nil {
		t.Fatalf("State() error = %v", err)
	}
	if st.NTPBackend != "timesyncd" {
		t.Fatalf("NTPBackend = %q, want timesyncd", st.NTPBackend)
	}
	wantServers := []string{"10.0.0.1", "ntp.internal"}
	if !reflect.DeepEqual(st.NTPServers, wantServers) {
		t.Fatalf("NTPServers = %v, want %v", st.NTPServers, wantServers)
	}
}

func TestValidateSystemTimeUpdateRejectsInvalidInputs(t *testing.T) {
	allowed := []string{"UTC", "Europe/Moscow", "Asia/Yekaterinburg"}
	cases := []struct {
		name string
		req  SystemTimeUpdate
	}{
		{"empty timezone", SystemTimeUpdate{NTPEnabled: true}},
		{"traversal timezone", SystemTimeUpdate{Timezone: "../etc/passwd", NTPEnabled: true}},
		{"unknown timezone", SystemTimeUpdate{Timezone: "Mars/Olympus", NTPEnabled: true}},
		{"ntp newline injection", SystemTimeUpdate{Timezone: "UTC", NTPEnabled: true, NTPServers: []string{"ntp.local\nallow all"}}},
		{"ntp space injection", SystemTimeUpdate{Timezone: "UTC", NTPEnabled: true, NTPServers: []string{"ntp.local iburst"}}},
		{"ntp comment char", SystemTimeUpdate{Timezone: "UTC", NTPEnabled: true, NTPServers: []string{"#ntp.local"}}},
		{"ntp semicolon", SystemTimeUpdate{Timezone: "UTC", NTPEnabled: true, NTPServers: []string{"ntp.local;rm"}}},
		{"too many servers", SystemTimeUpdate{Timezone: "UTC", NTPEnabled: true, NTPServers: []string{"1.1.1.1", "1.1.1.2", "1.1.1.3", "1.1.1.4", "1.1.1.5", "1.1.1.6", "1.1.1.7", "1.1.1.8", "1.1.1.9"}}},
		{"manual time when ntp enabled", SystemTimeUpdate{Timezone: "UTC", NTPEnabled: true, ManualTime: "2026-11-19T04:00"}},
		{"manual time year too old", SystemTimeUpdate{Timezone: "UTC", NTPEnabled: false, ManualTime: "1999-01-01T12:00"}},
		{"manual time year too far", SystemTimeUpdate{Timezone: "UTC", NTPEnabled: false, ManualTime: "2105-01-01T12:00"}},
		{"manual time garbage", SystemTimeUpdate{Timezone: "UTC", NTPEnabled: false, ManualTime: "not-a-date"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ValidateSystemTimeUpdate(tc.req, allowed); err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
		})
	}

	okReq, err := ValidateSystemTimeUpdate(SystemTimeUpdate{
		Timezone:   "Europe/Moscow",
		NTPEnabled: true,
		NTPServers: []string{" ntp.corp.local ", "2001:db8::1", "192.168.10.1"},
	}, allowed)
	if err != nil {
		t.Fatalf("valid NTP request failed: %v", err)
	}
	if !reflect.DeepEqual(okReq.NTPServers, []string{"ntp.corp.local", "2001:db8::1", "192.168.10.1"}) {
		t.Fatalf("normalized NTPServers = %v", okReq.NTPServers)
	}

	okManual, err := ValidateSystemTimeUpdate(SystemTimeUpdate{
		Timezone:   "UTC",
		NTPEnabled: false,
		ManualTime: "2026-11-19T04:15",
	}, allowed)
	if err != nil {
		t.Fatalf("valid manual request failed: %v", err)
	}
	if okManual.ManualTime != "2026-11-19 04:15:00" {
		t.Fatalf("normalized ManualTime = %q, want 2026-11-19 04:15:00", okManual.ManualTime)
	}
}

func TestSystemTimeApplyChronyWritesAndClearsSources(t *testing.T) {
	chronyDir := filepath.Join(t.TempDir(), "sources.d")
	if err := os.MkdirAll(chronyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &fakeSystemTimeRunner{
		showOut: "Timezone=UTC\nNTP=yes\nNTPSynchronized=yes\nCanNTP=yes\n",
		tzOut:   "UTC\nEurope/Moscow\n",
	}
	runner.onCommand = func(name string, args ...string) {
		if name == "timedatectl" && len(args) == 2 && args[0] == "set-timezone" {
			runner.showOut = "Timezone=" + args[1] + "\nNTP=yes\nNTPSynchronized=yes\nCanNTP=yes\n"
		}
	}
	mgr := &SystemTimeManager{
		ChronySourcesDir: chronyDir,
		TimesyncdConfDir: filepath.Join(t.TempDir(), "missing"),
		Runner:           runner,
	}

	// 1. Change timezone + set corporate NTP
	st, err := mgr.Apply(context.Background(), SystemTimeUpdate{
		Timezone:   "Europe/Moscow",
		NTPEnabled: true,
		NTPServers: []string{"ntp.corp.local", "192.168.10.1"},
	})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if st.Timezone != "Europe/Moscow" || !reflect.DeepEqual(st.NTPServers, []string{"ntp.corp.local", "192.168.10.1"}) {
		t.Fatalf("unexpected state: %+v", st)
	}
	data, err := os.ReadFile(filepath.Join(chronyDir, "omb.sources"))
	if err != nil {
		t.Fatalf("omb.sources not written: %v", err)
	}
	wantFile := "# Managed by Oh My Boot\nserver ntp.corp.local iburst prefer\nserver 192.168.10.1 iburst prefer\n"
	if string(data) != wantFile {
		t.Fatalf("omb.sources = %q, want %q", string(data), wantFile)
	}
	if !containsCall(runner.calls, "timedatectl set-timezone Europe/Moscow") ||
		!containsCall(runner.calls, "timedatectl set-ntp true") ||
		!containsCall(runner.calls, "systemctl restart chrony") {
		t.Fatalf("missing expected commands in %v", runner.calls)
	}

	// 2. Change ONLY timezone -> should NOT restart chrony
	runner.calls = nil
	if _, err := mgr.Apply(context.Background(), SystemTimeUpdate{
		Timezone:   "UTC",
		NTPEnabled: true,
		NTPServers: []string{"ntp.corp.local", "192.168.10.1"},
	}); err != nil {
		t.Fatalf("Apply timezone-only error = %v", err)
	}
	if containsCall(runner.calls, "systemctl restart chrony") {
		t.Fatalf("should not restart chrony when only timezone changed; calls = %v", runner.calls)
	}

	// 3. Clear NTP servers -> removes omb.sources and restarts chrony
	runner.calls = nil
	st, err = mgr.Apply(context.Background(), SystemTimeUpdate{
		Timezone:   "UTC",
		NTPEnabled: true,
		NTPServers: nil,
	})
	if err != nil {
		t.Fatalf("Apply clear servers error = %v", err)
	}
	if len(st.NTPServers) != 0 {
		t.Fatalf("expected empty NTPServers, got %v", st.NTPServers)
	}
	if _, err := os.Stat(filepath.Join(chronyDir, "omb.sources")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected omb.sources to be removed, stat err = %v", err)
	}
	if !containsCall(runner.calls, "systemctl restart chrony") {
		t.Fatalf("expected chrony restart on clearing servers, calls = %v", runner.calls)
	}
}

func TestSystemTimeApplyTimesyncdWritesDropIn(t *testing.T) {
	tsDir := filepath.Join(t.TempDir(), "timesyncd.conf.d")
	if err := os.MkdirAll(tsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &fakeSystemTimeRunner{
		showOut: "Timezone=UTC\nNTP=no\nNTPSynchronized=no\nCanNTP=yes\n",
		tzOut:   "UTC\n",
	}
	mgr := &SystemTimeManager{
		ChronySourcesDir: filepath.Join(t.TempDir(), "missing"),
		TimesyncdConfDir: tsDir,
		Runner:           runner,
	}

	if _, err := mgr.Apply(context.Background(), SystemTimeUpdate{
		Timezone:   "UTC",
		NTPEnabled: true,
		NTPServers: []string{"10.20.0.1", "ntp.corp.local"},
	}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(tsDir, "omb.conf"))
	if err != nil {
		t.Fatalf("omb.conf not written: %v", err)
	}
	want := "# Managed by Oh My Boot\n[Time]\nNTP=10.20.0.1 ntp.corp.local\n"
	if string(data) != want {
		t.Fatalf("omb.conf = %q, want %q", string(data), want)
	}
	if !containsCall(runner.calls, "systemctl restart systemd-timesyncd") {
		t.Fatalf("expected systemd-timesyncd restart, calls = %v", runner.calls)
	}
}

func TestSystemTimeApplyManualDisablesNTPAndSetsTime(t *testing.T) {
	runner := &fakeSystemTimeRunner{
		showOut: "Timezone=Europe/Moscow\nNTP=yes\nNTPSynchronized=yes\nCanNTP=yes\n",
		tzOut:   "Europe/Moscow\nUTC\n",
	}
	mgr := &SystemTimeManager{
		ChronySourcesDir: filepath.Join(t.TempDir(), "missing-chrony"),
		TimesyncdConfDir: filepath.Join(t.TempDir(), "missing-ts"),
		Runner:           runner,
	}

	if _, err := mgr.Apply(context.Background(), SystemTimeUpdate{
		Timezone:   "Europe/Moscow",
		NTPEnabled: false,
		ManualTime: "2026-11-19T05:30",
	}); err != nil {
		t.Fatalf("Apply manual error = %v", err)
	}
	if !containsCall(runner.calls, "timedatectl set-ntp false") ||
		!containsCall(runner.calls, "timedatectl set-time 2026-11-19 05:30:00") {
		t.Fatalf("missing manual commands in %v", runner.calls)
	}

	// Manual mode without manual_time -> only disables NTP, no set-time
	runner.calls = nil
	if _, err := mgr.Apply(context.Background(), SystemTimeUpdate{
		Timezone:   "Europe/Moscow",
		NTPEnabled: false,
	}); err != nil {
		t.Fatalf("Apply manual empty time error = %v", err)
	}
	for _, c := range runner.calls {
		if len(c) >= 2 && c[1] == "set-time" {
			t.Fatalf("unexpected set-time call: %v", runner.calls)
		}
	}
}

func TestSystemTimeApplyReturnsUnavailableWithoutBackend(t *testing.T) {
	runner := &fakeSystemTimeRunner{
		showOut: "Timezone=UTC\nNTP=yes\nNTPSynchronized=no\nCanNTP=yes\n",
		tzOut:   "UTC\n",
	}
	mgr := &SystemTimeManager{
		ChronySourcesDir: filepath.Join(t.TempDir(), "missing-chrony"),
		TimesyncdConfDir: filepath.Join(t.TempDir(), "missing-ts"),
		Runner:           runner,
	}
	_, err := mgr.Apply(context.Background(), SystemTimeUpdate{
		Timezone:   "UTC",
		NTPEnabled: true,
		NTPServers: []string{"ntp.corp.local"},
	})
	if !errors.Is(err, ErrNTPBackendUnavailable) {
		t.Fatalf("err = %v, want ErrNTPBackendUnavailable", err)
	}
}

func TestHandlerWithSystemTimeHTTP(t *testing.T) {
	chronyDir := filepath.Join(t.TempDir(), "sources.d")
	if err := os.MkdirAll(chronyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &fakeSystemTimeRunner{
		showOut: "Timezone=Europe/Moscow\nNTP=yes\nNTPSynchronized=yes\nCanNTP=yes\n",
		tzOut:   "UTC\nEurope/Moscow\n",
	}
	mgr := &SystemTimeManager{
		ChronySourcesDir: chronyDir,
		TimesyncdConfDir: filepath.Join(t.TempDir(), "missing"),
		Runner:           runner,
	}
	h := HandlerWithSystemTime(http.NotFoundHandler(), mgr)

	// GET /system/time -> 200
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/system/time", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /system/time = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var st SystemTimeState
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode GET /system/time: %v", err)
	}
	if st.Timezone != "Europe/Moscow" {
		t.Fatalf("Timezone = %q", st.Timezone)
	}

	// POST /system/time invalid -> 400
	badBody, _ := json.Marshal(SystemTimeUpdate{Timezone: "Europe/Moscow", NTPEnabled: true, ManualTime: "2026-11-19T05:00"})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/system/time", bytes.NewReader(badBody)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST invalid = %d, want 400", rec.Code)
	}

	// POST /system/time valid -> 200
	okBody, _ := json.Marshal(SystemTimeUpdate{Timezone: "UTC", NTPEnabled: true, NTPServers: []string{"ntp.corp.local"}})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/system/time", bytes.NewReader(okBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST valid = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	// nil manager -> 503
	hNil := HandlerWithSystemTime(http.NotFoundHandler(), nil)
	rec = httptest.NewRecorder()
	hNil.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/system/time", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET nil manager = %d, want 503", rec.Code)
	}
}

func containsCall(calls [][]string, want string) bool {
	for _, c := range calls {
		if strings.Join(c, " ") == want {
			return true
		}
	}
	return false
}
