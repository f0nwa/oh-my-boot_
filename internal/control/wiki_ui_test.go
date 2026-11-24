package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWikiRoutesAndAuth(t *testing.T) {
	store := &authOnlyStore{user: User{ID: 1, Username: "observer", Role: "observer", Enabled: true, Language: langRU}}
	srv := &Server{Store: store}
	h := srv.Handler()

	// Unauthenticated request redirects to /login.
	req := httptest.NewRequest(http.MethodGet, "/wiki", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("unauthenticated /wiki = %d (%s), want 303 -> /login", rec.Code, rec.Header().Get("Location"))
	}

	cookie := &http.Cookie{Name: "omb_session", Value: "test-token"}

	for _, tc := range []struct {
		path   string
		status int
		want   []string
	}{
		{"/wiki", http.StatusOK, []string{"База знаний oh my boot_", "wiki-cards", "/wiki/overview", "/wiki/project-history", `class="wiki-top-btn active"`}},
		{"/wiki?q=Secure+Boot", http.StatusOK, []string{"wiki-hit-card", "/wiki/secure-boot"}},
		{"/wiki/services-and-databases", http.StatusOK, []string{"Службы, Unix-сокеты и базы данных", "wiki-toc", "wiki-pager", "/assets/wiki.js", `class="wiki-top-btn active"`}},
		{"/wiki/no-such-article", http.StatusNotFound, []string{"Статья не найдена", "/wiki"}},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.status {
			t.Fatalf("GET %s: status %d, want %d", tc.path, rec.Code, tc.status)
		}
		body := rec.Body.String()
		for _, w := range tc.want {
			if !strings.Contains(body, w) {
				t.Errorf("GET %s: response missing %q", tc.path, w)
			}
		}
		if strings.Contains(body, "<details") && !strings.Contains(body, "draft-discard") {
			t.Errorf("GET %s: unexpected <details> tag", tc.path)
		}
	}
}

func TestWikiSearchInCmdKAndScriptEndpoint(t *testing.T) {
	store := &authOnlyStore{user: User{ID: 1, Username: "admin", Role: "admin", Enabled: true, Language: langRU}}
	srv := &Server{Store: store}
	h := srv.Handler()
	cookie := &http.Cookie{Name: "omb_session", Value: "test-token"}

	// /search?format=json returns Wiki hits.
	req := httptest.NewRequest(http.MethodGet, "/search?format=json&q=Secure+Boot", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /search: status %d", rec.Code)
	}
	var results []searchResult
	if err := json.Unmarshal(rec.Body.Bytes(), &results); err != nil {
		t.Fatalf("unmarshal /search: %v", err)
	}
	foundWiki := false
	for _, r := range results {
		if r.Kind == "Wiki" && r.Href == "/wiki/secure-boot" {
			foundWiki = true
			break
		}
	}
	if !foundWiki {
		t.Fatalf("expected Wiki hit /wiki/secure-boot in ⌘K results, got %+v", results)
	}

	// /assets/wiki.js is served as JS.
	reqJS := httptest.NewRequest(http.MethodGet, "/assets/wiki.js", nil)
	recJS := httptest.NewRecorder()
	h.ServeHTTP(recJS, reqJS)
	if recJS.Code != http.StatusOK || !strings.Contains(recJS.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("GET /assets/wiki.js = %d (%s)", recJS.Code, recJS.Header().Get("Content-Type"))
	}
}

func TestWikiEnglishModeRendersEnglishCatalogWithoutBanner(t *testing.T) {
	store := &authOnlyStore{user: User{ID: 1, Username: "admin", Role: "admin", Enabled: true, Language: langEN}}
	srv := &Server{Store: store}
	h := srv.Handler()
	cookie := &http.Cookie{Name: "omb_session", Value: "test-token"}

	// /wiki landing page in English
	req := httptest.NewRequest(http.MethodGet, "/wiki", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /wiki (EN) = %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "wiki-lang-note") || strings.Contains(body, "currently available in Russian") {
		t.Errorf("EN /wiki must not show obsolete Russian-only banner")
	}
	for _, want := range []string{"oh my boot_ Knowledge Base", "Getting Started", "What is oh my boot_", "Offline Installation, .deb Packages, Channels, and Recovery"} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /wiki (EN) missing %q", want)
		}
	}

	// /wiki/{slug} article page in English
	reqArt := httptest.NewRequest(http.MethodGet, "/wiki/services-and-databases", nil)
	reqArt.AddCookie(cookie)
	recArt := httptest.NewRecorder()
	h.ServeHTTP(recArt, reqArt)
	if recArt.Code != http.StatusOK {
		t.Fatalf("GET /wiki/services-and-databases (EN) = %d", recArt.Code)
	}
	artBody := recArt.Body.String()
	for _, want := range []string{"Services, Unix Sockets, and Databases", "<strong>Important:</strong>", `data-wiki-copy>Copy</button>`} {
		if !strings.Contains(artBody, want) {
			t.Errorf("GET /wiki/services-and-databases (EN) missing %q", want)
		}
	}

	// ⌘K search returns English Wiki titles and sections
	reqSearch := httptest.NewRequest(http.MethodGet, "/search?format=json&q=Secure+Boot", nil)
	reqSearch.AddCookie(cookie)
	recSearch := httptest.NewRecorder()
	h.ServeHTTP(recSearch, reqSearch)
	if recSearch.Code != http.StatusOK {
		t.Fatalf("GET /search (EN) = %d", recSearch.Code)
	}
	var results []searchResult
	if err := json.Unmarshal(recSearch.Body.Bytes(), &results); err != nil {
		t.Fatalf("unmarshal /search (EN): %v", err)
	}
	foundEN := false
	for _, r := range results {
		if r.Kind == "Wiki" && r.Href == "/wiki/secure-boot" && r.Title == "Secure Boot, iPXE shim 16.1, and UEFI CA Keys" {
			foundEN = true
			break
		}
	}
	if !foundEN {
		t.Fatalf("expected English Wiki hit in ⌘K results, got %+v", results)
	}
}

func TestWikiHomeCenteredThreeByTwoLayout(t *testing.T) {
	store := &authOnlyStore{user: User{ID: 1, Username: "admin", Role: "admin", Enabled: true, Language: langRU}}
	srv := &Server{Store: store}
	h := srv.Handler()
	cookie := &http.Cookie{Name: "omb_session", Value: "test-token"}

	req := httptest.NewRequest(http.MethodGet, "/wiki", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /wiki = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `class="wiki-home-title"`) {
		t.Errorf("GET /wiki missing wiki-home-title header wrapper")
	}

	data, err := templateFS.ReadFile("templates/style.css")
	if err != nil {
		t.Fatalf("read style.css: %v", err)
	}
	css := string(data)
	for _, want := range []string{
		`.wiki-home{max-width:1200px;margin:0 auto}`,
		`.wiki-cards{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:16px}`,
		`@media (max-width:980px){.wiki-cards{grid-template-columns:repeat(2,minmax(0,1fr))}}`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("style.css missing wiki home layout rule %q", want)
		}
	}
}
