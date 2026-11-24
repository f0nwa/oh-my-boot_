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

func TestWikiEnglishBannerShownInEnglishMode(t *testing.T) {
	store := &authOnlyStore{user: User{ID: 1, Username: "admin", Role: "admin", Enabled: true, Language: langEN}}
	srv := &Server{Store: store}
	h := srv.Handler()
	cookie := &http.Cookie{Name: "omb_session", Value: "test-token"}

	req := httptest.NewRequest(http.MethodGet, "/wiki", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /wiki (EN) = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Built-in knowledge base articles are currently available in Russian.") {
		t.Errorf("expected English language notice on /wiki in EN mode")
	}
	if !strings.Contains(body, "oh my boot_ Knowledge Base") {
		t.Errorf("expected translated heading on /wiki in EN mode")
	}
}
