package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type authOnlyStore struct {
	Storage
	user User
}

func (a authOnlyStore) Session(context.Context, string) (User, error)              { return a.user, nil }
func (a authOnlyStore) Audit(context.Context, int64, string, string, string) error { return nil }
func (a authOnlyStore) WindowsProfiles(context.Context) ([]WindowsProfile, error) {
	return []WindowsProfile{}, nil
}
func (a authOnlyStore) WindowsAnswers(context.Context) ([]WindowsAnswerVariant, error) {
	return []WindowsAnswerVariant{}, nil
}
func (a authOnlyStore) DriverConfigs(context.Context) ([]DriverConfig, error) {
	return []DriverConfig{}, nil
}

func TestServerRedirectsAnonymousDashboard(t *testing.T) {
	s := &Server{Store: authOnlyStore{user: User{ID: 1, Role: "observer", Enabled: true}}}
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, httptest.NewRequest("GET", "/", nil))
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/login" {
		t.Fatalf("response=%d %q", res.Code, res.Header().Get("Location"))
	}
}

func TestServerRejectsObserverMutation(t *testing.T) {
	s := &Server{Store: authOnlyStore{user: User{ID: 1, Role: "observer", Enabled: true}}}
	req := httptest.NewRequest("POST", "/menu/publish", strings.NewReader("title=Test"))
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("response=%d", res.Code)
	}
}

func TestServerRejectsObserverPasswordReset(t *testing.T) {
	s := &Server{Store: authOnlyStore{user: User{ID: 1, Role: "observer", Enabled: true}}}
	req := httptest.NewRequest("POST", "/users/reset-password", strings.NewReader("id=1"))
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("response=%d", res.Code)
	}
}

func TestServerRequiresCSRFForAdministratorMutation(t *testing.T) {
	s := &Server{Store: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
	req := httptest.NewRequest("POST", "/network/apply", strings.NewReader("mode=proxy"))
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("response=%d", res.Code)
	}
}

func (a authOnlyStore) MenuDraft(context.Context) (MenuDraft, bool, error) {
	return MenuDraft{}, false, nil
}
func (a authOnlyStore) SaveMenuDraft(context.Context, MenuDraft) error      { return nil }
func (a authOnlyStore) MenuVersions(context.Context) ([]MenuVersion, error) { return nil, nil }

func (a authOnlyStore) BootProfiles(context.Context) ([]BootProfile, error)   { return nil, nil }
func (a authOnlyStore) Installations(context.Context) ([]Installation, error) { return nil, nil }

func (a authOnlyStore) UbuntuAutoinstalls(context.Context) ([]UbuntuAutoinstallSummary, error) {
	return nil, nil
}
