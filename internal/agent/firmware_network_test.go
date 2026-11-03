package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFirmwareNetworkPersistsAcrossAgentRestartAndCanBeDisabled(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "dnsmasq.conf")
	state := filepath.Join(dir, "network.json")
	hosts := filepath.Join(dir, "dhcp", "hosts")
	a, err := NewApplier(conf, state, hosts, &fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	c := agentConfig()
	c.Mode, c.DHCPStart, c.DHCPEnd = "full", "192.0.2.100", "192.0.2.120"
	c.DefaultDriver, c.ReuseFirmwareNetwork = "snp", true
	if err := a.Apply(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved["reuse_firmware_network"] != true {
		t.Fatalf("lost durable setting: %s", data)
	}
	// Recovery must regenerate the option, rather than preserve a manual edit.
	if err := os.WriteFile(conf, []byte("stale config"), 0o640); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	a, err = NewApplier(conf, state, hosts, runner)
	if err != nil {
		t.Fatal(err)
	}
	current, ok := a.CurrentConfig()
	if !ok || !current.ReuseFirmwareNetwork || !strings.Contains(runner.tested, "encap:175,81,") {
		t.Fatalf("recovery lost startup behavior: %+v %s", current, runner.tested)
	}
	current.ReuseFirmwareNetwork = false
	if err := a.Apply(context.Background(), current); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "encap:175,81,") {
		t.Fatal("disabling leaves cached startup active")
	}
}
