package images

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type monitorRepository struct {
	mu       sync.Mutex
	storages []Storage
	marked   map[int64]string
}

func (r *monitorRepository) Storages(context.Context) ([]Storage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Storage(nil), r.storages...), nil
}

func (r *monitorRepository) MarkUnavailableIfReady(_ context.Context, id int64, message string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.storages {
		if r.storages[i].ID == id && r.storages[i].State == "ready" {
			r.storages[i].State = "unavailable"
			r.storages[i].Error = message
			if r.marked == nil {
				r.marked = map[int64]string{}
			}
			r.marked[id] = message
			return true, nil
		}
	}
	return false, nil
}

type recordingRescan struct {
	mu  sync.Mutex
	ids []int64
}

func (r *recordingRescan) start(id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ids = append(r.ids, id)
	return nil
}

func (r *recordingRescan) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.ids)
}

func TestMonitorMarksReadyStorageUnavailableWhenProbeFails(t *testing.T) {
	repo := &monitorRepository{storages: []Storage{{ID: 6, Name: "NAS", Kind: "smb", Root: "/mnt/nas", State: "ready"}}}
	rescan := &recordingRescan{}
	m := &StorageMonitor{Repo: repo, Rescan: rescan.start, Timeout: time.Second, Probe: func(Storage) error { return errors.New("host is down") }}
	m.CheckOnce(context.Background())
	if got := repo.marked[6]; !strings.Contains(got, "host is down") {
		t.Fatalf("storage not marked unavailable: %q", got)
	}
	if rescan.count() != 0 {
		t.Fatalf("unexpected rescan: %v", rescan.ids)
	}
}

func TestMonitorLeavesHealthyAndScanningStoragesAlone(t *testing.T) {
	repo := &monitorRepository{storages: []Storage{
		{ID: 1, Kind: "local", Root: "/srv/iso", State: "ready"},
		{ID: 2, Kind: "smb", Root: "/mnt/nas", State: "scanning"},
	}}
	probed := map[int64]bool{}
	rescan := &recordingRescan{}
	m := &StorageMonitor{Repo: repo, Rescan: rescan.start, Timeout: time.Second, Probe: func(s Storage) error { probed[s.ID] = true; return nil }}
	m.CheckOnce(context.Background())
	if len(repo.marked) != 0 || rescan.count() != 0 || probed[2] {
		t.Fatalf("changed healthy or scanning storage: marked=%v rescans=%v probed=%v", repo.marked, rescan.ids, probed)
	}
}

func TestMonitorRescansRecoveredStorageWithBackoff(t *testing.T) {
	repo := &monitorRepository{storages: []Storage{{ID: 6, Kind: "smb", Root: "/mnt/nas", State: "unavailable", Error: "host is down"}}}
	rescan := &recordingRescan{}
	now := time.Date(2026, 10, 20, 1, 0, 0, 0, time.UTC)
	m := &StorageMonitor{Repo: repo, Rescan: rescan.start, Timeout: time.Second, RescanBackoff: 10 * time.Minute, Now: func() time.Time { return now }, Probe: func(Storage) error { return nil }}
	m.CheckOnce(context.Background())
	m.CheckOnce(context.Background())
	if rescan.count() != 1 {
		t.Fatalf("recovered storage rescans within backoff = %d, want 1", rescan.count())
	}
	now = now.Add(11 * time.Minute)
	m.CheckOnce(context.Background())
	if rescan.count() != 2 {
		t.Fatalf("recovered storage rescans after backoff = %d, want 2", rescan.count())
	}
}

func TestMonitorDoesNotRescanStorageThatStillFails(t *testing.T) {
	repo := &monitorRepository{storages: []Storage{{ID: 6, Kind: "smb", Root: "/mnt/nas", State: "unavailable", Error: "Ожидает подключения"}}}
	rescan := &recordingRescan{}
	m := &StorageMonitor{Repo: repo, Rescan: rescan.start, Timeout: time.Second, Probe: func(Storage) error { return errors.New("not mounted") }}
	m.CheckOnce(context.Background())
	if rescan.count() != 0 || len(repo.marked) != 0 || repo.storages[0].Error != "Ожидает подключения" {
		t.Fatalf("unavailable storage changed: rescans=%v marked=%v error=%q", rescan.ids, repo.marked, repo.storages[0].Error)
	}
}

func TestMonitorTreatsHungProbeAsUnavailableAndDoesNotStackProbes(t *testing.T) {
	repo := &monitorRepository{storages: []Storage{{ID: 6, Kind: "smb", Root: "/mnt/nas", State: "ready"}}}
	release := make(chan struct{})
	defer close(release)
	var mu sync.Mutex
	calls := 0
	m := &StorageMonitor{Repo: repo, Rescan: (&recordingRescan{}).start, Timeout: 20 * time.Millisecond, Probe: func(Storage) error {
		mu.Lock()
		calls++
		mu.Unlock()
		<-release
		return nil
	}}
	m.CheckOnce(context.Background())
	if got := repo.marked[6]; !strings.Contains(got, "не отвечает") {
		t.Fatalf("hung storage not marked unavailable: %q", got)
	}
	m.CheckOnce(context.Background())
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("probe calls while first probe is hung = %d, want 1", calls)
	}
}

func TestProbeStorageReadsLocalDirectories(t *testing.T) {
	root := realTempDir(t)
	if err := ProbeStorage(Storage{Kind: "local", Root: root}); err != nil {
		t.Fatalf("empty local storage: %v", err)
	}
	if err := ProbeStorage(Storage{Kind: "local", Root: root + "/missing"}); err == nil {
		t.Fatal("missing local storage accepted")
	}
	if err := ProbeStorage(Storage{Kind: "smb", Root: root}); err == nil {
		t.Fatal("unmounted SMB storage accepted")
	}
}
