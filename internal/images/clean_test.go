package images

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanEmptyParentDirs(t *testing.T) {
	root := realTempDir(t)
	dir := filepath.Join(root, "folderA", "folderB")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "test.iso")
	if err := os.WriteFile(target, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	// Delete file first
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}

	if err := cleanEmptyParentDirs(target, root); err != nil {
		t.Fatalf("cleanEmptyParentDirs failed: %v", err)
	}

	// folderA and folderB should be gone
	if _, err := os.Stat(filepath.Join(root, "folderA")); !os.IsNotExist(err) {
		t.Fatalf("expected folderA to be removed, err: %v", err)
	}
	// root must still exist
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("expected root to remain, err: %v", err)
	}
}

func TestCleanEmptyParentDirsStopsAtNonEmpty(t *testing.T) {
	root := realTempDir(t)
	dir := filepath.Join(root, "folderA", "folderB")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	target1 := filepath.Join(dir, "test1.iso")
	if err := os.WriteFile(target1, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, "folderA", "keep.iso")
	if err := os.WriteFile(other, []byte("data2"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(target1); err != nil {
		t.Fatal(err)
	}

	if err := cleanEmptyParentDirs(target1, root); err != nil {
		t.Fatalf("cleanEmptyParentDirs failed: %v", err)
	}

	// folderB should be removed
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("expected folderB to be removed, err: %v", err)
	}
	// folderA must still exist because keep.iso is there
	if _, err := os.Stat(filepath.Join(root, "folderA")); err != nil {
		t.Fatalf("expected folderA to remain, err: %v", err)
	}
}

func TestCleanEmptyParentDirsCleansVetoFiles(t *testing.T) {
	root := realTempDir(t)
	dir := filepath.Join(root, "folderA", "folderB")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "test.iso")
	if err := os.WriteFile(target, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	// Add veto files left by macOS
	if err := os.WriteFile(filepath.Join(dir, ".DS_Store"), []byte("ds"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "._test.iso"), []byte("appledouble"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}

	if err := cleanEmptyParentDirs(target, root); err != nil {
		t.Fatalf("cleanEmptyParentDirs failed: %v", err)
	}

	// Entire folderA should be removed despite .DS_Store and ._test.iso
	if _, err := os.Stat(filepath.Join(root, "folderA")); !os.IsNotExist(err) {
		t.Fatalf("expected folderA to be removed including veto files, err: %v", err)
	}
}

func TestEnsureDirMode(t *testing.T) {
	root := realTempDir(t)
	dir := filepath.Join(root, "nested", "target")
	if err := ensureDirMode(dir, root, 0770|os.ModeSetgid); err != nil {
		t.Fatalf("ensureDirMode failed: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatal("expected dir to be a directory")
	}
	// Check permissions on dir: on unix it should match 0770 & mode bits
	perm := info.Mode().Perm()
	if perm&0070 != 0070 {
		t.Fatalf("expected group rwx (070), got perm: %o", perm)
	}
}
