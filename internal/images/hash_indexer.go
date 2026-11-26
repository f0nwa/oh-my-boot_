package images

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// HashIndexRepository is the database subset used by HashIndexer.
type HashIndexRepository interface {
	Settings(context.Context) (CatalogSettings, error)
	HashIndexCounts(context.Context) (HashIndexCounts, error)
	HashIndexCandidates(context.Context) ([]HashIndexCandidate, error)
	SaveImageSHA256(ctx context.Context, storageID int64, path string, size int64, modified time.Time, sha256Hex string) error
}

// HashIndexer computes SHA-256 checksums for catalogued images sequentially,
// one file at a time, with configurable read-rate throttling.
type HashIndexer struct {
	Repo           HashIndexRepository
	IsStorageBusy  func(storageID int64) bool
	PollInterval   time.Duration
	PausedInterval time.Duration
	InterFilePause time.Duration
	ErrorBackoff   time.Duration
	ChunkSize      int

	notifyCh chan struct{}
	enabled  atomic.Bool
	rateMBps atomic.Int64

	mu            sync.RWMutex
	status        HashIndexStatus
	backoff       map[string]time.Time
	cancelCurrent context.CancelFunc
}

func NewHashIndexer(repo HashIndexRepository, isBusy func(int64) bool) *HashIndexer {
	h := &HashIndexer{
		Repo:           repo,
		IsStorageBusy:  isBusy,
		PollInterval:   10 * time.Second,
		PausedInterval: 2 * time.Second,
		InterFilePause: 500 * time.Millisecond,
		ErrorBackoff:   60 * time.Second,
		ChunkSize:      256 << 10,
		notifyCh:       make(chan struct{}, 1),
		backoff:        map[string]time.Time{},
		status: HashIndexStatus{
			Enabled:  true,
			RateMBps: 50,
			State:    "idle",
		},
	}
	h.enabled.Store(true)
	h.rateMBps.Store(50)
	return h
}

// Notify wakes the background loop to re-check settings and pending images.
func (h *HashIndexer) Notify() {
	if h == nil {
		return
	}
	select {
	case h.notifyCh <- struct{}{}:
	default:
	}
}

// ApplySettings pushes updated settings immediately into the running worker.
func (h *HashIndexer) ApplySettings(settings CatalogSettings) {
	if h == nil {
		return
	}
	h.enabled.Store(settings.HashIndexEnabled)
	h.rateMBps.Store(int64(settings.HashIndexRateMBps))
	h.mu.Lock()
	h.status.Enabled = settings.HashIndexEnabled
	h.status.RateMBps = settings.HashIndexRateMBps
	if !settings.HashIndexEnabled && h.cancelCurrent != nil {
		h.cancelCurrent()
	}
	h.mu.Unlock()
	h.Notify()
}

// Status returns the current indexer status, refreshing counts/settings when not actively hashing.
func (h *HashIndexer) Status(ctx context.Context) HashIndexStatus {
	if h == nil {
		return HashIndexStatus{State: "disabled"}
	}
	h.mu.RLock()
	st := h.status
	if h.status.Current != nil {
		cp := *h.status.Current
		st.Current = &cp
	}
	running := st.State == "running" && st.Current != nil
	h.mu.RUnlock()

	if h.Repo == nil {
		return st
	}
	if settings, err := h.Repo.Settings(ctx); err == nil {
		st.Enabled = settings.HashIndexEnabled
		st.RateMBps = settings.HashIndexRateMBps
		if !settings.HashIndexEnabled && !running {
			st.State = "disabled"
		}
	}
	if counts, err := h.Repo.HashIndexCounts(ctx); err == nil {
		st.TotalCount = counts.Total
		st.IndexedCount = counts.Indexed
		st.PendingCount = counts.Pending
		if !running && st.Enabled && st.PendingCount == 0 {
			st.State = "idle"
		}
	}
	return st
}

// Run executes the sequential background indexing loop until ctx is canceled.
func (h *HashIndexer) Run(ctx context.Context) {
	if h == nil || h.Repo == nil {
		return
	}
	poll := h.PollInterval
	if poll <= 0 {
		poll = 10 * time.Second
	}
	pausedWait := h.PausedInterval
	if pausedWait <= 0 {
		pausedWait = 2 * time.Second
	}
	interPause := h.InterFilePause

	for {
		if ctx.Err() != nil {
			return
		}
		didWork, paused := h.step(ctx)
		if didWork {
			if interPause > 0 {
				if !sleepContext(ctx, interPause, h.notifyCh) {
					return
				}
			}
			continue
		}
		wait := poll
		if paused {
			wait = pausedWait
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-h.notifyCh:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (h *HashIndexer) step(ctx context.Context) (didWork bool, paused bool) {
	settings, err := h.Repo.Settings(ctx)
	if err != nil {
		log.Printf("Images hash indexer: cannot load settings: %v", err)
		return false, false
	}
	h.enabled.Store(settings.HashIndexEnabled)
	h.rateMBps.Store(int64(settings.HashIndexRateMBps))

	counts, err := h.Repo.HashIndexCounts(ctx)
	if err != nil {
		log.Printf("Images hash indexer: cannot count images: %v", err)
		return false, false
	}

	if !settings.HashIndexEnabled {
		h.setState(settings, counts, "disabled", nil)
		return false, false
	}
	if counts.Pending == 0 {
		h.setState(settings, counts, "idle", nil)
		return false, false
	}

	candidates, err := h.Repo.HashIndexCandidates(ctx)
	if err != nil {
		log.Printf("Images hash indexer: cannot list candidates: %v", err)
		return false, false
	}
	if len(candidates) == 0 {
		h.setState(settings, counts, "idle", nil)
		return false, false
	}

	now := time.Now()
	var selected *HashIndexCandidate
	skippedBusy := false
	for i := range candidates {
		c := &candidates[i]
		if h.IsStorageBusy != nil && h.IsStorageBusy(c.StorageID) {
			skippedBusy = true
			continue
		}
		if h.inBackoff(c.StorageID, c.Path, now) {
			continue
		}
		selected = c
		break
	}
	if selected == nil {
		if skippedBusy {
			h.setState(settings, counts, "paused", nil)
			return false, true
		}
		h.setState(settings, counts, "idle", nil)
		return false, false
	}

	err = h.hashCandidate(ctx, settings, counts, *selected)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return false, false
		}
		log.Printf("Images hash indexer: %s/%s: %v", selected.StorageName, selected.Path, err)
		h.markBackoff(selected.StorageID, selected.Path, time.Now())
		return false, false
	}
	h.clearBackoff(selected.StorageID, selected.Path)
	return true, false
}

func (h *HashIndexer) hashCandidate(ctx context.Context, settings CatalogSettings, counts HashIndexCounts, cand HashIndexCandidate) error {
	fullPath := filepath.Join(cand.StorageRoot, filepath.FromSlash(cand.Path))
	before, err := os.Lstat(fullPath)
	if err != nil {
		return err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return fmt.Errorf("not a regular file")
	}
	if before.Size() != cand.Size || !before.ModTime().Equal(cand.Modified) {
		// File changed since last scan; skip silently until next scan updates catalog.
		return fmt.Errorf("file metadata changed since scan")
	}

	file, err := os.Open(fullPath)
	if err != nil {
		return err
	}
	defer file.Close()

	fileCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	cur := &HashIndexCurrent{
		StorageID:   cand.StorageID,
		StorageName: cand.StorageName,
		Path:        cand.Path,
		Size:        cand.Size,
	}
	h.mu.Lock()
	h.cancelCurrent = cancel
	h.status = HashIndexStatus{
		Enabled:      settings.HashIndexEnabled,
		RateMBps:     settings.HashIndexRateMBps,
		State:        "running",
		TotalCount:   counts.Total,
		IndexedCount: counts.Indexed,
		PendingCount: counts.Pending,
		Current:      cur,
	}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.cancelCurrent = nil
		h.status.Current = nil
		if !h.enabled.Load() {
			h.status.State = "disabled"
		} else {
			h.status.State = "idle"
		}
		h.mu.Unlock()
	}()

	chunkSize := h.ChunkSize
	if chunkSize <= 0 {
		chunkSize = 256 << 10
	}
	buf := make([]byte, chunkSize)
	hasher := sha256.New()

	var bytesDone int64
	startedAt := time.Now()
	rateWindowStart := startedAt
	var rateWindowBytes int64
	lastRate := h.rateMBps.Load()
	lastReport := startedAt

	for {
		if !h.enabled.Load() {
			return context.Canceled
		}
		if h.IsStorageBusy != nil && h.IsStorageBusy(cand.StorageID) {
			return context.Canceled
		}
		if err := fileCtx.Err(); err != nil {
			return err
		}

		n, readErr := file.Read(buf)
		if n > 0 {
			_, _ = hasher.Write(buf[:n])
			bytesDone += int64(n)
			rateWindowBytes += int64(n)

			// Apply read-rate throttle (0 = unlimited).
			curRate := h.rateMBps.Load()
			if curRate != lastRate {
				lastRate = curRate
				rateWindowStart = time.Now()
				rateWindowBytes = 0
			}
			if curRate > 0 {
				bytesPerSec := curRate * 1024 * 1024
				expected := time.Duration(float64(rateWindowBytes) / float64(bytesPerSec) * float64(time.Second))
				elapsed := time.Since(rateWindowStart)
				if expected > elapsed {
					if !sleepContext(fileCtx, expected-elapsed, nil) {
						return context.Canceled
					}
				}
			}

			now := time.Now()
			if now.Sub(lastReport) >= 250*time.Millisecond || bytesDone >= cand.Size {
				totalElapsed := now.Sub(startedAt).Seconds()
				var speedBPS int64
				if totalElapsed > 0 {
					speedBPS = int64(float64(bytesDone) / totalElapsed)
				}
				pct := 0
				if cand.Size > 0 {
					pct = int((bytesDone * 100) / cand.Size)
					if pct > 100 {
						pct = 100
					}
				} else {
					pct = 100
				}
				h.mu.Lock()
				if h.status.Current != nil {
					h.status.Current.BytesDone = bytesDone
					h.status.Current.Percent = pct
					h.status.Current.SpeedBPS = speedBPS
				}
				h.status.RateMBps = int(h.rateMBps.Load())
				h.mu.Unlock()
				lastReport = now
			}
		}

		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
	}

	after, err := os.Lstat(fullPath)
	if err != nil {
		return err
	}
	if after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return fmt.Errorf("file changed during hashing")
	}

	shaHex := hex.EncodeToString(hasher.Sum(nil))
	if err := h.Repo.SaveImageSHA256(ctx, cand.StorageID, cand.Path, cand.Size, cand.Modified, shaHex); err != nil {
		return err
	}
	if updatedCounts, err := h.Repo.HashIndexCounts(ctx); err == nil {
		h.mu.Lock()
		h.status.TotalCount = updatedCounts.Total
		h.status.IndexedCount = updatedCounts.Indexed
		h.status.PendingCount = updatedCounts.Pending
		h.mu.Unlock()
	}
	return nil
}

func (h *HashIndexer) setState(settings CatalogSettings, counts HashIndexCounts, state string, cur *HashIndexCurrent) {
	h.mu.Lock()
	h.status = HashIndexStatus{
		Enabled:      settings.HashIndexEnabled,
		RateMBps:     settings.HashIndexRateMBps,
		State:        state,
		TotalCount:   counts.Total,
		IndexedCount: counts.Indexed,
		PendingCount: counts.Pending,
		Current:      cur,
	}
	h.mu.Unlock()
}

func (h *HashIndexer) backoffKey(storageID int64, path string) string {
	return fmt.Sprintf("%d:%s", storageID, path)
}

func (h *HashIndexer) inBackoff(storageID int64, path string, now time.Time) bool {
	h.mu.RLock()
	until, ok := h.backoff[h.backoffKey(storageID, path)]
	h.mu.RUnlock()
	return ok && now.Before(until)
}

func (h *HashIndexer) markBackoff(storageID int64, path string, now time.Time) {
	d := h.ErrorBackoff
	if d <= 0 {
		d = 60 * time.Second
	}
	h.mu.Lock()
	h.backoff[h.backoffKey(storageID, path)] = now.Add(d)
	h.mu.Unlock()
}

func (h *HashIndexer) clearBackoff(storageID int64, path string) {
	h.mu.Lock()
	delete(h.backoff, h.backoffKey(storageID, path))
	h.mu.Unlock()
}

func sleepContext(ctx context.Context, d time.Duration, wake <-chan struct{}) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-wake:
		return ctx.Err() == nil
	case <-timer.C:
		return ctx.Err() == nil
	}
}
