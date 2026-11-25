package images

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"
)

// MonitorRepository is the storage state used by the background availability check.
type MonitorRepository interface {
	Storages(context.Context) ([]Storage, error)
	MarkUnavailableIfReady(context.Context, int64, string) (bool, error)
}

// StorageMonitor periodically checks that catalogued storages are still readable.
// A ready storage that fails the check becomes unavailable; an unavailable storage
// that passes it is rescanned, which refreshes the catalog and marks it ready.
type StorageMonitor struct {
	Repo          MonitorRepository
	Probe         func(Storage) error
	Rescan        func(int64) error
	Settings      func(context.Context) (CatalogSettings, error)
	Timeout       time.Duration
	RescanBackoff time.Duration
	Now           func() time.Time

	mu         sync.Mutex
	inflight   map[int64]bool
	lastRescan map[int64]time.Time
}

func (m *StorageMonitor) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// Run checks storages every interval until ctx is done.
func (m *StorageMonitor) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		m.CheckOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// CheckOnce probes every ready, unavailable, or never-scanned (unknown) storage once.
func (m *StorageMonitor) CheckOnce(ctx context.Context) {
	storages, err := m.Repo.Storages(ctx)
	if err != nil {
		log.Printf("Images storage check: cannot list storages: %v", err)
		return
	}
	var settings CatalogSettings
	if m.Settings != nil {
		if s, err := m.Settings(ctx); err == nil {
			settings = s
		}
	}
	var wg sync.WaitGroup
	for _, storage := range storages {
		if storage.State != "ready" && storage.State != "unavailable" && storage.State != "unknown" {
			continue
		}
		wg.Add(1)
		go func(storage Storage) {
			defer wg.Done()
			m.check(ctx, storage, settings)
		}(storage)
	}
	wg.Wait()
}

func (m *StorageMonitor) check(ctx context.Context, storage Storage, settings CatalogSettings) {
	probeErr, finished := m.probe(storage)
	if !finished {
		// A previous probe of this storage is still blocked; wait for it.
		return
	}
	if probeErr != nil {
		if storage.State != "ready" {
			return
		}
		message := "Хранилище недоступно: " + probeErr.Error()
		changed, err := m.Repo.MarkUnavailableIfReady(ctx, storage.ID, message)
		if err != nil {
			log.Printf("Images storage check: cannot mark storage %d unavailable: %v", storage.ID, err)
		} else if changed {
			log.Printf("Images storage check: storage %d unavailable: %v", storage.ID, probeErr)
		}
		return
	}
	if storage.State == "unavailable" || storage.State == "unknown" {
		if m.Rescan == nil {
			return
		}
		m.mu.Lock()
		if m.lastRescan == nil {
			m.lastRescan = map[int64]time.Time{}
		}
		last, seen := m.lastRescan[storage.ID]
		if seen && m.RescanBackoff > 0 && m.now().Sub(last) < m.RescanBackoff {
			m.mu.Unlock()
			return
		}
		m.lastRescan[storage.ID] = m.now()
		m.mu.Unlock()
		if err := m.Rescan(storage.ID); err != nil {
			log.Printf("Images storage check: cannot scan storage %d (%s): %v", storage.ID, storage.State, err)
			return
		}
		if storage.State == "unknown" {
			log.Printf("Images storage check: storage %d initial scan started", storage.ID)
		} else {
			log.Printf("Images storage check: storage %d is reachable again, rescan started", storage.ID)
		}
		return
	}
	if storage.State == "ready" && settings.RescanEnabled && m.Rescan != nil {
		interval := time.Duration(settings.RescanIntervalMinutes) * time.Minute
		if interval > 0 {
			m.mu.Lock()
			if m.lastRescan == nil {
				m.lastRescan = map[int64]time.Time{}
			}
			last, seen := m.lastRescan[storage.ID]
			tooSoonSinceLastTrigger := seen && m.now().Sub(last) < interval
			tooSoonSinceLastScan := storage.LastScanAt != nil && m.now().Sub(*storage.LastScanAt) < interval
			if tooSoonSinceLastTrigger || tooSoonSinceLastScan {
				m.mu.Unlock()
				return
			}
			m.lastRescan[storage.ID] = m.now()
			m.mu.Unlock()
			if err := m.Rescan(storage.ID); err != nil {
				if !errors.Is(err, errScanRunning) {
					log.Printf("Images storage check: periodic rescan of storage %d failed: %v", storage.ID, err)
				}
				return
			}
			log.Printf("Images storage check: periodic rescan of storage %d started", storage.ID)
		}
	}
}

// probe runs the probe with a timeout. finished is false when an earlier probe
// of the same storage has not returned yet, so no new probe was started.
func (m *StorageMonitor) probe(storage Storage) (err error, finished bool) {
	m.mu.Lock()
	if m.inflight == nil {
		m.inflight = map[int64]bool{}
	}
	if m.inflight[storage.ID] {
		m.mu.Unlock()
		return nil, false
	}
	m.inflight[storage.ID] = true
	m.mu.Unlock()
	result := make(chan error, 1)
	go func() {
		defer func() {
			m.mu.Lock()
			delete(m.inflight, storage.ID)
			m.mu.Unlock()
		}()
		result <- m.Probe(storage)
	}()
	timeout := m.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-result:
		return err, true
	case <-timer.C:
		return fmt.Errorf("не отвечает дольше %s", timeout), true
	}
}

// ProbeStorage checks that a storage root is mounted (when it must be) and
// readable. It stats the root as well as reading it: after the NAS recreates
// a shared folder the CIFS client may still list the directory from its cache
// while stat, which the rescan needs, fails with "stale file handle".
func ProbeStorage(storage Storage) error {
	if storage.Kind == "mounted" || storage.Kind == "smb" || storage.Kind == "nfs" {
		if _, ok := MountIdentity(storage.Root); !ok {
			return fmt.Errorf("не смонтировано")
		}
	}
	if _, err := os.Lstat(storage.Root); err != nil {
		return err
	}
	dir, err := os.Open(storage.Root)
	if err != nil {
		return err
	}
	defer dir.Close()
	if _, err := dir.ReadDir(1); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}
