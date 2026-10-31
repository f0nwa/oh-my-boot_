package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"omb/internal/winpe"
	"strings"
	"testing"
)

func TestWindowsAnswerAssetsAreImmutableAndChecked(t *testing.T) {
	profiles := t.TempDir()
	answers := t.TempDir()
	parent := filepath.Join(profiles, "17")
	if err := os.Mkdir(parent, 0755); err != nil {
		t.Fatal(err)
	}
	script := "@echo off\r\nY:\\17\\sources\\setup.exe /InstallFrom:Y:\\17\\sources\\install.wim\r\n"
	if err := os.WriteFile(filepath.Join(parent, "omb-setup.cmd"), []byte(script), 0644); err != nil {
		t.Fatal(err)
	}
	xml := []byte(`<unattend xmlns="urn:schemas-microsoft-com:unattend"/>`)
	sum := sha256.Sum256(xml)
	req := WindowsAnswerRequest{ID: 4, ProfileID: 17, SHA256: hex.EncodeToString(sum[:]), XML: xml}
	m := &WindowsISOManager{AssetRoot: profiles, AnswerRoot: answers}
	if err := m.PrepareAnswer(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if err := m.PrepareAnswer(context.Background(), req); err != nil {
		t.Fatalf("idempotent upload rejected: %v", err)
	}
	check := WindowsAnswerCheck{ID: 4, ProfileID: 17, SHA256: req.SHA256}
	if err := m.CheckAnswer(context.Background(), check); err != nil {
		t.Fatal(err)
	}
	asset, err := os.ReadFile(filepath.Join(answers, "4", "omb-answer.xml"))
	if err != nil || string(asset) != string(xml) {
		t.Fatalf("wrong XML asset: %q %v", asset, err)
	}
	cmd, err := os.ReadFile(filepath.Join(answers, "4", "omb-setup.cmd"))
	if err != nil || !strings.Contains(string(cmd), `/Unattend:X:\Windows\System32\omb-answer.xml`) {
		t.Fatalf("variant script does not select answer file: %q %v", cmd, err)
	}
	changed := req
	changed.XML = []byte(`<unattend xmlns="urn:schemas-microsoft-com:unattend"><settings/></unattend>`)
	if err := m.PrepareAnswer(context.Background(), changed); err == nil {
		t.Fatal("accepted changed body under existing variant ID")
	}
	if err := os.Remove(filepath.Join(answers, "4", "omb-setup.cmd")); err != nil {
		t.Fatal(err)
	}
	if err := m.CheckAnswer(context.Background(), check); err == nil {
		t.Fatal("accepted incomplete variant assets")
	}
}

func TestWindowsAnswerRejectsMissingParentAndSymlinkTarget(t *testing.T) {
	profiles := t.TempDir()
	answers := t.TempDir()
	xml := []byte(`<unattend xmlns="urn:schemas-microsoft-com:unattend"/>`)
	sum := sha256.Sum256(xml)
	req := WindowsAnswerRequest{ID: 5, ProfileID: 17, SHA256: hex.EncodeToString(sum[:]), XML: xml}
	m := &WindowsISOManager{AssetRoot: profiles, AnswerRoot: answers}
	if err := m.PrepareAnswer(context.Background(), req); err == nil {
		t.Fatal("accepted missing prepared ISO profile")
	}
	if err := os.Mkdir(filepath.Join(profiles, "17"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profiles, "17", "omb-setup.cmd"), []byte("Y:\\17\\sources\\setup.exe /InstallFrom:Y:\\17\\sources\\install.wim\r\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(answers, "5")); err != nil {
		t.Fatal(err)
	}
	if err := m.PrepareAnswer(context.Background(), req); err == nil {
		t.Fatal("accepted symlink variant asset target")
	}
}

func TestWindowsAnswerAgentUnitAllowsAssetWrites(t *testing.T) {
	unit, err := os.ReadFile("../../deploy/systemd/omb-agent.service")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(unit), "\n") {
		if strings.HasPrefix(line, "ReadWritePaths=") {
			for _, path := range strings.Fields(strings.TrimPrefix(line, "ReadWritePaths=")) {
				if path == "/srv/omb/tftp/windows-answers" {
					return
				}
			}
		}
	}
	t.Fatal("Agent systemd sandbox does not allow answer asset writes")
}

// TestWindowsAnswerAcceptsLegacyStartup: variants prepared before the pe.cmd
// mapping keep their script and still pass the check.
func TestWindowsAnswerAcceptsLegacyStartup(t *testing.T) {
	m, check, _ := preparedAnswer(t)
	base, err := os.ReadFile(filepath.Join(m.AssetRoot, "17", "omb-setup.cmd"))
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := winpe.RenderAnswerStartupLegacy(base, 17)
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(m.AnswerRoot, "4", "omb-setup.cmd")
	if err := os.WriteFile(script, legacy, 0640); err != nil {
		t.Fatal(err)
	}
	if err := m.CheckAnswer(context.Background(), check); err != nil {
		t.Fatalf("legacy script rejected: %v", err)
	}
	if err := os.WriteFile(script, []byte("@echo off\r\nformat C:\r\n"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := m.CheckAnswer(context.Background(), check); err == nil {
		t.Fatal("accepted a foreign script")
	}
}
