package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsAnswerAssetsAreImmutableAndUnbound(t *testing.T) {
	answers := t.TempDir()
	xml := []byte(`<unattend xmlns="urn:schemas-microsoft-com:unattend"/>`)
	sum := sha256.Sum256(xml)
	req := WindowsAnswerRequest{ID: 4, SHA256: hex.EncodeToString(sum[:]), XML: xml}
	m := &WindowsISOManager{AnswerRoot: answers}
	if err := m.PrepareAnswer(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if err := m.PrepareAnswer(context.Background(), req); err != nil {
		t.Fatalf("idempotent upload rejected: %v", err)
	}
	check := WindowsAnswerCheck{ID: 4, SHA256: req.SHA256}
	if err := m.CheckAnswer(context.Background(), check); err != nil {
		t.Fatal(err)
	}
	asset, err := os.ReadFile(filepath.Join(answers, "4", "omb-answer.xml"))
	if err != nil || string(asset) != string(xml) {
		t.Fatalf("wrong XML asset: %q %v", asset, err)
	}
	if _, err := os.Stat(filepath.Join(answers, "4", "omb-setup.cmd")); !os.IsNotExist(err) {
		t.Fatalf("unbound answer unexpectedly created omb-setup.cmd: %v", err)
	}
	changed := req
	changed.XML = []byte(`<unattend xmlns="urn:schemas-microsoft-com:unattend"><settings/></unattend>`)
	if err := m.PrepareAnswer(context.Background(), changed); err == nil {
		t.Fatal("accepted changed body under existing variant ID")
	}
	if err := os.WriteFile(filepath.Join(answers, "4", "omb-answer.xml"), []byte("<unattend/>"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := m.CheckAnswer(context.Background(), check); err == nil {
		t.Fatal("accepted tampered answer XML")
	}
}

func TestWindowsAnswerRejectsSymlinkTarget(t *testing.T) {
	answers := t.TempDir()
	xml := []byte(`<unattend xmlns="urn:schemas-microsoft-com:unattend"/>`)
	sum := sha256.Sum256(xml)
	req := WindowsAnswerRequest{ID: 5, SHA256: hex.EncodeToString(sum[:]), XML: xml}
	m := &WindowsISOManager{AnswerRoot: answers}
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
