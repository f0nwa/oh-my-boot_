package boot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// RetentionDays are the choices for automatic cleanup of the request log;
// 0 keeps everything.
var RetentionDays = []int{0, 7, 30, 90, 180, 365}

// JournalStore deletes and measures the request log.
type JournalStore interface {
	DeleteBefore(ctx context.Context, before time.Time) (int64, error)
	Stats(ctx context.Context) (JournalStats, error)
}

// JournalStats describes the request log as it is now.
type JournalStats struct {
	Count  int64      `json:"count"`
	Oldest *time.Time `json:"oldest,omitempty"`
	Bytes  int64      `json:"bytes"`
}

// JournalState is what Control shows about the log and its cleanup.
type JournalState struct {
	JournalStats
	Days        int        `json:"days"`
	ClearedAt   *time.Time `json:"cleared_at,omitempty"`
	LastRun     *time.Time `json:"last_run,omitempty"`
	LastDeleted int64      `json:"last_deleted"`
}

// Journal keeps the cleanup setting in a small file and deletes old requests
// once an hour.
type Journal struct {
	Path  string
	Store JournalStore
	Now   func() time.Time

	mu       sync.Mutex
	settings journalSettings
	loaded   bool
	lastRun  *time.Time
	deleted  int64
}

type journalSettings struct {
	Days      int        `json:"days"`
	ClearedAt *time.Time `json:"cleared_at,omitempty"`
}

func (j *Journal) now() time.Time {
	if j.Now != nil {
		return j.Now().UTC()
	}
	return time.Now().UTC()
}

func (j *Journal) load() {
	if j.loaded {
		return
	}
	j.loaded = true
	if data, err := os.ReadFile(j.Path); err == nil {
		_ = json.Unmarshal(data, &j.settings)
	}
}

func (j *Journal) save() error {
	data, err := json.Marshal(j.settings)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(j.Path), 0o750); err != nil {
		return err
	}
	tmp := j.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, j.Path)
}

// Settings reports cleanup settings without querying the request log.
func (j *Journal) Settings() JournalState {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.load()
	return JournalState{Days: j.settings.Days, ClearedAt: j.settings.ClearedAt, LastRun: j.lastRun, LastDeleted: j.deleted}
}

// State reports the setting, the last cleanup and the log's size.
func (j *Journal) State(ctx context.Context) (JournalState, error) {
	state := j.Settings()
	if j.Store == nil {
		return state, errors.New("request log unavailable")
	}
	stats, err := j.Store.Stats(ctx)
	state.JournalStats = stats
	return state, err
}

// SetDays changes automatic cleanup and applies it at once.
func (j *Journal) SetDays(ctx context.Context, days int) error {
	if !slices.Contains(RetentionDays, days) {
		return fmt.Errorf("unsupported retention %d", days)
	}
	j.mu.Lock()
	j.load()
	j.settings.Days = days
	err := j.save()
	j.mu.Unlock()
	if err != nil {
		return err
	}
	_, err = j.RunOnce(ctx)
	return err
}

// Clear deletes the whole log and remembers when, so that Control also stops
// showing older DHCP lines it reads from journald.
func (j *Journal) Clear(ctx context.Context) (int64, error) {
	if j.Store == nil {
		return 0, errors.New("request log unavailable")
	}
	at := j.now()
	deleted, err := j.Store.DeleteBefore(ctx, at.Add(time.Second))
	if err != nil {
		return deleted, err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.load()
	j.settings.ClearedAt = &at
	return deleted, j.save()
}

// RunOnce deletes requests older than the retention period, if one is set.
func (j *Journal) RunOnce(ctx context.Context) (int64, error) {
	j.mu.Lock()
	j.load()
	days := j.settings.Days
	j.mu.Unlock()
	if days == 0 || j.Store == nil {
		return 0, nil
	}
	now := j.now()
	deleted, err := j.Store.DeleteBefore(ctx, now.AddDate(0, 0, -days))
	j.mu.Lock()
	j.lastRun, j.deleted = &now, deleted
	j.mu.Unlock()
	return deleted, err
}

// Run cleans up once an hour until ctx ends.
func (j *Journal) Run(ctx context.Context) {
	for {
		if deleted, err := j.RunOnce(ctx); err != nil {
			log.Printf("request log cleanup: %v", err)
		} else if deleted > 0 {
			log.Printf("request log cleanup: deleted %d old requests", deleted)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Hour):
		}
	}
}

// handleJournal serves GET /journal and /journal/settings, PUT
// /journal/retention and POST /journal/clear on the control socket.
func handleJournal(mux *http.ServeMux, j *Journal) {
	mux.HandleFunc("GET /journal/settings", func(w http.ResponseWriter, r *http.Request) {
		if j == nil {
			http.Error(w, "request log unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(j.Settings())
	})
	mux.HandleFunc("GET /journal", func(w http.ResponseWriter, r *http.Request) {
		if j == nil {
			http.Error(w, "request log unavailable", http.StatusServiceUnavailable)
			return
		}
		state, err := j.State(r.Context())
		if err != nil {
			http.Error(w, "request log unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(state)
	})
	mux.HandleFunc("PUT /journal/retention", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Days int `json:"days"`
		}
		if j == nil || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&input) != nil {
			http.Error(w, "invalid retention", http.StatusBadRequest)
			return
		}
		if err := j.SetDays(r.Context(), input.Days); err != nil {
			http.Error(w, "invalid retention", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /journal/clear", func(w http.ResponseWriter, r *http.Request) {
		if j == nil {
			http.Error(w, "request log unavailable", http.StatusServiceUnavailable)
			return
		}
		deleted, err := j.Clear(r.Context())
		if err != nil {
			http.Error(w, "request log unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int64{"deleted": deleted})
	})
}
