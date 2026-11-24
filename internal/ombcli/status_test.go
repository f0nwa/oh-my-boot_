package ombcli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omb/internal/setup"
)

func TestDetectLang(t *testing.T) {
	tests := []struct {
		flag string
		env  map[string]string
		want string
		err  bool
	}{
		{flag: "ru", env: map[string]string{"LANG": "en_US.UTF-8"}, want: "ru"},
		{flag: "en", env: map[string]string{"LANG": "ru_RU.UTF-8"}, want: "en"},
		{flag: "de", err: true},
		{env: map[string]string{"LANG": "ru_RU.UTF-8"}, want: "ru"},
		{env: map[string]string{"LC_ALL": "ru_RU.UTF-8", "LANG": "C"}, want: "ru"},
		{env: map[string]string{"LANG": "C.UTF-8"}, want: "en"},
		{env: map[string]string{"LANG": "en_US.UTF-8"}, want: "en"},
		{env: map[string]string{}, want: "en"},
	}

	for _, tc := range tests {
		got, err := DetectLang(tc.flag, func(k string) string { return tc.env[k] })
		if tc.err {
			if err == nil {
				t.Fatalf("DetectLang(%q) expected error", tc.flag)
			}
			continue
		}
		if err != nil {
			t.Fatalf("DetectLang(%q) unexpected error: %v", tc.flag, err)
		}
		if got != tc.want {
			t.Fatalf("DetectLang(%q, %v) = %q, want %q", tc.flag, tc.env, got, tc.want)
		}
	}
}

func TestStatusFormattingAll7Services(t *testing.T) {
	root := t.TempDir()
	releasePath := filepath.Join(root, "usr", "share", "omb", "release.json")
	if err := os.MkdirAll(filepath.Dir(releasePath), 0o755); err != nil {
		t.Fatal(err)
	}
	releaseJSON := `{
  "channel": "dev",
  "commit": "0123456789abcdef0123456789abcdef01234567",
  "display_version": "0.2.1-dev.2",
  "package": "omb 0.2.1~dev.2",
  "version": "0.2.1~dev.2"
}`
	if err := os.WriteFile(releasePath, []byte(releaseJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	stateDir := filepath.Join(root, "var", "lib", "omb-deployment")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "CHANNEL"), []byte("dev\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "INSTALL_MODE"), []byte("online\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := &mockRunner{
		fn: func(_ context.Context, spec setup.CmdSpec) ([]byte, error) {
			if spec.Name == "systemctl" && len(spec.Args) == 2 && spec.Args[0] == "is-active" {
				switch spec.Args[1] {
				case "omb-dnsmasq":
					return []byte("inactive\n"), errors.New("exit status 3")
				case "smbd":
					return []byte("failed\n"), errors.New("exit status 3")
				default:
					return []byte("active\n"), nil
				}
			}
			return nil, nil
		},
	}

	var out bytes.Buffer
	app := &App{
		RootDir: root,
		Lang:    "ru",
		Out:     &out,
		Runner:  runner,
	}

	if err := app.Status(context.Background()); err != nil {
		t.Fatalf("Status failed: %v", err)
	}

	text := out.String()
	for _, want := range []string{
		"oh my boot_",
		"0.2.1-dev.2",
		"0123456789",
		"dev",
		"online",
		"postgresql",
		"omb-boot",
		"omb-agent",
		"omb-images",
		"omb-control",
		"omb-dnsmasq",
		"smbd",
		"active",
		"inactive",
		"failed",
		"Файрвол:",
		"UFW выключен (порты открыты)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("expected status output to contain %q, got:\n%s", want, text)
		}
	}
}

func TestStatusUFWActiveAndInactive(t *testing.T) {
	tests := []struct {
		name      string
		lang      string
		ufwOut    string
		ufwErr    error
		wantLabel string
		wantValue string
	}{
		{
			name:      "ru active",
			lang:      "ru",
			ufwOut:    "Status: active\n",
			wantLabel: "Файрвол:",
			wantValue: "UFW активен (проверьте правила: 8443/tcp, 8080/tcp, 67/69/4011/udp, 445/tcp)",
		},
		{
			name:      "en active",
			lang:      "en",
			ufwOut:    "Status: active\n",
			wantLabel: "Firewall:",
			wantValue: "UFW active (check rules: 8443/tcp, 8080/tcp, 67/69/4011/udp, 445/tcp)",
		},
		{
			name:      "en inactive",
			lang:      "en",
			ufwOut:    "Status: inactive\n",
			wantLabel: "Firewall:",
			wantValue: "UFW inactive (ports open)",
		},
		{
			name:      "ru ufw missing",
			lang:      "ru",
			ufwErr:    errors.New("not found"),
			wantLabel: "Файрвол:",
			wantValue: "UFW выключен (порты открыты)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner := &mockRunner{
				fn: func(_ context.Context, spec setup.CmdSpec) ([]byte, error) {
					if spec.Name == "ufw" {
						return []byte(tc.ufwOut), tc.ufwErr
					}
					if spec.Name == "systemctl" {
						return []byte("active\n"), nil
					}
					return nil, errors.New("not installed")
				},
			}
			var out bytes.Buffer
			app := &App{
				RootDir: t.TempDir(),
				Lang:    tc.lang,
				Out:     &out,
				Runner:  runner,
			}
			if err := app.Status(context.Background()); err != nil {
				t.Fatalf("Status failed: %v", err)
			}
			text := out.String()
			if !strings.Contains(text, tc.wantLabel) {
				t.Errorf("expected output to contain %q, got:\n%s", tc.wantLabel, text)
			}
			if !strings.Contains(text, tc.wantValue) {
				t.Errorf("expected output to contain %q, got:\n%s", tc.wantValue, text)
			}
		})
	}
}
