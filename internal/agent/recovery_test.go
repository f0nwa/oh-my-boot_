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
	onStop    func(unit string)
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
	case "stop":
		if s.onStop != nil {
			s.onStop(unit)
		}
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
	mountRoot, uploadRoot := "/mnt/omb-smb", "/mnt/omb-smb-upload"
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
	smb := &SMBManager{CredentialsDir: creds, UnitDir: units, MountRoot: mountRoot, UploadRoot: uploadRoot, Systemctl: ctl, Probe: func(string) error { return nil }}
	windows := &WindowsISOManager{ConfigRoot: winiso, Systemctl: ctl}
	return &StorageRecovery{SMB: smb, Windows: windows, ConfigPath: filepath.Join(base, "storage-recovery.json")}, readUnit, uploadUnit, base
}

func TestRecoveryRestartsFailedMountsThenProfiles(t *testing.T) {
	ctl := &recoverySystemctl{active: map[string]bool{"omb-winiso@3.service": true}}
	recovery, readUnit, uploadUnit, _ := recoveryFixture(t, ctl)
	status := recovery.RunOnce(context.Background())
	want := []string{readUnit, uploadUnit, "omb-winiso@1.service"}
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
		if strings.HasPrefix(unit, "omb-winiso@") {
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

func TestRecoveryRemountsAnActiveButUnreadableShare(t *testing.T) {
	key := strings.Repeat("a", 32)
	readTarget := filepath.Join("/mnt/omb-smb", key)
	ctl := &recoverySystemctl{active: map[string]bool{}}
	recovery, readUnit, uploadUnit, base := recoveryFixture(t, ctl)
	for _, unit := range []string{readUnit, uploadUnit, "omb-winiso@1.service", "omb-winiso@3.service"} {
		ctl.active[unit] = true
	}
	winiso := filepath.Join(base, "winiso")
	// Profile 1 reads its ISO from the stale share, profile 3 from elsewhere.
	if err := os.WriteFile(filepath.Join(winiso, "1.json"), []byte(`{"id":1,"storage_root":"`+readTarget+`","relative_path":"win.iso"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(winiso, "3.json"), []byte(`{"id":3,"storage_root":"/srv/iso","relative_path":"other.iso"}`), 0600); err != nil {
		t.Fatal(err)
	}
	stale := true
	recovery.SMB.Probe = func(path string) error {
		if stale && path == readTarget {
			return errors.New("stale file handle")
		}
		return nil
	}
	// Stopping a profile makes its unit inactive, as systemd would.
	ctl.onStop = func(unit string) { ctl.active[unit] = false }
	status := recovery.RunOnce(context.Background())
	var order []string
	for _, call := range ctl.calls {
		if strings.HasPrefix(call, "stop ") || strings.HasPrefix(call, "restart ") {
			order = append(order, call)
		}
	}
	want := []string{"stop omb-winiso@1.service", "restart " + readUnit, "restart omb-winiso@1.service"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", order, want)
	}
	if status.LastError != "" || !strings.Contains(strings.Join(status.Actions, "\n"), "stale file handle") {
		t.Fatalf("status = %+v", status)
	}
	// Readable again: the next run leaves everything alone.
	stale = false
	ctl.calls = nil
	if status := recovery.RunOnce(context.Background()); len(status.Actions) != 0 {
		t.Fatalf("healthy run acted: %v", status.Actions)
	}
}

func TestProbeDirReadsAndFailsOnMissing(t *testing.T) {
	if err := probeDir(t.TempDir()); err != nil {
		t.Fatalf("empty dir: %v", err)
	}
	if err := probeDir(filepath.Join(t.TempDir(), "gone")); err == nil {
		t.Fatal("missing dir passed")
	}
}

func nfsRecoveryFixture(t *testing.T, ctl *recoverySystemctl) (*StorageRecovery, string, string, string) {
	t.Helper()
	base := t.TempDir()
	configs, units, winiso := filepath.Join(base, "nfs"), filepath.Join(base, "units"), filepath.Join(base, "winiso")
	for _, dir := range []string{configs, units, winiso} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	key := strings.Repeat("b", 32)
	mountRoot, uploadRoot := "/mnt/omb-nfs", "/mnt/omb-nfs-upload"
	readUnit, uploadUnit := systemdMountUnitName(filepath.Join(mountRoot, key)), systemdMountUnitName(filepath.Join(uploadRoot, key))
	for path, content := range map[string]string{
		filepath.Join(configs, key+".json"): `{"key":"` + key + `","server":"10.0.0.1","export_path":"/exports/iso","version":"4.1"}`,
		filepath.Join(units, readUnit):     "[Mount]\n",
		filepath.Join(units, uploadUnit):   "[Mount]\n",
		filepath.Join(winiso, "2.json"):    "{}",
		filepath.Join(winiso, "4.json"):    "{}",
		filepath.Join(winiso, "notes.txt"): "ignored",
	} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	nfs := &NFSManager{
		ConfigDir:  configs,
		UnitDir:    units,
		MountRoot:  mountRoot,
		UploadRoot: uploadRoot,
		ImagesUID:  "1001",
		ImagesGID:  "1001",
		Systemctl:  ctl,
		Probe:      func(string) error { return nil },
	}
	windows := &WindowsISOManager{ConfigRoot: winiso, Systemctl: ctl}
	return &StorageRecovery{NFS: nfs, Windows: windows, ConfigPath: filepath.Join(base, "storage-recovery.json")}, readUnit, uploadUnit, base
}

func TestRecoveryRestartsFailedNFSMountsThenProfiles(t *testing.T) {
	ctl := &recoverySystemctl{active: map[string]bool{"omb-winiso@4.service": true}}
	recovery, readUnit, uploadUnit, _ := nfsRecoveryFixture(t, ctl)
	status := recovery.RunOnce(context.Background())
	want := []string{readUnit, uploadUnit, "omb-winiso@2.service"}
	if got := ctl.restarts(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("restarts = %v, want %v", got, want)
	}
	if status.LastError != "" || len(status.Actions) != 3 || status.LastRun.IsZero() {
		t.Fatalf("status = %+v", status)
	}
}

func TestRecoveryRemountsAnActiveButUnreadableNFSShare(t *testing.T) {
	key := strings.Repeat("b", 32)
	readTarget := filepath.Join("/mnt/omb-nfs", key)
	ctl := &recoverySystemctl{active: map[string]bool{}}
	recovery, readUnit, uploadUnit, base := nfsRecoveryFixture(t, ctl)
	for _, unit := range []string{readUnit, uploadUnit, "omb-winiso@2.service", "omb-winiso@4.service"} {
		ctl.active[unit] = true
	}
	winiso := filepath.Join(base, "winiso")
	if err := os.WriteFile(filepath.Join(winiso, "2.json"), []byte(`{"id":2,"storage_root":"`+readTarget+`","relative_path":"win.iso"}`), 0600); err != nil {
		t.Fatal(err)
	}
	stale := true
	recovery.NFS.Probe = func(path string) error {
		if stale && path == readTarget {
			return errors.New("stale file handle")
		}
		return nil
	}
	ctl.onStop = func(unit string) { ctl.active[unit] = false }
	status := recovery.RunOnce(context.Background())
	var order []string
	for _, call := range ctl.calls {
		if strings.HasPrefix(call, "stop ") || strings.HasPrefix(call, "restart ") {
			order = append(order, call)
		}
	}
	want := []string{"stop omb-winiso@2.service", "restart " + readUnit, "restart omb-winiso@2.service"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", order, want)
	}
	if status.LastError != "" || !strings.Contains(strings.Join(status.Actions, "\n"), "stale file handle") {
		t.Fatalf("status = %+v", status)
	}
}

func TestRecoverySkipsProfilesWhileNFSIsDown(t *testing.T) {
	ctl := &recoverySystemctl{active: map[string]bool{}, failStart: map[string]bool{}}
	recovery, readUnit, _, _ := nfsRecoveryFixture(t, ctl)
	ctl.failStart[readUnit] = true
	status := recovery.RunOnce(context.Background())
	for _, unit := range ctl.restarts() {
		if strings.HasPrefix(unit, "omb-winiso@") {
			t.Fatalf("profile restarted while NFS is down: %v", ctl.restarts())
		}
	}
	if !strings.Contains(status.LastError, readUnit) {
		t.Fatalf("error does not name the failed mount: %+v", status)
	}
}
