package images

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReceiveISOIntoExistingFolderAndRejectsReplacement(t *testing.T) {
	root := realTempDir(t)
	if err := os.MkdirAll(filepath.Join(root, "Windows", "Test"), 0755); err != nil {
		t.Fatal(err)
	}
	got, err := ReceiveISO(context.Background(), root, "Windows/Test", "installer.iso", strings.NewReader("iso-data"), int64(len("iso-data")))
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != "Windows/Test/installer.iso" || got.Size != 8 || got.SHA256 != "d4fdbb796c321761669fd0ca447caa175f74addbcff636d98016ab8f4f36ab91" {
		t.Fatalf("unexpected result: %+v", got)
	}
	content, err := os.ReadFile(filepath.Join(root, "Windows", "Test", "installer.iso"))
	if err != nil || string(content) != "iso-data" {
		t.Fatalf("uploaded content=%q err=%v", content, err)
	}
	if _, err := ReceiveISO(context.Background(), root, "Windows/Test", "installer.iso", strings.NewReader("replacement"), 11); err == nil {
		t.Fatal("existing ISO was replaced")
	}
	content, err = os.ReadFile(filepath.Join(root, "Windows", "Test", "installer.iso"))
	if err != nil || string(content) != "iso-data" {
		t.Fatalf("existing content changed: %q err=%v", content, err)
	}
}

func TestReceiveISORejectsTraversalAndSymlinkedFolder(t *testing.T) {
	root := realTempDir(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	for _, folder := range []string{"../outside", "link", "missing", "/absolute", `back\\slash`} {
		if _, err := ReceiveISO(context.Background(), root, folder, "test.iso", strings.NewReader("a"), 1); err == nil {
			t.Errorf("accepted folder %q", folder)
		}
	}
	for _, name := range []string{"../test.iso", ".hidden.iso", "bad.txt", "bad\\name.iso", "trail .iso ", ""} {
		if _, err := ReceiveISO(context.Background(), root, "", name, strings.NewReader("a"), 1); err == nil {
			t.Errorf("accepted name %q", name)
		}
	}
}

func TestReceiveISODeletesIncompleteFile(t *testing.T) {
	root := realTempDir(t)
	if _, err := ReceiveISO(context.Background(), root, "", "short.iso", strings.NewReader("short"), 10); err == nil {
		t.Fatal("accepted short stream")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("partial upload remains: %v err=%v", entries, err)
	}
}

func TestListFoldersSkipsSymlinksAndIncludesEmptyFolders(t *testing.T) {
	root := realTempDir(t)
	if err := os.MkdirAll(filepath.Join(root, "Windows", "Empty"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	folders, err := ListFolders(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(folders, ",") != ",Windows,Windows/Empty" {
		t.Fatalf("folders=%q", folders)
	}
}

func TestReceiveISOSetsReadableMode(t *testing.T) {
	root := realTempDir(t)
	got, err := ReceiveISO(context.Background(), root, "", "readable.iso", strings.NewReader("iso-content"), int64(len("iso-content")))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, got.Path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0644 {
		t.Fatalf("expected perm 0644, got %#o", perm)
	}
}
