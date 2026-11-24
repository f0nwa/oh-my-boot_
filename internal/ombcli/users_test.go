package ombcli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omb/internal/setup"
)

type mockRunner struct {
	calls []setup.CmdSpec
	fn    func(ctx context.Context, spec setup.CmdSpec) ([]byte, error)
}

func (m *mockRunner) Run(ctx context.Context, spec setup.CmdSpec) ([]byte, error) {
	m.calls = append(m.calls, spec)
	if m.fn != nil {
		return m.fn(ctx, spec)
	}
	return nil, nil
}

func TestParseEnvFile(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "control.env")
	content := `# comment line
OMB_CONTROL_LISTEN=192.0.2.10:8443
OMB_CONTROL_DATABASE_URL="host=/var/run/postgresql dbname=omb_control user=omb-control"
export SINGLE_QUOTED='hello world'
`
	if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := ParseEnvFile(envPath)
	if err != nil {
		t.Fatalf("ParseEnvFile error: %v", err)
	}
	if got["OMB_CONTROL_LISTEN"] != "192.0.2.10:8443" {
		t.Fatalf("unexpected OMB_CONTROL_LISTEN: %q", got["OMB_CONTROL_LISTEN"])
	}
	wantDSN := "host=/var/run/postgresql dbname=omb_control user=omb-control"
	if got["OMB_CONTROL_DATABASE_URL"] != wantDSN {
		t.Fatalf("unexpected DSN: got %q, want %q", got["OMB_CONTROL_DATABASE_URL"], wantDSN)
	}
	if got["SINGLE_QUOTED"] != "hello world" {
		t.Fatalf("unexpected SINGLE_QUOTED: %q", got["SINGLE_QUOTED"])
	}
}

func TestValidatePasswordUTF8Runes(t *testing.T) {
	// 9 Cyrillic runes = 18 UTF-8 bytes, must be rejected (< 12 runes).
	nineCyrillic := "парольАБВ"
	if err := ValidatePassword(nineCyrillic); err == nil {
		t.Fatalf("expected 9 Cyrillic runes (%d bytes) to be rejected", len(nineCyrillic))
	}

	// 12 Cyrillic runes must be accepted.
	twelveCyrillic := "парольАБВГДЕ"
	if err := ValidatePassword(twelveCyrillic); err != nil {
		t.Fatalf("expected 12 Cyrillic runes to be accepted, got %v", err)
	}

	// Newlines must be rejected.
	if err := ValidatePassword("validpassword123\n"); err == nil {
		t.Fatal("expected password with newline to be rejected")
	}
	if err := ValidatePassword("validpassword123\r"); err == nil {
		t.Fatal("expected password with CR to be rejected")
	}
}

func TestResetPasswordInteractivePickAndRun(t *testing.T) {
	root := t.TempDir()
	writeTestControlEnv(t, root)

	runner := &mockRunner{
		fn: func(_ context.Context, spec setup.CmdSpec) ([]byte, error) {
			joined := strings.Join(spec.Args, " ")
			if strings.Contains(joined, "SELECT username, role, enabled, totp_enabled") {
				return []byte("admin\tadmin\tt\tf\noperator1\toperator\tt\tt\n"), nil
			}
			return nil, nil
		},
	}

	secrets := []string{"short", "StrongPassphrase2026!", "WrongRepeat2026!", "StrongPassphrase2026!", "StrongPassphrase2026!"}
	secretIdx := 0

	var out, errOut bytes.Buffer
	app := &App{
		RootDir: root,
		Lang:    "ru",
		In:      strings.NewReader("\n"), // default choice "1" -> admin
		Out:     &out,
		ErrOut:  &errOut,
		Euid:    func() int { return 0 },
		Runner:  runner,
		ReadSecret: func() (string, error) {
			s := secrets[secretIdx]
			secretIdx++
			return s, nil
		},
	}

	if err := app.ResetPassword(context.Background(), ResetPasswordOptions{}); err != nil {
		t.Fatalf("ResetPassword failed: %v", err)
	}

	if !strings.Contains(out.String(), "1) admin") || !strings.Contains(out.String(), "2) operator1") {
		t.Fatalf("expected numbered user list in output, got:\n%s", out.String())
	}

	var foundCtl bool
	for _, c := range runner.calls {
		if c.Name == "runuser" && strings.Contains(strings.Join(c.Args, " "), "/usr/bin/omb-controlctl reset-password -username admin") {
			foundCtl = true
			if string(c.Stdin) != "StrongPassphrase2026!\n" {
				t.Fatalf("unexpected password stdin: %q", string(c.Stdin))
			}
		}
	}
	if !foundCtl {
		t.Fatalf("expected omb-controlctl reset-password invocation, calls: %+v", runner.calls)
	}
}

func TestResetPasswordStdinAndExternalUserRejection(t *testing.T) {
	root := t.TempDir()
	writeTestControlEnv(t, root)

	runner := &mockRunner{
		fn: func(_ context.Context, spec setup.CmdSpec) ([]byte, error) {
			joined := strings.Join(spec.Args, " ")
			if strings.Contains(joined, "SELECT username, role, enabled, totp_enabled") {
				return []byte("admin\tadmin\tt\tf\n"), nil
			}
			if strings.Contains(string(spec.Stdin), "SELECT auth_source") {
				return []byte("ldap\n"), nil
			}
			return nil, nil
		},
	}

	// Reject external AD/LDAP user with a clear error.
	var out, errOut bytes.Buffer
	app := &App{
		RootDir: root,
		Lang:    "en",
		In:      strings.NewReader("ValidPassword12345!\n"),
		Out:     &out,
		ErrOut:  &errOut,
		Euid:    func() int { return 0 },
		Runner:  runner,
	}
	err := app.ResetPassword(context.Background(), ResetPasswordOptions{
		Username:      "jdoe@corp.local",
		PasswordStdin: true,
	})
	if err == nil || !strings.Contains(err.Error(), "external account (ldap)") {
		t.Fatalf("expected external user error, got: %v", err)
	}

	// Succeed with --password-stdin for local user admin.
	app.In = strings.NewReader("ValidPassword12345!\n")
	if err := app.ResetPassword(context.Background(), ResetPasswordOptions{
		Username:      "admin",
		PasswordStdin: true,
	}); err != nil {
		t.Fatalf("expected --password-stdin to succeed, got: %v", err)
	}
}

func TestResetPasswordRequiresRoot(t *testing.T) {
	app := &App{
		Lang: "en",
		Euid: func() int { return 1000 },
	}
	if err := app.ResetPassword(context.Background(), ResetPasswordOptions{Username: "admin"}); err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("expected root error, got %v", err)
	}
}

func TestReset2FA(t *testing.T) {
	root := t.TempDir()
	writeTestControlEnv(t, root)

	runner := &mockRunner{
		fn: func(_ context.Context, spec setup.CmdSpec) ([]byte, error) {
			joined := strings.Join(spec.Args, " ")
			if strings.Contains(joined, "SELECT username, role, enabled, totp_enabled") {
				return []byte("admin\tadmin\tt\tt\nops\toperator\tt\tf\n"), nil
			}
			return nil, nil
		},
	}

	var out, errOut bytes.Buffer
	app := &App{
		RootDir: root,
		Lang:    "en",
		In:      strings.NewReader("1\ny\n"),
		Out:     &out,
		ErrOut:  &errOut,
		Euid:    func() int { return 0 },
		Runner:  runner,
	}

	if err := app.Reset2FA(context.Background(), Reset2FAOptions{}); err != nil {
		t.Fatalf("Reset2FA failed: %v", err)
	}
	if !strings.Contains(out.String(), "2FA: on") || !strings.Contains(out.String(), "2FA: off") {
		t.Fatalf("expected 2FA status in list, got:\n%s", out.String())
	}

	var foundCtl bool
	for _, c := range runner.calls {
		if c.Name == "runuser" && strings.Contains(strings.Join(c.Args, " "), "/usr/bin/omb-controlctl reset-2fa -username admin") {
			foundCtl = true
		}
	}
	if !foundCtl {
		t.Fatalf("expected omb-controlctl reset-2fa invocation, calls: %+v", runner.calls)
	}
}

func writeTestControlEnv(t *testing.T, root string) {
	t.Helper()
	p := filepath.Join(root, "etc", "omb", "control.env")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "OMB_CONTROL_LISTEN=192.0.2.10:8443\nOMB_CONTROL_DATABASE_URL=\"host=/var/run/postgresql dbname=omb_control user=omb-control\"\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
