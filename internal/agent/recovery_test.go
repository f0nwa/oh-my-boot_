package agent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type recoverySystemctl struct {
	mu        sync.Mutex
	active    map[string]bool
	failStart map[string]bool
	calls     []string
}

func (s *recoverySystemctl) Run(_ context.Context, args ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, strings.Join(args, " "))
	unit := args[len(args)-1]
	switch args[0] {
	case "is-active":
		if s.active[unit] {
			return nil
		}
		return errors.New("inactive")
	case "restart":
		if s.failStart[unit] {
			return errors.New("start failed")
		}
		s.active[unit] = true
		return nil
	}
	return nil
}

func (s *recoverySystemctl) restarts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, call := range s.calls {
		if strings.HasPrefix(call, "restart ") {
			out = append(out, strings.TrimPrefix(call, "restart "))
		}
	}
	return out
}

func recoveryFixture(t *testing.T, ctl *recoverySystemctl) (*StorageRecovery, string, string, string) {
	t.Helper()
	base := t.TempDir()
	creds, units, winiso := filepath.Join(base, "smb"), filepath.Join(base, "units"), filepath.Join(base, "winiso")
	for _, dir := range []string{creds, units, winiso} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	key := strings.Repeat("a", 32)
	mountRoot, uploadRoot := "/mnt/pxe-platform-smb", "/mnt/pxe-platform-smb-upload"
	readUnit, uploadUnit := smbUnitName(filepath.Join(mountRoot, key)), smbUnitName(filepath.Join(uploadRoot, key))
	for path, content := range map[string]string{
		filepath.Join(creds, key+".cred"):  "username=u\n",
		filepath.Join(units, readUnit):     "[Mount]\n",
		filepath.Join(units, uploadUnit):   "[Mount]\n",
		filepath.Join(winiso, "1.json"):    "{}",
		filepath.Join(winiso, "3.json"):    "{}",
		filepath.Join(winiso, "notes.txt"): "ignored",
	} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	smb := &SMBManager{CredentialsDir: creds, UnitDir: units, MountRoot: mountRoot, UploadRoot: uploadRoot, Systemctl: ctl}
	windows := &WindowsISOManager{ConfigRoot: winiso, Systemctl: ctl}
	return &StorageRecovery{SMB: smb, Windows: windows, ConfigPath: filepath.Join(base, "storage-recovery.json")}, readUnit, uploadUnit, base
}

func TestRecoveryRestartsFailedMountsThenProfiles(t *testing.T) {
	ctl := &recoverySystemctl{active: map[string]bool{"pxe-winiso@3.service": true}}
	recovery, readUnit, uploadUnit, _ := recoveryFixture(t, ctl)
	status := recovery.RunOnce(context.Background())
	want := []string{readUnit, uploadUnit, "pxe-winiso@1.service"}
	if got := ctl.restarts(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("restarts = %v, want %v", got, want)
	}
	if status.LastError != "" || len(status.Actions) != 3 || status.LastRun.IsZero() {
		t.Fatalf("status = %+v", status)
	}
	// Everything is active now: a second run changes nothing.
	ctl.calls = nil
	status = recovery.RunOnce(context.Background())
	if len(ctl.restarts()) != 0 || len(status.Actions) != 0 {
		t.Fatalf("healthy run restarted %v", ctl.restarts())
	}
}

func TestRecoverySkipsProfilesWhileStorageIsDown(t *testing.T) {
	ctl := &recoverySystemctl{active: map[string]bool{}, failStart: map[string]bool{}}
	recovery, readUnit, _, _ := recoveryFixture(t, ctl)
	ctl.failStart[readUnit] = true
	status := recovery.RunOnce(context.Background())
	for _, unit := range ctl.restarts() {
		if strings.HasPrefix(unit, "pxe-winiso@") {
			t.Fatalf("profile restarted while storage is down: %v", ctl.restarts())
		}
	}
	if !strings.Contains(status.LastError, readUnit) {
		t.Fatalf("error does not name the failed mount: %+v", status)
	}
}

func TestRecoveryConfigDefaultsSavesAndValidates(t *testing.T) {
	recovery, _, _, _ := recoveryFixture(t, &recoverySystemctl{active: map[string]bool{}})
	if cfg := recovery.Config(); !cfg.Enabled || cfg.IntervalSeconds != 60 {
		t.Fatalf("default config = %+v", cfg)
	}
	if err := recovery.SetConfig(RecoveryConfig{Enabled: false, IntervalSeconds: 300}); err != nil {
		t.Fatal(err)
	}
	reloaded := &StorageRecovery{ConfigPath: recovery.ConfigPath}
	if cfg := reloaded.Config(); cfg.Enabled || cfg.IntervalSeconds != 300 {
		t.Fatalf("saved config = %+v", cfg)
	}
	for _, bad := range []int{0, 59, 3601} {
		if err := recovery.SetConfig(RecoveryConfig{Enabled: true, IntervalSeconds: bad}); err == nil {
			t.Fatalf("interval %d accepted", bad)
		}
	}
}

func TestRecoveryDisabledDoesNothingOnSchedule(t *testing.T) {
	ctl := &recoverySystemctl{active: map[string]bool{}}
	recovery, _, _, _ := recoveryFixture(t, ctl)
	if err := recovery.SetConfig(RecoveryConfig{Enabled: false, IntervalSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	recovery.Run(ctx)
	if len(ctl.calls) != 0 {
		t.Fatalf("disabled recovery ran: %v", ctl.calls)
	}
}

func TestRecoveryHTTP(t *testing.T) {
	ctl := &recoverySystemctl{active: map[string]bool{}}
	recovery, _, _, _ := recoveryFixture(t, ctl)
	handler := HandlerWithRecovery(nil, recovery.SMB, recovery.Windows, nil, recovery)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodPut, "/storage-recovery", strings.NewReader(`{"enabled":true,"interval_seconds":120}`)))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"interval_seconds":120`) {
		t.Fatalf("put: %d %s", res.Code, res.Body.String())
	}
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodPut, "/storage-recovery", strings.NewReader(`{"enabled":true,"interval_seconds":5}`)))
	if res.Code != http.StatusBadRequest {
		t.Fatalf("bad interval: %d", res.Code)
	}
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/storage-recovery/run", nil))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"actions":[`) || len(ctl.restarts()) == 0 {
		t.Fatalf("run: %d %s", res.Code, res.Body.String())
	}
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/storage-recovery", nil))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"last_run"`) {
		t.Fatalf("get: %d %s", res.Code, res.Body.String())
	}
}
