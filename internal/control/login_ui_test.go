package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestLoginScriptEndpoint(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/assets/login.js", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Fatalf("expected javascript content type, got %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("expected Cache-Control no-store, got %q", cc)
	}
	body := rec.Body.String()
	for _, want := range []string{"is-floating", "submitBtn.disabled", "addEventListener", "login-net", "DHCP DISCOVER", "ipxe-shim.efi", "snponly-shim.efi", "undionly.kpxe", "boot/menu.ipxe"} {
		if !strings.Contains(body, want) {
			t.Errorf("login.js missing %q", want)
		}
	}
}

func TestLoginPageRendersFloatingLabelsAndDisabledSubmit(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	body := rec.Body.String()

	if !strings.Contains(body, `<div class="field-float">`) {
		t.Errorf("login page lacks .field-float wrapper: %s", body)
	}
	if !strings.Contains(body, `name="username"`) || !strings.Contains(body, `id="login-username"`) {
		t.Errorf("login page lacks username input: %s", body)
	}
	if !strings.Contains(body, `<label for="login-username">Имя пользователя</label>`) {
		t.Errorf("login page lacks label for username: %s", body)
	}
	if !strings.Contains(body, `name="password"`) || !strings.Contains(body, `id="login-password"`) {
		t.Errorf("login page lacks password input: %s", body)
	}
	if !strings.Contains(body, `<label for="login-password">Пароль</label>`) {
		t.Errorf("login page lacks label for password: %s", body)
	}
	if !strings.Contains(body, `<button class="btn btn-primary" disabled>Войти</button>`) {
		t.Errorf("login page lacks disabled submit button: %s", body)
	}
	if !strings.Contains(body, `<script defer src="/assets/login.js"></script>`) {
		t.Errorf("login page lacks /assets/login.js script: %s", body)
	}
	if strings.Contains(body, "Авторизация на сервере OMB") {
		t.Errorf("login page must not contain extra subtitle: %s", body)
	}
	if !strings.Contains(body, `<div class="login-net-bg" aria-hidden="true"><svg class="login-net"`) {
		t.Errorf("login page lacks SVG network background wrapper")
	}

	cssBytes, err := templateFS.ReadFile("templates/style.css")
	if err != nil {
		t.Fatalf("read style.css: %v", err)
	}
	css := string(cssBytes)
	for _, want := range []string{
		".login-net-bg{position:fixed;inset:0;z-index:0",
		"login-cursor-blink 1.1s steps(1,end) infinite",
		"@media (max-width:759px){.login-net-bg{display:none}}",
		".login-brand .wordmark-cursor{animation:none}",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("style.css missing login animation rule %q", want)
		}
	}
	if strings.Contains(css, "backdrop-filter:blur") {
		t.Errorf("style.css should not use heavy backdrop-filter blur on login")
	}
}

func TestLoginPageEnglishLocalization(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	body := rec.Body.String()

	if !strings.Contains(body, `<label for="login-username">Username</label>`) {
		t.Errorf("login page lacks English label for username: %s", body)
	}
	if !strings.Contains(body, `<label for="login-password">Password</label>`) {
		t.Errorf("login page lacks English label for password: %s", body)
	}
	if !strings.Contains(body, `<button class="btn btn-primary" disabled>Sign in</button>`) {
		t.Errorf("login page lacks English disabled submit button: %s", body)
	}
}

func TestLoginPageBrowser(t *testing.T) {
	if os.Getenv("OMB_UI_TEST") != "1" {
		t.Skip("set OMB_UI_TEST=1 and NODE_PATH to run browser check")
	}
	dir := t.TempDir()
	assets := regexp.MustCompile(`(?:src|href)="(/assets/[^"?]+)"`)
	s := &Server{}
	for _, lang := range []string{langRU, langEN} {
		data := pageData{Lang: lang, CSRF: "test-csrf-token"}
		rec := httptest.NewRecorder()
		render(rec, "login", data)
		body := rec.Body.String()
		if err := os.WriteFile(filepath.Join(dir, "login-"+lang+".html"), rec.Body.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		for _, match := range assets.FindAllStringSubmatch(body, -1) {
			res := httptest.NewRecorder()
			s.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, match[1], nil))
			assetPath := filepath.Join(dir, strings.TrimPrefix(match[1], "/assets/"))
			if err := os.MkdirAll(filepath.Dir(assetPath), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(assetPath, res.Body.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "../../scripts/test-login-ui.cjs", dir)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser test failed: %v\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "LOGIN_UI_BROWSER_TESTS_PASS") {
		t.Fatalf("browser check missing success marker: %s", string(out))
	}
}

func TestLoginPageCSRFReuseNoStoreAndTTL(t *testing.T) {
	s := &Server{}
	h := s.Handler()

	// First GET /login issues a valid 43-char CSRF cookie with no-store and 12h MaxAge.
	req1 := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first GET /login status=%d", rec1.Code)
	}
	if cc := rec1.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("expected Cache-Control: no-store, got %q", cc)
	}
	var csrfCookie *http.Cookie
	for _, c := range rec1.Result().Cookies() {
		if c.Name == loginCSRFCookieName {
			csrfCookie = c
		}
	}
	if csrfCookie == nil || !isValidCSRFToken(csrfCookie.Value) {
		t.Fatalf("expected valid %s cookie, got %+v", loginCSRFCookieName, csrfCookie)
	}
	if csrfCookie.MaxAge != int(loginCSRFLifetime.Seconds()) {
		t.Fatalf("expected MaxAge=%d, got %d", int(loginCSRFLifetime.Seconds()), csrfCookie.MaxAge)
	}
	if !strings.Contains(rec1.Body.String(), `name="csrf" value="`+csrfCookie.Value+`"`) {
		t.Fatalf("first GET /login form missing CSRF value %q", csrfCookie.Value)
	}

	// Second GET /login (e.g. another browser tab) reuses the same valid CSRF token instead of overwriting it.
	req2 := httptest.NewRequest(http.MethodGet, "/login", nil)
	req2.AddCookie(&http.Cookie{Name: loginCSRFCookieName, Value: csrfCookie.Value})
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second GET /login status=%d", rec2.Code)
	}
	var csrfCookie2 *http.Cookie
	for _, c := range rec2.Result().Cookies() {
		if c.Name == loginCSRFCookieName {
			csrfCookie2 = c
		}
	}
	if csrfCookie2 == nil || csrfCookie2.Value != csrfCookie.Value {
		t.Fatalf("expected second GET /login to reuse token %q, got %+v", csrfCookie.Value, csrfCookie2)
	}
	if !strings.Contains(rec2.Body.String(), `name="csrf" value="`+csrfCookie.Value+`"`) {
		t.Fatalf("second GET /login form missing reused CSRF value")
	}

	// Malformed or short cookie value is NOT reused; a fresh random token is minted.
	req3 := httptest.NewRequest(http.MethodGet, "/login", nil)
	req3.AddCookie(&http.Cookie{Name: loginCSRFCookieName, Value: "short-bad-token"})
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req3)
	var csrfCookie3 *http.Cookie
	for _, c := range rec3.Result().Cookies() {
		if c.Name == loginCSRFCookieName {
			csrfCookie3 = c
		}
	}
	if csrfCookie3 == nil || !isValidCSRFToken(csrfCookie3.Value) || csrfCookie3.Value == "short-bad-token" {
		t.Fatalf("expected fresh valid CSRF token for malformed cookie, got %+v", csrfCookie3)
	}
}

func TestLoginPostInvalidCSRFRedirectsToLogin(t *testing.T) {
	s := &Server{}
	h := s.Handler()

	cases := []struct {
		name   string
		cookie string
		form   string
	}{
		{name: "missing_cookie", cookie: "", form: "stale-token"},
		{name: "mismatched_token", cookie: "cookie-token", form: "old-form-token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			form := url.Values{"csrf": {tc.form}, "username": {"admin"}, "password": {"secretpassword"}}
			req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: loginCSRFCookieName, Value: tc.cookie})
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusSeeOther {
				t.Fatalf("expected 303 redirect on invalid CSRF, got %d body=%q", rec.Code, rec.Body.String())
			}
			loc := rec.Header().Get("Location")
			wantLoc := "/login?error=" + url.QueryEscape(loginCSRFExpiredMsg)
			if loc != wantLoc {
				t.Fatalf("expected redirect to %q, got %q", wantLoc, loc)
			}

			// Following the redirect renders the localized friendly message in RU and EN.
			for _, tcLang := range []struct {
				accept string
				want   string
			}{
				{accept: "ru-RU,ru;q=0.9", want: loginCSRFExpiredMsg},
				{accept: "en-US,en;q=0.9", want: "The sign-in page expired. Please try again."},
			} {
				getReq := httptest.NewRequest(http.MethodGet, loc, nil)
				getReq.Header.Set("Accept-Language", tcLang.accept)
				getRec := httptest.NewRecorder()
				h.ServeHTTP(getRec, getReq)
				if getRec.Code != http.StatusOK {
					t.Fatalf("GET %s status=%d", loc, getRec.Code)
				}
				if !strings.Contains(getRec.Body.String(), tcLang.want) {
					t.Errorf("GET %s (%s) missing message %q", loc, tcLang.accept, tcLang.want)
				}
			}
		})
	}
}


