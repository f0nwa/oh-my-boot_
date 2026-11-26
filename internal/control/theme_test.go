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
	allIcons := []string{`class="theme-auto"`, `class="theme-sun"`, `class="theme-moon"`}
	for _, tc := range []struct{ theme, current, next, label, icon string }{
		{"dark", "dark", "system", "Сейчас тёмная тема. Включить как в системе", `class="theme-moon"`},
		{"light", "light", "dark", "Сейчас светлая тема. Включить тёмную", `class="theme-sun"`},
		{"system", "system", "light", "Сейчас как в системе. Включить светлую", `class="theme-auto"`},
		{"", "system", "light", "Сейчас как в системе. Включить светлую", `class="theme-auto"`},
		{"blue", "system", "light", "Сейчас как в системе. Включить светлую", `class="theme-auto"`},
	} {
		res := httptest.NewRecorder()
		render(res, "account", pageData{Info: requestContext{User: User{ID: 1, Username: "admin", Role: "admin", Theme: tc.theme}, CSRF: "x", Path: "/account"}})
		body := res.Body.String()
		for _, want := range []string{`data-theme="` + tc.current + `"`, `action="/settings/theme"`, `name="theme" value="` + tc.next + `"`, `aria-label="` + tc.label + `"`, tc.icon} {
			if !strings.Contains(body, want) {
				t.Errorf("theme %q: page lacks %q", tc.theme, want)
			}
		}
		for _, icon := range allIcons {
			if icon != tc.icon && strings.Contains(body, icon) {
				t.Errorf("theme %q: page must not render %q alongside %q", tc.theme, icon, tc.icon)
			}
		}
	}
}

func TestThemeButtonInHeaderBesideAccount(t *testing.T) {
	for _, page := range []string{"account", "installations"} {
		res := httptest.NewRecorder()
		render(res, page, pageData{Info: requestContext{User: User{ID: 1, Username: "admin", Role: "admin", Theme: "dark"}, CSRF: "token", Path: "/installations"}})
		body := res.Body.String()
		selector := strings.Index(body, `class="theme-form"`)
		account := strings.Index(body, `class="account"`)
		logout := strings.Index(body, `action="/logout"`)
		if selector < 0 || account < selector || logout < account {
			t.Errorf("%s: theme button must precede account and logout in the header", page)
		}
		for _, want := range []string{`name="theme" value="system"`, `aria-label="Сейчас тёмная тема. Включить как в системе"`, `name="return" value="/installations"`, `class="theme-moon"`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: header lacks %q", page, want)
			}
		}
		if strings.Count(body, `class="theme-cycle"`) != 1 || strings.Contains(body, `<select name="theme"`) {
			t.Errorf("%s: expected one theme button and no dropdown", page)
		}
		if page == "account" && strings.Contains(body, `<h2>Оформление</h2>`) {
			t.Error("account page still has the former theme section")
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
	for _, tc := range []struct{ theme, want string }{
		{"system", "Using system theme. Switch to light theme"},
		{"light", "Using light theme. Switch to dark theme"},
		{"dark", "Using dark theme. Follow system theme"},
	} {
		res := httptest.NewRecorder()
		render(res, "account", pageData{Info: requestContext{User: User{ID: 1, Role: "admin", Theme: tc.theme, Language: langEN}, CSRF: "x"}})
		if !strings.Contains(res.Body.String(), tc.want) {
			t.Errorf("English header for theme %q lacks %q", tc.theme, tc.want)
		}
	}
}

func TestSetThemeStoresChoiceAndReturnsSafely(t *testing.T) {
	post := func(store *themeStore, form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/settings/theme", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
		res := httptest.NewRecorder()
		(&Server{Store: store}).Handler().ServeHTTP(res, req)
		return res
	}
	for _, tc := range []struct{ theme, ret, location string }{
		{"dark", "/account", "/account"},
		{"light", "/menu", "/menu"},
		{"dark", "/installations/new", "/installations/new"},
		{"light", "/events/machines/AA-BB-CC-DD-EE-FF", "/events/machines/AA-BB-CC-DD-EE-FF"},
		{"dark", "/answers/17/edit", "/answers/17/edit"},
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
	for _, want := range []string{":root[data-theme=dark]{", "@media (prefers-color-scheme:dark){:root[data-theme=system]{", "color-scheme:dark", ".theme-form .theme-auto-fill{fill:currentColor;stroke:none}"} {
		if !strings.Contains(css, want) {
			t.Errorf("stylesheet lacks %q", want)
		}
	}
}
