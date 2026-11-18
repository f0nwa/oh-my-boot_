package images

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type memoryMigrationRepo struct {
	APIRepository
	storages map[int64]Storage
	images   map[string]Image
	deleted  []string
}

func (m *memoryMigrationRepo) Storage(_ context.Context, id int64) (Storage, error) {
	if s, ok := m.storages[id]; ok {
		return s, nil
	}
	return Storage{}, os.ErrNotExist
}

func (m *memoryMigrationRepo) RecordUpload(_ context.Context, img Image) error {
	if m.images == nil {
		m.images = make(map[string]Image)
	}
	m.images[filepath.Join(string(rune(img.StorageID)), img.Path)] = img
	return nil
}

func (m *memoryMigrationRepo) DeleteImage(_ context.Context, id int64, path string) error {
	m.deleted = append(m.deleted, path)
	delete(m.images, filepath.Join(string(rune(id)), path))
	return nil
}

func TestMigrationEngineCopyAndCommit(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	subfolder := filepath.Join(srcDir, "Windows", "11")
	if err := os.MkdirAll(subfolder, 0755); err != nil {
		t.Fatal(err)
	}

	testData := strings.Repeat("OMB-ISO-CONTENT-BLOCK-", 1024) // ~23 KB
	relPath := "Windows/11/win11.iso"
	if err := os.WriteFile(filepath.Join(srcDir, relPath), []byte(testData), 0644); err != nil {
		t.Fatal(err)
	}

	repo := &memoryMigrationRepo{
		storages: map[int64]Storage{
			1: {ID: 1, Kind: "local", Root: srcDir, State: "ready"},
			2: {ID: 2, Kind: "local", Root: dstDir, State: "ready", UploadsEnabled: true},
		},
	}

	engine := NewMigrationEngine(repo)

	job, err := engine.StartMigration(context.Background(), 1, relPath, srcDir, 2, dstDir)
	if err != nil {
		t.Fatalf("StartMigration failed: %v", err)
	}

	// Wait for copy to complete
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current, ok := engine.GetMigration(job.ID)
		if !ok {
			t.Fatal("job not found")
		}
		if current.State == "ready_to_rebind" {
			break
		}
		if current.State == "failed" {
			t.Fatalf("job failed: %s", current.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}

	got, ok := engine.GetMigration(job.ID)
	if !ok || got.State != "ready_to_rebind" {
		t.Fatalf("expected state ready_to_rebind, got %+v", got)
	}
	if got.CopiedBytes != int64(len(testData)) || got.TotalBytes != int64(len(testData)) {
		t.Fatalf("byte count mismatch: copied=%d, total=%d", got.CopiedBytes, got.TotalBytes)
	}

	// Verify destination file exists and matches
	dstContent, err := os.ReadFile(filepath.Join(dstDir, relPath))
	if err != nil {
		t.Fatalf("destination file missing: %v", err)
	}
	if string(dstContent) != testData {
		t.Fatal("destination file content mismatch")
	}

	// Verify source file still exists before commit
	if _, err := os.Stat(filepath.Join(srcDir, relPath)); err != nil {
		t.Fatalf("source file should still exist before commit: %v", err)
	}

	// Commit migration (delete source)
	if err := engine.CommitDeleteSource(context.Background(), job.ID); err != nil {
		t.Fatalf("CommitDeleteSource failed: %v", err)
	}

	// Now source file must be gone
	if _, err := os.Stat(filepath.Join(srcDir, relPath)); !os.IsNotExist(err) {
		t.Fatalf("source file should be deleted after commit, got err=%v", err)
	}

	gotAfter, _ := engine.GetMigration(job.ID)
	if gotAfter.State != "completed" {
		t.Fatalf("expected state completed, got %s", gotAfter.State)
	}
}

func TestMigrationEngineCancelCleansUp(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	testData := strings.Repeat("A", 1024*1024)
	relPath := "test.iso"
	if err := os.WriteFile(filepath.Join(srcDir, relPath), []byte(testData), 0644); err != nil {
		t.Fatal(err)
	}

	repo := &memoryMigrationRepo{
		storages: map[int64]Storage{
			1: {ID: 1, Kind: "local", Root: srcDir, State: "ready"},
			2: {ID: 2, Kind: "local", Root: dstDir, State: "ready", UploadsEnabled: true},
		},
	}

	engine := NewMigrationEngine(repo)
	job, err := engine.StartMigration(context.Background(), 1, relPath, srcDir, 2, dstDir)
	if err != nil {
		t.Fatal(err)
	}

	if err := engine.CancelMigration(job.ID); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)

	got, _ := engine.GetMigration(job.ID)
	if got.State != "canceled" {
		t.Fatalf("expected canceled, got %s", got.State)
	}

	// Verify no .part or final files in dstDir
	files, _ := os.ReadDir(dstDir)
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".part") || f.Name() == relPath {
			t.Fatalf("leftover file found: %s", f.Name())
		}
	}
}

type deleteNoRowsMigrationRepo struct {
	memoryMigrationRepo
}

func (r *deleteNoRowsMigrationRepo) DeleteImage(_ context.Context, _ int64, _ string) error {
	return pgx.ErrNoRows
}

func TestCommitDeleteSourceIsIdempotentWhenCatalogRowAlreadyDeleted(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	relPath := "retry.iso"
	if err := os.WriteFile(filepath.Join(srcDir, relPath), []byte("ISO-DATA"), 0644); err != nil {
		t.Fatal(err)
	}

	repo := &deleteNoRowsMigrationRepo{
		memoryMigrationRepo: memoryMigrationRepo{
			storages: map[int64]Storage{
				1: {ID: 1, Kind: "local", Root: srcDir, State: "ready"},
				2: {ID: 2, Kind: "local", Root: dstDir, State: "ready", UploadsEnabled: true},
			},
		},
	}
	engine := NewMigrationEngine(repo)
	job, err := engine.StartMigration(context.Background(), 1, relPath, srcDir, 2, dstDir)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := engine.GetMigration(job.ID)
		if got.State == "ready_to_rebind" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := engine.CommitDeleteSource(context.Background(), job.ID); err != nil {
		t.Fatalf("CommitDeleteSource should succeed when catalog row already deleted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(srcDir, relPath)); !os.IsNotExist(err) {
		t.Fatalf("source file should be deleted, stat err=%v", err)
	}
	got, _ := engine.GetMigration(job.ID)
	if got.State != "completed" {
		t.Fatalf("expected completed state, got %s", got.State)
	}
}

func TestListMigrationsSortedByStartedAt(t *testing.T) {
	engine := NewMigrationEngine(&memoryMigrationRepo{})
	t0 := time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)
	engine.jobs["m3"] = &MigrationJob{ID: "m3", StartedAt: t0.Add(3 * time.Minute)}
	engine.jobs["m1"] = &MigrationJob{ID: "m1", StartedAt: t0.Add(1 * time.Minute)}
	engine.jobs["m2"] = &MigrationJob{ID: "m2", StartedAt: t0.Add(2 * time.Minute)}

	for iter := 0; iter < 10; iter++ {
		list := engine.ListMigrations()
		if len(list) != 3 || list[0].ID != "m1" || list[1].ID != "m2" || list[2].ID != "m3" {
			t.Fatalf("expected deterministic StartedAt order [m1 m2 m3], got %v %v %v", list[0].ID, list[1].ID, list[2].ID)
		}
	}
}
