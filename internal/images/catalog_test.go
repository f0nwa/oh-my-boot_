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
		filepath.Join(root, ".omb-upload-123.part"): "partial-upload",
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
	if len(got.Images) != 1 || got.Images[0].Path != "nested/Ubuntu.ISO" || got.Images[0].Size != 8 || got.OtherBytes != 5 {
		t.Fatalf("unexpected catalog: %+v", got)
	}
}

func TestScanSkipsMacOSMetadataAndRecycleDirs(t *testing.T) {
	root := realTempDir(t)
	for _, dir := range []string{"#recycle", ".trash", ".hidden", "valid"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for path, content := range map[string]string{
		filepath.Join(root, "valid", "real.iso"):                "real-iso",
		filepath.Join(root, "valid", "._real.iso"):              "appledouble",
		filepath.Join(root, "valid", ".DS_Store"):               "ds-store",
		filepath.Join(root, "#recycle", "deleted.iso"):          "recycle-iso",
		filepath.Join(root, ".trash", "trashed.iso"):            "trash-iso",
		filepath.Join(root, ".hidden", "hidden.iso"):           "hidden-iso",
	} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Images) != 1 || got.Images[0].Path != "valid/real.iso" {
		t.Fatalf("expected only valid/real.iso, got: %+v", got.Images)
	}
	if got.OtherBytes != 0 {
		t.Fatalf("expected 0 otherBytes, got: %d", got.OtherBytes)
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
