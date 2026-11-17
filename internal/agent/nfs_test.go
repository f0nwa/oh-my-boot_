package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type fakeNFSSystemctl struct {
	mu      sync.Mutex
	calls   [][]string
	active  map[string]bool
	failCmd string
	failArg string
}

func newFakeNFSSystemctl() *fakeNFSSystemctl {
	return &fakeNFSSystemctl{
		active: make(map[string]bool),
	}
}

func (f *fakeNFSSystemctl) Run(_ context.Context, args ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string(nil), args...))
	if len(args) > 0 {
		if f.failCmd != "" && args[0] == f.failCmd {
			if f.failArg == "" || (len(args) > 1 && strings.Contains(strings.Join(args[1:], " "), f.failArg)) {
				return os.ErrPermission
			}
		}
		unit := args[len(args)-1]
		switch args[0] {
		case "enable":
			for _, a := range args[1:] {
				if a == "--now" {
					f.active[unit] = true
				}
			}
		case "start", "restart":
			f.active[unit] = true
		case "stop":
			f.active[unit] = false
		case "disable":
			f.active[unit] = false
		case "is-active":
			if !f.active[unit] {
				return errors.New("inactive")
			}
			return nil
		}
	}
	return nil
}

func (f *fakeNFSSystemctl) Calls() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]string, len(f.calls))
	copy(out, f.calls)
	return out
}

func testNFSManager(t *testing.T, runner SMBSystemctl) *NFSManager {
	t.Helper()
	base := t.TempDir()
	return &NFSManager{
		ConfigDir:  filepath.Join(base, "nfs-config"),
		UnitDir:    filepath.Join(base, "units"),
		MountRoot:  filepath.Join(base, "mounts"),
		UploadRoot: filepath.Join(base, "uploads"),
		ImagesUID:  "123",
		ImagesGID:  "456",
		Systemctl:  runner,
	}
}

func validNFSRequest() NFSMountRequest {
	return NFSMountRequest{
		Key:        "0123456789abcdef0123456789abcdef",
		Server:     "192.168.10.10",
		ExportPath: "/iso",
		Version:    "4.1",
	}
}

func TestNFSUnitGenerationOptionsAndTimeout(t *testing.T) {
	runner := newFakeNFSSystemctl()
	manager := testNFSManager(t, runner)

	// Test NFS version 4.1
	req41 := validNFSRequest()
	res, err := manager.Mount(context.Background(), req41)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	if !res.Writable {
		t.Fatalf("expected writable mount by default")
	}

	roUnitPath := filepath.Join(manager.UnitDir, systemdMountUnitName(filepath.Join(manager.MountRoot, req41.Key)))
	roContent, err := os.ReadFile(roUnitPath)
	if err != nil {
		t.Fatalf("failed to read RO unit: %v", err)
	}
	roStr := string(roContent)
	if !strings.Contains(roStr, "Type=nfs\n") {
		t.Fatalf("RO unit missing Type=nfs: %s", roStr)
	}
	if !strings.Contains(roStr, "Options=ro,nosuid,nodev,noexec,hard,proto=tcp,vers=4.1\n") {
		t.Fatalf("RO unit missing expected options: %s", roStr)
	}
	if !strings.Contains(roStr, "TimeoutSec=15s\n") {
		t.Fatalf("RO unit missing TimeoutSec=15s: %s", roStr)
	}
	if !strings.Contains(roStr, "What=192.168.10.10:/iso\n") {
		t.Fatalf("RO unit missing What: %s", roStr)
	}
	if !strings.Contains(roStr, "Where="+filepath.Join(manager.MountRoot, req41.Key)+"\n") {
		t.Fatalf("RO unit missing Where: %s", roStr)
	}

	rwUnitPath := filepath.Join(manager.UnitDir, systemdMountUnitName(filepath.Join(manager.UploadRoot, req41.Key)))
	rwContent, err := os.ReadFile(rwUnitPath)
	if err != nil {
		t.Fatalf("failed to read RW unit: %v", err)
	}
	rwStr := string(rwContent)
	if !strings.Contains(rwStr, "Type=nfs\n") {
		t.Fatalf("RW unit missing Type=nfs: %s", rwStr)
	}
	if !strings.Contains(rwStr, "Options=rw,nosuid,nodev,noexec,hard,proto=tcp,vers=4.1\n") {
		t.Fatalf("RW unit missing expected options: %s", rwStr)
	}
	if !strings.Contains(rwStr, "TimeoutSec=15s\n") {
		t.Fatalf("RW unit missing TimeoutSec=15s: %s", rwStr)
	}
	if !strings.Contains(rwStr, "What=192.168.10.10:/iso\n") {
		t.Fatalf("RW unit missing What: %s", rwStr)
	}
	if !strings.Contains(rwStr, "Where="+filepath.Join(manager.UploadRoot, req41.Key)+"\n") {
		t.Fatalf("RW unit missing Where: %s", rwStr)
	}

	// Test NFS version 3
	req3 := validNFSRequest()
	req3.Key = "fedcba9876543210fedcba9876543210"
	req3.Version = "3"
	res3, err := manager.Mount(context.Background(), req3)
	if err != nil {
		t.Fatalf("Mount v3 failed: %v", err)
	}
	if !res3.Writable {
		t.Fatalf("expected writable mount by default")
	}

	roUnitPath3 := filepath.Join(manager.UnitDir, systemdMountUnitName(filepath.Join(manager.MountRoot, req3.Key)))
	roContent3, err := os.ReadFile(roUnitPath3)
	if err != nil {
		t.Fatalf("failed to read RO unit: %v", err)
	}
	roStr3 := string(roContent3)
	if !strings.Contains(roStr3, "Options=ro,nosuid,nodev,noexec,hard,proto=tcp,vers=3,nolock\n") {
		t.Fatalf("RO unit v3 missing expected options: %s", roStr3)
	}
	if !strings.Contains(roStr3, "TimeoutSec=15s\n") {
		t.Fatalf("RO unit v3 missing TimeoutSec=15s: %s", roStr3)
	}

	rwUnitPath3 := filepath.Join(manager.UnitDir, systemdMountUnitName(filepath.Join(manager.UploadRoot, req3.Key)))
	rwContent3, err := os.ReadFile(rwUnitPath3)
	if err != nil {
		t.Fatalf("failed to read RW unit: %v", err)
	}
	rwStr3 := string(rwContent3)
	if !strings.Contains(rwStr3, "Options=rw,nosuid,nodev,noexec,hard,proto=tcp,vers=3,nolock\n") {
		t.Fatalf("RW unit v3 missing expected options: %s", rwStr3)
	}
	if !strings.Contains(rwStr3, "TimeoutSec=15s\n") {
		t.Fatalf("RW unit v3 missing TimeoutSec=15s: %s", rwStr3)
	}
}

func TestNFSIPv6SourceFormatting(t *testing.T) {
	runner := newFakeNFSSystemctl()
	manager := testNFSManager(t, runner)

	req := validNFSRequest()
	req.Server = "2001:db8::1"
	req.ExportPath = "/iso"

	if _, err := manager.Mount(context.Background(), req); err != nil {
		t.Fatalf("Mount failed: %v", err)
	}

	roUnitPath := filepath.Join(manager.UnitDir, systemdMountUnitName(filepath.Join(manager.MountRoot, req.Key)))
	roContent, err := os.ReadFile(roUnitPath)
	if err != nil {
		t.Fatalf("failed to read RO unit: %v", err)
	}
	if !strings.Contains(string(roContent), "What=[2001:db8::1]:/iso\n") {
		t.Fatalf("expected bracketed IPv6 in What, got: %s", string(roContent))
	}

	// Also verify already-bracketed IPv6
	req2 := validNFSRequest()
	req2.Key = "11112222333344445555666677778888"
	req2.Server = "[2001:db8::1]"
	req2.ExportPath = "/iso"

	if _, err := manager.Mount(context.Background(), req2); err != nil {
		t.Fatalf("Mount failed: %v", err)
	}

	roUnitPath2 := filepath.Join(manager.UnitDir, systemdMountUnitName(filepath.Join(manager.MountRoot, req2.Key)))
	roContent2, err := os.ReadFile(roUnitPath2)
	if err != nil {
		t.Fatalf("failed to read RO unit 2: %v", err)
	}
	if !strings.Contains(string(roContent2), "What=[2001:db8::1]:/iso\n") {
		t.Fatalf("expected bracketed IPv6 in What, got: %s", string(roContent2))
	}
}

func TestNFSConfigSavedWith0600(t *testing.T) {
	runner := newFakeNFSSystemctl()
	manager := testNFSManager(t, runner)

	req := validNFSRequest()
	if _, err := manager.Mount(context.Background(), req); err != nil {
		t.Fatalf("Mount failed: %v", err)
	}

	cfgPath := filepath.Join(manager.ConfigDir, req.Key+".json")
	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("config file missing: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("expected config file mode 0600, got %o", info.Mode().Perm())
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	var loaded NFSMountRequest
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("failed to parse config JSON: %v", err)
	}
	if loaded.Key != req.Key || loaded.Server != req.Server || loaded.ExportPath != req.ExportPath || loaded.Version != req.Version {
		t.Fatalf("loaded config does not match request: %+v vs %+v", loaded, req)
	}
}

func TestNFSMountAutoDetection(t *testing.T) {
	// Case A: WriteProbe succeeds -> returns Writable: true
	t.Run("WritableSuccess", func(t *testing.T) {
		runner := newFakeNFSSystemctl()
		manager := testNFSManager(t, runner)
		probedPath := ""
		probedUID, probedGID := "", ""
		manager.WriteProbe = func(path string, uid, gid string) error {
			probedPath = path
			probedUID = uid
			probedGID = gid
			return nil
		}

		req := validNFSRequest()
		res, err := manager.Mount(context.Background(), req)
		if err != nil {
			t.Fatalf("Mount failed: %v", err)
		}
		if !res.Writable {
			t.Fatalf("expected Writable == true")
		}
		if probedPath != filepath.Join(manager.UploadRoot, req.Key) {
			t.Fatalf("unexpected probed path: %q", probedPath)
		}
		if probedUID != "123" || probedGID != "456" {
			t.Fatalf("unexpected probed uid/gid: %s/%s", probedUID, probedGID)
		}

		// Both units should exist
		roUnit := filepath.Join(manager.UnitDir, systemdMountUnitName(filepath.Join(manager.MountRoot, req.Key)))
		rwUnit := filepath.Join(manager.UnitDir, systemdMountUnitName(filepath.Join(manager.UploadRoot, req.Key)))
		if _, err := os.Stat(roUnit); err != nil {
			t.Fatalf("RO unit missing: %v", err)
		}
		if _, err := os.Stat(rwUnit); err != nil {
			t.Fatalf("RW unit missing: %v", err)
		}
	})

	// Case B: WriteProbe fails -> returns Writable: false, cleans up RW unit, no error
	t.Run("WriteProbeFailureCleanRO", func(t *testing.T) {
		runner := newFakeNFSSystemctl()
		manager := testNFSManager(t, runner)
		manager.WriteProbe = func(path string, uid, gid string) error {
			return os.ErrPermission
		}

		req := validNFSRequest()
		res, err := manager.Mount(context.Background(), req)
		if err != nil {
			t.Fatalf("Mount should succeed with RO even if write fails, got err: %v", err)
		}
		if res.Writable {
			t.Fatalf("expected Writable == false")
		}

		// RO unit should exist
		roUnit := filepath.Join(manager.UnitDir, systemdMountUnitName(filepath.Join(manager.MountRoot, req.Key)))
		if _, err := os.Stat(roUnit); err != nil {
			t.Fatalf("RO unit missing: %v", err)
		}

		// RW unit and directory should be removed
		rwUnit := filepath.Join(manager.UnitDir, systemdMountUnitName(filepath.Join(manager.UploadRoot, req.Key)))
		if _, err := os.Stat(rwUnit); !os.IsNotExist(err) {
			t.Fatalf("RW unit should have been removed: %v", err)
		}
		uploadDir := filepath.Join(manager.UploadRoot, req.Key)
		if _, err := os.Stat(uploadDir); !os.IsNotExist(err) {
			t.Fatalf("Upload dir should have been removed: %v", err)
		}

		// Verify systemctl calls included disable --now for the upload unit
		rwUnitName := systemdMountUnitName(uploadDir)
		foundDisable := false
		for _, call := range runner.Calls() {
			if len(call) >= 3 && call[0] == "disable" && call[1] == "--now" && call[2] == rwUnitName {
				foundDisable = true
				break
			}
		}
		if !foundDisable {
			t.Fatalf("expected disable --now for %s in calls: %v", rwUnitName, runner.Calls())
		}
	})

	// Case C: RW systemctl enable fails -> returns Writable: false, cleans up, no error
	t.Run("RWUnitEnableFailureCleanRO", func(t *testing.T) {
		runner := newFakeNFSSystemctl()
		manager := testNFSManager(t, runner)
		req := validNFSRequest()

		rwUnitName := systemdMountUnitName(filepath.Join(manager.UploadRoot, req.Key))
		runner.failCmd = "enable"
		runner.failArg = rwUnitName

		res, err := manager.Mount(context.Background(), req)
		if err != nil {
			t.Fatalf("Mount should succeed with RO even if RW enable fails, got err: %v", err)
		}
		if res.Writable {
			t.Fatalf("expected Writable == false")
		}

		// RO unit should exist
		roUnit := filepath.Join(manager.UnitDir, systemdMountUnitName(filepath.Join(manager.MountRoot, req.Key)))
		if _, err := os.Stat(roUnit); err != nil {
			t.Fatalf("RO unit missing: %v", err)
		}
		// RW unit should be cleaned up
		rwUnit := filepath.Join(manager.UnitDir, rwUnitName)
		if _, err := os.Stat(rwUnit); !os.IsNotExist(err) {
			t.Fatalf("RW unit should have been removed: %v", err)
		}
	})
}

func TestNFSCheckUpload(t *testing.T) {
	runner := newFakeNFSSystemctl()
	manager := testNFSManager(t, runner)

	// Step 1: Initial mount is RO (WriteProbe fails)
	writeAllowed := false
	manager.WriteProbe = func(path string, uid, gid string) error {
		if !writeAllowed {
			return os.ErrPermission
		}
		return nil
	}

	req := validNFSRequest()
	res, err := manager.Mount(context.Background(), req)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	if res.Writable {
		t.Fatalf("expected initial Writable == false")
	}

	// Step 2: CheckUpload when still read-only -> returns false, nil
	writable, err := manager.CheckUpload(context.Background(), req.Key)
	if err != nil {
		t.Fatalf("CheckUpload failed: %v", err)
	}
	if writable {
		t.Fatalf("expected CheckUpload == false")
	}

	// Step 3: Write permission is now granted on server -> CheckUpload returns true, nil
	writeAllowed = true
	writable, err = manager.CheckUpload(context.Background(), req.Key)
	if err != nil {
		t.Fatalf("CheckUpload failed: %v", err)
	}
	if !writable {
		t.Fatalf("expected CheckUpload == true after permission granted")
	}

	// Verify upload unit now exists and is created
	rwUnit := filepath.Join(manager.UnitDir, systemdMountUnitName(filepath.Join(manager.UploadRoot, req.Key)))
	if _, err := os.Stat(rwUnit); err != nil {
		t.Fatalf("RW unit should exist after CheckUpload: %v", err)
	}

	// Step 4: Write permission is revoked again -> CheckUpload returns false, nil and removes unit
	writeAllowed = false
	writable, err = manager.CheckUpload(context.Background(), req.Key)
	if err != nil {
		t.Fatalf("CheckUpload failed: %v", err)
	}
	if writable {
		t.Fatalf("expected CheckUpload == false after permission revoked")
	}
	if _, err := os.Stat(rwUnit); !os.IsNotExist(err) {
		t.Fatalf("RW unit should have been removed after permission revoked: %v", err)
	}
}

func TestNFSEnableUpload(t *testing.T) {
	runner := newFakeNFSSystemctl()
	manager := testNFSManager(t, runner)

	// Mount as RO initially
	manager.WriteProbe = func(path string, uid, gid string) error {
		return os.ErrPermission
	}

	req := validNFSRequest()
	if _, err := manager.Mount(context.Background(), req); err != nil {
		t.Fatalf("Mount failed: %v", err)
	}

	// Try EnableUpload with mismatched export path -> must fail
	badReq := req
	badReq.ExportPath = "/different"
	if err := manager.EnableUpload(context.Background(), badReq); err == nil {
		t.Fatalf("expected error for mismatched export path")
	}

	// Try EnableUpload with mismatched version -> must fail
	badReq2 := req
	badReq2.Version = "3"
	if err := manager.EnableUpload(context.Background(), badReq2); err == nil {
		t.Fatalf("expected error for mismatched version")
	}

	// Try EnableUpload when write probe still fails -> must return error
	if err := manager.EnableUpload(context.Background(), req); err == nil {
		t.Fatalf("expected error when write probe fails")
	}

	// Now make write probe succeed -> EnableUpload must succeed
	manager.WriteProbe = func(path string, uid, gid string) error {
		return nil
	}
	if err := manager.EnableUpload(context.Background(), req); err != nil {
		t.Fatalf("EnableUpload failed: %v", err)
	}

	// RW unit must exist
	rwUnit := filepath.Join(manager.UnitDir, systemdMountUnitName(filepath.Join(manager.UploadRoot, req.Key)))
	if _, err := os.Stat(rwUnit); err != nil {
		t.Fatalf("RW unit missing after EnableUpload: %v", err)
	}
}

func TestNFSRemove(t *testing.T) {
	runner := newFakeNFSSystemctl()
	manager := testNFSManager(t, runner)

	req := validNFSRequest()
	res, err := manager.Mount(context.Background(), req)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	if !res.Writable {
		t.Fatalf("expected writable mount")
	}

	roUnit := filepath.Join(manager.UnitDir, systemdMountUnitName(filepath.Join(manager.MountRoot, req.Key)))
	rwUnit := filepath.Join(manager.UnitDir, systemdMountUnitName(filepath.Join(manager.UploadRoot, req.Key)))
	cfgPath := filepath.Join(manager.ConfigDir, req.Key+".json")

	if _, err := os.Stat(roUnit); err != nil {
		t.Fatalf("RO unit missing before Remove: %v", err)
	}
	if _, err := os.Stat(rwUnit); err != nil {
		t.Fatalf("RW unit missing before Remove: %v", err)
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("config missing before Remove: %v", err)
	}

	if err := manager.Remove(context.Background(), req.Key); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	if _, err := os.Stat(roUnit); !os.IsNotExist(err) {
		t.Fatalf("RO unit still exists after Remove: %v", err)
	}
	if _, err := os.Stat(rwUnit); !os.IsNotExist(err) {
		t.Fatalf("RW unit still exists after Remove: %v", err)
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatalf("config still exists after Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(manager.MountRoot, req.Key)); !os.IsNotExist(err) {
		t.Fatalf("mount dir still exists after Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(manager.UploadRoot, req.Key)); !os.IsNotExist(err) {
		t.Fatalf("upload dir still exists after Remove: %v", err)
	}

	// Verify daemon-reload was called
	foundReload := false
	for _, call := range runner.Calls() {
		if len(call) == 1 && call[0] == "daemon-reload" {
			foundReload = true
			break
		}
	}
	if !foundReload {
		t.Fatalf("expected daemon-reload call, calls: %v", runner.Calls())
	}
}

func TestNFSRecoverMounts(t *testing.T) {
	runner := newFakeNFSSystemctl()
	manager := testNFSManager(t, runner)

	req1 := validNFSRequest()
	req1.Key = "11111111111111111111111111111111"
	if _, err := manager.Mount(context.Background(), req1); err != nil {
		t.Fatalf("Mount 1 failed: %v", err)
	}

	req2 := validNFSRequest()
	req2.Key = "22222222222222222222222222222222"
	if _, err := manager.Mount(context.Background(), req2); err != nil {
		t.Fatalf("Mount 2 failed: %v", err)
	}

	// Mark RO unit of req1 and RW unit of req2 as inactive
	roUnit1 := systemdMountUnitName(filepath.Join(manager.MountRoot, req1.Key))
	rwUnit2 := systemdMountUnitName(filepath.Join(manager.UploadRoot, req2.Key))

	runner.active[roUnit1] = false
	runner.active[rwUnit2] = false

	actions, err := manager.RecoverMounts(context.Background())
	if err != nil {
		t.Fatalf("RecoverMounts failed: %v", err)
	}

	restartedRO1 := false
	restartedRW2 := false
	for _, a := range actions {
		if strings.Contains(a, roUnit1) {
			restartedRO1 = true
		}
		if strings.Contains(a, rwUnit2) {
			restartedRW2 = true
		}
	}
	if !restartedRO1 || !restartedRW2 {
		t.Fatalf("expected restarts for %s and %s, got actions: %v", roUnit1, rwUnit2, actions)
	}
}

func TestNFSStaleMountsAndRemountStale(t *testing.T) {
	runner := newFakeNFSSystemctl()
	manager := testNFSManager(t, runner)

	req1 := validNFSRequest()
	req1.Key = "11111111111111111111111111111111"
	if _, err := manager.Mount(context.Background(), req1); err != nil {
		t.Fatalf("Mount 1 failed: %v", err)
	}

	req2 := validNFSRequest()
	req2.Key = "22222222222222222222222222222222"
	if _, err := manager.Mount(context.Background(), req2); err != nil {
		t.Fatalf("Mount 2 failed: %v", err)
	}

	// Make probe fail only for req1 mount target
	badTarget := filepath.Join(manager.MountRoot, req1.Key)
	manager.Probe = func(path string) error {
		if path == badTarget {
			return errors.New("stale file handle")
		}
		return nil
	}

	stale := manager.StaleMounts()
	if len(stale) != 1 {
		t.Fatalf("expected 1 stale mount, got %d: %+v", len(stale), stale)
	}
	if stale[0].Target != badTarget {
		t.Fatalf("expected stale target %s, got %s", badTarget, stale[0].Target)
	}

	actions, err := manager.RemountStale(context.Background(), stale)
	if err != nil {
		t.Fatalf("RemountStale failed: %v", err)
	}
	if len(actions) != 1 || !strings.Contains(actions[0], stale[0].Unit) {
		t.Fatalf("unexpected remount actions: %v", actions)
	}

	// Verify restart was called
	foundRestart := false
	for _, call := range runner.Calls() {
		if len(call) >= 2 && call[0] == "restart" && call[1] == stale[0].Unit {
			foundRestart = true
			break
		}
	}
	if !foundRestart {
		t.Fatalf("expected restart call for %s, got: %v", stale[0].Unit, runner.Calls())
	}
}

func TestNFSProbeWriteDir(t *testing.T) {
	dir := t.TempDir()
	if err := ProbeWriteDir(dir); err != nil {
		t.Fatalf("ProbeWriteDir failed on writable temp dir: %v", err)
	}

	// Non-existent dir should fail
	if err := ProbeWriteDir(filepath.Join(dir, "does-not-exist")); err == nil {
		t.Fatalf("ProbeWriteDir should fail on non-existent dir")
	}
}

func TestNFSReconnectStopsExistingUploadMount(t *testing.T) {
	runner := newFakeNFSSystemctl()
	manager := testNFSManager(t, runner)
	req := validNFSRequest()

	if _, err := manager.Mount(context.Background(), req); err != nil {
		t.Fatal(err)
	}

	uploadUnitName := systemdMountUnitName(filepath.Join(manager.UploadRoot, req.Key))

	if _, err := manager.Mount(context.Background(), req); err != nil {
		t.Fatal(err)
	}

	stopped := false
	for _, call := range runner.Calls() {
		if len(call) >= 2 && call[0] == "stop" && call[1] == uploadUnitName {
			stopped = true
			break
		}
	}
	if !stopped {
		t.Fatalf("expected existing upload unit %s to be stopped during re-mount: %v", uploadUnitName, runner.Calls())
	}
}
