package control

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
		if filter.Severity != "" && filter.Severity != "all" && ev.Severity != filter.Severity {
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

func (m *sysEventTestStore) PruneSystemEvents(_ context.Context, _ time.Time) error {
	m.pruned++
	return nil
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

	// 5. omb-images service goes down -> 1 service error event
	imgAPI.err = errors.New("connect refused")
	mon.CheckOnce(context.Background(), now.Add(120*time.Second))
	if len(store.events) != 3 || store.events[2].Category != "service" || store.events[2].Severity != "error" {
		t.Fatalf("expected service error event, got %+v", store.events)
	}

	// 6. omb-images service recovers -> 1 service info event
	imgAPI.err = nil
	mon.CheckOnce(context.Background(), now.Add(150*time.Second))
	if len(store.events) != 4 || store.events[3].Category != "service" || store.events[3].Severity != "info" {
		t.Fatalf("expected service recovery event, got %+v", store.events)
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
