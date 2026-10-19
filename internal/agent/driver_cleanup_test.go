package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveDriverConfigPreservesSharedWIM(t *testing.T) {
	base := canonicalTestDir(t)
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
	root, outside := canonicalTestDir(t), canonicalTestDir(t)
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
	configRoot, wimRoot := canonicalTestDir(t), canonicalTestDir(t)
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

func TestDriverConfigWIMKeyReadsManifestAfterDatabaseWriteFailure(t *testing.T) {
	root := canonicalTestDir(t)
	dir := filepath.Join(root, "7")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("a", 64)
	content, err := json.Marshal(driverConfigManifest{Request: DriverBuildRequest{ConfigID: 7}, Result: DriverBuildResult{WIMKey: key}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "build.json"), content, 0644); err != nil {
		t.Fatal(err)
	}
	m := &DriverManager{ConfigRoot: root}
	if got, err := m.ConfigWIMKey(7); err != nil || got != key {
		t.Fatalf("manifest key=%q err=%v", got, err)
	}
	if got, err := m.ConfigWIMKey(8); err != nil || got != "" {
		t.Fatalf("missing config key=%q err=%v", got, err)
	}
	if err := os.Mkdir(filepath.Join(root, "9"), 0755); err != nil {
		t.Fatal(err)
	}
	if got, err := m.ConfigWIMKey(9); err != nil || got != "" {
		t.Fatalf("partial config key=%q err=%v", got, err)
	}
}
