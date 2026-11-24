package ombcli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChannelReadAndSwitchOffline(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "var", "lib", "omb-deployment")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "CHANNEL"), []byte("stable\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "INSTALL_MODE"), []byte("offline\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	app := &App{
		RootDir: root,
		Lang:    "en",
		Out:     &out,
		Euid:    func() int { return 0 },
		Runner:  &mockRunner{},
	}

	// Read current channel without args.
	if err := app.Channel(context.Background(), ChannelOptions{}); err != nil {
		t.Fatalf("Channel() read failed: %v", err)
	}
	if !strings.Contains(out.String(), "stable") || !strings.Contains(out.String(), "offline") {
		t.Fatalf("expected stable & offline in output, got:\n%s", out.String())
	}

	// Switch to dev in offline mode -> writes CHANNEL (0600) and prints offline-deploy.sh hint.
	out.Reset()
	if err := app.Channel(context.Background(), ChannelOptions{Target: "dev"}); err != nil {
		t.Fatalf("Channel(dev) failed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(stateDir, "CHANNEL"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "dev" {
		t.Fatalf("CHANNEL file = %q, want dev", string(data))
	}
	info, err := os.Stat(filepath.Join(stateDir, "CHANNEL"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("CHANNEL mode = %o, want 0600", info.Mode().Perm())
	}
	if !strings.Contains(out.String(), "offline-deploy.sh update --allow-channel-switch") {
		t.Fatalf("expected offline-deploy.sh hint, got:\n%s", out.String())
	}
}

func TestChannelSwitchOnlineNoUpdateAndDeclinedPrompt(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "var", "lib", "omb-deployment")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "CHANNEL"), []byte("stable\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "INSTALL_MODE"), []byte("online\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	app := &App{
		RootDir: root,
		Lang:    "ru",
		Out:     &out,
		In:      strings.NewReader("n\n"),
		Euid:    func() int { return 0 },
		Runner:  &mockRunner{},
	}

	// With --no-update, prints `sudo omb update` without prompting.
	if err := app.Channel(context.Background(), ChannelOptions{Target: "dev", NoUpdate: true}); err != nil {
		t.Fatalf("Channel(dev, --no-update) failed: %v", err)
	}
	if !strings.Contains(out.String(), "sudo omb update") {
		t.Fatalf("expected sudo omb update hint, got:\n%s", out.String())
	}

	// Switch back to stable and answer 'n' to immediate update prompt.
	out.Reset()
	if err := app.Channel(context.Background(), ChannelOptions{Target: "stable"}); err != nil {
		t.Fatalf("Channel(stable) failed: %v", err)
	}
	if !strings.Contains(out.String(), "Обновить OMB до последнего выпуска канала stable сейчас?") {
		t.Fatalf("expected immediate update prompt, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "sudo omb update") {
		t.Fatalf("expected deferred update hint, got:\n%s", out.String())
	}
}

func TestChannelInvalidAndNonRoot(t *testing.T) {
	app := &App{
		RootDir: t.TempDir(),
		Lang:    "en",
		Euid:    func() int { return 1000 },
		Out:     &bytes.Buffer{},
		Runner:  &mockRunner{},
	}
	if err := app.Channel(context.Background(), ChannelOptions{Target: "nightly"}); err == nil {
		t.Fatal("expected error for invalid channel")
	}
	if err := app.Channel(context.Background(), ChannelOptions{Target: "dev"}); err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("expected root error, got %v", err)
	}
}
