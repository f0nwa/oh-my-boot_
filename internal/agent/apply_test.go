package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pxe-platform/internal/network"
)

type fakeRunner struct {
	tested   string
	restarts int
	stops    int
	failOnce bool
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
		TFTPRoot: "/srv/pxe/tftp",
	}
}

func TestApplyRestoresPreviousConfigAfterRestartFailure(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "dnsmasq.conf")
	if err := os.WriteFile(configPath, []byte("previous working config\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{failOnce: true}
	applier, err := NewApplier(configPath, filepath.Join(dir, "state.json"), runner)
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
	applier, err := NewApplier(filepath.Join(dir, "dnsmasq.conf"), filepath.Join(dir, "state.json"), runner)
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
	applier, err := NewApplier(configPath, filepath.Join(dir, "state.json"), runner)
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
