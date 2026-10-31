package agent

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"omb/internal/drivers"
)

func driverArchive(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for name, data := range map[string]string{"vendor/net.inf": "[Version]\nClass=Net\n[Models.NTamd64]\n", "vendor/net.sys": "driver"} {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestDriverImportAndCheckImmutable(t *testing.T) {
	manager := &DriverManager{Root: t.TempDir()}
	archive := driverArchive(t)
	got, err := manager.Import(context.Background(), bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.INFs) != 1 || got.INFs[0].Path != "net.inf" {
		t.Fatalf("wrong inventory: %+v", got)
	}
	if err := manager.CheckPackage(context.Background(), got.SHA256); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Import(context.Background(), bytes.NewReader(archive), int64(len(archive))); err != nil {
		t.Fatalf("idempotent import: %v", err)
	}
	file := filepath.Join(manager.Root, got.SHA256, "files", "net.sys")
	if err := os.WriteFile(file, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := manager.CheckPackage(context.Background(), got.SHA256); err == nil {
		t.Fatal("accepted modified package")
	}
	if _, err := manager.Import(context.Background(), bytes.NewReader(archive), int64(len(archive))); err == nil {
		t.Fatal("overwrote modified immutable package")
	}
}

func TestDriverImportRejectsOversizeBeforeReading(t *testing.T) {
	manager := &DriverManager{Root: t.TempDir()}
	if _, err := manager.Import(context.Background(), bytes.NewReader(nil), drivers.MaxArchiveBytes+1); err == nil {
		t.Fatal("accepted excessive size")
	}
}

func TestDriverCheckRejectsUnexpectedFile(t *testing.T) {
	manager := &DriverManager{Root: t.TempDir()}
	archive := driverArchive(t)
	got, err := manager.Import(context.Background(), bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(manager.Root, got.SHA256, "files", "unexpected.sys")
	if err := os.WriteFile(extra, []byte("extra"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := manager.CheckPackage(context.Background(), got.SHA256); err == nil {
		t.Fatal("accepted unexpected package file")
	}
}

func TestDriverImportHTTPAndCheck(t *testing.T) {
	manager := &DriverManager{Root: t.TempDir()}
	handler := HandlerWithDriverManager(nil, nil, nil, manager)
	archive := driverArchive(t)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/winpe-drivers/import", bytes.NewReader(archive)))
	if res.Code != http.StatusCreated {
		t.Fatalf("import: %d %s", res.Code, res.Body.String())
	}
	var inventory drivers.Inventory
	if err := json.Unmarshal(res.Body.Bytes(), &inventory); err != nil {
		t.Fatal(err)
	}
	request, err := json.Marshal(map[string]string{"sha256": inventory.SHA256})
	if err != nil {
		t.Fatal(err)
	}
	check := httptest.NewRecorder()
	handler.ServeHTTP(check, httptest.NewRequest(http.MethodPost, "/winpe-drivers/check", bytes.NewReader(request)))
	if check.Code != http.StatusNoContent {
		t.Fatalf("check: %d %s", check.Code, check.Body.String())
	}
}
