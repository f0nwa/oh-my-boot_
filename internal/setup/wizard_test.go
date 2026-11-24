package setup

import (
	"bytes"
	"net/netip"
	"strings"
	"testing"
	"unicode/utf8"
)

func testIfaces2NIC() []NetIface {
	return []NetIface{
		{
			Name:   "ens18",
			IP:     netip.MustParseAddr("192.0.2.10"),
			Prefix: netip.MustParsePrefix("192.168.10.0/24"),
		},
		{
			Name:   "ens19",
			IP:     netip.MustParseAddr("10.30.0.10"),
			Prefix: netip.MustParsePrefix("10.30.0.0/24"),
		},
	}
}

func testIfaces1NIC() []NetIface {
	return []NetIface{
		{
			Name:   "ens18",
			IP:     netip.MustParseAddr("192.0.2.10"),
			Prefix: netip.MustParsePrefix("192.168.10.0/24"),
		},
	}
}

func mockSecrets(values ...string) func() (string, error) {
	idx := 0
	return func() (string, error) {
		if idx >= len(values) {
			return "", nil
		}
		v := values[idx]
		idx++
		return v, nil
	}
}

func TestWizardInteractive2NICRussian(t *testing.T) {
	in := strings.NewReader("1\n1\n1\n2\n\n\ny\n")
	var out bytes.Buffer

	opts, confirmed, err := RunWizard(
		in,
		&out,
		mockSecrets("StrongPass1234!", "StrongPass1234!"),
		testIfaces2NIC(),
		Options{},
		true,
	)
	if err != nil {
		t.Fatalf("RunWizard returned error: %v", err)
	}
	if !confirmed {
		t.Fatalf("expected confirmed=true, got false")
	}

	got := out.String()
	for _, want := range []string{"oh my boot", "[1/5]", "[2/5]", "[3/5]", "[4/5]", "[5/5]"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, got)
		}
	}
	// Verify brand mark includes underscore (may have ANSI between boot and _)
	if !strings.Contains(got, "_") {
		t.Errorf("expected output to contain brand cursor underscore")
	}

	if opts.Lang != "ru" {
		t.Errorf("Lang = %q, want %q", opts.Lang, "ru")
	}
	if opts.ManagementIP != "192.0.2.10" {
		t.Errorf("ManagementIP = %q, want %q", opts.ManagementIP, "192.0.2.10")
	}
	if opts.AdminPassword != "StrongPass1234!" {
		t.Errorf("AdminPassword = %q, want %q", opts.AdminPassword, "StrongPass1234!")
	}
	if opts.PXEInterface != "ens19" {
		t.Errorf("PXEInterface = %q, want %q", opts.PXEInterface, "ens19")
	}
	if opts.PXEServerIP != "10.30.0.10" {
		t.Errorf("PXEServerIP = %q, want %q", opts.PXEServerIP, "10.30.0.10")
	}
	if opts.PXESubnet != "10.30.0.0/24" {
		t.Errorf("PXESubnet = %q, want %q", opts.PXESubnet, "10.30.0.0/24")
	}
	if opts.DHCPMode != "full" {
		t.Errorf("DHCPMode = %q, want %q", opts.DHCPMode, "full")
	}
	if opts.DHCPStart != "10.30.0.100" || opts.DHCPEnd != "10.30.0.200" {
		t.Errorf("DHCP range = %s-%s, want 10.30.0.100-10.30.0.200", opts.DHCPStart, opts.DHCPEnd)
	}
	if opts.AllowedInterfaces != "ens19" {
		t.Errorf("AllowedInterfaces = %q, want %q", opts.AllowedInterfaces, "ens19")
	}
	if opts.SkipPXE {
		t.Errorf("SkipPXE = true, want false")
	}
}

func TestWizardInteractive1NICEnglish(t *testing.T) {
	// 2 (English), 1 (ens18 mgmt), 1 (ens18 PXE), "" (default [1] Proxy DHCP), y (confirm)
	in := strings.NewReader("2\n1\n1\n\ny\n")
	var out bytes.Buffer

	opts, confirmed, err := RunWizard(
		in,
		&out,
		mockSecrets("StrongPass1234!", "StrongPass1234!"),
		testIfaces1NIC(),
		Options{},
		false,
	)
	if err != nil {
		t.Fatalf("RunWizard returned error: %v", err)
	}
	if !confirmed {
		t.Fatalf("expected confirmed=true, got false")
	}

	got := out.String()
	if !strings.Contains(got, "Only 1 network interface found") {
		t.Errorf("expected single-NIC warning in English output, got:\n%s", got)
	}
	if !strings.Contains(got, "DHCP mode [1]") {
		t.Errorf("expected Proxy DHCP default [1] in English output, got:\n%s", got)
	}
	if opts.Lang != "en" {
		t.Errorf("Lang = %q, want %q", opts.Lang, "en")
	}
	if opts.PXEInterface != "ens18" {
		t.Errorf("PXEInterface = %q, want %q", opts.PXEInterface, "ens18")
	}
	if opts.DHCPMode != "proxy" {
		t.Errorf("DHCPMode = %q, want %q", opts.DHCPMode, "proxy")
	}
	if opts.AllowedInterfaces != "ens18" {
		t.Errorf("AllowedInterfaces = %q, want %q", opts.AllowedInterfaces, "ens18")
	}
}

func TestWizardInteractiveDeferredPXE(t *testing.T) {
	// 2 (English), 1 (ens18 mgmt), 3 (Configure later in web panel), y (confirm)
	in := strings.NewReader("2\n1\n3\ny\n")
	var out bytes.Buffer

	opts, confirmed, err := RunWizard(
		in,
		&out,
		mockSecrets("StrongPass1234!", "StrongPass1234!"),
		testIfaces2NIC(),
		Options{},
		false,
	)
	if err != nil {
		t.Fatalf("RunWizard returned error: %v", err)
	}
	if !confirmed {
		t.Fatalf("expected confirmed=true, got false")
	}
	if opts.DHCPMode != "skip" {
		t.Errorf("DHCPMode = %q, want %q", opts.DHCPMode, "skip")
	}
	if opts.PXEInterface != "none" {
		t.Errorf("PXEInterface = %q, want %q", opts.PXEInterface, "none")
	}
	if !opts.SkipPXE {
		t.Errorf("SkipPXE = false, want true")
	}
	if opts.AllowedInterfaces == "" || opts.AllowedInterfaces == "omb-pxe-off" {
		t.Errorf("AllowedInterfaces = %q, expected detected interface(s), not omb-pxe-off", opts.AllowedInterfaces)
	}
	if !strings.Contains(opts.AllowedInterfaces, "ens19") {
		t.Errorf("AllowedInterfaces = %q, expected ens19", opts.AllowedInterfaces)
	}
}

func TestWizardRepromptsOnShortOrMismatchedPassword(t *testing.T) {
	// 2 (English), 1 (ens18 mgmt), 1 (ens18 PXE), 1 (Proxy DHCP), y (confirm)
	in := strings.NewReader("2\n1\n1\n1\ny\n")
	var out bytes.Buffer

	// Sequence:
	// 1. "short" -> rejected (<12 chars)
	// 2. "HasNewline\n12345" -> rejected (contains newline)
	// 3. "ValidPassword123" + "MismatchPass1234" -> rejected (mismatch)
	// 4. "ValidPassword123" + "ValidPassword123" -> accepted
	opts, confirmed, err := RunWizard(
		in,
		&out,
		mockSecrets("short", "HasNewline\n12345", "ValidPassword123", "MismatchPass1234", "ValidPassword123", "ValidPassword123"),
		testIfaces1NIC(),
		Options{},
		false,
	)
	if err != nil {
		t.Fatalf("RunWizard returned error: %v", err)
	}
	if !confirmed {
		t.Fatalf("expected confirmed=true, got false")
	}
	if opts.AdminPassword != "ValidPassword123" {
		t.Errorf("AdminPassword = %q, want %q", opts.AdminPassword, "ValidPassword123")
	}
}

func TestColorEnabledHonorsNoColorAndNonTTY(t *testing.T) {
	var buf bytes.Buffer
	if ColorEnabled(&buf, func(string) string { return "" }) {
		t.Errorf("expected ColorEnabled=false for non-TTY bytes.Buffer")
	}
	if ColorEnabled(&buf, func(k string) string {
		if k == "NO_COLOR" {
			return "1"
		}
		return ""
	}) {
		t.Errorf("expected ColorEnabled=false when NO_COLOR=1")
	}
}

func TestWizardNoColorOmitsANSI(t *testing.T) {
	in := strings.NewReader("1\n1\n1\n2\n\n\ny\n")
	var out bytes.Buffer

	_, confirmed, err := RunWizard(
		in,
		&out,
		mockSecrets("StrongPass1234!", "StrongPass1234!"),
		testIfaces2NIC(),
		Options{},
		false,
	)
	if err != nil {
		t.Fatalf("RunWizard returned error: %v", err)
	}
	if !confirmed {
		t.Fatalf("expected confirmed=true, got false")
	}
	if strings.Contains(out.String(), "\x1b[") {
		t.Errorf("expected zero ANSI escape sequences when color=false, got:\n%q", out.String())
	}
	if !strings.Contains(out.String(), "oh my boot_") {
		t.Errorf("expected plain 'oh my boot_' header when color=false")
	}
}

func TestNonInteractiveFlagValidation(t *testing.T) {
	ifaces := testIfaces2NIC()

	t.Run("missing management ip", func(t *testing.T) {
		_, err := ResolveNonInteractive(
			Options{Lang: "en", PXEInterface: "ens19", DHCPMode: "proxy"},
			ifaces,
			strings.NewReader("StrongPass1234!\n"),
			true,
		)
		if err == nil {
			t.Fatalf("expected error for missing management IP, got nil")
		}
	})

	t.Run("short password from stdin", func(t *testing.T) {
		_, err := ResolveNonInteractive(
			Options{Lang: "en", ManagementIP: "192.0.2.10", PXEInterface: "ens19", DHCPMode: "proxy"},
			ifaces,
			strings.NewReader("short\n"),
			true,
		)
		if err == nil {
			t.Fatalf("expected error for short password, got nil")
		}
	})

	t.Run("missing admin-password-stdin flag", func(t *testing.T) {
		_, err := ResolveNonInteractive(
			Options{Lang: "en", ManagementIP: "192.0.2.10", PXEInterface: "ens19", DHCPMode: "proxy"},
			ifaces,
			nil,
			false,
		)
		if err == nil {
			t.Fatalf("expected error when admin password is missing, got nil")
		}
	})

	t.Run("valid full dhcp flags", func(t *testing.T) {
		got, err := ResolveNonInteractive(
			Options{
				Lang:         "ru",
				ManagementIP: "192.0.2.10",
				PXEInterface: "ens19",
				DHCPMode:     "full",
				DHCPStart:    "10.30.0.110",
				DHCPEnd:      "10.30.0.190",
				StateDir:     "/var/lib/omb-deployment",
				InstallMode:  "offline",
			},
			ifaces,
			strings.NewReader("StrongPass1234!\n"),
			true,
		)
		if err != nil {
			t.Fatalf("ResolveNonInteractive returned unexpected error: %v", err)
		}
		if got.ManagementIP != "192.0.2.10" {
			t.Errorf("ManagementIP = %q, want 192.0.2.10", got.ManagementIP)
		}
		if got.AdminPassword != "StrongPass1234!" {
			t.Errorf("AdminPassword = %q, want StrongPass1234!", got.AdminPassword)
		}
		if got.PXEInterface != "ens19" || got.PXEServerIP != "10.30.0.10" || got.PXESubnet != "10.30.0.0/24" {
			t.Errorf("unexpected PXE fields: iface=%q ip=%q subnet=%q", got.PXEInterface, got.PXEServerIP, got.PXESubnet)
		}
		if got.DHCPMode != "full" || got.DHCPStart != "10.30.0.110" || got.DHCPEnd != "10.30.0.190" {
			t.Errorf("unexpected DHCP fields: mode=%q start=%q end=%q", got.DHCPMode, got.DHCPStart, got.DHCPEnd)
		}
		if got.AllowedInterfaces != "ens19" {
			t.Errorf("AllowedInterfaces = %q, want ens19", got.AllowedInterfaces)
		}
		if got.InstallMode != "offline" {
			t.Errorf("InstallMode = %q, want offline", got.InstallMode)
		}
	})

	t.Run("valid skip pxe", func(t *testing.T) {
		got, err := ResolveNonInteractive(
			Options{
				ManagementIP: "192.0.2.10",
				SkipPXE:      true,
			},
			ifaces,
			strings.NewReader("StrongPass1234!\n"),
			true,
		)
		if err != nil {
			t.Fatalf("ResolveNonInteractive returned unexpected error: %v", err)
		}
		if got.Lang != "ru" {
			t.Errorf("Lang = %q, want default ru", got.Lang)
		}
		if !got.SkipPXE || got.DHCPMode != "skip" || got.PXEInterface != "none" {
			t.Errorf("expected SkipPXE=true DHCPMode=skip PXEInterface=none, got %+v", got)
		}
		if got.AllowedInterfaces != "omb-pxe-off" {
			t.Errorf("AllowedInterfaces = %q, want omb-pxe-off", got.AllowedInterfaces)
		}
	})

	t.Run("rejects 9-rune Cyrillic password (18 UTF-8 bytes)", func(t *testing.T) {
		pw := "Абвгдежзи"
		if len(pw) != 18 || utf8.RuneCountInString(pw) != 9 {
			t.Fatalf("unexpected test password dimensions: bytes=%d runes=%d", len(pw), utf8.RuneCountInString(pw))
		}
		_, err := ResolveNonInteractive(
			Options{ManagementIP: "192.0.2.10", SkipPXE: true},
			ifaces,
			strings.NewReader(pw+"\n"),
			true,
		)
		if err == nil {
			t.Fatalf("expected error for 9-rune Cyrillic password, got nil")
		}
	})

	t.Run("accepts 12-rune Cyrillic password", func(t *testing.T) {
		pw := "Абвгдежзийкл"
		if utf8.RuneCountInString(pw) != 12 {
			t.Fatalf("expected 12 runes, got %d", utf8.RuneCountInString(pw))
		}
		got, err := ResolveNonInteractive(
			Options{ManagementIP: "192.0.2.10", SkipPXE: true},
			ifaces,
			strings.NewReader(pw+"\n"),
			true,
		)
		if err != nil {
			t.Fatalf("expected 12-rune Cyrillic password to succeed, got: %v", err)
		}
		if got.AdminPassword != pw {
			t.Errorf("AdminPassword = %q, want %q", got.AdminPassword, pw)
		}
	})

	t.Run("rejects pxe-interface none with dhcp-mode full or proxy", func(t *testing.T) {
		for _, mode := range []string{"full", "proxy"} {
			_, err := ResolveNonInteractive(
				Options{ManagementIP: "192.0.2.10", PXEInterface: "none", DHCPMode: mode},
				ifaces,
				strings.NewReader("StrongPass1234!\n"),
				true,
			)
			if err == nil {
				t.Errorf("expected error for --pxe-interface none with --dhcp-mode %s", mode)
			}
		}
	})

	t.Run("rejects invalid pxe-interface syntax even with dhcp-mode skip", func(t *testing.T) {
		_, err := ResolveNonInteractive(
			Options{ManagementIP: "192.0.2.10", PXEInterface: "ens19;rm -rf /", DHCPMode: "skip"},
			ifaces,
			strings.NewReader("StrongPass1234!\n"),
			true,
		)
		if err == nil {
			t.Fatalf("expected error for invalid --pxe-interface syntax with --dhcp-mode skip")
		}
	})
}

func TestWizardPolishVersionUnassignedIPAndSingleNICWarning(t *testing.T) {
	// Step 0: 2 (English)
	// Step 1: 10.99.99.99 (rejected - not assigned to any detected interface), then 192.0.2.10 (accepted)
	// Step 3: 1 (ens18)
	// Step 4: 1 (Proxy DHCP) - verify 1-NIC Full DHCP conflict warning is printed
	// Step 5: y (confirm)
	in := strings.NewReader("2\n10.99.99.99\n192.0.2.10\n1\n1\ny\n")
	var out bytes.Buffer

	opts, confirmed, err := RunWizard(
		in,
		&out,
		mockSecrets("StrongPass1234!", "StrongPass1234!"),
		testIfaces1NIC(),
		Options{Version: "v0.2.0"},
		false,
	)
	if err != nil {
		t.Fatalf("RunWizard returned error: %v", err)
	}
	if !confirmed {
		t.Fatalf("expected confirmed=true")
	}
	if opts.ManagementIP != "192.0.2.10" {
		t.Errorf("ManagementIP = %q, want 192.0.2.10", opts.ManagementIP)
	}
	got := out.String()
	if !strings.Contains(got, "v0.2.0") {
		t.Errorf("expected header to contain version v0.2.0, got:\n%s", got)
	}
	if !strings.Contains(got, "not assigned to any detected interface") {
		t.Errorf("expected unassigned IPv4 error message, got:\n%s", got)
	}
	if !strings.Contains(got, "Full DHCP on a single-NIC host will conflict") {
		t.Errorf("expected 1-NIC Full DHCP conflict warning in Step 4, got:\n%s", got)
	}
}
