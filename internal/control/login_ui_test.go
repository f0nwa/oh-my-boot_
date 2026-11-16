package control

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	for _, want := range []string{"is-floating", "submitBtn.disabled", "addEventListener"} {
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
		data := samplePageData(lang)
		data.CSRF = "test-csrf-token"
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

