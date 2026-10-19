package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveDriverConfigPreservesSharedWIM(t *testing.T) {
	base := t.TempDir()
	configRoot, wimRoot := filepath.Join(base, "configs"), filepath.Join(base, "wims")
	for _, root := range []string{configRoot, wimRoot} {
		if err := os.Mkdir(root, 0755); err != nil {
			t.Fatal(err)
		}
	}
	key := strings.Repeat("a", 64)
	if err := os.Mkdir(filepath.Join(configRoot, "3"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(wimRoot, key), 0755); err != nil {
		t.Fatal(err)
	}
	m := &DriverManager{ConfigRoot: configRoot, WIMRoot: wimRoot}
	if err := m.RemoveConfig(context.Background(), 3, key, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wimRoot, key)); err != nil {
		t.Fatalf("shared WIM removed: %v", err)
	}
	if err := m.RemoveConfig(context.Background(), 3, key, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(wimRoot, key)); !os.IsNotExist(err) {
		t.Fatalf("WIM remains: %v", err)
	}
}

func TestRemoveManagedDirectoryRejectsSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "3")); err != nil {
		t.Fatal(err)
	}
	if err := removeManagedDirectory(root, "3"); err == nil {
		t.Fatal("accepted symlink child")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal(err)
	}
}

func TestAgentRemoveDriverConfigEndpointValidatesAndRemoves(t *testing.T) {
	configRoot, wimRoot := t.TempDir(), t.TempDir()
	key := strings.Repeat("a", 64)
	if err := os.Mkdir(filepath.Join(configRoot, "3"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(wimRoot, key), 0755); err != nil {
		t.Fatal(err)
	}
	h := HandlerWithDriverManager(nil, nil, nil, &DriverManager{ConfigRoot: configRoot, WIMRoot: wimRoot})
	bad := httptest.NewRecorder()
	h.ServeHTTP(bad, httptest.NewRequest(http.MethodPost, "/winpe-drivers/remove-config", strings.NewReader(`{"id":3,"wim_key":"../unsafe","remove_wim":true}`)))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bad request status=%d", bad.Code)
	}
	good := httptest.NewRecorder()
	h.ServeHTTP(good, httptest.NewRequest(http.MethodPost, "/winpe-drivers/remove-config", strings.NewReader(`{"id":3,"wim_key":"`+key+`","remove_wim":true}`)))
	if good.Code != http.StatusNoContent {
		t.Fatalf("cleanup status=%d: %s", good.Code, good.Body.String())
	}
	if _, err := os.Lstat(filepath.Join(wimRoot, key)); !os.IsNotExist(err) {
		t.Fatalf("WIM remains: %v", err)
	}
}
