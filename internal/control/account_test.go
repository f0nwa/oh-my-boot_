package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type accountStore struct {
	authOnlyStore
	language string
	changed  struct {
		id                     int64
		token, current, newPwd string
	}
	wrongCurrent bool
	audits       []string
}

func (s *accountStore) SetUserLanguage(_ context.Context, _ int64, language string) error {
	s.language = language
	return nil
}

func (s *accountStore) ChangeOwnPassword(_ context.Context, id int64, token, current, next string) error {
	if s.wrongCurrent {
		return errWrongPassword
	}
	if len([]rune(next)) < 12 {
		return errShortPassword
	}
	s.changed.id, s.changed.token, s.changed.current, s.changed.newPwd = id, token, current, next
	return nil
}

func (s *accountStore) Audit(_ context.Context, _ int64, action, result, detail string) error {
	s.audits = append(s.audits, action+"|"+result+"|"+detail)
	return nil
}

func accountRequest(method, path string, form url.Values) *http.Request {
	var req *http.Request
	if method == http.MethodGet {
		req = httptest.NewRequest(method, path, nil)
	} else {
		form.Set("csrf", CSRFToken("test-token"))
		req = httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	return req
}

func TestAccountPageShowsProfileLanguageAndPasswordForm(t *testing.T) {
	store := &accountStore{authOnlyStore: authOnlyStore{user: User{ID: 3, Username: "viewer", Role: "observer", Enabled: true, Language: langEN}}}
	res := httptest.NewRecorder()
	(&Server{Store: store}).Handler().ServeHTTP(res, accountRequest(http.MethodGet, "/account", nil))
	body := res.Body.String()
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, body)
	}
	for _, want := range []string{
		"viewer", "Observer", "View state, catalog and logs only",
		`action="/settings/language"`, `name="return" value="/account"`, `<option value="en" selected>`,
		`action="/account/password"`, `name="current_password"`, `data-password-generator`, `src="/assets/password.js"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("account page lacks %q", want)
		}
	}
}

func TestHeaderLinksToAccountInsteadOfLanguageSwitch(t *testing.T) {
	res := httptest.NewRecorder()
	render(res, "dashboard", pageData{Info: requestContext{User: User{ID: 1, Username: "fonwa", Role: "admin"}, CSRF: "x", Path: "/"}})
	body := res.Body.String()
	if !strings.Contains(body, `href="/account"`) || strings.Contains(body, `name="language" value="en"`) {
		t.Fatalf("header must link to the account and have no language buttons: %s", body)
	}
}

func TestChangeOwnPassword(t *testing.T) {
	store := &accountStore{authOnlyStore: authOnlyStore{user: User{ID: 3, Username: "viewer", Role: "observer", Enabled: true}}}
	s := &Server{Store: store}
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, accountRequest(http.MethodPost, "/account/password", url.Values{"current_password": {"old-password-123"}, "new_password": {"new-password-456"}}))
	if location, _ := url.QueryUnescape(res.Header().Get("Location")); res.Code != http.StatusSeeOther || location != "/account?message=Пароль изменён" {
		t.Fatalf("change status=%d location=%q", res.Code, location)
	}
	if store.changed.id != 3 || store.changed.token != "test-token" || store.changed.current != "old-password-123" || store.changed.newPwd != "new-password-456" {
		t.Fatalf("unexpected change: %+v", store.changed)
	}
	if strings.Join(store.audits, ",") != "account.password|ok|" {
		t.Fatalf("audits = %v", store.audits)
	}

	store.audits, store.wrongCurrent = nil, true
	res = httptest.NewRecorder()
	s.Handler().ServeHTTP(res, accountRequest(http.MethodPost, "/account/password", url.Values{"current_password": {"bad"}, "new_password": {"new-password-456"}}))
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `role="alert"`) || !strings.Contains(res.Body.String(), "Текущий пароль неверен") || !strings.Contains(res.Body.String(), `action="/account/password"`) {
		t.Fatalf("wrong current password: %d %s", res.Code, res.Body.String())
	}
	if strings.Join(store.audits, ",") != "account.password|error|wrong current password" {
		t.Fatalf("audits = %v", store.audits)
	}

	store.wrongCurrent = false
	res = httptest.NewRecorder()
	s.Handler().ServeHTTP(res, accountRequest(http.MethodPost, "/account/password", url.Values{"current_password": {"old-password-123"}, "new_password": {"short"}}))
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "Новый пароль должен содержать не меньше 12 символов") {
		t.Fatalf("short password: %d %s", res.Code, res.Body.String())
	}
}

func TestLanguageFormReturnsToAccount(t *testing.T) {
	if got := safeReturnPath("/account"); got != "/account" {
		t.Fatalf("safeReturnPath(/account) = %q", got)
	}
}

func TestPasswordInputsOfferGenerator(t *testing.T) {
	res := httptest.NewRecorder()
	render(res, "users", pageData{Info: requestContext{User: User{ID: 1, Role: "admin"}, CSRF: "x"}})
	body := res.Body.String()
	if strings.Count(body, "data-password-generator") != 2 || !strings.Contains(body, `src="/assets/password.js"`) {
		t.Fatalf("users page password fields lack the generator: %s", body)
	}
	script := httptest.NewRecorder()
	(&Server{}).Handler().ServeHTTP(script, httptest.NewRequest(http.MethodGet, "/assets/password.js", nil))
	if script.Code != http.StatusOK || !strings.Contains(script.Body.String(), "crypto.getRandomValues") {
		t.Fatalf("password script: %d", script.Code)
	}
}
