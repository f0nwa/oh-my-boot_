package images

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeHashRepo struct {
	mu         sync.Mutex
	settings   CatalogSettings
	candidates []HashIndexCandidate
	saved      map[string]string
	total      int
}

func newFakeHashRepo(settings CatalogSettings, cands []HashIndexCandidate) *fakeHashRepo {
	return &fakeHashRepo{
		settings:   settings,
		candidates: append([]HashIndexCandidate(nil), cands...),
		saved:      map[string]string{},
		total:      len(cands),
	}
}

func (f *fakeHashRepo) Settings(context.Context) (CatalogSettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.settings, nil
}

func (f *fakeHashRepo) HashIndexCounts(context.Context) (HashIndexCounts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	indexed := len(f.saved)
	pending := f.total - indexed
	if pending < 0 {
		pending = 0
	}
	return HashIndexCounts{Total: f.total, Indexed: indexed, Pending: pending}, nil
}

func (f *fakeHashRepo) HashIndexCandidates(context.Context) ([]HashIndexCandidate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []HashIndexCandidate
	for _, c := range f.candidates {
		key := filepath.ToSlash(c.Path)
		if _, ok := f.saved[key]; !ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeHashRepo) SaveImageSHA256(_ context.Context, _ int64, path string, _ int64, _ time.Time, shaHex string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved[filepath.ToSlash(path)] = shaHex
	return nil
}

func TestHashIndexerSequentialQueueAndBusySkip(t *testing.T) {
	root1 := t.TempDir()
	root2 := t.TempDir()

	dataA := []byte("small-boot-wim-payload")
	dataB := []byte(strings.Repeat("iso-payload-block-", 256))
	fileA := filepath.Join(root1, "boot.wim")
	fileB := filepath.Join(root2, "win11.iso")
	if err := os.WriteFile(fileA, dataA, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, dataB, 0644); err != nil {
		t.Fatal(err)
	}
	statA, _ := os.Lstat(fileA)
	statB, _ := os.Lstat(fileB)

	wantA := sha256.Sum256(dataA)
	wantB := sha256.Sum256(dataB)

	repo := newFakeHashRepo(CatalogSettings{
		RescanEnabled:         true,
		RescanIntervalMinutes: 30,
		HashIndexEnabled:      true,
		HashIndexRateMBps:     0,
	}, []HashIndexCandidate{
		{StorageID: 1, StorageName: "Local", StorageKind: "local", StorageRoot: root1, Path: "boot.wim", Size: statA.Size(), Modified: statA.ModTime()},
		{StorageID: 2, StorageName: "NAS", StorageKind: "smb", StorageRoot: root2, Path: "win11.iso", Size: statB.Size(), Modified: statB.ModTime()},
	})

	busyStorage := int64(1)
	indexer := NewHashIndexer(repo, func(id int64) bool { return id == busyStorage })
	indexer.InterFilePause = 0

	// Storage 1 is busy, so storage 2 should be picked first.
	didWork, paused := indexer.step(context.Background())
	if !didWork || paused {
		t.Fatalf("step 1: got didWork=%v paused=%v, want true/false", didWork, paused)
	}
	if got := repo.saved["win11.iso"]; got != hex.EncodeToString(wantB[:]) {
		t.Fatalf("win11.iso sha256 = %q, want %x", got, wantB)
	}
	if _, exists := repo.saved["boot.wim"]; exists {
		t.Fatalf("boot.wim should not be hashed while storage 1 is busy")
	}

	// Still busy -> paused.
	didWork, paused = indexer.step(context.Background())
	if didWork || !paused {
		t.Fatalf("step 2: got didWork=%v paused=%v, want false/true", didWork, paused)
	}
	if st := indexer.Status(context.Background()); st.State != "paused" || st.PendingCount != 1 {
		t.Fatalf("status while paused: %+v", st)
	}

	// Release storage 1 -> boot.wim gets hashed.
	busyStorage = 0
	didWork, paused = indexer.step(context.Background())
	if !didWork || paused {
		t.Fatalf("step 3: got didWork=%v paused=%v, want true/false", didWork, paused)
	}
	if got := repo.saved["boot.wim"]; got != hex.EncodeToString(wantA[:]) {
		t.Fatalf("boot.wim sha256 = %q, want %x", got, wantA)
	}

	st := indexer.Status(context.Background())
	if st.State != "idle" || st.IndexedCount != 2 || st.PendingCount != 0 || st.TotalCount != 2 {
		t.Fatalf("final status = %+v", st)
	}
}

func TestHashIndexerDisabledSkipsWork(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "test.iso")
	if err := os.WriteFile(path, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Lstat(path)
	repo := newFakeHashRepo(CatalogSettings{
		RescanEnabled:         true,
		RescanIntervalMinutes: 30,
		HashIndexEnabled:      false,
		HashIndexRateMBps:     50,
	}, []HashIndexCandidate{
		{StorageID: 1, StorageName: "Local", StorageKind: "local", StorageRoot: root, Path: "test.iso", Size: st.Size(), Modified: st.ModTime()},
	})
	indexer := NewHashIndexer(repo, nil)
	didWork, paused := indexer.step(context.Background())
	if didWork || paused {
		t.Fatalf("disabled step: got didWork=%v paused=%v", didWork, paused)
	}
	if len(repo.saved) != 0 {
		t.Fatalf("expected no hashes when disabled")
	}
	status := indexer.Status(context.Background())
	if status.State != "disabled" || status.Enabled {
		t.Fatalf("expected disabled status, got %+v", status)
	}
}

func TestHTTPSettingsAndHashIndexStatus(t *testing.T) {
	repo := &httpRepository{}
	repo.settings = DefaultCatalogSettings()
	srv := NewServer(repo)
	h := srv.Handler()

	// PUT settings with hash_index_enabled and hash_index_rate_mbps
	req := httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader(`{"rescan_enabled":true,"rescan_interval_minutes":15,"hash_index_enabled":false,"hash_index_rate_mbps":25}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /settings status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var updated CatalogSettings
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.HashIndexEnabled || updated.HashIndexRateMBps != 25 || updated.RescanIntervalMinutes != 15 {
		t.Fatalf("unexpected updated settings: %+v", updated)
	}

	// Invalid rate -> 400
	badReq := httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader(`{"rescan_enabled":true,"rescan_interval_minutes":15,"hash_index_enabled":true,"hash_index_rate_mbps":2000}`))
	badRec := httptest.NewRecorder()
	h.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid rate, got %d", badRec.Code)
	}

	// GET /hash-index/status
	stReq := httptest.NewRequest(http.MethodGet, "/hash-index/status", nil)
	stRec := httptest.NewRecorder()
	h.ServeHTTP(stRec, stReq)
	if stRec.Code != http.StatusOK {
		t.Fatalf("GET /hash-index/status = %d", stRec.Code)
	}
	var status HashIndexStatus
	if err := json.Unmarshal(stRec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Enabled || status.RateMBps != 25 || status.State != "disabled" {
		t.Fatalf("unexpected hash index status: %+v", status)
	}
}
