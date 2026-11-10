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
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
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
		`class="card account-card"`, `class="account-card-head"`, `data-account-password-open`,
		`class="kv account-kv"`, `class="account-lang-block"`, `class="account-lang-row"`,
		`id="account-password-dialog"`, `class="user-dialog"`,
		"viewer", "Observer", "View state, catalog and logs only",
		`action="/settings/language"`, `name="return" value="/account"`, `<option value="en" selected>`,
		`action="/account/password"`, `name="current_password"`, `data-password-generator`,
		`data-password-confirm="#account-confirm-password"`, `name="confirm_password"`, `id="account-confirm-password"`,
		`src="/assets/password.js"`, `src="/assets/account.js"`,
		`class="btn btn-primary"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("account page lacks %q", want)
		}
	}
	if strings.Contains(body, "<table>") || strings.Contains(body, "<details") || strings.Contains(body, "<summary") || strings.Contains(body, "data-auto-open") {
		t.Errorf("account page should use compact card and closed dialog without table or details")
	}
}

func TestHeaderLinksToAccountInsteadOfLanguageSwitch(t *testing.T) {
	res := httptest.NewRecorder()
	render(res, "installations", pageData{Info: requestContext{User: User{ID: 1, Username: "fonwa", Role: "admin"}, CSRF: "x", Path: "/"}})
	body := res.Body.String()
	if !strings.Contains(body, `href="/account"`) || strings.Contains(body, `name="language" value="en"`) {
		t.Fatalf("header must link to the account and have no language buttons: %s", body)
	}
}

func TestChangeOwnPassword(t *testing.T) {
	store := &accountStore{authOnlyStore: authOnlyStore{user: User{ID: 3, Username: "viewer", Role: "observer", Enabled: true}}}
	s := &Server{Store: store}
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, accountRequest(http.MethodPost, "/account/password", url.Values{
		"current_password": {"old-password-123"},
		"new_password":     {"new-password-456"},
		"confirm_password": {"new-password-456"},
	}))
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
	s.Handler().ServeHTTP(res, accountRequest(http.MethodPost, "/account/password", url.Values{
		"current_password": {"bad"},
		"new_password":     {"new-password-456"},
		"confirm_password": {"new-password-456"},
	}))
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `role="alert"`) || !strings.Contains(res.Body.String(), "Текущий пароль неверен") || !strings.Contains(res.Body.String(), `action="/account/password"`) || !strings.Contains(res.Body.String(), `data-auto-open="true"`) {
		t.Fatalf("wrong current password: %d %s", res.Code, res.Body.String())
	}
	if strings.Join(store.audits, ",") != "account.password|error|wrong current password" {
		t.Fatalf("audits = %v", store.audits)
	}

	store.wrongCurrent = false
	store.changed.newPwd = ""
	res = httptest.NewRecorder()
	s.Handler().ServeHTTP(res, accountRequest(http.MethodPost, "/account/password", url.Values{
		"current_password": {"old-password-123"},
		"new_password":     {"new-password-456"},
		"confirm_password": {"other-password-456"},
	}))
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `role="alert"`) || !strings.Contains(res.Body.String(), "Пароли не совпадают") || !strings.Contains(res.Body.String(), `data-auto-open="true"`) {
		t.Fatalf("mismatched passwords: %d %s", res.Code, res.Body.String())
	}
	if store.changed.newPwd != "" {
		t.Fatalf("mismatched passwords must not call ChangeOwnPassword")
	}

	res = httptest.NewRecorder()
	s.Handler().ServeHTTP(res, accountRequest(http.MethodPost, "/account/password", url.Values{
		"current_password": {"old-password-123"},
		"new_password":     {"short"},
		"confirm_password": {"short"},
	}))
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
	scriptBody := script.Body.String()
	if script.Code != http.StatusOK ||
		!strings.Contains(scriptBody, "crypto.getRandomValues") ||
		!strings.Contains(scriptBody, "pw-tools") ||
		!strings.Contains(scriptBody, "data-password-confirm") ||
		!strings.Contains(scriptBody, "pw-wrap") ||
		!strings.Contains(scriptBody, "pw-toggle") ||
		!strings.Contains(scriptBody, "Show password") ||
		!strings.Contains(scriptBody, "Hide password") ||
		!strings.Contains(scriptBody, "!@#$%^&*-_+=") ||
		!strings.Contains(scriptBody, "randomIndex") {
		t.Fatalf("password script: %d %s", script.Code, scriptBody)
	}
	if strings.Contains(scriptBody, "input.type = 'text'") || strings.Contains(scriptBody, "confirmInput.type = 'text'") {
		t.Fatalf("generated password must stay masked as password")
	}

	loginRes := httptest.NewRecorder()
	(&Server{}).Handler().ServeHTTP(loginRes, httptest.NewRequest(http.MethodGet, "/login", nil))
	if loginRes.Code != http.StatusOK || !strings.Contains(loginRes.Body.String(), `src="/assets/password.js"`) {
		t.Fatalf("login page must include password.js: %d", loginRes.Code)
	}
}

func TestAccountScriptEndpoint(t *testing.T) {
	rec := httptest.NewRecorder()
	(&Server{}).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/account.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/javascript; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	body := rec.Body.String()
	for _, want := range []string{"account-password-dialog", "data-account-password-open", "showModal"} {
		if !strings.Contains(body, want) {
			t.Fatalf("account.js lacks %q", want)
		}
	}
}
