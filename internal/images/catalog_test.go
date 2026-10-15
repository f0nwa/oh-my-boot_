package images

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanFindsISOsAndSkipsSymlinks(t *testing.T) {
	root := realTempDir(t)
	other := realTempDir(t)
	if err := os.Mkdir(filepath.Join(root, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		filepath.Join(root, "nested", "Ubuntu.ISO"): "iso-data",
		filepath.Join(root, "notes.txt"):            "notes",
		filepath.Join(other, "secret.iso"):          "outside",
	} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(other, "secret.iso"), filepath.Join(root, "linked.iso")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(root, "linked-dir")); err != nil {
		t.Fatal(err)
	}

	got, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != "nested/Ubuntu.ISO" || got[0].Size != 8 {
		t.Fatalf("unexpected catalog: %+v", got)
	}
}

func TestScanRejectsSymlinkedRoot(t *testing.T) {
	realRoot := realTempDir(t)
	link := filepath.Join(realTempDir(t), "linked-root")
	if err := os.Symlink(realRoot, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Scan(link); err == nil {
		t.Fatal("expected symlinked root to be rejected")
	}
	child := filepath.Join(realRoot, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Scan(filepath.Join(link, "child")); err == nil {
		t.Fatal("expected symlinked ancestor to be rejected")
	}
}
