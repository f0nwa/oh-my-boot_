package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadProfileRequestBindsConfigToInstanceID(t *testing.T) {
	file := filepath.Join(t.TempDir(), "17.json")
	if err := os.WriteFile(file, []byte(`{"id":17,"storage_root":"/mnt/pxe-platform-smb/key7","relative_path":"win10.iso","size":3,"modified":"2026-10-16T15:00:00Z"}`), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := readProfileRequest(file, 17)
	if err != nil || got.ID != 17 || got.RelativePath != "win10.iso" {
		t.Fatalf("valid config rejected: %+v %v", got, err)
	}
	if _, err := readProfileRequest(file, 18); err == nil {
		t.Fatal("instance 18 accepted profile 17 config")
	}
	if err := os.WriteFile(file, []byte(`{"id":17,"unknown":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readProfileRequest(file, 17); err == nil {
		t.Fatal("unknown config field accepted")
	}
}
