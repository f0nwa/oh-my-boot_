package control

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
