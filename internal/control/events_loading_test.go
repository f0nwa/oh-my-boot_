package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"omb/internal/agent"
	"omb/internal/boot"
	"omb/internal/images"
)

type loadingMenuStore struct {
	authOnlyStore
	reads atomic.Int32
}

func (s *loadingMenuStore) MenuVersions(context.Context) ([]MenuVersion, error) {
	s.reads.Add(1)
	return nil, nil
}
func (s *loadingMenuStore) MenuDraft(context.Context) (MenuDraft, bool, error) {
	return MenuDraft{}, false, nil
}
func (s *loadingMenuStore) UbuntuAutoinstall(context.Context, int64) (UbuntuAutoinstallFile, error) {
	return UbuntuAutoinstallFile{UserData: []byte("#cloud-config\n")}, nil
}

func loadingStore(role string) *loadingMenuStore {
	return &loadingMenuStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: role, Enabled: true}}}
}

func TestEventsShellDoesNotReadSources(t *testing.T) {
	var calls atomic.Int32
	api := ueficaAgentClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	for _, role := range []string{"admin", "operator", "observer"} {
		s := &Server{Store: loadingStore(role), Boot: &BootClient{http: api.http}, Agent: api}
		body := libraryGet(t, s, "/events?tab=requests&mac=bc:24:11:04:2a:ed")
		if calls.Load() != 0 {
			t.Fatalf("shell waited for %d source requests", calls.Load())
		}
		if !strings.Contains(body, `id="events-content"`) || !strings.Contains(body, `/assets/events.js`) {
			t.Fatalf("%s shell has no async content loader", role)
		}
	}
}

func TestEventsHTTPContentDoesNotReadDHCP(t *testing.T) {
	var agentCalls atomic.Int32
	agent := ueficaAgentClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agentCalls.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	api := ueficaAgentClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" {
			t.Errorf("unexpected Boot source %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[{"at":"2026-11-10T12:00:00Z","kind":"menu","path":"/wanted<script>","mac":"bc:24:11:04:2a:ed"},{"at":"2026-11-10T12:00:01Z","kind":"menu","path":"/other","mac":"bc:24:11:04:2a:ee"}]`)
	}))
	s := &Server{Store: loadingStore("observer"), Boot: &BootClient{http: api.http}, Agent: agent}
	body := libraryGet(t, s, "/events/content?tab=requests&mac=bc:24:11:04:2a:ed")
	if agentCalls.Load() != 0 || !strings.Contains(body, "/wanted&lt;script&gt;") || strings.Contains(body, "/other") {
		t.Fatalf("HTTP content calls=%d body=%s", agentCalls.Load(), body)
	}
}

func TestBackgroundGETSkipsMenuSummary(t *testing.T) {
	for _, path := range []string{"/api/system/metrics", "/events/services", "/events/content?tab=requests", "/images/storages/1/folders", "/ubuntu-autoinstall/1/download"} {
		store := loadingStore("observer")
		s := &Server{Store: store, Images: &fakeImagesAPI{catalog: images.Catalog{
			Storages: []images.Storage{{ID: 1, Kind: "local", Uploads: true, State: "ready"}},
		}}}
		libraryGet(t, s, path)
		if n := store.reads.Load(); n != 0 {
			t.Fatalf("%s reads menu history %d times", path, n)
		}
	}
}

func TestHTMLMenuSummaryReadsVersionsOnce(t *testing.T) {
	store := loadingStore("observer")
	libraryGet(t, &Server{Store: store}, "/account")
	if got := store.reads.Load(); got != 1 {
		t.Fatalf("HTML summary reads=%d want=1", got)
	}
}

func TestEventsDHCPSurvivesSlowBootEvents(t *testing.T) {
	api := ueficaAgentClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/events":
			<-r.Context().Done()
		case "/journal/settings":
			fmt.Fprint(w, `{}`)
		case "/dhcp-events":
			fmt.Fprint(w, `[{"at":"2026-11-10T12:00:00Z","kind":"PXE","mac":"bc:24:11:04:2a:ed","detail":"healthy-dhcp"}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	s := &Server{Store: loadingStore("observer"), Boot: &BootClient{http: api.http}, Agent: api}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/events/content?tab=dhcp", nil).WithContext(ctx)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if !strings.Contains(res.Body.String(), "healthy-dhcp") || strings.Contains(res.Body.String(), "Журнал DHCP недоступен") {
		t.Fatalf("healthy DHCP blocked by Boot: %s", res.Body)
	}
}

func TestDHCPDoesNotShowClearedRowsWhenBoundaryUnavailable(t *testing.T) {
	api := ueficaAgentClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dhcp-events" {
			fmt.Fprint(w, `[{"at":"2026-11-10T12:00:00Z","kind":"PXE","mac":"bc:24:11:04:2a:ed"}]`)
			return
		}
		http.Error(w, "Boot unavailable", http.StatusServiceUnavailable)
	}))
	s := &Server{Boot: &BootClient{http: api.http}, Agent: api}
	if events, err := s.dhcpEvents(context.Background()); err == nil || len(events) != 0 {
		t.Fatalf("unknown clear boundary exposed old rows: events=%+v err=%v", events, err)
	}
}

func TestEventsJournalFragmentRoleRights(t *testing.T) {
	boot := journalBoot(t, &journalStore{})
	for _, role := range []string{"admin", "operator", "observer"} {
		s := &Server{Store: loadingStore(role), Boot: boot}
		req := httptest.NewRequest(http.MethodGet, "/events/journal", nil)
		req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
		res := httptest.NewRecorder()
		s.Handler().ServeHTTP(res, req)
		if role == "admin" {
			if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `action="/events/retention"`) {
				t.Fatalf("admin metadata lacks retention form: %d %s", res.Code, res.Body)
			}
		} else if res.Code != http.StatusForbidden || strings.Contains(res.Body.String(), "journal-days") {
			t.Fatalf("%s accesses admin metadata: %d %s", role, res.Code, res.Body)
		}
	}
}

func TestEventsPaginationRequests(t *testing.T) {
	t0 := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	allEvents := make([]boot.Event, 120)
	for i := 0; i < 120; i++ {
		allEvents[i] = boot.Event{
			ID:         int64(120 - i),
			At:         t0.Add(-time.Duration(i) * time.Minute),
			Kind:       "menu",
			Path:       fmt.Sprintf("/boot/req-%03d", i+1),
			RemoteAddr: "192.168.1.50:12345",
			MAC:        "aa:bb:cc:dd:ee:01",
		}
	}

	api := ueficaAgentClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		limit := 50
		if l, err := strconv.Atoi(q.Get("limit")); err == nil && l > 0 {
			limit = l
		}
		var beforeTime *time.Time
		if bt := q.Get("before_time"); bt != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, bt); err == nil {
				beforeTime = &parsed
			}
		}
		var beforeID int64
		if bid := q.Get("before_id"); bid != "" {
			beforeID, _ = strconv.ParseInt(bid, 10, 64)
		}

		var matched []boot.Event
		for _, e := range allEvents {
			if beforeTime != nil {
				if e.At.After(*beforeTime) {
					continue
				}
				if e.At.Equal(*beforeTime) && beforeID > 0 && e.ID >= beforeID {
					continue
				}
			}
			matched = append(matched, e)
			if len(matched) == limit {
				break
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(matched)
	}))

	store := loadingStore("observer")
	s := &Server{Store: store, Boot: &BootClient{http: api.http}, Agent: api}

	// 1. Initial request GET /events/content?tab=requests
	body1 := libraryGet(t, s, "/events/content?tab=requests")
	if !strings.Contains(body1, "/boot/req-001") {
		t.Errorf("initial requests missing /boot/req-001")
	}
	if !strings.Contains(body1, "/boot/req-050") {
		t.Errorf("initial requests missing /boot/req-050")
	}
	if strings.Contains(body1, "/boot/req-051") {
		t.Errorf("initial requests should not contain /boot/req-051")
	}
	if !strings.Contains(body1, `id="events-sentinel"`) {
		t.Errorf("missing events-sentinel")
	}
	if !strings.Contains(body1, `data-has-more="true"`) {
		t.Errorf("expected data-has-more=\"true\"")
	}
	event50 := allEvents[49]
	wantTime := event50.At.Format(time.RFC3339Nano)
	wantID := strconv.FormatInt(event50.ID, 10)
	if !strings.Contains(body1, `data-before-time="`+wantTime+`"`) {
		t.Errorf("expected data-before-time=%q in body", wantTime)
	}
	if !strings.Contains(body1, `data-before-id="`+wantID+`"`) {
		t.Errorf("expected data-before-id=%q in body", wantID)
	}

	// 2. Second request with partial=1, before_time and before_id
	url2 := fmt.Sprintf("/events/content?tab=requests&partial=1&before_time=%s&before_id=%s", url.QueryEscape(wantTime), wantID)
	body2 := libraryGet(t, s, url2)
	if strings.Contains(body2, "<table") || strings.Contains(body2, "<thead") {
		t.Errorf("partial should not render full table structure")
	}
	if strings.Contains(body2, "/boot/req-001") || strings.Contains(body2, "/boot/req-050") {
		t.Errorf("partial should not repeat req-001..req-050")
	}
	if !strings.Contains(body2, "/boot/req-051") {
		t.Errorf("partial missing /boot/req-051")
	}
	if !strings.Contains(body2, "/boot/req-100") {
		t.Errorf("partial missing /boot/req-100")
	}
	if strings.Contains(body2, "/boot/req-101") {
		t.Errorf("partial should not contain /boot/req-101")
	}
	if !strings.Contains(body2, `data-has-more="true"`) {
		t.Errorf("expected data-has-more=\"true\" for second page")
	}
	event100 := allEvents[99]
	wantTime2 := event100.At.Format(time.RFC3339Nano)
	wantID2 := strconv.FormatInt(event100.ID, 10)
	if !strings.Contains(body2, `data-before-time="`+wantTime2+`"`) {
		t.Errorf("expected data-before-time=%q in body2", wantTime2)
	}
	if !strings.Contains(body2, `data-before-id="`+wantID2+`"`) {
		t.Errorf("expected data-before-id=%q in body2", wantID2)
	}

	// 3. Third request: final 20 items
	url3 := fmt.Sprintf("/events/content?tab=requests&partial=1&before_time=%s&before_id=%s", url.QueryEscape(wantTime2), wantID2)
	body3 := libraryGet(t, s, url3)
	if !strings.Contains(body3, "/boot/req-101") || !strings.Contains(body3, "/boot/req-120") {
		t.Errorf("partial missing req-101..req-120")
	}
	if !strings.Contains(body3, `data-has-more="false"`) {
		t.Errorf("expected data-has-more=\"false\" for final page")
	}
}

func TestEventsPaginationDHCP(t *testing.T) {
	t0 := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	allDHCP := make([]agent.DHCPEvent, 120)
	for i := 0; i < 120; i++ {
		allDHCP[i] = agent.DHCPEvent{
			At:     t0.Add(-time.Duration(i) * time.Minute),
			Kind:   "dhcp-ack",
			MAC:    fmt.Sprintf("aa:bb:cc:00:%02x:%02x", i/256, i%256),
			IP:     fmt.Sprintf("192.168.1.%d", (i%200)+10),
			Detail: fmt.Sprintf("dhcp-lease-%03d", i+1),
		}
	}

	api := ueficaAgentClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/events":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[]`))
		case "/journal/settings":
			fmt.Fprint(w, `{}`)
		case "/dhcp-events":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(allDHCP)
		default:
			http.NotFound(w, r)
		}
	}))

	store := loadingStore("observer")
	s := &Server{Store: store, Boot: &BootClient{http: api.http}, Agent: api}

	// 1. Initial request GET /events/content?tab=dhcp
	body1 := libraryGet(t, s, "/events/content?tab=dhcp")
	if !strings.Contains(body1, "dhcp-lease-001") {
		t.Errorf("initial dhcp missing dhcp-lease-001")
	}
	if !strings.Contains(body1, "dhcp-lease-050") {
		t.Errorf("initial dhcp missing dhcp-lease-050")
	}
	if strings.Contains(body1, "dhcp-lease-051") {
		t.Errorf("initial dhcp should not contain dhcp-lease-051")
	}
	if !strings.Contains(body1, `id="events-sentinel"`) {
		t.Errorf("missing events-sentinel")
	}
	if !strings.Contains(body1, `data-has-more="true"`) {
		t.Errorf("expected data-has-more=\"true\"")
	}
	if !strings.Contains(body1, `data-next-offset="50"`) {
		t.Errorf("expected data-next-offset=\"50\"")
	}

	// 2. Second request with partial=1 and offset=50
	body2 := libraryGet(t, s, "/events/content?tab=dhcp&partial=1&offset=50")
	if strings.Contains(body2, "<table") || strings.Contains(body2, "<thead") {
		t.Errorf("partial should not render full table structure")
	}
	if strings.Contains(body2, "dhcp-lease-001") || strings.Contains(body2, "dhcp-lease-050") {
		t.Errorf("partial should not repeat dhcp-lease-001..dhcp-lease-050")
	}
	if !strings.Contains(body2, "dhcp-lease-051") {
		t.Errorf("partial missing dhcp-lease-051")
	}
	if !strings.Contains(body2, "dhcp-lease-100") {
		t.Errorf("partial missing dhcp-lease-100")
	}
	if strings.Contains(body2, "dhcp-lease-101") {
		t.Errorf("partial should not contain dhcp-lease-101")
	}
	if !strings.Contains(body2, `data-has-more="true"`) {
		t.Errorf("expected data-has-more=\"true\" for second page")
	}
	if !strings.Contains(body2, `data-next-offset="100"`) {
		t.Errorf("expected data-next-offset=\"100\"")
	}

	// 3. Third request: final 20 items with offset=100
	body3 := libraryGet(t, s, "/events/content?tab=dhcp&partial=1&offset=100")
	if !strings.Contains(body3, "dhcp-lease-101") || !strings.Contains(body3, "dhcp-lease-120") {
		t.Errorf("partial missing dhcp-lease-101..dhcp-lease-120")
	}
	if !strings.Contains(body3, `data-has-more="false"`) {
		t.Errorf("expected data-has-more=\"false\" for final page")
	}
}

func TestEventsPaginationPartialErrors(t *testing.T) {
	api := ueficaAgentClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	s := &Server{
		Store: loadingStore("admin"),
		Boot:  &BootClient{http: api.http},
		Agent: api,
	}

	// Requests tab error on partial returns 503
	req := httptest.NewRequest("GET", "/events/content?tab=requests&partial=1", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on requests partial error, got %d", rr.Code)
	}

	// DHCP tab error on partial returns 503
	reqDHCP := httptest.NewRequest("GET", "/events/content?tab=dhcp&partial=1", nil)
	reqDHCP.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	rrDHCP := httptest.NewRecorder()
	s.Handler().ServeHTTP(rrDHCP, reqDHCP)
	if rrDHCP.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on dhcp partial error, got %d", rrDHCP.Code)
	}
}
