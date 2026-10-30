package boot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type memoryJournal struct{ events []Event }

func (m *memoryJournal) DeleteBefore(_ context.Context, before time.Time) (int64, error) {
	var kept []Event
	var n int64
	for _, e := range m.events {
		if e.At.Before(before) {
			n++
		} else {
			kept = append(kept, e)
		}
	}
	m.events = kept
	return n, nil
}

func (m *memoryJournal) Stats(context.Context) (JournalStats, error) {
	stats := JournalStats{Count: int64(len(m.events)), Bytes: int64(len(m.events)) * 100}
	for i := range m.events {
		if stats.Oldest == nil || m.events[i].At.Before(*stats.Oldest) {
			stats.Oldest = &m.events[i].At
		}
	}
	return stats, nil
}

func TestJournalRetentionAndClear(t *testing.T) {
	now := time.Date(2026, 10, 23, 13, 0, 0, 0, time.UTC)
	store := &memoryJournal{events: []Event{{At: now.AddDate(0, 0, -100)}, {At: now.AddDate(0, 0, -40)}, {At: now.Add(-time.Hour)}}}
	path := filepath.Join(t.TempDir(), "journal.json")
	j := &Journal{Path: path, Store: store, Now: func() time.Time { return now }}
	// Off by default: nothing is deleted.
	if n, err := j.RunOnce(context.Background()); n != 0 || err != nil || len(store.events) != 3 {
		t.Fatalf("default deleted %d: %v", n, err)
	}
	if err := j.SetDays(context.Background(), 45); err == nil {
		t.Fatal("unsupported period accepted")
	}
	if err := j.SetDays(context.Background(), 90); err != nil || len(store.events) != 2 {
		t.Fatalf("90 days: %v, %d left", err, len(store.events))
	}
	// The setting survives a restart.
	again := &Journal{Path: path, Store: store, Now: func() time.Time { return now }}
	state, err := again.State(context.Background())
	if err != nil || state.Days != 90 || state.Count != 2 {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	if n, err := again.Clear(context.Background()); err != nil || n != 2 || len(store.events) != 0 {
		t.Fatalf("clear %d: %v", n, err)
	}
	if state, _ := again.State(context.Background()); state.ClearedAt == nil || !state.ClearedAt.Equal(now) || state.Days != 90 {
		t.Fatalf("after clear: %+v", state)
	}
}

func TestJournalControlAPI(t *testing.T) {
	store := &memoryJournal{events: []Event{{At: time.Now().Add(-time.Hour)}}}
	j := &Journal{Path: filepath.Join(t.TempDir(), "journal.json"), Store: store}
	p, err := NewPublisher(filepath.Join(t.TempDir(), "menu"), []byte("#!ipxe\n"))
	if err != nil {
		t.Fatal(err)
	}
	h := ControlHandlerWithJournal(p, nil, nil, j, nil)
	do := func(method, path, body string) *httptest.ResponseRecorder {
		res := httptest.NewRecorder()
		h.ServeHTTP(res, httptest.NewRequest(method, path, strings.NewReader(body)))
		return res
	}
	if res := do(http.MethodPut, "/journal/retention", `{"days":12}`); res.Code != http.StatusBadRequest {
		t.Fatalf("bad days: %d", res.Code)
	}
	if res := do(http.MethodPut, "/journal/retention", `{"days":30}`); res.Code != http.StatusNoContent {
		t.Fatalf("days: %d", res.Code)
	}
	var state JournalState
	if res := do(http.MethodGet, "/journal", ""); json.Unmarshal(res.Body.Bytes(), &state) != nil || state.Days != 30 || state.Count != 1 {
		t.Fatalf("state=%s", res.Body)
	}
	if res := do(http.MethodPost, "/journal/clear", ""); res.Code != 200 || !strings.Contains(res.Body.String(), `"deleted":1`) {
		t.Fatalf("clear=%d %s", res.Code, res.Body)
	}
}
