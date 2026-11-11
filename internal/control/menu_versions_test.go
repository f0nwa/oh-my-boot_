package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type menuVersionsMockStore struct {
	authOnlyStore
	versions []MenuVersion
}

func (m *menuVersionsMockStore) MenuVersions(ctx context.Context) ([]MenuVersion, error) {
	versions := append([]MenuVersion(nil), m.versions...)
	for i := range versions {
		if versions[i].SHA256 == "" {
			sum := sha256.Sum256([]byte(versions[i].Script))
			versions[i].SHA256 = hex.EncodeToString(sum[:])
		}
	}
	return versions, nil
}

func (m *menuVersionsMockStore) MenuDraft(ctx context.Context) (MenuDraft, bool, error) {
	return MenuDraft{Title: "OMB Menu"}, true, nil
}

func TestMenuPageDoesNotEvaluateAllVersions(t *testing.T) {
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	// Version 1 has an unresolvable item (installation 999999).
	// Version 2 has an unresolvable item.
	// Version 3 is active and clean.
	versions := []MenuVersion{
		{ID: 1, Title: "Broken v1", Script: "#!ipxe\nitem install_999999 Broken 1\n", At: now.Add(-2 * time.Hour), Items: []MenuVersionItem{{InstallationID: 999999, Title: "Broken 1"}}},
		{ID: 2, Title: "Broken v2", Script: "#!ipxe\nitem install_888888 Broken 2\n", At: now.Add(-1 * time.Hour), Items: []MenuVersionItem{{InstallationID: 888888, Title: "Broken 2"}}},
		{ID: 3, Title: "Active v3", Script: "#!ipxe\n", At: now, Active: true},
	}
	store := &menuVersionsMockStore{
		authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}},
		versions:      versions,
	}
	server := &Server{Store: store}

	req := httptest.NewRequest(http.MethodGet, "/menu", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("GET /menu returned status %d", res.Code)
	}

	body := res.Body.String()

	// Menu page should show the version history button with the versions count.
	if !strings.Contains(body, "data-menu-versions-open") {
		t.Fatalf("expected button[data-menu-versions-open] in /menu body")
	}
	if !strings.Contains(body, `<span class="chip mono">3</span>`) {
		t.Fatalf("expected versions count 3 in button chip: %s", body)
	}

	// Menu page should have the dialog with data-async-url pointing to /menu/versions.
	if !strings.Contains(body, `id="menu-versions-dialog"`) {
		t.Fatalf("expected id=\"menu-versions-dialog\" in /menu body")
	}
	if !strings.Contains(body, `data-async-url="/menu/versions"`) {
		t.Fatalf("expected data-async-url=\"/menu/versions\" in /menu body")
	}

	// Menu page must NOT render archive version rows or "Удалённый пункт".
	if strings.Contains(body, "#1") || strings.Contains(body, "#2") {
		t.Fatalf("/menu should not render individual archive version numbers: %s", body)
	}
	if strings.Contains(body, "Удалённый пункт") {
		t.Fatalf("/menu should not evaluate or display unrestorable reason 'Удалённый пункт': %s", body)
	}
	if strings.Contains(body, `action="/menu/rollback"`) {
		t.Fatalf("/menu should not render rollback forms: %s", body)
	}
}

func TestMenuVersionsEndpoint(t *testing.T) {
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	versions := []MenuVersion{
		{ID: 1, Title: "Broken v1", Script: "#!ipxe\nitem install_999999 Broken 1\n", At: now.Add(-2 * time.Hour), Items: []MenuVersionItem{{InstallationID: 999999, Title: "Broken 1"}}},
		{ID: 2, Title: "Active v2", Script: "#!ipxe\n", At: now, Active: true},
	}

	// Test 1: Admin role receives table with versions and action forms.
	storeAdmin := &menuVersionsMockStore{
		authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}},
		versions:      versions,
	}
	serverAdmin := &Server{Store: storeAdmin}

	req := httptest.NewRequest(http.MethodGet, "/menu/versions", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	serverAdmin.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("GET /menu/versions returned status %d", res.Code)
	}
	bodyAdmin := res.Body.String()

	if !strings.Contains(bodyAdmin, "#1") || !strings.Contains(bodyAdmin, "#2") {
		t.Fatalf("GET /menu/versions should render versions #1 and #2: %s", bodyAdmin)
	}
	if !strings.Contains(bodyAdmin, "Активна") {
		t.Fatalf("expected active version to be marked 'Активна': %s", bodyAdmin)
	}
	if !strings.Contains(bodyAdmin, "Удалённый пункт") {
		t.Fatalf("expected broken version to have 'Удалённый пункт': %s", bodyAdmin)
	}

	// Test 2: Observer role does not see rollback actions.
	storeObserver := &menuVersionsMockStore{
		authOnlyStore: authOnlyStore{user: User{ID: 2, Role: "observer", Enabled: true}},
		versions:      versions,
	}
	serverObserver := &Server{Store: storeObserver}

	reqObs := httptest.NewRequest(http.MethodGet, "/menu/versions", nil)
	reqObs.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	resObs := httptest.NewRecorder()
	serverObserver.Handler().ServeHTTP(resObs, reqObs)

	if resObs.Code != http.StatusOK {
		t.Fatalf("GET /menu/versions for observer returned status %d", resObs.Code)
	}
	bodyObs := resObs.Body.String()
	if strings.Contains(bodyObs, `action="/menu/rollback"`) || strings.Contains(bodyObs, "Действия") {
		t.Fatalf("observer should not see rollback actions or column: %s", bodyObs)
	}
}

func TestMenuVersionsShowsCompactSHA(t *testing.T) {
	digest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	store := &menuVersionsMockStore{
		authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}},
		versions:      []MenuVersion{{ID: 1, Title: "OMB Menu", SHA256: digest, At: time.Now(), Items: []MenuVersionItem{}}},
	}
	req := httptest.NewRequest(http.MethodGet, "/menu/versions", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	(&Server{Store: store}).Handler().ServeHTTP(res, req)
	body := res.Body.String()
	if res.Code != http.StatusOK || !strings.Contains(body, `title="`+digest+`"`) || !strings.Contains(body, `0123…cdef`) {
		t.Fatalf("expected compact SHA with full value in title: status %d, body %s", res.Code, body)
	}
}

func TestMenuPageHasNoDetails(t *testing.T) {
	data := samplePageData(langRU)
	data.Changes = []MenuChange{
		{Kind: "add", Key: "install_101", Title: "Ubuntu 24.04"},
		{Kind: "remove", Key: "install_102", Title: "Old Windows"},
	}
	data.VersionsCount = 7

	body := renderPage("menu", data)

	if strings.Contains(body, "<details") || strings.Contains(body, "</details>") {
		t.Fatalf("menu page must not contain <details>: %s", body)
	}
	if strings.Contains(body, "<summary") || strings.Contains(body, "</summary>") {
		t.Fatalf("menu page must not contain <summary>: %s", body)
	}

	// Must have the changes card without details.
	if !strings.Contains(body, "menu-changes-card") || !strings.Contains(body, `id="menu-changes"`) {
		t.Fatalf("menu page should contain changes card with id=\"menu-changes\": %s", body)
	}

	// Must have the versions button and dialog.
	if !strings.Contains(body, "data-menu-versions-open") {
		t.Fatalf("menu page lacks data-menu-versions-open: %s", body)
	}
	if !strings.Contains(body, `id="menu-versions-dialog"`) {
		t.Fatalf("menu page lacks id=\"menu-versions-dialog\": %s", body)
	}
	if !strings.Contains(body, `data-async-url="/menu/versions"`) {
		t.Fatalf("menu page lacks data-async-url=\"/menu/versions\": %s", body)
	}
}
