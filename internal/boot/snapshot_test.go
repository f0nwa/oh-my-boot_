package boot

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateSnapshotKeepsPublishedMenu(t *testing.T) {
	path := filepath.Join(t.TempDir(), "menu.ipxe")
	first, err := LoadOrCreateSnapshot(path, "http://192.0.2.10:8080")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("snapshot was not saved: %v", err)
	}
	second, err := LoadOrCreateSnapshot(path, "http://192.0.2.99:8080")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("existing published menu was replaced")
	}
}

func TestLoadOrCreateSnapshotRejectsCorruptSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "menu.ipxe")
	if err := os.WriteFile(path, []byte("not a menu"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateSnapshot(path, "http://192.0.2.10:8080"); err == nil {
		t.Fatal("expected corrupt snapshot to fail startup")
	}
}
