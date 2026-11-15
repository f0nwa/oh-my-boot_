package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

func TestSearchMatchSHA(t *testing.T) {
	fullSHA := "5eecca2780bd49c900565e124516a1bd666ec5e012825f34991b6ba1ef2fa6cf"
	// Short query (<3 chars) should return false
	if searchMatchSHA("5e", fullSHA) {
		t.Errorf("expected 2-char query to not match sha")
	}
	// Prefix of SHA
	if !searchMatchSHA("5eec", fullSHA) {
		t.Errorf("expected 5eec to match full sha")
	}
	// Substring of SHA
	if !searchMatchSHA("ca2780bd", fullSHA) {
		t.Errorf("expected ca2780bd to match full sha")
	}
	// Short SHA representation
	if !searchMatchSHA("5eec…a6cf", fullSHA) {
		t.Errorf("expected 5eec…a6cf to match full sha")
	}
	if !searchMatchSHA("5eec...a6cf", fullSHA) {
		t.Errorf("expected 5eec...a6cf to match full sha")
	}
	// Unrelated query
	if searchMatchSHA("abcdef", fullSHA) {
		t.Errorf("expected unrelated query to not match")
	}
}

type searchMockStore struct {
	authOnlyStore
	users              []User
	windowsAnswers     []WindowsAnswerVariant
	ubuntuAutoinstalls []UbuntuAutoinstallSummary
	driverPackages     []DriverPackage
	menuVersions       []MenuVersion
}

func (s searchMockStore) Users(context.Context) ([]User, error) { return s.users, nil }
func (s searchMockStore) WindowsAnswers(context.Context) ([]WindowsAnswerVariant, error) {
	return s.windowsAnswers, nil
}
func (s searchMockStore) UbuntuAutoinstalls(context.Context) ([]UbuntuAutoinstallSummary, error) {
	return s.ubuntuAutoinstalls, nil
}
func (s searchMockStore) DriverPackages(context.Context) ([]DriverPackage, error) {
	return s.driverPackages, nil
}
func (s searchMockStore) MenuVersions(context.Context) ([]MenuVersion, error) {
	return s.menuVersions, nil
}

func TestSearchAllByAuthorAndSHA(t *testing.T) {
	s := libraryServer()
	mockStore := searchMockStore{
		authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}},
		users: []User{
			{ID: 10, Username: "vlad_operator", Role: "operator"},
		},
		windowsAnswers: []WindowsAnswerVariant{
			{ID: 1, Title: "Win 11 Lab", SHA256: "5eecca2780bd49c900565e124516a1bd666ec5e012825f34991b6ba1ef2fa6cf"},
		},
		ubuntuAutoinstalls: []UbuntuAutoinstallSummary{
			{ID: 2, Title: "Ubuntu Server Minimal", SHA256: "9d21af870233ea8536128fa80295da9f7765ec01924bce63029141f2214414bb"},
		},
		driverPackages: []DriverPackage{
			{SHA256: "a1b490f82348cbe02934ef098234ea76123498a123908fde781293847a9889f0"},
		},
		menuVersions: []MenuVersion{
			{ID: 42, Author: "vlad", Active: true, SHA256: "7c01593cba394f0982054ec098124ef98012354cde89123450918234912edd45"},
		},
	}
	s.Store = mockStore

	// Set authors cache directly
	s.authorsCache.set(ResourceAuthors{
		Answers:       map[int64]string{1: "vlad"},
		UbuntuAnswers: map[int64]string{2: "admin"},
		Drivers:       map[string]string{"a1b490f82348cbe02934ef098234ea76123498a123908fde781293847a9889f0": "vlad"},
	}, time.Now())

	// 1. Search by Author "vlad"
	req := httptest.NewRequest(http.MethodGet, "/search?format=json&q=vlad", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)

	var results []searchResult
	if err := json.Unmarshal(res.Body.Bytes(), &results); err != nil {
		t.Fatalf("failed to parse json: %v", err)
	}

	foundAnswer, foundDriver, foundMenu, foundUser := false, false, false, false
	for _, r := range results {
		if r.Kind == "Скрипт установки" && strings.Contains(r.Title, "Win 11 Lab") {
			foundAnswer = true
		}
		if r.Kind == "Пакет драйверов" && strings.Contains(r.Sub, "vlad") {
			foundDriver = true
		}
		if r.Kind == "Версия меню" && strings.Contains(r.Title, "42") {
			foundMenu = true
		}
		if r.Kind == "Пользователь" && r.Title == "vlad_operator" {
			foundUser = true
		}
	}
	if !foundAnswer {
		t.Errorf("expected to find Windows answer by author 'vlad', got: %+v", results)
	}
	if !foundDriver {
		t.Errorf("expected to find Driver package by author 'vlad', got: %+v", results)
	}
	if !foundMenu {
		t.Errorf("expected to find Menu version by author 'vlad', got: %+v", results)
	}
	if !foundUser {
		t.Errorf("expected to find User by username 'vlad', got: %+v", results)
	}

	// 2. Search by SHA prefix "5eec"
	reqSHA := httptest.NewRequest(http.MethodGet, "/search?format=json&q=5eec", nil)
	reqSHA.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	resSHA := httptest.NewRecorder()
	s.Handler().ServeHTTP(resSHA, reqSHA)

	var resultsSHA []searchResult
	if err := json.Unmarshal(resSHA.Body.Bytes(), &resultsSHA); err != nil {
		t.Fatalf("failed to parse json: %v", err)
	}
	foundSHAMatch := false
	for _, r := range resultsSHA {
		if r.Kind == "Скрипт установки" && strings.Contains(r.Title, "Win 11 Lab") {
			foundSHAMatch = true
		}
	}
	if !foundSHAMatch {
		t.Errorf("expected to find Windows answer by SHA prefix '5eec', got: %+v", resultsSHA)
	}
}

func TestSearchAnswersJSONAndPage(t *testing.T) {
	s := libraryServer()
	s.Store = authOnlyStore{user: User{ID: 2, Role: "operator", Enabled: true}}
	req := httptest.NewRequest(http.MethodGet, "/search?format=json&q=%D0%BE%D0%B1%D1%80%D0%B0%D0%B7", nil) // «образ»
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
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
	empty.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
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
	if !strings.Contains(body, `class="search-spinner"`) || !strings.Contains(body, `class="search-loader"`) {
		t.Fatal("layout lacks the search animation loader and spinner elements")
	}
	script := httptest.NewRecorder()
	s.Handler().ServeHTTP(script, httptest.NewRequest(http.MethodGet, "/assets/search.js", nil))
	if script.Code != http.StatusOK || !strings.Contains(script.Body.String(), "showModal") {
		t.Fatalf("search.js status=%d", script.Code)
	}
	if !strings.Contains(script.Body.String(), "setLoading") || !strings.Contains(script.Body.String(), "is-loading") {
		t.Fatal("search.js lacks loading state handlers")
	}
}
