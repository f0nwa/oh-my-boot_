package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		`action="/users/delete"`,
		`popovertarget="user-menu-2"`,
		`data-user-delete-id="2"`,
		`data-user-delete-name="viewer"`,
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
		`id="user-delete-dialog"`,
		`id="delete-modal-user"`,
		`id="delete-modal-id"`,
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

type userChangeMockStore struct {
	authOnlyStore
	users       []User
	called      bool
	deletedID   int64
	deletedName string
	lastErr     error
	audits      []string
}

func (s *userChangeMockStore) Users(context.Context) ([]User, error) {
	return s.users, nil
}

func (s *userChangeMockStore) ChangeUser(context.Context, int64, int64, string, bool) error {
	s.called = true
	return s.lastErr
}

func (s *userChangeMockStore) DeleteUser(_ context.Context, id, _ int64) (string, error) {
	s.called = true
	s.deletedID = id
	if s.lastErr != nil {
		return "", s.lastErr
	}
	return s.deletedName, nil
}

func (s *userChangeMockStore) Audit(_ context.Context, _ int64, action, result, detail string) error {
	s.audits = append(s.audits, action+"|"+result+"|"+detail)
	return nil
}

func TestUsersPageProtectsCurrentUserRow(t *testing.T) {
	for _, tc := range []struct {
		lang, you string
	}{
		{langRU, ">вы<"},
		{langEN, ">you<"},
	} {
		rec := httptest.NewRecorder()
		render(rec, "users", samplePageData(tc.lang))
		if rec.Code != http.StatusOK {
			t.Fatalf("render users (%s): %d", tc.lang, rec.Code)
		}
		body := rec.Body.String()
		for _, want := range []string{
			tc.you,
			`data-user-pw-id="1"`,
			`id="user-change-2"`,
			`data-user-save="2"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("users (%s) lacks %q", tc.lang, want)
			}
		}
		if strings.Contains(body, `id="user-change-1"`) || strings.Contains(body, `data-user-save="1"`) || strings.Contains(body, `popovertarget="user-menu-1"`) || strings.Contains(body, `data-user-delete-id="1"`) {
			t.Errorf("users (%s) must not render change form, Save button or delete menu for current user", tc.lang)
		}
		selfStart := strings.Index(body, `data-user-id="1"`)
		selfEnd := strings.Index(body, `data-user-id="2"`)
		if selfStart < 0 || selfEnd <= selfStart {
			t.Fatalf("unexpected row order in users (%s)", tc.lang)
		}
		selfRow := body[selfStart:selfEnd]
		if !strings.Contains(selfRow, `<select name="role" disabled`) || !strings.Contains(selfRow, `type="checkbox" name="enabled"`) || !strings.Contains(selfRow, `disabled`) {
			t.Errorf("current user row (%s) must disable role and enabled controls: %s", tc.lang, selfRow)
		}
	}

	css, _ := templateFS.ReadFile("templates/style.css")
	if !strings.Contains(string(css), ".user-table td small{display:inline") {
		t.Errorf("stylesheet must keep user id and badge inline in .user-table")
	}
}

func TestUserChangeBlocksSelfDemotionAndDisable(t *testing.T) {
	admin := User{ID: 1, Username: "vlad", Role: "admin", Enabled: true, Language: langRU}
	other := User{ID: 2, Username: "op", Role: "operator", Enabled: true}

	for _, tc := range []struct {
		name string
		form url.Values
	}{
		{"demote self", url.Values{"id": {"1"}, "role": {"operator"}, "enabled": {"on"}}},
		{"disable self", url.Values{"id": {"1"}, "role": {"admin"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &userChangeMockStore{
				authOnlyStore: authOnlyStore{user: admin},
				users:         []User{admin, other},
			}
			res := httptest.NewRecorder()
			(&Server{Store: store}).Handler().ServeHTTP(res, accountRequest(http.MethodPost, "/users/change", tc.form))
			if res.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", res.Code)
			}
			if store.called {
				t.Fatalf("ChangeUser in store must not be called for self demote/disable")
			}
			body := res.Body.String()
			if !strings.Contains(body, `role="alert"`) || !strings.Contains(body, "Нельзя отключить свою учётную запись или понизить свою роль администратора") {
				t.Fatalf("missing translated self-protection alert: %s", body)
			}
			if len(store.audits) == 0 || !strings.HasPrefix(store.audits[0], "user.change|error|") {
				t.Fatalf("audits = %v", store.audits)
			}
		})
	}

	t.Run("last admin error is translated", func(t *testing.T) {
		enAdmin := admin
		enAdmin.Language = langEN
		store := &userChangeMockStore{
			authOnlyStore: authOnlyStore{user: enAdmin},
			users:         []User{enAdmin, other},
			lastErr:       errLastAdmin,
		}
		res := httptest.NewRecorder()
		(&Server{Store: store}).Handler().ServeHTTP(res, accountRequest(http.MethodPost, "/users/change", url.Values{"id": {"2"}, "role": {"operator"}, "enabled": {"on"}}))
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "You cannot disable or demote the last administrator") {
			t.Fatalf("last admin alert: %d %s", res.Code, res.Body.String())
		}
	})
}

func TestUserDeleteHandler(t *testing.T) {
	admin := User{ID: 1, Username: "vlad", Role: "admin", Enabled: true, Language: langRU}
	other := User{ID: 2, Username: "op", Role: "operator", Enabled: true}

	t.Run("success", func(t *testing.T) {
		store := &userChangeMockStore{
			authOnlyStore: authOnlyStore{user: admin},
			users:         []User{admin, other},
			deletedName:   "op",
		}
		res := httptest.NewRecorder()
		(&Server{Store: store}).Handler().ServeHTTP(res, accountRequest(http.MethodPost, "/users/delete", url.Values{"id": {"2"}}))
		if res.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303: %s", res.Code, res.Body.String())
		}
		loc, _ := url.QueryUnescape(res.Header().Get("Location"))
		if loc != "/users?message=Пользователь «op» удалён" {
			t.Fatalf("location = %q", loc)
		}
		if store.deletedID != 2 {
			t.Fatalf("deletedID = %d, want 2", store.deletedID)
		}
		if len(store.audits) == 0 || store.audits[0] != "user.delete|ok|id=2 username=op" {
			t.Fatalf("audits = %v", store.audits)
		}
	})

	t.Run("blocks self deletion", func(t *testing.T) {
		store := &userChangeMockStore{
			authOnlyStore: authOnlyStore{user: admin},
			users:         []User{admin, other},
		}
		res := httptest.NewRecorder()
		(&Server{Store: store}).Handler().ServeHTTP(res, accountRequest(http.MethodPost, "/users/delete", url.Values{"id": {"1"}}))
		if res.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", res.Code)
		}
		if store.called {
			t.Fatalf("DeleteUser in store must not be called for self deletion")
		}
		if !strings.Contains(res.Body.String(), "Нельзя удалить свою учётную запись") {
			t.Fatalf("missing self-delete error message: %s", res.Body.String())
		}
	})

	t.Run("last admin error in English", func(t *testing.T) {
		enAdmin := admin
		enAdmin.Language = langEN
		store := &userChangeMockStore{
			authOnlyStore: authOnlyStore{user: enAdmin},
			users:         []User{enAdmin, other},
			lastErr:       errLastAdmin,
		}
		res := httptest.NewRecorder()
		(&Server{Store: store}).Handler().ServeHTTP(res, accountRequest(http.MethodPost, "/users/delete", url.Values{"id": {"2"}}))
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "You cannot delete the last administrator") {
			t.Fatalf("last admin delete alert: %d %s", res.Code, res.Body.String())
		}
	})

	t.Run("user not found", func(t *testing.T) {
		store := &userChangeMockStore{
			authOnlyStore: authOnlyStore{user: admin},
			users:         []User{admin},
			lastErr:       errUserNotFound,
		}
		res := httptest.NewRecorder()
		(&Server{Store: store}).Handler().ServeHTTP(res, accountRequest(http.MethodPost, "/users/delete", url.Values{"id": {"99"}}))
		if res.Code != http.StatusNotFound || !strings.Contains(res.Body.String(), "Пользователь не найден") {
			t.Fatalf("not found alert: %d %s", res.Code, res.Body.String())
		}
	})

	t.Run("operator forbidden", func(t *testing.T) {
		store := &userChangeMockStore{
			authOnlyStore: authOnlyStore{user: other},
		}
		res := httptest.NewRecorder()
		(&Server{Store: store}).Handler().ServeHTTP(res, accountRequest(http.MethodPost, "/users/delete", url.Values{"id": {"1"}}))
		if res.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", res.Code)
		}
	})
}
