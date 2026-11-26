package images

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateStorageChecksRootWithoutScanningChildren(t *testing.T) {
	root := realTempDir(t)
	unreadable := filepath.Join(root, "unreadable")
	if err := os.Mkdir(unreadable, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0700) })
	if _, err := ValidateStorage("Local", "local", root); err != nil {
		t.Fatalf("a valid root should be registered before a full scan: %v", err)
	}
	link := filepath.Join(realTempDir(t), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateStorage("Link", "local", link); err == nil {
		t.Fatal("expected symlinked root rejection")
	}
	child := filepath.Join(link, "child")
	if err := os.Mkdir(filepath.Join(root, "child"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateStorage("Link child", "local", child); err == nil {
		t.Fatal("expected symlinked ancestor rejection")
	}
}

func TestValidateStorageRejectsRelativePathAndInvalidKind(t *testing.T) {
	if _, err := ValidateStorage("Local", "local", "relative/path"); err == nil {
		t.Fatal("relative path accepted")
	}
	if _, err := ValidateStorage("Local", "smb", t.TempDir()); err == nil {
		t.Fatal("unknown kind accepted")
	}
}

func TestImageKindFromPath(t *testing.T) {
	cases := []struct {
		name string
		kind string
		ok   bool
	}{
		{"win11.iso", "iso", true},
		{"Windows/Server.ISO", "iso", true},
		{"boot.wim", "wim", true},
		{"sources/BOOT.WIM", "wim", true},
		{"install.esd", "esd", true},
		{"sources/INSTALL.ESD", "esd", true},
		{"readme.txt", "", false},
		{"archive.tar.gz", "", false},
		{"noext", "", false},
	}
	for _, tc := range cases {
		got, ok := ImageKindFromPath(tc.name)
		if got != tc.kind || ok != tc.ok {
			t.Errorf("ImageKindFromPath(%q) = (%q, %v), want (%q, %v)", tc.name, got, ok, tc.kind, tc.ok)
		}
	}
}
