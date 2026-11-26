package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"omb/internal/agent"
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
	driverConfigs      []DriverConfig
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
func (s searchMockStore) DriverConfigs(context.Context) ([]DriverConfig, error) {
	return s.driverConfigs, nil
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
		driverConfigs: []DriverConfig{
			{ID: 8, Title: "Ноутбуки Dell", Author: "vlad", Build: agent.DriverBuildResult{WIMSHA256: "f4120000111122223333444455556666777788889999aaaabbbbccccddddeeee"}},
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

	// 3. Search WinPE driver config by Cyrillic title and WIM SHA
	for _, q := range []string{"Dell", "f4120000"} {
		reqCfg := httptest.NewRequest(http.MethodGet, "/search?format=json&q="+url.QueryEscape(q), nil)
		reqCfg.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
		resCfg := httptest.NewRecorder()
		s.Handler().ServeHTTP(resCfg, reqCfg)
		var cfgResults []searchResult
		if err := json.Unmarshal(resCfg.Body.Bytes(), &cfgResults); err != nil {
			t.Fatal(err)
		}
		foundCfg := false
		for _, r := range cfgResults {
			if r.Kind == "Конфигурация WinPE" && r.Title == "Ноутбуки Dell" && r.Href == "/drivers#driver-config-8" {
				foundCfg = true
			}
		}
		if !foundCfg {
			t.Errorf("query %q did not match WinPE driver config: %+v", q, cfgResults)
		}
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

type searchWIMMockStore struct {
	searchMockStore
	bootImages    []WindowsBootImage
	installImages []WindowsInstallImage
}

func (s searchWIMMockStore) ListWindowsBootImages(context.Context) ([]WindowsBootImage, error) {
	return s.bootImages, nil
}
func (s searchWIMMockStore) GetWindowsBootImage(context.Context, int64) (WindowsBootImage, error) {
	return WindowsBootImage{}, nil
}
func (s searchWIMMockStore) UpsertFileBootImage(context.Context, WindowsBootImageSource) (WindowsBootImage, error) {
	return WindowsBootImage{}, nil
}
func (s searchWIMMockStore) SetBootImageReady(context.Context, int64, string, bool, agent.WindowsWIMInfo) error {
	return nil
}
func (s searchWIMMockStore) SetBootImageError(context.Context, int64, string, string) error {
	return nil
}
func (s searchWIMMockStore) SetBootImageTitle(context.Context, int64, string) error { return nil }
func (s searchWIMMockStore) DeleteWindowsBootImage(context.Context, int64) error    { return nil }
func (s searchWIMMockStore) ListWindowsInstallImages(context.Context) ([]WindowsInstallImage, error) {
	return s.installImages, nil
}
func (s searchWIMMockStore) GetWindowsInstallImage(context.Context, int64) (WindowsInstallImage, error) {
	return WindowsInstallImage{}, nil
}
func (s searchWIMMockStore) UpsertWindowsInstallImage(context.Context, WindowsInstallImageSource) (WindowsInstallImage, error) {
	return WindowsInstallImage{}, nil
}
func (s searchWIMMockStore) SetInstallImageReady(context.Context, int64, string, agent.WindowsWIMInfo) error {
	return nil
}
func (s searchWIMMockStore) SetInstallImageError(context.Context, int64, string, string) error {
	return nil
}
func (s searchWIMMockStore) SetInstallImageTitle(context.Context, int64, string) error { return nil }
func (s searchWIMMockStore) DeleteWindowsInstallImage(context.Context, int64) error    { return nil }

func TestSearchAllIncludesBootAndInstallWIM(t *testing.T) {
	s := libraryServer()
	s.Store = searchWIMMockStore{
		searchMockStore: searchMockStore{
			authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}},
		},
		bootImages: []WindowsBootImage{
			{
				ID:         1,
				SourceKind: WIMSourceKindFile,
				Path:       "winpe/universal-boot.wim",
				Title:      "Win11 Universal Boot",
				SHA256:     "a1b2c3d4e5f60000111122223333444455556666777788889999aaaabbbbcccc",
				State:      "ready",
			},
			{
				ID:               2,
				SourceKind:       WIMSourceKindISO,
				WindowsProfileID: 10,
				Title:            "ISO Derived Boot",
				SHA256:           "deadbeef0000111122223333444455556666777788889999aaaabbbbcccc1234",
				State:            "ready",
			},
		},
		installImages: []WindowsInstallImage{
			{
				ID:          2,
				Path:        "wims/corp-gold.esd",
				InstallFile: "install.esd",
				Title:       "Corp Gold Edition",
				State:       "ready",
			},
		},
	}

	reqBoot := httptest.NewRequest(http.MethodGet, "/search?format=json&q=Universal", nil)
	reqBoot.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	resBoot := httptest.NewRecorder()
	s.Handler().ServeHTTP(resBoot, reqBoot)
	var bootHits []searchResult
	if err := json.Unmarshal(resBoot.Body.Bytes(), &bootHits); err != nil {
		t.Fatal(err)
	}
	foundBoot := false
	for _, r := range bootHits {
		if r.Title == "Win11 Universal Boot" && strings.HasPrefix(r.Href, "/images?tab=boot") {
			foundBoot = true
		}
	}
	if !foundBoot {
		t.Fatalf("expected boot.wim in ⌘K search results, got %+v", bootHits)
	}

	reqInst := httptest.NewRequest(http.MethodGet, "/search?format=json&q=Corp+Gold", nil)
	reqInst.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	resInst := httptest.NewRecorder()
	s.Handler().ServeHTTP(resInst, reqInst)
	var instHits []searchResult
	if err := json.Unmarshal(resInst.Body.Bytes(), &instHits); err != nil {
		t.Fatal(err)
	}
	foundInst := false
	for _, r := range instHits {
		if r.Title == "Corp Gold Edition" && strings.HasPrefix(r.Href, "/images?tab=install") {
			foundInst = true
		}
	}
	if !foundInst {
		t.Fatalf("expected install.wim in ⌘K search results, got %+v", instHits)
	}

	// English mode: ISO-derived boot.wim with SHA256 must translate "Из ISO" to "From ISO".
	s.Store = searchWIMMockStore{
		searchMockStore: searchMockStore{
			authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true, Language: langEN}},
		},
		bootImages: s.Store.(searchWIMMockStore).bootImages,
	}
	reqEN := httptest.NewRequest(http.MethodGet, "/search?format=json&q=Derived", nil)
	reqEN.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	resEN := httptest.NewRecorder()
	s.Handler().ServeHTTP(resEN, reqEN)
	var enHits []searchResult
	if err := json.Unmarshal(resEN.Body.Bytes(), &enHits); err != nil {
		t.Fatal(err)
	}
	foundEN := false
	for _, r := range enHits {
		if r.Title == "ISO Derived Boot" {
			foundEN = true
			if !strings.HasPrefix(r.Sub, "From ISO · ") {
				t.Fatalf("expected English prefix 'From ISO · ' in Sub, got %q", r.Sub)
			}
			if strings.Contains(r.Sub, "Из ISO") {
				t.Fatalf("unexpected Russian text in EN search result Sub: %q", r.Sub)
			}
		}
	}
	if !foundEN {
		t.Fatalf("expected ISO-derived boot.wim in EN search results, got %+v", enHits)
	}
}
