package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeSMBSystemctl struct {
	calls [][]string
	fail  string
}

func (f *fakeSMBSystemctl) Run(_ context.Context, args ...string) error {
	f.calls = append(f.calls, append([]string(nil), args...))
	if len(args) > 0 && args[0] == f.fail {
		return os.ErrPermission
	}
	return nil
}

func testSMBManager(t *testing.T, runner *fakeSMBSystemctl) *SMBManager {
	t.Helper()
	base := t.TempDir()
	return &SMBManager{CredentialsDir: filepath.Join(base, "creds"), UnitDir: filepath.Join(base, "units"), MountRoot: filepath.Join(base, "mounts"), UploadRoot: filepath.Join(base, "uploads"), ImagesUID: "123", ImagesGID: "456", Systemctl: runner}
}

func validSMBRequest() SMBMountRequest {
	return SMBMountRequest{Key: "0123456789abcdef0123456789abcdef", Server: "192.168.10.10", Share: "Downloads", Subpath: "iso", Username: "reader", Password: "a,b$secret"}
}

func TestSMBMountWritesProtectedCredentialsAndNoPasswordInUnitOrCommands(t *testing.T) {
	runner := &fakeSMBSystemctl{}
	manager := testSMBManager(t, runner)
	req := validSMBRequest()
	if err := manager.Mount(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	credPath := filepath.Join(manager.CredentialsDir, req.Key+".cred")
	info, err := os.Stat(credPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("credential file mode=%v err=%v", info, err)
	}
	unitPath := filepath.Join(manager.UnitDir, smbUnitName(filepath.Join(manager.MountRoot, req.Key)))
	unit, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(unit), req.Password) || !strings.Contains(string(unit), "ro,nosuid,nodev,noexec") || !strings.Contains(string(unit), "What=//192.168.10.10/Downloads/iso\n") || strings.Contains(string(unit), "prefixpath=") {
		t.Fatalf("unsafe or incomplete mount unit: %s", unit)
	}
	for _, args := range runner.calls {
		if strings.Contains(strings.Join(args, " "), req.Password) {
			t.Fatalf("password in systemctl arguments: %q", args)
		}
	}
}

func TestSMBUploadMountUsesExistingCredentialsWithoutChangingReadMount(t *testing.T) {
	runner := &fakeSMBSystemctl{}
	manager := testSMBManager(t, runner)
	req := validSMBRequest()
	if err := manager.Mount(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	readUnit := filepath.Join(manager.UnitDir, smbUnitName(filepath.Join(manager.MountRoot, req.Key)))
	before, err := os.ReadFile(readUnit)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.EnableUpload(context.Background(), req.Key, req.Server, req.Share, req.Subpath); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(readUnit)
	if err != nil || string(before) != string(after) {
		t.Fatalf("read-only mount changed: %v", err)
	}
	uploadUnit := filepath.Join(manager.UnitDir, smbUnitName(filepath.Join(manager.UploadRoot, req.Key)))
	unit, err := os.ReadFile(uploadUnit)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unit), "rw,nosuid,nodev,noexec") || !strings.Contains(string(unit), "What=//192.168.10.10/Downloads/iso\n") || strings.Contains(string(unit), req.Password) {
		t.Fatalf("upload mount options or credentials unsafe: %s", unit)
	}
}

func TestSMBUploadMountCanRetryAfterSystemctlFailure(t *testing.T) {
	runner := &fakeSMBSystemctl{fail: "enable"}
	manager := testSMBManager(t, runner)
	req := validSMBRequest()
	runner.fail = ""
	if err := manager.Mount(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	runner.fail = "enable"
	if err := manager.EnableUpload(context.Background(), req.Key, req.Server, req.Share, req.Subpath); err == nil {
		t.Fatal("expected mount failure")
	}
	runner.fail = ""
	if err := manager.EnableUpload(context.Background(), req.Key, req.Server, req.Share, req.Subpath); err != nil {
		t.Fatal(err)
	}
	enables := 0
	for _, call := range runner.calls {
		if len(call) == 3 && call[0] == "enable" && strings.Contains(call[2], "uploads") {
			enables++
		}
	}
	if enables != 2 {
		t.Fatalf("want two upload mount attempts, got %d", enables)
	}
}

func TestSMBRemoveCleansUpUploadMount(t *testing.T) {
	runner := &fakeSMBSystemctl{}
	manager := testSMBManager(t, runner)
	req := validSMBRequest()
	if err := manager.Mount(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if err := manager.EnableUpload(context.Background(), req.Key, req.Server, req.Share, req.Subpath); err != nil {
		t.Fatal(err)
	}
	if err := manager.Remove(context.Background(), req.Key); err != nil {
		t.Fatal(err)
	}
	uploadUnit := filepath.Join(manager.UnitDir, smbUnitName(filepath.Join(manager.UploadRoot, req.Key)))
	if _, err := os.Stat(uploadUnit); !os.IsNotExist(err) {
		t.Fatalf("upload unit remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(manager.UploadRoot, req.Key)); !os.IsNotExist(err) {
		t.Fatalf("upload mount directory remains: %v", err)
	}
}

func TestSMBReconnectRestartsPreviouslyEnabledUploadMount(t *testing.T) {
	runner := &fakeSMBSystemctl{}
	manager := testSMBManager(t, runner)
	req := validSMBRequest()
	if err := manager.Mount(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if err := manager.EnableUpload(context.Background(), req.Key, req.Server, req.Share, req.Subpath); err != nil {
		t.Fatal(err)
	}
	req.Password = "new-password"
	if err := manager.Mount(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	uploadUnitName := smbUnitName(filepath.Join(manager.UploadRoot, req.Key))
	stopped, started := false, 0
	for _, call := range runner.calls {
		if len(call) == 2 && call[0] == "stop" && call[1] == uploadUnitName {
			stopped = true
		}
		if len(call) == 3 && call[0] == "enable" && call[2] == uploadUnitName {
			started++
		}
	}
	if !stopped || started != 2 {
		t.Fatalf("upload mount was not refreshed after credentials change: %v", runner.calls)
	}
}

func TestSMBMountRejectsInvalidInputBeforeWriting(t *testing.T) {
	manager := testSMBManager(t, &fakeSMBSystemctl{})
	for _, req := range []SMBMountRequest{
		func() SMBMountRequest { r := validSMBRequest(); r.Subpath = "../iso"; return r }(),
		func() SMBMountRequest { r := validSMBRequest(); r.Password = "bad\npassword"; return r }(),
		func() SMBMountRequest { r := validSMBRequest(); r.Server = "192.168.10.10,vers=1.0"; return r }(),
	} {
		if err := manager.Mount(context.Background(), req); err == nil {
			t.Fatalf("accepted invalid request: %+v", req)
		}
	}
	if _, err := os.Stat(manager.CredentialsDir); !os.IsNotExist(err) {
		t.Fatalf("invalid input created credential directory: %v", err)
	}
}

func TestSMBMountRetriesOnlyItsOwnUnit(t *testing.T) {
	runner := &fakeSMBSystemctl{}
	manager := testSMBManager(t, runner)
	req := validSMBRequest()
	if err := manager.Mount(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	req.Password = "replacement"
	if err := manager.Mount(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	cred, err := os.ReadFile(filepath.Join(manager.CredentialsDir, req.Key+".cred"))
	if err != nil || !strings.Contains(string(cred), "password=replacement") {
		t.Fatalf("credentials were not replaced: err=%v", err)
	}
	stops := 0
	for _, call := range runner.calls {
		if len(call) == 2 && call[0] == "stop" && strings.Contains(call[1], req.Key) {
			stops++
		}
	}
	if stops != 1 {
		t.Fatalf("retry stop count=%d calls=%v", stops, runner.calls)
	}
}

func TestSMBRemoveKeepsFilesWhenSystemctlFails(t *testing.T) {
	runner := &fakeSMBSystemctl{}
	manager := testSMBManager(t, runner)
	req := validSMBRequest()
	if err := manager.Mount(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	runner.fail = "disable"
	if err := manager.Remove(context.Background(), req.Key); err == nil {
		t.Fatal("expected cleanup failure")
	}
	if _, err := os.Stat(filepath.Join(manager.CredentialsDir, req.Key+".cred")); err != nil {
		t.Fatalf("credentials removed before cleanup succeeded: %v", err)
	}
}

func TestSMBRemoveCanRetryAfterCleanupFinished(t *testing.T) {
	runner := &fakeSMBSystemctl{}
	manager := testSMBManager(t, runner)
	req := validSMBRequest()
	if err := manager.Mount(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if err := manager.Remove(context.Background(), req.Key); err != nil {
		t.Fatal(err)
	}
	runner.fail = "disable"
	if err := manager.Remove(context.Background(), req.Key); err != nil {
		t.Fatalf("retry after completed cleanup: %v", err)
	}
}

func TestSMBMountRejectsUnreadableShare(t *testing.T) {
	manager := testSMBManager(t, &fakeSMBSystemctl{})
	probed := false
	manager.Probe = func(path string) error {
		probed = true
		if path != filepath.Join(manager.MountRoot, validSMBRequest().Key) {
			t.Fatalf("probe path = %q", path)
		}
		return os.ErrPermission
	}
	if err := manager.Mount(context.Background(), validSMBRequest()); err == nil || !probed {
		t.Fatalf("unreadable share passed: err=%v probed=%v", err, probed)
	}
}

func TestSMBEnableUploadRejectsShareWithoutWriteAccess(t *testing.T) {
	manager := testSMBManager(t, &fakeSMBSystemctl{})
	req := validSMBRequest()
	if err := manager.Mount(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	probed := false
	manager.WriteProbe = func(path string) error {
		probed = true
		if path != filepath.Join(manager.UploadRoot, req.Key) {
			t.Fatalf("write probe path = %q", path)
		}
		return os.ErrPermission
	}
	if err := manager.EnableUpload(context.Background(), req.Key, req.Server, req.Share, req.Subpath); err == nil || !probed {
		t.Fatalf("read-only share passed: err=%v probed=%v", err, probed)
	}
}

func TestSMBWriteProbeWritesReadsAndRemovesTemporaryFile(t *testing.T) {
	root := t.TempDir()
	if err := probeSMBWrite(root); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("write probe left files: %+v", entries)
	}
}
