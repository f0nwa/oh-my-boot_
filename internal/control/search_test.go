package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchMatchFoldsMACSeparators(t *testing.T) {
	for _, q := range []string{"2a:ed", "2aed", "04-2A-ED", "bc:24"} {
		if !searchMatch(q, "bc:24:11:04:2a:ed") {
			t.Errorf("%q does not match the MAC", q)
		}
	}
	if searchMatch("ffff", "bc:24:11:04:2a:ed") || !searchMatch("win", "", "Windows 11") {
		t.Fatal("match")
	}
}

func TestSearchAnswersJSONAndPage(t *testing.T) {
	s := libraryServer()
	s.Store = authOnlyStore{user: User{ID: 2, Role: "operator", Enabled: true}}
	req := httptest.NewRequest(http.MethodGet, "/search?format=json&q=%D0%BE%D0%B1%D1%80%D0%B0%D0%B7", nil) // «образ»
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	var results []searchResult
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &results) != nil {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	found := false
	for _, r := range results {
		found = found || (r.Href == "/images" && r.Kind == "Раздел")
	}
	if !found {
		t.Fatalf("results=%+v", results)
	}
	empty := httptest.NewRequest(http.MethodGet, "/search?format=json&q=", nil)
	empty.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	res = httptest.NewRecorder()
	s.Handler().ServeHTTP(res, empty)
	if strings.TrimSpace(res.Body.String()) != "[]" {
		t.Fatalf("empty query=%s", res.Body.String())
	}
	body := libraryGet(t, s, "/search?q=%D0%BE%D0%B1%D1%80%D0%B0%D0%B7")
	if !strings.Contains(body, `class="search-page-results"`) || !strings.Contains(body, `href="/images"`) {
		t.Fatalf("search page: %s", body)
	}
	if !strings.Contains(body, `id="search-dialog"`) || !strings.Contains(body, `data-search-open`) || !strings.Contains(body, `/assets/search.js`) {
		t.Fatal("layout lacks the ⌘K dialog")
	}
	script := httptest.NewRecorder()
	s.Handler().ServeHTTP(script, httptest.NewRequest(http.MethodGet, "/assets/search.js", nil))
	if script.Code != http.StatusOK || !strings.Contains(script.Body.String(), "showModal") {
		t.Fatalf("search.js status=%d", script.Code)
	}
}
