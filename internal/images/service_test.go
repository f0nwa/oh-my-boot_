package images

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type contextAwareRepository struct {
	memoryRepository
	errorContextValid bool
}

type canceledLookupRepository struct{ contextAwareRepository }

func (m *canceledLookupRepository) Storage(ctx context.Context, _ int64) (Storage, error) {
	return Storage{}, ctx.Err()
}

func (m *contextAwareRepository) ScanError(ctx context.Context, id int64, message string) error {
	m.errorContextValid = ctx.Err() == nil
	return m.memoryRepository.ScanError(ctx, id, message)
}

type memoryRepository struct {
	storages map[int64]Storage
	images   map[int64]map[string]Image
	state    map[int64]string
}

func (m *memoryRepository) Storage(_ context.Context, id int64) (Storage, error) {
	return m.storages[id], nil
}
func (m *memoryRepository) SaveScan(_ context.Context, id int64, found []Image, otherBytes int64) error {
	if m.images[id] == nil {
		m.images[id] = map[string]Image{}
	}
	for path, image := range m.images[id] {
		image.Present = false
		m.images[id][path] = image
	}
	for _, image := range found {
		image.StorageID = id
		m.images[id][image.Path] = image
	}
	if s, ok := m.storages[id]; ok {
		s.OtherBytes = otherBytes
		m.storages[id] = s
	}
	m.state[id] = "ready"
	return nil
}
func (m *memoryRepository) ScanError(_ context.Context, id int64, _ string) error {
	m.state[id] = "unavailable"
	return nil
}
func (m *memoryRepository) DeleteImage(_ context.Context, id int64, path string) error {
	delete(m.images[id], path)
	return nil
}

func TestScanStorageKeepsSeparateCatalogsAndMarksMissing(t *testing.T) {
	roots := []string{realTempDir(t), realTempDir(t)}
	repo := &memoryRepository{storages: map[int64]Storage{}, images: map[int64]map[string]Image{}, state: map[int64]string{}}
	for i, root := range roots {
		id := int64(i + 1)
		repo.storages[id] = Storage{ID: id, Root: root, Kind: "local"}
		if err := os.WriteFile(filepath.Join(root, "same.iso"), []byte("ISO"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := ScanStorage(context.Background(), repo, id); err != nil {
			t.Fatal(err)
		}
	}
	if !repo.images[1]["same.iso"].Present || !repo.images[2]["same.iso"].Present {
		t.Fatalf("same-name files did not remain separate: %+v", repo.images)
	}
	if err := os.Remove(filepath.Join(roots[0], "same.iso")); err != nil {
		t.Fatal(err)
	}
	if err := ScanStorage(context.Background(), repo, 1); err != nil {
		t.Fatal(err)
	}
	if repo.images[1]["same.iso"].Present || !repo.images[2]["same.iso"].Present {
		t.Fatalf("rescan changed the wrong catalog: %+v", repo.images)
	}
}

func TestScanStoragePreservesCatalogOnReadError(t *testing.T) {
	root := realTempDir(t)
	repo := &memoryRepository{
		storages: map[int64]Storage{1: {ID: 1, Root: root, Kind: "local"}},
		images:   map[int64]map[string]Image{1: {"old.iso": {Path: "old.iso", Present: true}}},
		state:    map[int64]string{},
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := ScanStorage(context.Background(), repo, 1); err == nil {
		t.Fatal("expected scan failure")
	}
	if !repo.images[1]["old.iso"].Present || repo.state[1] != "unavailable" {
		t.Fatalf("previous catalog lost after failed scan: %+v, state=%s", repo.images, repo.state[1])
	}
}

func TestMountedScanPreservesCatalogWhenMountChanges(t *testing.T) {
	repo := &memoryRepository{
		storages: map[int64]Storage{1: {ID: 1, Name: "NAS", Root: realTempDir(t), Kind: "mounted"}},
		images:   map[int64]map[string]Image{1: {"old.iso": {Path: "old.iso", Present: true}}},
		state:    map[int64]string{},
	}
	checks := 0
	mount := func(string) (string, bool) {
		checks++
		if checks == 1 {
			return "mount-1", true
		}
		return "mount-2", true
	}
	if err := scanStorageWith(context.Background(), repo, 1, mount, func(string) (ScanResult, error) { return ScanResult{}, nil }); err == nil {
		t.Fatal("expected changed mount to fail")
	}
	if checks != 2 || !repo.images[1]["old.iso"].Present || repo.state[1] != "unavailable" {
		t.Fatalf("catalog changed on mount replacement: checks=%d images=%+v state=%s", checks, repo.images, repo.state[1])
	}
}

func TestSMBScanPreservesCatalogWhenUnmounted(t *testing.T) {
	repo := &memoryRepository{
		storages: map[int64]Storage{1: {ID: 1, Name: "SMB", Root: realTempDir(t), Kind: "smb"}},
		images:   map[int64]map[string]Image{1: {"old.iso": {Path: "old.iso", Present: true}}},
		state:    map[int64]string{},
	}
	scanCalled := false
	err := scanStorageWith(context.Background(), repo, 1, func(string) (string, bool) { return "", false }, func(string) (ScanResult, error) {
		scanCalled = true
		return ScanResult{}, nil
	})
	if err == nil || scanCalled || !repo.images[1]["old.iso"].Present || repo.state[1] != "unavailable" {
		t.Fatalf("unmounted SMB changed catalog: err=%v scan=%v images=%+v state=%s", err, scanCalled, repo.images, repo.state[1])
	}
}

func TestScanFailureUsesFreshContextToStoreError(t *testing.T) {
	repo := &contextAwareRepository{memoryRepository: memoryRepository{
		storages: map[int64]Storage{1: {ID: 1, Root: realTempDir(t), Kind: "local"}},
		images:   map[int64]map[string]Image{},
		state:    map[int64]string{},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = scanStorageWith(ctx, repo, 1, func(string) (string, bool) { return "", false }, func(string) (ScanResult, error) { return ScanResult{}, errors.New("read failed") })
	if !repo.errorContextValid || repo.state[1] != "unavailable" {
		t.Fatalf("failure was not persisted with fresh context: valid=%v state=%s", repo.errorContextValid, repo.state[1])
	}
}

func TestScanStorageRecordsOtherBytes(t *testing.T) {
	root := realTempDir(t)
	repo := &memoryRepository{
		storages: map[int64]Storage{1: {ID: 1, Root: root, Kind: "local"}},
		images:   map[int64]map[string]Image{},
		state:    map[int64]string{},
	}
	if err := os.WriteFile(filepath.Join(root, "win.iso"), []byte("123456"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "readme.txt"), []byte("hello-world"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ScanStorage(context.Background(), repo, 1); err != nil {
		t.Fatal(err)
	}
	if repo.storages[1].OtherBytes != 11 {
		t.Fatalf("OtherBytes = %d, want 11", repo.storages[1].OtherBytes)
	}
}

func TestCanceledStorageLookupUsesFreshContextToStoreError(t *testing.T) {
	repo := &canceledLookupRepository{contextAwareRepository{memoryRepository: memoryRepository{
		storages: map[int64]Storage{1: {ID: 1, Root: realTempDir(t), Kind: "local"}},
		images:   map[int64]map[string]Image{},
		state:    map[int64]string{},
	}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ScanStorage(ctx, repo, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if !repo.errorContextValid || repo.state[1] != "unavailable" {
		t.Fatalf("lookup cancellation was not recorded: valid=%v state=%s", repo.errorContextValid, repo.state[1])
	}
}
