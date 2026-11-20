package control

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type stubExternalChecker struct {
	calls  atomic.Int32
	status ExternalUserStatus
	err    error
}

func (c *stubExternalChecker) CheckExternalUser(_ context.Context, _, _ string) (ExternalUserStatus, error) {
	c.calls.Add(1)
	return c.status, c.err
}

func TestHighestRoleAndExternalPasswordPolicy(t *testing.T) {
	if got := HighestRole("observer", "operator"); got != "operator" {
		t.Fatalf("HighestRole = %q, want operator", got)
	}
	if got := HighestRole("observer", "admin", "operator"); got != "admin" {
		t.Fatalf("HighestRole = %q, want admin", got)
	}
	if got := HighestRole("unknown"); got != "" {
		t.Fatalf("HighestRole = %q, want empty", got)
	}

	extUser := User{
		ID:                 2,
		Username:           "i.petrov",
		AuthSource:         AuthSourceLDAP,
		ExternalID:         "ad-1:guid-1",
		Role:               "operator",
		Enabled:            true,
		MustChangePassword: true,
		PasswordChangedAt:  time.Now().Add(-365 * 24 * time.Hour),
	}
	if extUser.IsLocal() {
		t.Fatal("LDAP user must not be local")
	}
	st := EvaluatePasswordState(extUser, PasswordPolicy{MaxAgeDays: 30, WarnDays: 7}, time.Now())
	if st.RequiresChange() || !st.NeverExpires {
		t.Fatalf("external user password state = %+v, want NeverExpires=true RequiresChange=false", st)
	}
}

func TestVerifyExternalSessionHybridCacheAndLivePOST(t *testing.T) {
	checker := &stubExternalChecker{
		status: ExternalUserStatus{Active: true, Role: "operator", Username: "i.petrov"},
	}
	srv := &Server{ExternalChecker: checker}
	user := User{
		ID:         10,
		Username:   "i.petrov",
		AuthSource: AuthSourceLDAP,
		ExternalID: "ad-1:1234",
		Role:       "observer",
		Enabled:    true,
		Language:   "ru",
	}

	// First GET hits live checker and promotes role observer -> operator.
	u1, code, _ := srv.verifyExternalSession(context.Background(), user, http.MethodGet)
	if code != 0 || u1.Role != "operator" {
		t.Fatalf("first GET: code=%d role=%q", code, u1.Role)
	}
	if got := checker.calls.Load(); got != 1 {
		t.Fatalf("calls = %d, want 1", got)
	}

	// Second GET within 30s uses cache without hitting checker.
	u2, code, _ := srv.verifyExternalSession(context.Background(), u1, http.MethodGet)
	if code != 0 || u2.Role != "operator" {
		t.Fatalf("second GET: code=%d role=%q", code, u2.Role)
	}
	if got := checker.calls.Load(); got != 1 {
		t.Fatalf("cached GET calls = %d, want 1", got)
	}

	// POST always bypasses cache: simulate revocation in AD.
	checker.status = ExternalUserStatus{Active: false}
	_, code, _ = srv.verifyExternalSession(context.Background(), u1, http.MethodPost)
	if code != http.StatusForbidden {
		t.Fatalf("revoked POST code = %d, want 403", code)
	}
	if got := checker.calls.Load(); got != 2 {
		t.Fatalf("live POST calls = %d, want 2", got)
	}

	// Outage returns 503 Service Unavailable.
	checker.err = errors.New("connection refused")
	_, code, _ = srv.verifyExternalSession(context.Background(), u1, http.MethodGet)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("outage GET code = %d, want 503", code)
	}
}

func TestRenderIntegrationsUsersAndAccountForExternalUsers(t *testing.T) {
	// 1. /integrations template (Variant 1, no <details>/<summary>).
	rec := httptest.NewRecorder()
	render(rec, "integrations", pageData{
		Info: requestContext{
			User: User{ID: 1, Username: "admin", AuthSource: AuthSourceLocal, Role: "admin", Enabled: true, Language: "ru"},
			CSRF: "csrf-token",
			Path: "/integrations",
		},
		Integrations: IntegrationsView{
			LDAP: LDAPIntegrationView{
				Enabled: true,
				Config:  LDAPConfig{ServerURL: "ldaps://dc01.corp.local:636", BaseDN: "DC=corp,DC=local", NestedGroups: true},
			},
			Keycloak: KeycloakIntegrationView{
				Enabled:     false,
				Config:      KeycloakConfig{IssuerURL: "https://sso.corp.local/realms/corp", ClientID: "omb-control"},
				CallbackURL: "https://pxe.corp.local:8443/auth/oidc/callback",
			},
		},
	})
	body := rec.Body.String()
	for _, want := range []string{"Active Directory / LDAP", "Keycloak (OIDC)", "id=\"ldap-config-dialog\"", "id=\"keycloak-config-dialog\"", "/assets/integrations.js"} {
		if !strings.Contains(body, want) {
			t.Errorf("/integrations missing %q", want)
		}
	}
	if strings.Contains(body, "<details") || strings.Contains(body, "<summary") {
		t.Error("/integrations must not contain <details>/<summary>")
	}

	// 2. /login template (Variant A with LDAP + Keycloak enabled).
	recLogin := httptest.NewRecorder()
	render(recLogin, "login", pageData{Lang: "ru", CSRF: "c1", LDAPEnabled: true, KeycloakEnabled: true})
	loginHTML := recLogin.Body.String()
	for _, want := range []string{"login-mode-seg", "data-login-mode=\"ldap\"", "data-login-mode=\"local\"", "href=\"/auth/oidc/login\""} {
		if !strings.Contains(loginHTML, want) {
			t.Errorf("/login missing %q", want)
		}
	}

	// 3. /users template with local + LDAP user.
	recUsers := httptest.NewRecorder()
	render(recUsers, "users", pageData{
		Info: requestContext{
			User: User{ID: 1, Username: "admin", AuthSource: AuthSourceLocal, Role: "admin", Enabled: true, Language: "ru"},
			CSRF: "c1",
			Path: "/users",
		},
		Users: []User{
			{ID: 1, Username: "admin", AuthSource: AuthSourceLocal, Role: "admin", Enabled: true, FSAccessMode: "rw"},
			{ID: 2, Username: "i.petrov", AuthSource: AuthSourceLDAP, ExternalID: "ad:1", DisplayName: "Иван Петров", Role: "operator", Enabled: true, FSAccessMode: "ro"},
		},
		NowTime: time.Now(),
	})
	usersHTML := recUsers.Body.String()
	for _, want := range []string{"chip-src-ldap", "из AD", "data-user-fs-id=\"2\""} {
		if !strings.Contains(usersHTML, want) {
			t.Errorf("/users missing %q", want)
		}
	}
	if strings.Contains(usersHTML, "data-user-delete-id=\"2\"") || strings.Contains(usersHTML, "data-user-pw-id=\"2\"") {
		t.Error("/users must hide local password reset and delete actions for external user #2")
	}

	// 4. /account template for external user with SMB/SFTP access.
	recAcc := httptest.NewRecorder()
	render(recAcc, "account", pageData{
		Info: requestContext{
			User: User{ID: 2, Username: "i.petrov", AuthSource: AuthSourceLDAP, ExternalID: "ad:1", Role: "operator", Enabled: true, Language: "ru", FSAccessMode: "rw"},
			CSRF: "c1",
			Path: "/account",
		},
		FileServer: fileServerView{SMBEnabled: true, SFTPEnabled: true, SMBAddress: "smb://192.168.10.135/iso", SFTPPort: 2022},
	})
	accHTML := recAcc.Body.String()
	if strings.Contains(accHTML, "data-account-password-open") || strings.Contains(accHTML, "id=\"account-password-dialog\"") {
		t.Error("/account must hide OMB password change for external user")
	}
	if !strings.Contains(accHTML, "LDAP / Active Directory") || !strings.Contains(accHTML, "data-account-fs-password-open") {
		t.Error("/account must show external source and preserve SMB/SFTP card")
	}
}
