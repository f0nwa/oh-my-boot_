package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"omb/internal/drivers"
)

type countingAuthorsStore struct {
	authOnlyStore
	calls    atomic.Int64
	authors  ResourceAuthors
	packages []DriverPackage
	configs  []DriverConfig
}

func (c *countingAuthorsStore) ResourceAuthors(context.Context) (ResourceAuthors, error) {
	c.calls.Add(1)
	return c.authors, nil
}

func (c *countingAuthorsStore) DriverPackages(context.Context) ([]DriverPackage, error) {
	return c.packages, nil
}

func (c *countingAuthorsStore) DriverConfigs(context.Context) ([]DriverConfig, error) {
	return c.configs, nil
}

func (c *countingAuthorsStore) WindowsProfiles(context.Context) ([]WindowsProfile, error) {
	return []WindowsProfile{}, nil
}

func (c *countingAuthorsStore) WindowsAnswers(context.Context) ([]WindowsAnswerVariant, error) {
	return []WindowsAnswerVariant{}, nil
}

func (c *countingAuthorsStore) CreateDriverPackage(context.Context, drivers.Inventory, string, string) (bool, error) {
	return true, nil
}

func (c *countingAuthorsStore) Audit(context.Context, int64, string, string, string) error {
	return nil
}

func TestResourceAuthorsCacheHit(t *testing.T) {
	store := &countingAuthorsStore{
		authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}},
		authors: ResourceAuthors{
			Installations:   map[int64]string{10: "alice"},
			Answers:         map[int64]string{20: "bob"},
			UbuntuAnswers:   map[int64]string{30: "ubuntu-creator"},
			Images:          map[string]string{"1:test.iso": "carol"},
			ImageUploadedAt: map[string]time.Time{"1:test.iso": time.Now()},
			Drivers:         map[string]string{"abc": "dave"},
			DriverConfigs:   map[int64]string{40: "eve"},
		},
	}
	s := &Server{Store: store}

	ctx := context.Background()

	// 1st call: should hit store
	first := s.resourceAuthors(ctx)
	if calls := store.calls.Load(); calls != 1 {
		t.Fatalf("expected 1 store call, got %d", calls)
	}
	if first.Installations[10] != "alice" || first.Drivers["abc"] != "dave" || first.UbuntuAnswers[30] != "ubuntu-creator" || first.DriverConfigs[40] != "eve" {
		t.Fatalf("unexpected first authors: %+v", first)
	}

	first.UbuntuAnswers[30] = "changed outside cache"
	first.DriverConfigs[40] = "mutated"
	// 2nd call: should hit cache without calling store again
	second := s.resourceAuthors(ctx)
	if calls := store.calls.Load(); calls != 1 {
		t.Fatalf("expected store calls to remain 1 (cache hit), got %d", calls)
	}
	if second.Installations[10] != "alice" || second.Drivers["abc"] != "dave" || second.UbuntuAnswers[30] != "ubuntu-creator" || second.DriverConfigs[40] != "eve" {
		t.Fatalf("unexpected cached authors: %+v", second)
	}
}

func TestResourceAuthorsCacheInvalidation(t *testing.T) {
	store := &countingAuthorsStore{
		authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}},
		authors: ResourceAuthors{
			Installations: map[int64]string{10: "initial"},
			Drivers:       map[string]string{"abc": "initial"},
		},
	}
	s := &Server{
		Store:          store,
		DriverPackages: &fakeDriverAgent{},
	}
	ctx := context.Background()

	// Initial load -> store called once
	_ = s.resourceAuthors(ctx)
	if calls := store.calls.Load(); calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}

	// Invalidate via Server method
	store.authors.Installations[10] = "after_server_invalidate"
	s.InvalidateResourceAuthorsCache()
	afterMethod := s.resourceAuthors(ctx)
	if calls := store.calls.Load(); calls != 2 {
		t.Fatalf("expected 2 calls after s.InvalidateResourceAuthorsCache(), got %d", calls)
	}
	if afterMethod.Installations[10] != "after_server_invalidate" {
		t.Fatalf("expected updated value, got %s", afterMethod.Installations[10])
	}

	// Invalidate via package-level function (works on defaultResourceAuthorsCache)
	defaultResourceAuthorsCache.set(ResourceAuthors{Installations: map[int64]string{1: "test"}}, time.Now())
	if _, ok := defaultResourceAuthorsCache.get(time.Now()); !ok {
		t.Fatalf("expected default cache to have entry")
	}
	InvalidateResourceAuthorsCache()
	if _, ok := defaultResourceAuthorsCache.get(time.Now()); ok {
		t.Fatalf("expected default cache to be invalidated")
	}

	// Invalidate via driver import endpoint
	_ = s.resourceAuthors(ctx) // prime s.authorsCache
	callsBeforeImport := store.calls.Load()
	importReq := testDriverUpload(t, "zip", CSRFToken("test-token"), "driver.zip", oneDriverZIP(t))
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, importReq)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("import failed: %d %s", res.Code, res.Body.String())
	}

	// resourceAuthors should now re-fetch because driver.import invalidated cache
	_ = s.resourceAuthors(ctx)
	if calls := store.calls.Load(); calls <= callsBeforeImport {
		t.Fatalf("expected cache to be invalidated by driver import, calls before=%d, after=%d", callsBeforeImport, calls)
	}
}

func TestDriversPageHasNoDetails(t *testing.T) {
	// 1. Check embedded raw template
	rawTemplate, err := templateFS.ReadFile("templates/drivers.html")
	if err != nil {
		t.Fatalf("failed to read templates/drivers.html: %v", err)
	}
	rawStr := string(rawTemplate)
	if strings.Contains(rawStr, "<details") || strings.Contains(rawStr, "<summary") {
		t.Fatalf("templates/drivers.html contains forbidden <details> or <summary> tags: %s", rawStr)
	}

	// 2. Check rendered page as admin (where config deletion and packages are rendered)
	store := &countingAuthorsStore{
		authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}},
		packages: []DriverPackage{
			{
				SHA256: "pkg-sha-12345",
				Author: "admin",
				Inventory: drivers.Inventory{
					SHA256: "pkg-sha-12345",
					Files:  []drivers.File{{Path: "net.inf"}, {Path: "net.sys"}, {Path: "net.cat"}},
					INFs: []drivers.INF{
						{Path: "net.inf", Architecture: "amd64", Class: "Net", HardwareIDs: []string{"PCI\\VEN_8086&DEV_100E"}},
					},
				},
			},
		},
		configs: []DriverConfig{
			{
				ID:        1,
				Title:     "Config 1",
				Folder:    "Windows",
				ProfileID: 10,
				State:     "ready",
			},
		},
	}
	s := &Server{Store: store}
	req := httptest.NewRequest(http.MethodGet, "/drivers", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("GET /drivers status = %d: %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	mainStart := strings.Index(body, "<main")
	if mainStart == -1 {
		t.Fatalf("missing <main> in rendered page")
	}
	mainBody := body[mainStart:]

	if strings.Contains(mainBody, "<details") || strings.Contains(mainBody, "<summary") {
		t.Fatalf("rendered /drivers page contains forbidden <details> or <summary> tags in main: %s", mainBody)
	}

	// Verify dialogs and chips are present
	if !strings.Contains(mainBody, `id="driver-delete-dialog-1"`) {
		t.Fatalf("missing driver-delete-dialog-1 in rendered page: %s", mainBody)
	}
	if !strings.Contains(mainBody, `id="driver-package-dialog-pkg-sha-12345"`) {
		t.Fatalf("missing driver-package-dialog in rendered page: %s", mainBody)
	}
	if !strings.Contains(mainBody, `1 INF`) {
		t.Fatalf("missing INF chip in rendered page: %s", mainBody)
	}
}
