package winpe

import (
	"os"
	"strings"
	"testing"
)

func TestUpdateStartupServerIP(t *testing.T) {
	raw, err := os.ReadFile("../../deploy/boot-assets/windows-profiles/omb-setup.cmd.in")
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	updated, changed, err := UpdateStartupServerIP(raw, "192.168.10.135")
	if err != nil {
		t.Fatalf("UpdateStartupServerIP: %v", err)
	}
	if !changed {
		t.Fatal("expected template to change")
	}
	text := string(updated)
	if strings.Contains(text, `\\10.30.0.10\ombisos`) {
		t.Fatalf("legacy IP still present:\n%s", text)
	}
	if strings.Count(text, `\\192.168.10.135\ombisos`) != 2 {
		t.Fatalf("expected 2 updated UNC paths, got:\n%s", text)
	}

	// Idempotent when applied again with the same IP.
	again, changedAgain, err := UpdateStartupServerIP(updated, "192.168.10.135")
	if err != nil {
		t.Fatalf("second UpdateStartupServerIP: %v", err)
	}
	if changedAgain || string(again) != text {
		t.Fatal("expected idempotent update")
	}

	// Also handles Strelec and answer I: mappings with profile suffix.
	strelec := []byte("net use Z: \\\\10.30.0.10\\ombisos\\12 pw /user:omb-iso\r\nnet use I: \\\\10.30.0.10\\ombisos\\12 pw\r\n")
	out, changed, err := UpdateStartupServerIP(strelec, "10.50.0.10")
	if err != nil || !changed {
		t.Fatalf("strelec update failed: changed=%v err=%v", changed, err)
	}
	if !strings.Contains(string(out), `\\10.50.0.10\ombisos\12`) || strings.Contains(string(out), `10.30.0.10`) {
		t.Fatalf("unexpected strelec output: %s", out)
	}
}
