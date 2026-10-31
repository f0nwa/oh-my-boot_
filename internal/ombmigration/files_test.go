package ombmigration

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCopyTreePreservesSetgidForRegeneratedAnswerOwnership(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "copy")
	if err := os.Mkdir(source, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(source, 0750|os.ModeSetgid); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	if before.Mode()&os.ModeSetgid == 0 {
		t.Skip("filesystem does not support setgid")
	}
	if err = copyTree(source, target); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if after.Mode()&os.ModeSetgid == 0 {
		t.Fatal("copied answer root lost setgid; rebuilt XML would inherit wrong group")
	}
	record, err := recordFile(root, "/copy")
	if err != nil {
		t.Fatal(err)
	}
	if os.FileMode(record.Mode)&os.ModeSetgid == 0 {
		t.Fatal("tree fingerprint ignores setgid ownership policy")
	}
}

func TestISOModificationIdentityUsesCatalogMicrosecondPrecision(t *testing.T) {
	actual := time.Unix(1756188573, 378684300)
	stored := time.Unix(1756188573, 378684000)
	if !sameCatalogTime(actual, stored) {
		t.Fatal("fractional CIFS nanoseconds differ from PostgreSQL microsecond precision")
	}
	if sameCatalogTime(actual.Add(time.Microsecond), stored) {
		t.Fatal("accepted a changed catalog timestamp")
	}
}
