package control

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"omb/internal/images"
)

type sysEventTestStore struct {
	authOnlyStore
	events []SystemEvent
	pruned int
}

func (m *sysEventTestStore) RecordSystemEvent(_ context.Context, ev SystemEvent) error {
	ev.ID = int64(len(m.events) + 1)
	m.events = append(m.events, ev)
	return nil
}

func (m *sysEventTestStore) SystemEventsPaged(_ context.Context, filter SystemEventFilter, limit int) ([]SystemEvent, error) {
	var out []SystemEvent
	for i := len(m.events) - 1; i >= 0; i-- {
		ev := m.events[i]
		if filter.Severity == "active" {
			if ev.Severity != "error" || ev.ResolvedAt != nil {
				continue
			}
		} else if filter.Severity != "" && filter.Severity != "all" && ev.Severity != filter.Severity {
			continue
		}
		if filter.Category != "" && filter.Category != "all" && ev.Category != filter.Category {
			continue
		}
		if filter.Query != "" && !strings.Contains(strings.ToLower(ev.Title+" "+ev.Detail), strings.ToLower(filter.Query)) {
			continue
		}
		out = append(out, ev)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (m *sysEventTestStore) CountRecentSystemErrors(_ context.Context, since time.Time) (int, error) {
	n := 0
	for _, ev := range m.events {
		if ev.Severity == "error" && !ev.OccurredAt.Before(since) {
			n++
		}
	}
	return n, nil
}

func (m *sysEventTestStore) CountActiveSystemErrors(_ context.Context) (int, error) {
	n := 0
	for _, ev := range m.events {
		if ev.Severity == "error" && ev.ResolvedAt == nil {
			n++
		}
	}
	return n, nil
}

func (m *sysEventTestStore) ResolveSystemEvents(_ context.Context, sourceKey string, resolvedAt time.Time) error {
	for i := range m.events {
		if m.events[i].SourceKey == sourceKey && m.events[i].ResolvedAt == nil && (m.events[i].Severity == "error" || m.events[i].Severity == "warning") {
			t := resolvedAt
			m.events[i].ResolvedAt = &t
		}
	}
	return nil
}

func (m *sysEventTestStore) PruneSystemEvents(_ context.Context, _ time.Time) error {
	m.pruned++
	return nil
}

func (m *sysEventTestStore) PruneResolvedSystemEvents(_ context.Context) (int64, error) {
	var kept []SystemEvent
	var deleted int64
	for _, ev := range m.events {
		if ev.ResolvedAt != nil {
			deleted++
		} else {
			kept = append(kept, ev)
		}
	}
	m.events = kept
	return deleted, nil
}

func (m *sysEventTestStore) ClearAllSystemEvents(_ context.Context) (int64, error) {
	n := int64(len(m.events))
	m.events = nil
	return n, nil
}

type switchableImagesAPI struct {
	fakeImagesAPI
	catalog images.Catalog
	err     error
}

func (a *switchableImagesAPI) Catalog(context.Context) (images.Catalog, error) {
	if a.err != nil {
		return images.Catalog{}, a.err
	}
	return a.catalog, nil
}

func TestSystemEventMonitorStorageAndServiceTransitions(t *testing.T) {
	store := &sysEventTestStore{
		authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}},
	}
	imgAPI := &switchableImagesAPI{
		catalog: images.Catalog{
			Storages: []images.Storage{{ID: 2, Name: "NAS", Kind: "smb", State: "ready"}},
		},
	}
	s := &Server{Store: store, Images: imgAPI}
	mon := NewSystemEventMonitor(s)
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

	// 1. Initial healthy tick -> 0 events
	mon.CheckOnce(context.Background(), now)
	if len(store.events) != 0 {
		t.Fatalf("expected 0 events on healthy baseline, got %+v", store.events)
	}

	// 2. Storage goes unavailable -> 1 error event
	imgAPI.catalog.Storages[0].State = "unavailable"
	imgAPI.catalog.Storages[0].Error = "mount timeout"
	mon.CheckOnce(context.Background(), now.Add(30*time.Second))
	if len(store.events) != 1 || store.events[0].Severity != "error" || store.events[0].Category != "storage" {
		t.Fatalf("expected 1 storage error event, got %+v", store.events)
	}

	// 3. Repeated tick while still unavailable -> no duplicate
	mon.CheckOnce(context.Background(), now.Add(60*time.Second))
	if len(store.events) != 1 {
		t.Fatalf("expected no duplicate event while still unavailable, got %d", len(store.events))
	}

	// 4. Storage recovers -> 1 info event
	imgAPI.catalog.Storages[0].State = "ready"
	imgAPI.catalog.Storages[0].Error = ""
	mon.CheckOnce(context.Background(), now.Add(90*time.Second))
	if len(store.events) != 2 || store.events[1].Severity != "info" || !strings.Contains(store.events[1].Title, "восстановлено") {
		t.Fatalf("expected recovery info event, got %+v", store.events)
	}
	if store.events[0].ResolvedAt == nil {
		t.Fatalf("expected storage error to be marked resolved, got nil ResolvedAt")
	}
	if active := s.activeSystemErrorCount(context.Background()); active != 0 {
		t.Fatalf("expected 0 active system errors after recovery, got %d", active)
	}

	// 5. omb-images service goes down -> 1 service error event
	imgAPI.err = errors.New("connect refused")
	mon.CheckOnce(context.Background(), now.Add(120*time.Second))
	if len(store.events) != 3 || store.events[2].Category != "service" || store.events[2].Severity != "error" {
		t.Fatalf("expected service error event, got %+v", store.events)
	}
	if active := s.activeSystemErrorCount(context.Background()); active != 1 {
		t.Fatalf("expected 1 active system error while service is down, got %d", active)
	}

	// 6. omb-images service recovers -> 1 service info event
	imgAPI.err = nil
	mon.CheckOnce(context.Background(), now.Add(150*time.Second))
	if len(store.events) != 4 || store.events[3].Category != "service" || store.events[3].Severity != "info" {
		t.Fatalf("expected service recovery event, got %+v", store.events)
	}
	if store.events[2].ResolvedAt == nil {
		t.Fatalf("expected service error to be marked resolved, got nil ResolvedAt")
	}
	if active := s.activeSystemErrorCount(context.Background()); active != 0 {
		t.Fatalf("expected 0 active system errors after service recovery, got %d", active)
	}
}

func TestSystemEventsTabAndFiltering(t *testing.T) {
	now := time.Now().UTC()
	store := &sysEventTestStore{
		authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "observer", Enabled: true}},
		events: []SystemEvent{
			{ID: 1, OccurredAt: now.Add(-10 * time.Minute), Severity: "info", Category: "storage", Title: "Хранилище «NAS» восстановлено", Link: "/storages"},
			{ID: 2, OccurredAt: now.Add(-5 * time.Minute), Severity: "error", Category: "storage", Title: "Хранилище «NAS» недоступно", Detail: "SMB timeout", Link: "/storages"},
			{ID: 3, OccurredAt: now.Add(-1 * time.Minute), Severity: "warning", Category: "network", Title: "Обнаружен DHCP-флуд от aa:bb:cc:dd:ee:ff", Link: "/events/machines/aa:bb:cc:dd:ee:ff"},
		},
	}
	s := &Server{Store: store}
	h := s.Handler()
	token := "test-token"

	// Full page with tab=system shows tab-badge with 1 recent error
	req := httptest.NewRequest("GET", "/events?tab=system", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: token})
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("GET /events?tab=system = %d", res.Code)
	}
	body := res.Body.String()
	if !strings.Contains(body, `tab-badge`) || !strings.Contains(body, `/events/content?tab=system`) {
		t.Fatalf("expected tab-badge and async system content url in /events?tab=system")
	}

	// Filtered content by sev=error&cat=storage
	req = httptest.NewRequest("GET", "/events/content?tab=system&sev=error&cat=storage", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: token})
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("GET /events/content = %d", res.Code)
	}
	body = res.Body.String()
	if !strings.Contains(body, "SMB timeout") || strings.Contains(body, "DHCP-флуд") {
		t.Fatalf("unexpected filtered system events body: %s", body)
	}

	// Partial infinite scroll fragment
	req = httptest.NewRequest("GET", "/events/content?tab=system&partial=1", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: token})
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "events-sentinel") {
		t.Fatalf("partial system events failed: %d", res.Code)
	}
}

func TestSystemEventsActiveFilterAndResolvedRendering(t *testing.T) {
	now := time.Now().UTC()
	resolvedAt := now.Add(-2 * time.Minute)
	store := &sysEventTestStore{
		authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}},
		events: []SystemEvent{
			{ID: 1, OccurredAt: now.Add(-10 * time.Minute), Severity: "error", Category: "storage", SourceKey: "storage:1", Title: "Хранилище «NAS» недоступно", ResolvedAt: &resolvedAt},
			{ID: 2, OccurredAt: now.Add(-5 * time.Minute), Severity: "error", Category: "storage", SourceKey: "storage:2", Title: "Хранилище «DSM» недоступно"}, // active (ResolvedAt == nil)
		},
	}
	s := &Server{Store: store}
	h := s.Handler()
	token := "admin-token"

	// 1. Tab badge should reflect active errors count (1, not 2)
	req := httptest.NewRequest("GET", "/events?tab=system&full=1", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: token})
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("GET /events?tab=system&full=1 = %d", res.Code)
	}
	body := res.Body.String()
	if !strings.Contains(body, `<span class="tab-badge mono">1</span>`) {
		t.Fatalf("expected tab-badge with count 1, got body: %s", body)
	}
	// Check for admin clear button and dialog
	if !strings.Contains(body, "data-system-events-clear-open") || !strings.Contains(body, "system-events-clear-dialog") {
		t.Fatalf("expected admin to see data-system-events-clear-open and system-events-clear-dialog")
	}

	// 2. Render content with sev=active: only active event (ID 2) returned
	req = httptest.NewRequest("GET", "/events/content?tab=system&sev=active", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: token})
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("GET /events/content?sev=active = %d", res.Code)
	}
	body = res.Body.String()
	if !strings.Contains(body, "Хранилище «DSM» недоступно") || strings.Contains(body, "Хранилище «NAS» недоступно") {
		t.Fatalf("sev=active filter should only show unresolved events: %s", body)
	}

	// 3. Render content with sev=all: resolved event has .row-ev-resolved and .badge-resolved
	req = httptest.NewRequest("GET", "/events/content?tab=system&sev=all", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: token})
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("GET /events/content?sev=all = %d", res.Code)
	}
	body = res.Body.String()
	if !strings.Contains(body, "row-ev-resolved") || !strings.Contains(body, "badge-resolved") || !strings.Contains(body, "✓ Исправлено") {
		t.Fatalf("expected resolved row to have row-ev-resolved and badge-resolved: %s", body)
	}
}

func TestSystemEventsClearEndpoint(t *testing.T) {
	now := time.Now().UTC()
	resolvedAt := now.Add(-2 * time.Minute)
	store := &sysEventTestStore{
		authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}},
		events: []SystemEvent{
			{ID: 1, OccurredAt: now.Add(-10 * time.Minute), Severity: "error", SourceKey: "storage:1", Title: "Хранилище «NAS» недоступно", ResolvedAt: &resolvedAt},
			{ID: 2, OccurredAt: now.Add(-5 * time.Minute), Severity: "error", SourceKey: "storage:2", Title: "Хранилище «DSM» недоступно"}, // active
		},
	}
	s := &Server{Store: store}
	h := s.Handler()
	token := "admin-token"

	// 1. Clear resolved: should remove ID 1, keep ID 2
	form := url.Values{"csrf": {CSRFToken(token)}, "scope": {"resolved"}}
	req := httptest.NewRequest("POST", "/events/system/clear", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: token})
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("POST /events/system/clear status = %d, want %d", res.Code, http.StatusSeeOther)
	}
	if len(store.events) != 1 || store.events[0].ID != 2 {
		t.Fatalf("expected only active event ID 2 to remain, got %+v", store.events)
	}

	// 2. Clear all: should remove everything
	form = url.Values{"csrf": {CSRFToken(token)}, "scope": {"all"}}
	req = httptest.NewRequest("POST", "/events/system/clear", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: token})
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("POST /events/system/clear all status = %d, want %d", res.Code, http.StatusSeeOther)
	}
	if len(store.events) != 0 {
		t.Fatalf("expected 0 events after clear all, got %d", len(store.events))
	}
}

type testAgentRT func(*http.Request) (*http.Response, error)

func (f testAgentRT) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSystemEventMonitorCheckAgentUsesStatusEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"applied":false}`))
	})
	mux.HandleFunc("GET /config", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	client := &http.Client{Transport: testAgentRT(func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Result(), nil
	})}

	store := &sysEventTestStore{}
	srv := &Server{
		Store: store,
		Agent: &AgentClient{http: client},
	}
	mon := NewSystemEventMonitor(srv)
	mon.CheckOnce(context.Background(), time.Now())

	if len(store.events) != 0 {
		t.Fatalf("expected 0 errors when agent /status is 200 and /config is 404, got: %+v", store.events)
	}
}
