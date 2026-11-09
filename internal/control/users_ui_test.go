package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUsersScriptEndpoint(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/assets/users.js", nil)
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	contentType := rec.Header().Get("Content-Type")
	if contentType != "text/javascript; charset=utf-8" {
		t.Fatalf("expected Content-Type text/javascript; charset=utf-8, got %q", contentType)
	}

	body := rec.Body.String()
	for _, expected := range []string{"user-search", "user-password-dialog"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("users.js does not contain expected identifier %q", expected)
		}
	}
}

func TestUsersPageNoDetailsOrSummary(t *testing.T) {
	for _, lang := range []string{langRU, langEN} {
		rec := httptest.NewRecorder()
		render(rec, "users", samplePageData(lang))
		if rec.Code != http.StatusOK {
			t.Fatalf("render users (%s) failed with status %d", lang, rec.Code)
		}
		body := rec.Body.String()
		if strings.Contains(body, "<details") {
			t.Errorf("users page (%s) must not contain <details>", lang)
		}
		if strings.Contains(body, "<summary") {
			t.Errorf("users page (%s) must not contain <summary>", lang)
		}
	}
}

func TestUsersPageToolbarAndTable(t *testing.T) {
	rec := httptest.NewRecorder()
	render(rec, "users", samplePageData(langRU))
	if rec.Code != http.StatusOK {
		t.Fatalf("render users failed: %d", rec.Code)
	}
	body := rec.Body.String()

	requiredSelectors := []string{
		`class="toolbar"`,
		`id="user-search"`,
		`id="user-count"`,
		`class="user-table"`,
		`data-user-row`,
		`data-user-pw-id="1"`,
		`data-user-pw-name="admin"`,
		`data-user-add`,
		`/users/change`,
		`action="/users/create"`,
		`action="/users/reset-password"`,
	}
	for _, sel := range requiredSelectors {
		if !strings.Contains(body, sel) {
			t.Errorf("users page missing required markup element: %s", sel)
		}
	}
}

func TestUsersPageModals(t *testing.T) {
	rec := httptest.NewRecorder()
	render(rec, "users", samplePageData(langRU))
	if rec.Code != http.StatusOK {
		t.Fatalf("render users failed: %d", rec.Code)
	}
	body := rec.Body.String()

	modalRequirements := []string{
		`id="user-create-dialog"`,
		`id="user-password-dialog"`,
		`class="user-dialog"`,
		`data-close-dialog`,
		`src="/assets/users.js"`,
		`src="/assets/password.js"`,
	}
	for _, req := range modalRequirements {
		if !strings.Contains(body, req) {
			t.Errorf("users page missing modal requirement: %s", req)
		}
	}

	pwGenCount := strings.Count(body, "data-password-generator")
	if pwGenCount != 2 {
		t.Errorf("expected 2 data-password-generator inputs, got %d", pwGenCount)
	}
}
