package agent

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// SystemRunner manages only the dedicated PXE dnsmasq unit.
type SystemRunner struct {
	DnsmasqBinary   string
	SystemctlBinary string
}

func (r SystemRunner) Test(ctx context.Context, path string) error {
	binary := r.DnsmasqBinary
	if binary == "" {
		binary = "dnsmasq"
	}
	return run(ctx, binary, "--test", "--conf-file="+path)
}

func (r SystemRunner) Restart(ctx context.Context) error {
	return run(ctx, r.systemctl(), "restart", "omb-dnsmasq")
}

func (r SystemRunner) Stop(ctx context.Context) error {
	return run(ctx, r.systemctl(), "stop", "omb-dnsmasq")
}

func (r SystemRunner) systemctl() string {
	if r.SystemctlBinary != "" {
		return r.SystemctlBinary
	}
	return "systemctl"
}

func run(ctx context.Context, binary string, args ...string) error {
	output, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", binary, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (r SystemRunner) Reload(ctx context.Context) error {
	return run(ctx, r.systemctl(), "kill", "--signal=SIGHUP", "omb-dnsmasq.service")
}
