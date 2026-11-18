package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type passwordPolicyHTTPStore struct {
	authOnlyStore
	policy      PasswordPolicy
	loginUser   User
	users       []User
	audits      []string
	savedPolicy *PasswordPolicy
	createdCall *struct {
		username, password, role, mode string
		customDays                     int
		mustChange                     bool
	}
	resetCall *struct {
		id         int64
		password   string
		mustChange bool
	}
	userRuleCall *struct {
		id, actorID int64
		mode        string
		customDays  int
		mustChange  *bool
	}
	changedOwn struct {
		id                   int64
		token, current, next string
	}
	wrongCurrent bool
}

func (s *passwordPolicyHTTPStore) PasswordPolicy(context.Context) (PasswordPolicy, error) {
	return s.policy, nil
}

func (s *passwordPolicyHTTPStore) SetPasswordPolicy(_ context.Context, p PasswordPolicy) error {
	s.policy = p
	copyP := p
	s.savedPolicy = &copyP
	return nil
}

func (s *passwordPolicyHTTPStore) Login(_ context.Context, username, _ string) (User, string, error) {
	if s.loginUser.ID == 0 || !strings.EqualFold(s.loginUser.Username, username) {
		return User{}, "", errUserNotFound
	}
	return s.loginUser, "login-tok", nil
}

func (s *passwordPolicyHTTPStore) Users(context.Context) ([]User, error) {
	return s.users, nil
}

func (s *passwordPolicyHTTPStore) CreateUserWithPolicy(_ context.Context, username, password, role, mode string, customDays int, mustChange bool) error {
	s.createdCall = &struct {
		username, password, role, mode string
		customDays                     int
		mustChange                     bool
	}{username, password, role, mode, customDays, mustChange}
	return nil
}

func (s *passwordPolicyHTTPStore) ResetPasswordWithOptions(_ context.Context, id int64, password string, mustChange bool) error {
	s.resetCall = &struct {
		id         int64
		password   string
		mustChange bool
	}{id, password, mustChange}
	return nil
}

func (s *passwordPolicyHTTPStore) SetUserPasswordPolicy(_ context.Context, id, actorID int64, mode string, customDays int, mustChange *bool) error {
	var mcCopy *bool
	if mustChange != nil {
		v := *mustChange
		mcCopy = &v
	}
	s.userRuleCall = &struct {
		id, actorID int64
		mode        string
		customDays  int
		mustChange  *bool
	}{id, actorID, mode, customDays, mcCopy}
	return nil
}

func (s *passwordPolicyHTTPStore) ChangeOwnPassword(_ context.Context, id int64, token, current, next string) error {
	if s.wrongCurrent {
		return errWrongPassword
	}
	if len([]rune(next)) < 12 {
		return errShortPassword
	}
	if current == next {
		return errSamePassword
	}
	s.changedOwn.id, s.changedOwn.token, s.changedOwn.current, s.changedOwn.next = id, token, current, next
	return nil
}

func (s *passwordPolicyHTTPStore) Audit(_ context.Context, _ int64, action, result, detail string) error {
	s.audits = append(s.audits, action+"|"+result+"|"+detail)
	return nil
}

func (s *passwordPolicyHTTPStore) Logout(context.Context, string) error { return nil }
func (s *passwordPolicyHTTPStore) SetUserLanguage(context.Context, int64, string) error {
	return nil
}
func (s *passwordPolicyHTTPStore) SetUserTheme(context.Context, int64, string) error { return nil }

func TestLoginRedirectsToChangePasswordWhenRequired(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name    string
		user    User
		policy  PasswordPolicy
		wantLoc string
	}{
		{
			name:    "must change",
			user:    User{ID: 2, Username: "op", Role: "operator", Enabled: true, MustChangePassword: true, PasswordChangedAt: now},
			policy:  PasswordPolicy{},
			wantLoc: "/change-password",
		},
		{
			name:    "expired",
			user:    User{ID: 2, Username: "op", Role: "operator", Enabled: true, PasswordChangedAt: now.Add(-100 * 24 * time.Hour)},
			policy:  PasswordPolicy{MaxAgeDays: 90, WarnDays: 7},
			wantLoc: "/change-password",
		},
		{
			name:    "ok",
			user:    User{ID: 2, Username: "op", Role: "operator", Enabled: true, PasswordChangedAt: now},
			policy:  PasswordPolicy{MaxAgeDays: 90, WarnDays: 7},
			wantLoc: "/",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &passwordPolicyHTTPStore{loginUser: tc.user, policy: tc.policy}
			form := url.Values{"username": {"op"}, "password": {"ValidPass123!"}, "csrf": {"login-csrf"}}
			req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(&http.Cookie{Name: "omb_login_csrf", Value: "login-csrf"})
			res := httptest.NewRecorder()
			(&Server{Store: store}).Handler().ServeHTTP(res, req)
			if res.Code != http.StatusSeeOther || res.Header().Get("Location") != tc.wantLoc {
				t.Fatalf("status=%d loc=%q want=%q", res.Code, res.Header().Get("Location"), tc.wantLoc)
			}
		})
	}
}

func TestForcedPasswordChangeBlocksOtherRoutesAndAllowsChangeFlow(t *testing.T) {
	now := time.Now()
	store := &passwordPolicyHTTPStore{
		authOnlyStore: authOnlyStore{
			user: User{
				ID:                 5,
				Username:           "forced_op",
				Role:               "admin",
				Enabled:            true,
				MustChangePassword: true,
				PasswordChangedAt:  now,
			},
		},
		policy: PasswordPolicy{MaxAgeDays: 90, WarnDays: 7},
	}
	h := (&Server{Store: store}).Handler()

	for _, path := range []string{"/", "/users", "/account", "/installations"} {
		res := httptest.NewRecorder()
		h.ServeHTTP(res, accountRequest(http.MethodGet, path, nil))
		if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/change-password" {
			t.Fatalf("GET %s status=%d loc=%q, want 303 -> /change-password", path, res.Code, res.Header().Get("Location"))
		}
	}

	// API/JSON and POST mutations must return 403.
	apiReq := accountRequest(http.MethodGet, "/api/images/migrations", nil)
	apiReq.Header.Set("Accept", "application/json")
	apiRes := httptest.NewRecorder()
	h.ServeHTTP(apiRes, apiReq)
	if apiRes.Code != http.StatusForbidden {
		t.Fatalf("GET /api/images/migrations status=%d want 403", apiRes.Code)
	}

	postRes := httptest.NewRecorder()
	h.ServeHTTP(postRes, accountRequest(http.MethodPost, "/menu/publish", url.Values{}))
	if postRes.Code != http.StatusForbidden {
		t.Fatalf("POST /menu/publish status=%d want 403", postRes.Code)
	}

	// GET /change-password renders isolated form without main nav.
	pageRes := httptest.NewRecorder()
	h.ServeHTTP(pageRes, accountRequest(http.MethodGet, "/change-password", nil))
	if pageRes.Code != http.StatusOK {
		t.Fatalf("GET /change-password status=%d body=%s", pageRes.Code, pageRes.Body.String())
	}
	body := pageRes.Body.String()
	for _, want := range []string{`action="/change-password"`, `action="/logout"`, "forced_op", "Требуется смена пароля при входе"} {
		if !strings.Contains(body, want) {
			t.Errorf("/change-password missing %q", want)
		}
	}
	if strings.Contains(body, `class="topnav"`) {
		t.Errorf("/change-password must not render main topnav")
	}

	// Language switch and logout remain allowed.
	langRes := httptest.NewRecorder()
	h.ServeHTTP(langRes, accountRequest(http.MethodPost, "/settings/language", url.Values{"language": {"en"}, "return": {"/change-password"}}))
	if langRes.Code != http.StatusSeeOther {
		t.Fatalf("POST /settings/language status=%d", langRes.Code)
	}

	// Same password rejected.
	sameRes := httptest.NewRecorder()
	h.ServeHTTP(sameRes, accountRequest(http.MethodPost, "/change-password", url.Values{
		"current_password": {"TempPass12345"},
		"new_password":     {"TempPass12345"},
		"confirm_password": {"TempPass12345"},
	}))
	if sameRes.Code != http.StatusBadRequest || !strings.Contains(sameRes.Body.String(), "Новый пароль не должен совпадать с текущим") {
		t.Fatalf("same password: %d %s", sameRes.Code, sameRes.Body.String())
	}

	// Valid change succeeds and redirects to /.
	okRes := httptest.NewRecorder()
	h.ServeHTTP(okRes, accountRequest(http.MethodPost, "/change-password", url.Values{
		"current_password": {"TempPass12345"},
		"new_password":     {"FreshPass67890"},
		"confirm_password": {"FreshPass67890"},
	}))
	if okRes.Code != http.StatusSeeOther || okRes.Header().Get("Location") != "/" {
		t.Fatalf("valid change: %d %q %s", okRes.Code, okRes.Header().Get("Location"), okRes.Body.String())
	}
	if store.changedOwn.next != "FreshPass67890" {
		t.Fatalf("ChangeOwnPassword not called: %+v", store.changedOwn)
	}

	// Normal user visiting GET /change-password is redirected to /account.
	store.user.MustChangePassword = false
	redirRes := httptest.NewRecorder()
	h.ServeHTTP(redirRes, accountRequest(http.MethodGet, "/change-password", nil))
	if redirRes.Code != http.StatusSeeOther || redirRes.Header().Get("Location") != "/account" {
		t.Fatalf("GET /change-password when not required: %d %q", redirRes.Code, redirRes.Header().Get("Location"))
	}
}

func TestPasswordPolicyAdminHandlers(t *testing.T) {
	admin := User{ID: 1, Username: "admin", Role: "admin", Enabled: true, PasswordChangedAt: time.Now()}
	op := User{ID: 2, Username: "op1", Role: "operator", Enabled: true, PasswordChangedAt: time.Now()}
	store := &passwordPolicyHTTPStore{
		authOnlyStore: authOnlyStore{user: admin},
		users:         []User{admin, op},
		policy:        PasswordPolicy{MaxAgeDays: 0, WarnDays: 7},
	}
	h := (&Server{Store: store}).Handler()

	// Update global policy.
	res := httptest.NewRecorder()
	h.ServeHTTP(res, accountRequest(http.MethodPost, "/users/password-policy", url.Values{
		"max_age_days": {"90"},
		"warn_days":    {"14"},
	}))
	if res.Code != http.StatusSeeOther || store.savedPolicy == nil || store.savedPolicy.MaxAgeDays != 90 || store.savedPolicy.WarnDays != 14 {
		t.Fatalf("password-policy status=%d saved=%+v body=%s", res.Code, store.savedPolicy, res.Body.String())
	}

	// Update other user's policy + must_change_password.
	res = httptest.NewRecorder()
	h.ServeHTTP(res, accountRequest(http.MethodPost, "/users/user-password-policy", url.Values{
		"id":                    {"2"},
		"password_policy_mode":  {"custom"},
		"password_max_age_days": {"30"},
		"must_change_password":  {"on"},
	}))
	if res.Code != http.StatusSeeOther || store.userRuleCall == nil || store.userRuleCall.mode != "custom" || store.userRuleCall.customDays != 30 || store.userRuleCall.mustChange == nil || !*store.userRuleCall.mustChange {
		t.Fatalf("user-password-policy other status=%d call=%+v body=%s", res.Code, store.userRuleCall, res.Body.String())
	}

	// Self policy update allows mode=never, but must not force must_change_password on self.
	store.userRuleCall = nil
	res = httptest.NewRecorder()
	h.ServeHTTP(res, accountRequest(http.MethodPost, "/users/user-password-policy", url.Values{
		"id":                   {"1"},
		"password_policy_mode": {"never"},
		"must_change_password": {"on"},
	}))
	if res.Code != http.StatusSeeOther || store.userRuleCall == nil || store.userRuleCall.mode != "never" || store.userRuleCall.mustChange != nil {
		t.Fatalf("user-password-policy self status=%d call=%+v body=%s", res.Code, store.userRuleCall, res.Body.String())
	}

	// Create user with policy + must_change_password.
	res = httptest.NewRecorder()
	h.ServeHTTP(res, accountRequest(http.MethodPost, "/users/create", url.Values{
		"username":             {"new_op"},
		"password":             {"InitialPass123!"},
		"role":                 {"operator"},
		"password_policy_mode": {"policy"},
		"must_change_password": {"on"},
	}))
	if res.Code != http.StatusSeeOther || store.createdCall == nil || !store.createdCall.mustChange || store.createdCall.mode != "policy" {
		t.Fatalf("users/create status=%d call=%+v body=%s", res.Code, store.createdCall, res.Body.String())
	}

	// Reset password for other user passes mustChange=true, for self forces false.
	res = httptest.NewRecorder()
	h.ServeHTTP(res, accountRequest(http.MethodPost, "/users/reset-password", url.Values{
		"id":                   {"2"},
		"password":             {"ResetPass12345!"},
		"must_change_password": {"on"},
	}))
	if res.Code != http.StatusSeeOther || store.resetCall == nil || !store.resetCall.mustChange {
		t.Fatalf("reset other status=%d call=%+v", res.Code, store.resetCall)
	}

	res = httptest.NewRecorder()
	h.ServeHTTP(res, accountRequest(http.MethodPost, "/users/reset-password", url.Values{
		"id":                   {"1"},
		"password":             {"SelfResetPass123!"},
		"must_change_password": {"on"},
	}))
	if res.Code != http.StatusSeeOther || store.resetCall == nil || store.resetCall.mustChange {
		t.Fatalf("reset self must not force mustChange: status=%d call=%+v", res.Code, store.resetCall)
	}

	// Non-admin forbidden from policy endpoints.
	store.user = op
	res = httptest.NewRecorder()
	h.ServeHTTP(res, accountRequest(http.MethodPost, "/users/password-policy", url.Values{"max_age_days": {"30"}, "warn_days": {"7"}}))
	if res.Code != http.StatusForbidden {
		t.Fatalf("non-admin password-policy status=%d want 403", res.Code)
	}
}
