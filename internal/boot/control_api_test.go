package boot

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

type mockEventReader struct {
	lastLimit      int
	lastBeforeTime *time.Time
	lastBeforeID   int64
	lastMAC        string
	events         []Event
	err            error
}

func (m *mockEventReader) Recent(limit int) ([]Event, error) {
	m.lastLimit = limit
	return m.events, m.err
}

func (m *mockEventReader) RecentPaged(limit int, beforeTime *time.Time, beforeID int64, mac string) ([]Event, error) {
	m.lastLimit = limit
	m.lastBeforeTime = beforeTime
	m.lastBeforeID = beforeID
	m.lastMAC = mac
	return m.events, m.err
}

func TestControlAPIGetEvents(t *testing.T) {
	p, err := NewPublisher(filepath.Join(t.TempDir(), "menu.ipxe"), []byte("#!ipxe\n"))
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	sampleEvents := []Event{
		{ID: 2, Kind: "menu", Path: "/menu.ipxe", RemoteAddr: "10.0.0.1:1234", MAC: "aa:bb:cc:dd:ee:01", At: now},
		{ID: 1, Kind: "asset", Path: "/vmlinuz", RemoteAddr: "10.0.0.1:1234", MAC: "aa:bb:cc:dd:ee:01", At: now.Add(-time.Second)},
	}

	reader := &mockEventReader{events: sampleEvents}
	handler := ControlHandler(p, reader, nil)

	// Default call: GET /events
	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got []Event
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 || got[0].ID != 2 {
		t.Fatalf("unexpected events: %+v", got)
	}
	if reader.lastLimit != 50 && reader.lastLimit != 30 {
		t.Fatalf("unexpected default limit: %d", reader.lastLimit)
	}

	// Paged call with parameters
	cursorTimeStr := now.Format(time.RFC3339Nano)
	req = httptest.NewRequest(http.MethodGet, "/events?limit=25&before_time="+cursorTimeStr+"&before_id=2&mac=aa:bb:cc:dd:ee:01", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if reader.lastLimit != 25 {
		t.Errorf("limit = %d, want 25", reader.lastLimit)
	}
	if reader.lastBeforeTime == nil || !reader.lastBeforeTime.Equal(now) {
		t.Errorf("before_time = %v, want %v", reader.lastBeforeTime, now)
	}
	if reader.lastBeforeID != 2 {
		t.Errorf("before_id = %d, want 2", reader.lastBeforeID)
	}
	if reader.lastMAC != "aa:bb:cc:dd:ee:01" {
		t.Errorf("mac = %q, want aa:bb:cc:dd:ee:01", reader.lastMAC)
	}

	// Invalid limit
	for _, raw := range []string{"0", "-5", "abc"} {
		req = httptest.NewRequest(http.MethodGet, "/events?limit="+raw, nil)
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("limit=%q: got status %d, want 400", raw, rec.Code)
		}
	}

	// Invalid before_id
	for _, raw := range []string{"-1", "abc"} {
		req = httptest.NewRequest(http.MethodGet, "/events?before_id="+raw, nil)
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("before_id=%q: got status %d, want 400", raw, rec.Code)
		}
	}

	// Invalid before_time
	req = httptest.NewRequest(http.MethodGet, "/events?before_time=not-a-timestamp", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("before_time=invalid: got status %d, want 400", rec.Code)
	}

	// Error from reader returns 503
	reader.err = errors.New("db error")
	req = httptest.NewRequest(http.MethodGet, "/events", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("db error: got status %d, want 503", rec.Code)
	}
}
