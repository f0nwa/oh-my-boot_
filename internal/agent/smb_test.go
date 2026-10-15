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
	return &SMBManager{CredentialsDir: filepath.Join(base, "creds"), UnitDir: filepath.Join(base, "units"), MountRoot: filepath.Join(base, "mounts"), ImagesUID: "123", ImagesGID: "456", Systemctl: runner}
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
	if strings.Contains(string(unit), req.Password) || !strings.Contains(string(unit), "ro,nosuid,nodev,noexec") || !strings.Contains(string(unit), "prefixpath=iso") {
		t.Fatalf("unsafe or incomplete mount unit: %s", unit)
	}
	for _, args := range runner.calls {
		if strings.Contains(strings.Join(args, " "), req.Password) {
			t.Fatalf("password in systemctl arguments: %q", args)
		}
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
