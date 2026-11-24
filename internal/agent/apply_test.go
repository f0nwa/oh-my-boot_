package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omb/internal/network"
)

type fakeRunner struct {
	reloads   int
	reloadErr error
	tested    string
	restarts  int
	stops     int
	failOnce  bool
}

func (f *fakeRunner) Test(_ context.Context, path string) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	f.tested = string(contents)
	return nil
}

func (f *fakeRunner) Restart(context.Context) error {
	f.restarts++
	if f.failOnce {
		f.failOnce = false
		return errors.New("restart failed")
	}
	return nil
}

func (f *fakeRunner) Stop(context.Context) error {
	f.stops++
	return nil
}

func agentConfig() network.Config {
	return network.Config{
		Mode: "proxy", Interface: "enp2s0", ServerIP: "192.0.2.10",
		Subnet: "192.0.2.0/24", BootURL: "http://192.0.2.10:8080/boot/menu.ipxe",
		TFTPRoot: "/srv/omb/tftp",
	}
}

func TestApplyRestoresPreviousConfigAfterRestartFailure(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "dnsmasq.conf")
	if err := os.WriteFile(configPath, []byte("previous working config\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{failOnce: true}
	applier, err := NewApplier(configPath, filepath.Join(dir, "state.json"), filepath.Join(dir, "dhcp", "hosts"), runner)
	if err != nil {
		t.Fatal(err)
	}
	if err := applier.Apply(context.Background(), agentConfig()); err == nil {
		t.Fatal("expected restart failure")
	}
	got, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "previous working config\n" || runner.restarts != 2 {
		t.Fatalf("rollback failed: file=%q restarts=%d", got, runner.restarts)
	}
	if !strings.Contains(runner.tested, "dhcp-range=192.0.2.0,proxy") {
		t.Fatalf("candidate was not syntax checked: %q", runner.tested)
	}
}

func TestApplyRejectsInvalidConfigBeforeRunner(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{}
	applier, err := NewApplier(filepath.Join(dir, "dnsmasq.conf"), filepath.Join(dir, "state.json"), filepath.Join(dir, "dhcp", "hosts"), runner)
	if err != nil {
		t.Fatal(err)
	}
	c := agentConfig()
	c.Interface = ""
	if err := applier.Apply(context.Background(), c); err == nil {
		t.Fatal("expected validation failure")
	}
	if runner.tested != "" || runner.restarts != 0 {
		t.Fatal("invalid config reached external runner")
	}
}

func TestApplyFirstFailureLeavesServiceStopped(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "dnsmasq.conf")
	runner := &fakeRunner{failOnce: true}
	applier, err := NewApplier(configPath, filepath.Join(dir, "state.json"), filepath.Join(dir, "dhcp", "hosts"), runner)
	if err != nil {
		t.Fatal(err)
	}
	if err := applier.Apply(context.Background(), agentConfig()); err == nil {
		t.Fatal("expected restart failure")
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) || runner.stops != 1 {
		t.Fatalf("first failed apply left active config or service: stat=%v stops=%d", err, runner.stops)
	}
}

func TestNewApplierRestoresSavedServiceAfterRestart(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "dnsmasq.conf")
	statePath := filepath.Join(dir, "state.json")
	state, err := json.Marshal(agentConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, state, 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	applier, err := NewApplier(configPath, statePath, filepath.Join(dir, "dhcp", "hosts"), runner)
	if err != nil {
		t.Fatal(err)
	}
	if !applier.Status().Applied || runner.restarts != 1 || !strings.Contains(runner.tested, "dhcp-range=192.0.2.0,proxy") {
		t.Fatalf("saved service was not restored: status=%+v restarts=%d tested=%q", applier.Status(), runner.restarts, runner.tested)
	}
}

func (f *fakeRunner) Reload(context.Context) error { f.reloads++; return f.reloadErr }

func TestApplierApplySyncsLocalSMB(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "dnsmasq.conf")
	statePath := filepath.Join(dir, "network.json")
	hostsPath := filepath.Join(dir, "dhcp", "hosts")
	smbConf := filepath.Join(dir, "smb.conf")
	userMap := filepath.Join(dir, "samba-usermap.conf")
	smbState := filepath.Join(dir, "local-smb.json")

	runner := &fakeRunner{}
	smbRunner := &fakeLocalSMBRunner{}
	applier, err := NewApplier(configPath, statePath, hostsPath, runner)
	if err != nil {
		t.Fatal(err)
	}
	applier.LocalSMB = &LocalSMBManager{
		SMBConfPath:      smbConf,
		UserMapPath:      userMap,
		NetworkStatePath: statePath,
		StatePath:        smbState,
		LocalISORoot:     filepath.Join(dir, "iso"),
		Runner:           smbRunner,
	}

	cfg := agentConfig()
	if err := applier.Apply(context.Background(), cfg); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	content, err := os.ReadFile(smbConf)
	if err != nil {
		t.Fatalf("read smb.conf: %v", err)
	}
	confStr := string(content)
	if !strings.Contains(confStr, "interfaces = lo enp2s0") {
		t.Errorf("smb.conf missing 'interfaces = lo enp2s0', got:\n%s", confStr)
	}
	if !strings.Contains(confStr, "hosts allow = 127.0.0.1 192.0.2.0/24") {
		t.Errorf("smb.conf missing 'hosts allow = 127.0.0.1 192.0.2.0/24', got:\n%s", confStr)
	}
	if !smbRunner.hasCallWithArgs("systemctl", "enable", "--now", "smbd") {
		t.Errorf("expected 'systemctl enable --now smbd', calls: %+v", smbRunner.calls)
	}

	smbRunner.mu.Lock()
	callsBeforeWinPEOnly := len(smbRunner.calls)
	smbRunner.mu.Unlock()

	winpeOnly := cfg
	winpeOnly.WinPETimeoutSeconds = 120
	if err := applier.Apply(context.Background(), winpeOnly); err != nil {
		t.Fatalf("WinPE-only Apply failed: %v", err)
	}

	smbRunner.mu.Lock()
	callsAfterWinPEOnly := len(smbRunner.calls)
	smbRunner.mu.Unlock()

	if callsAfterWinPEOnly != callsBeforeWinPEOnly {
		t.Errorf("expected SyncPXENetwork not to be called when only WinPE wait parameters change; calls grew from %d to %d", callsBeforeWinPEOnly, callsAfterWinPEOnly)
	}
}
