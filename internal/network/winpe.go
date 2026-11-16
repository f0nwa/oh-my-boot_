package network

import "fmt"

// WinPE clients wait for the network and the installation share in a bounded
// retry loop. The defaults reproduce the earlier fixed 45 attempts about 2 s
// apart.
const (
	DefaultWinPETimeoutSeconds = 90
	DefaultWinPERetrySeconds   = 2
	minWinPETimeoutSeconds     = 10
	maxWinPETimeoutSeconds     = 600
	minWinPERetrySeconds       = 1
	maxWinPERetrySeconds       = 30

	WinPEExitActionCMD         = "cmd"
	WinPEExitActionRebootTimer = "reboot_timer"
	WinPEExitActionReboot      = "reboot"
)

// EffectiveWinPEExitAction returns the configured action upon Setup exit,
// defaulting to WinPEExitActionCMD ("cmd") when unset or empty.
func (c Config) EffectiveWinPEExitAction() string {
	switch c.WinPEExitAction {
	case WinPEExitActionRebootTimer:
		return WinPEExitActionRebootTimer
	case WinPEExitActionReboot:
		return WinPEExitActionReboot
	default:
		return WinPEExitActionCMD
	}
}

// EffectiveWinPETimeout is the configured wait limit, or the default when unset.
func (c Config) EffectiveWinPETimeout() int {
	if c.WinPETimeoutSeconds == 0 {
		return DefaultWinPETimeoutSeconds
	}
	return c.WinPETimeoutSeconds
}

// EffectiveWinPERetry is the configured pause between attempts, or the default.
func (c Config) EffectiveWinPERetry() int {
	if c.WinPERetrySeconds == 0 {
		return DefaultWinPERetrySeconds
	}
	return c.WinPERetrySeconds
}

// WinPEAttempts is the number of connection attempts for the effective values.
func (c Config) WinPEAttempts() int {
	return WinPEAttempts(c.EffectiveWinPETimeout(), c.EffectiveWinPERetry())
}

// WinPEAttempts rounds timeout/retry up so the loop covers the whole limit.
func WinPEAttempts(timeout, retry int) int {
	if retry <= 0 || timeout <= 0 {
		return 1
	}
	return (timeout + retry - 1) / retry
}

func validateWinPE(c Config) error {
	if c.WinPETimeoutSeconds != 0 && (c.WinPETimeoutSeconds < minWinPETimeoutSeconds || c.WinPETimeoutSeconds > maxWinPETimeoutSeconds) {
		return fmt.Errorf("WinPE wait limit must be between %d and %d seconds", minWinPETimeoutSeconds, maxWinPETimeoutSeconds)
	}
	if c.WinPERetrySeconds != 0 && (c.WinPERetrySeconds < minWinPERetrySeconds || c.WinPERetrySeconds > maxWinPERetrySeconds) {
		return fmt.Errorf("WinPE retry pause must be between %d and %d seconds", minWinPERetrySeconds, maxWinPERetrySeconds)
	}
	if c.EffectiveWinPETimeout() < c.EffectiveWinPERetry() {
		return fmt.Errorf("WinPE wait limit must not be shorter than the retry pause")
	}
	switch c.WinPEExitAction {
	case "", WinPEExitActionCMD, WinPEExitActionRebootTimer, WinPEExitActionReboot:
	default:
		return fmt.Errorf("WinPE exit action must be cmd, reboot_timer, or reboot")
	}
	return nil
}
