package agent

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omb/internal/drivers"
)

func buildTestDriverZIP(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDriverExportSinglePreservesCanonicalHashAndMultiFolders(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mgr := &DriverManager{Root: root}
	handler := HandlerWithDriverManager(nil, nil, nil, mgr)

	zipA := buildTestDriverZIP(t, map[string]string{
		"DellPack/Net/e1d68x64.inf": "[Version]\nClass=Net\n[Models.NTamd64]\nCopyFiles=e1d68x64.sys\n",
		"DellPack/Net/e1d68x64.sys": "BINARY-SYS-DATA-DELL",
		"DellPack/Net/e1d68x64.cat": "BINARY-CAT-DATA-DELL",
		"DellPack/VMD/iaStorVD.inf": "[Version]\nClass=SCSIAdapter\n[Models.NTamd64]\nCopyFiles=iaStorVD.sys\n",
		"DellPack/VMD/iaStorVD.sys": "BINARY-SYS-DATA-VMD",
	})
	invA, err := mgr.Import(context.Background(), bytes.NewReader(zipA), int64(len(zipA)))
	if err != nil {
		t.Fatalf("Import A: %v", err)
	}

	zipB := buildTestDriverZIP(t, map[string]string{
		"rtux64w10.inf": "[Version]\nClass=Net\n[Models.NTamd64]\nCopyFiles=rtux64w10.sys\n",
		"rtux64w10.sys": "BINARY-SYS-DATA-REALTEK",
	})
	invB, err := mgr.Import(context.Background(), bytes.NewReader(zipB), int64(len(zipB)))
	if err != nil {
		t.Fatalf("Import B: %v", err)
	}

	// 1. Single package export over HTTP produces a ZIP whose CanonicalHash equals invA.SHA256.
	reqBody, _ := json.Marshal(map[string]any{
		"items": []DriverExportItem{
			{SHA256: invA.SHA256, Folder: "Dell Latitude 5440 — LAN-" + invA.SHA256[:8]},
		},
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/winpe-drivers/export", bytes.NewReader(reqBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("single export status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("Content-Type = %q, want application/zip", ct)
	}
	reimported, err := drivers.InspectZIP(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("InspectZIP on exported archive: %v", err)
	}
	if reimported.SHA256 != invA.SHA256 {
		t.Fatalf("reimported SHA256 = %s, want %s", reimported.SHA256, invA.SHA256)
	}

	// 2. Multi-package export places each package into its own clean top-level folder.
	multiBody, _ := json.Marshal(map[string]any{
		"items": []DriverExportItem{
			{SHA256: invA.SHA256, Folder: "Dell-" + invA.SHA256[:8]},
			{SHA256: invB.SHA256, Folder: "driver-" + invB.SHA256[:8]},
		},
	})
	recMulti := httptest.NewRecorder()
	handler.ServeHTTP(recMulti, httptest.NewRequest(http.MethodPost, "/winpe-drivers/export", bytes.NewReader(multiBody)))
	if recMulti.Code != http.StatusOK {
		t.Fatalf("multi export status = %d, body = %s", recMulti.Code, recMulti.Body.String())
	}
	zr, err := zip.NewReader(bytes.NewReader(recMulti.Body.Bytes()), int64(recMulti.Body.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	gotNames := map[string]bool{}
	for _, f := range zr.File {
		gotNames[f.Name] = true
	}
	for _, want := range []string{
		"Dell-" + invA.SHA256[:8] + "/Net/e1d68x64.inf",
		"Dell-" + invA.SHA256[:8] + "/Net/e1d68x64.sys",
		"Dell-" + invA.SHA256[:8] + "/Net/e1d68x64.cat",
		"Dell-" + invA.SHA256[:8] + "/VMD/iaStorVD.inf",
		"Dell-" + invA.SHA256[:8] + "/VMD/iaStorVD.sys",
		"driver-" + invB.SHA256[:8] + "/rtux64w10.inf",
		"driver-" + invB.SHA256[:8] + "/rtux64w10.sys",
	} {
		if !gotNames[want] {
			t.Fatalf("missing %q in multi ZIP; got %v", want, gotNames)
		}
	}
	if len(gotNames) != 7 {
		t.Fatalf("unexpected extra entries in multi ZIP: %v", gotNames)
	}
}

func TestDriverExportValidationAndBusyRemoveGuard(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mgr := &DriverManager{Root: root}
	handler := HandlerWithDriverManager(nil, nil, nil, mgr)

	zipA := buildTestDriverZIP(t, map[string]string{
		"net.inf": "[Version]\nClass=Net\n[Models.NTamd64]\n",
	})
	invA, err := mgr.Import(context.Background(), bytes.NewReader(zipA), int64(len(zipA)))
	if err != nil {
		t.Fatal(err)
	}

	// Missing package returns 404 before writing body.
	missingHash := strings.Repeat("f", 64)
	missingBody, _ := json.Marshal(map[string]any{
		"items": []DriverExportItem{{SHA256: missingHash, Folder: "driver-ffffffff"}},
	})
	rec404 := httptest.NewRecorder()
	handler.ServeHTTP(rec404, httptest.NewRequest(http.MethodPost, "/winpe-drivers/export", bytes.NewReader(missingBody)))
	if rec404.Code != http.StatusNotFound {
		t.Fatalf("missing package status = %d, want 404", rec404.Code)
	}

	// Invalid folder names or duplicate folders return 400.
	for _, badItems := range [][]DriverExportItem{
		{},
		{{SHA256: "bad", Folder: "ok"}},
		{{SHA256: invA.SHA256, Folder: "../escape"}},
		{{SHA256: invA.SHA256, Folder: "nested/sub"}},
		{{SHA256: invA.SHA256, Folder: "trailing."}},
		{{SHA256: invA.SHA256, Folder: "dup"}, {SHA256: invA.SHA256, Folder: "DUP"}},
	} {
		raw, _ := json.Marshal(map[string]any{"items": badItems})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/winpe-drivers/export", bytes.NewReader(raw)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("items=%+v status=%d, want 400", badItems, rec.Code)
		}
	}

	// Active export blocks RemovePackage with 409 Conflict until closed.
	exp, err := mgr.PrepareExport(context.Background(), []DriverExportItem{
		{SHA256: invA.SHA256, Folder: "driver-" + invA.SHA256[:8]},
	})
	if err != nil {
		t.Fatalf("PrepareExport: %v", err)
	}
	delBody, _ := json.Marshal(map[string]string{"sha256": invA.SHA256})
	recBusy := httptest.NewRecorder()
	handler.ServeHTTP(recBusy, httptest.NewRequest(http.MethodPost, "/winpe-drivers/remove-package", bytes.NewReader(delBody)))
	if recBusy.Code != http.StatusConflict {
		t.Fatalf("RemovePackage during export status = %d, want 409", recBusy.Code)
	}
	if _, err := os.Stat(filepath.Join(root, invA.SHA256)); err != nil {
		t.Fatalf("package directory removed while busy: %v", err)
	}

	var streamed bytes.Buffer
	if err := exp.Stream(context.Background(), &streamed); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	exp.Close()

	recDel := httptest.NewRecorder()
	handler.ServeHTTP(recDel, httptest.NewRequest(http.MethodPost, "/winpe-drivers/remove-package", bytes.NewReader(delBody)))
	if recDel.Code != http.StatusNoContent {
		t.Fatalf("RemovePackage after export close status = %d, want 204", recDel.Code)
	}
}
