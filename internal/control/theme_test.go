package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type themeStore struct {
	authOnlyStore
	saved string
}

func (s *themeStore) SetUserTheme(_ context.Context, _ int64, theme string) error {
	s.saved = theme
	return nil
}

func TestPagesCarryUserTheme(t *testing.T) {
	for _, tc := range []struct{ theme, want string }{{"dark", "dark"}, {"light", "light"}, {"system", "system"}, {"", "system"}, {"blue", "system"}} {
		res := httptest.NewRecorder()
		render(res, "account", pageData{Info: requestContext{User: User{ID: 1, Username: "admin", Role: "admin", Theme: tc.theme}, CSRF: "x", Path: "/account"}})
		body := res.Body.String()
		for _, want := range []string{`data-theme="` + tc.want + `"`, `action="/settings/theme"`, `<option value="` + tc.want + `" selected>`} {
			if !strings.Contains(body, want) {
				t.Errorf("theme %q: page lacks %q", tc.theme, want)
			}
		}
	}
}

func TestLoginPageFollowsSystemTheme(t *testing.T) {
	res := httptest.NewRecorder()
	(&Server{}).Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/login", nil))
	if !strings.Contains(res.Body.String(), `data-theme="system"`) {
		t.Fatalf("login page must follow the system theme: %s", res.Body.String())
	}
}

func TestThemeChoiceIsTranslated(t *testing.T) {
	res := httptest.NewRecorder()
	render(res, "account", pageData{Info: requestContext{User: User{ID: 1, Role: "admin", Language: langEN}, CSRF: "x"}})
	for _, want := range []string{"Appearance", "Theme", "Same as system", "Light", "Dark"} {
		if !strings.Contains(res.Body.String(), want) {
			t.Errorf("English account page lacks %q", want)
		}
	}
}

func TestSetThemeStoresChoiceAndReturnsSafely(t *testing.T) {
	post := func(store *themeStore, form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/settings/theme", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
		res := httptest.NewRecorder()
		(&Server{Store: store}).Handler().ServeHTTP(res, req)
		return res
	}
	for _, tc := range []struct{ theme, ret, location string }{
		{"dark", "/account", "/account"},
		{"light", "/menu", "/menu"},
		{"system", "//evil.example/", "/"},
	} {
		store := &themeStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "observer", Enabled: true}}}
		res := post(store, url.Values{"csrf": {CSRFToken("test-token")}, "theme": {tc.theme}, "return": {tc.ret}})
		if res.Code != http.StatusSeeOther || res.Header().Get("Location") != tc.location || store.saved != tc.theme {
			t.Fatalf("%+v: status=%d location=%q saved=%q", tc, res.Code, res.Header().Get("Location"), store.saved)
		}
	}
	for _, form := range []url.Values{{"csrf": {CSRFToken("test-token")}, "theme": {"blue"}}, {"csrf": {"bad"}, "theme": {"dark"}}} {
		store := &themeStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "observer", Enabled: true}}}
		if res := post(store, form); res.Code < 400 || store.saved != "" {
			t.Fatalf("bad theme form accepted: %v status=%d saved=%q", form, res.Code, store.saved)
		}
	}
}

func TestStylesheetHasDarkTheme(t *testing.T) {
	res := httptest.NewRecorder()
	(&Server{}).Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/assets/style.css", nil))
	css := res.Body.String()
	for _, want := range []string{":root[data-theme=dark]{", "@media (prefers-color-scheme:dark){:root[data-theme=system]{", "color-scheme:dark"} {
		if !strings.Contains(css, want) {
			t.Errorf("stylesheet lacks %q", want)
		}
	}
}
