package images

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMigrationHTTPHandlers(t *testing.T) {
	srcDir := realTempDir(t)
	dstDir := realTempDir(t)

	testContent := "ISO-DATA-FOR-HTTP-TEST"
	if err := os.WriteFile(filepath.Join(srcDir, "test.iso"), []byte(testContent), 0644); err != nil {
		t.Fatal(err)
	}

	repo := &memoryMigrationRepo{
		storages: map[int64]Storage{
			1: {ID: 1, Kind: "local", Root: srcDir, State: "ready"},
			2: {ID: 2, Kind: "local", Root: dstDir, State: "ready", UploadsEnabled: true},
		},
	}

	server := NewServer(repo)
	server.writable = func(string) bool { return true }
	handler := server.Handler()

	// 1. POST /images/migrate
	reqBody, _ := json.Marshal(MigrateRequest{
		SourceStorageID: 1,
		Path:            "test.iso",
		TargetStorageID: 2,
	})
	req := httptest.NewRequest(http.MethodPost, "/images/migrate", bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", rec.Code, rec.Body.String())
	}

	var job MigrationJob
	if err := json.NewDecoder(rec.Body).Decode(&job); err != nil {
		t.Fatal(err)
	}
	if job.ID == "" || job.State != "copying" {
		t.Fatalf("unexpected job response: %+v", job)
	}

	// 2. GET /images/migrations
	reqList := httptest.NewRequest(http.MethodGet, "/images/migrations", nil)
	recList := httptest.NewRecorder()
	handler.ServeHTTP(recList, reqList)

	if recList.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", recList.Code)
	}
	var jobs []*MigrationJob
	if err := json.NewDecoder(recList.Body).Decode(&jobs); err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].ID != job.ID {
		t.Fatalf("unexpected list response: %+v", jobs)
	}

	// Wait for copy to finish
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		reqGet := httptest.NewRequest(http.MethodGet, "/images/migrations/"+job.ID, nil)
		recGet := httptest.NewRecorder()
		handler.ServeHTTP(recGet, reqGet)
		if recGet.Code == http.StatusOK {
			var current MigrationJob
			_ = json.NewDecoder(recGet.Body).Decode(&current)
			if current.State == "ready_to_rebind" {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 3. POST /images/migrations/{id}/commit-delete-source
	reqCommit := httptest.NewRequest(http.MethodPost, "/images/migrations/"+job.ID+"/commit-delete-source", nil)
	recCommit := httptest.NewRecorder()
	handler.ServeHTTP(recCommit, reqCommit)

	if recCommit.Code != http.StatusNoContent {
		t.Fatalf("expected 204 NoContent, got %d: %s", recCommit.Code, recCommit.Body.String())
	}

	// Verify source deleted
	if _, err := os.Stat(filepath.Join(srcDir, "test.iso")); !os.IsNotExist(err) {
		t.Fatalf("source file should be removed, err=%v", err)
	}
	// Verify destination present
	if _, err := os.Stat(filepath.Join(dstDir, "test.iso")); err != nil {
		t.Fatalf("destination file should exist, err=%v", err)
	}
}

func TestMigrationHTTPStartRejectsReadOnlySMBSourceBeforeCopy(t *testing.T) {
	srcRO := realTempDir(t)
	srcUpload := realTempDir(t)
	dstDir := realTempDir(t)
	if err := os.WriteFile(filepath.Join(srcRO, "smb.iso"), []byte("ISO"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcUpload, "smb.iso"), []byte("ISO"), 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(srcUpload, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(srcUpload, 0755) })

	key := "0123456789abcdef0123456789abcdef"
	repo := &memoryMigrationRepo{
		storages: map[int64]Storage{
			1: {ID: 1, Kind: "smb", Key: key, Server: "nas.example.com", Share: "iso", Root: srcRO, State: "ready"},
			2: {ID: 2, Kind: "local", Root: dstDir, State: "ready", UploadsEnabled: true},
		},
	}
	server := NewServer(repo)
	server.writable = func(string) bool { return true }
	server.mountIdentity = func(string) (string, bool) { return "smb-mount", true }
	server.uploadRoot = func(string) string { return srcUpload }

	reqBody, _ := json.Marshal(MigrateRequest{
		SourceStorageID: 1,
		Path:            "smb.iso",
		TargetStorageID: 2,
	})
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/images/migrate", bytes.NewReader(reqBody)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for read-only SMB source, got %d: %s", rec.Code, rec.Body.String())
	}
	if jobs := server.migrations.ListMigrations(); len(jobs) != 0 {
		t.Fatalf("expected no migration job created for read-only SMB source, got %+v", jobs)
	}
}
