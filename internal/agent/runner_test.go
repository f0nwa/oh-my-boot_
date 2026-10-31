package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemRunnerTargetsOnlyDedicatedPXEUnit(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "args")
	t.Setenv("OMB_RUNNER_LOG", logPath)
	binary := filepath.Join(dir, "record-args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$OMB_RUNNER_LOG\"\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := SystemRunner{DnsmasqBinary: binary, SystemctlBinary: binary}
	if err := runner.Test(context.Background(), "/tmp/candidate.conf"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(logPath)
	if err != nil || string(got) != "--test\n--conf-file=/tmp/candidate.conf\n" {
		t.Fatalf("wrong dnsmasq test invocation: %q %v", got, err)
	}
	if err := runner.Restart(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(logPath)
	if err != nil || !strings.Contains(string(got), "restart\nomb-dnsmasq\n") {
		t.Fatalf("restarted wrong service: %q %v", got, err)
	}
}
