package network

import "testing"

func TestWinPEConfigDefaults(t *testing.T) {
	var c Config
	if c.EffectiveWinPETimeout() != 90 || c.EffectiveWinPERetry() != 2 {
		t.Fatalf("defaults = %d/%d, want 90/2", c.EffectiveWinPETimeout(), c.EffectiveWinPERetry())
	}
	if DefaultWinPETimeoutSeconds != 90 || DefaultWinPERetrySeconds != 2 {
		t.Fatal("default constants changed")
	}
	if got := c.WinPEAttempts(); got != 45 {
		t.Fatalf("default attempts = %d, want 45", got)
	}
	if c.EffectiveWinPEExitAction() != WinPEExitActionCMD {
		t.Fatalf("default exit action = %q, want %q", c.EffectiveWinPEExitAction(), WinPEExitActionCMD)
	}
	c.WinPETimeoutSeconds, c.WinPERetrySeconds = 120, 5
	c.WinPEExitAction = WinPEExitActionRebootTimer
	if c.EffectiveWinPETimeout() != 120 || c.EffectiveWinPERetry() != 5 {
		t.Fatal("explicit values are not used")
	}
	if c.EffectiveWinPEExitAction() != WinPEExitActionRebootTimer {
		t.Fatalf("explicit exit action = %q, want %q", c.EffectiveWinPEExitAction(), WinPEExitActionRebootTimer)
	}
}

func TestWinPEAttemptsCalculation(t *testing.T) {
	for _, tc := range []struct{ timeout, retry, want int }{
		{90, 2, 45}, {10, 3, 4}, {60, 1, 60}, {600, 1, 600}, {10, 10, 1}, {31, 30, 2},
	} {
		if got := WinPEAttempts(tc.timeout, tc.retry); got != tc.want {
			t.Errorf("WinPEAttempts(%d,%d) = %d, want %d", tc.timeout, tc.retry, got, tc.want)
		}
	}
}

func TestWinPEConfigValidation(t *testing.T) {
	for _, tc := range []struct {
		name           string
		timeout, retry int
		exitAction     string
		ok             bool
	}{
		{"defaults", 0, 0, "", true},
		{"bounds low", 10, 1, "cmd", true},
		{"bounds high", 600, 30, "reboot_timer", true},
		{"reboot action", 60, 2, "reboot", true},
		{"timeout only", 300, 0, "", true},
		{"timeout too small", 9, 1, "", false},
		{"timeout too large", 601, 2, "", false},
		{"retry too small", 90, -1, "", false},
		{"retry too large", 90, 31, "", false},
		{"timeout negative", -5, 2, "", false},
		{"timeout below retry", 20, 25, "", false},
		{"default retry above timeout", 0, 30, "", true},
		{"invalid exit action", 90, 2, "shutdown", false},
	} {
		c := fullConfig()
		c.WinPETimeoutSeconds, c.WinPERetrySeconds = tc.timeout, tc.retry
		c.WinPEExitAction = tc.exitAction
		if err := Validate(c); (err == nil) != tc.ok {
			t.Errorf("%s: Validate error = %v, want ok=%t", tc.name, err, tc.ok)
		}
	}
}
