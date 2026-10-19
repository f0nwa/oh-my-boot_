package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"pxe-platform/internal/agent"
)

type deletionStore struct {
	authOnlyStore
	versions []MenuVersion
	profiles []WindowsProfile
	answers  []WindowsAnswerVariant
	configs  []DriverConfig
	deleted  []string
}

func (s *deletionStore) MenuVersions(context.Context) ([]MenuVersion, error) { return s.versions, nil }
func (s *deletionStore) WindowsProfiles(context.Context) ([]WindowsProfile, error) {
	return s.profiles, nil
}
func (s *deletionStore) WindowsAnswers(context.Context) ([]WindowsAnswerVariant, error) {
	return s.answers, nil
}
func (s *deletionStore) DriverConfigs(context.Context) ([]DriverConfig, error) { return s.configs, nil }
func (s *deletionStore) WindowsProfile(_ context.Context, id int64) (WindowsProfile, error) {
	for _, item := range s.profiles {
		if item.ID == id {
			return item, nil
		}
	}
	return WindowsProfile{}, pgx.ErrNoRows
}
func (s *deletionStore) WindowsAnswer(_ context.Context, id int64) (WindowsAnswerVariant, error) {
	for _, item := range s.answers {
		if item.ID == id {
			return item, nil
		}
	}
	return WindowsAnswerVariant{}, pgx.ErrNoRows
}
func (s *deletionStore) DriverConfig(_ context.Context, id int64) (DriverConfig, error) {
	for _, item := range s.configs {
		if item.ID == id {
			return item, nil
		}
	}
	return DriverConfig{}, pgx.ErrNoRows
}
func (s *deletionStore) DeleteWindowsProfile(_ context.Context, id int64) error {
	s.deleted = append(s.deleted, "profile")
	for i, item := range s.profiles {
		if item.ID == id {
			s.profiles = append(s.profiles[:i], s.profiles[i+1:]...)
			return nil
		}
	}
	return nil
}
func (s *deletionStore) DeleteWindowsAnswer(_ context.Context, id int64) error {
	s.deleted = append(s.deleted, "answer")
	for i, item := range s.answers {
		if item.ID == id {
			s.answers = append(s.answers[:i], s.answers[i+1:]...)
			return nil
		}
	}
	return nil
}
func (s *deletionStore) DeleteDriverConfig(_ context.Context, id int64) error {
	s.deleted = append(s.deleted, "config")
	for i, item := range s.configs {
		if item.ID == id {
			s.configs = append(s.configs[:i], s.configs[i+1:]...)
			return nil
		}
	}
	return nil
}

type deletionCleaner struct {
	calls     []string
	fail      bool
	removeWIM bool
}

func (c *deletionCleaner) RemoveWindowsProfile(context.Context, int64) error {
	c.calls = append(c.calls, "profile")
	if c.fail {
		return pgx.ErrNoRows
	}
	return nil
}
func (c *deletionCleaner) RemoveWindowsAnswer(context.Context, int64) error {
	c.calls = append(c.calls, "answer")
	if c.fail {
		return pgx.ErrNoRows
	}
	return nil
}
func (c *deletionCleaner) RemoveDriverConfig(_ context.Context, id int64, key string, removeWIM bool) error {
	c.calls = append(c.calls, "config")
	c.removeWIM = removeWIM
	if c.fail {
		return pgx.ErrNoRows
	}
	return nil
}

func submitDeletion(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("csrf="+CSRFToken("test-token")))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	return res
}

func TestDeleteDriverConfigBlocksActiveThenCleans(t *testing.T) {
	s := &deletionStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}, configs: []DriverConfig{{ID: 3, ProfileID: 17, State: "ready", Build: agent.DriverBuildResult{WIMKey: strings.Repeat("a", 64)}}}}
	c := &deletionCleaner{}
	server := &Server{Store: s, Cleanup: c}
	s.versions = []MenuVersion{{Active: true, Script: "item windriver_3 Windows test\n"}}
	if res := submitDeletion(t, server, "/drivers/configs/3/delete"); res.Code != http.StatusConflict || len(c.calls) != 0 {
		t.Fatalf("active status=%d cleanup=%v", res.Code, c.calls)
	}
	s.versions = []MenuVersion{{Active: true, Script: "#!ipxe\n"}}
	if res := submitDeletion(t, server, "/drivers/configs/3/delete"); res.Code != http.StatusSeeOther || strings.Join(c.calls, ",") != "config" || strings.Join(s.deleted, ",") != "config" {
		t.Fatalf("delete status=%d cleanup=%v db=%v", res.Code, c.calls, s.deleted)
	}
	if res := submitDeletion(t, server, "/drivers/configs/3/delete"); res.Code != http.StatusNotFound {
		t.Fatalf("repeat status=%d", res.Code)
	}
}

func TestDeleteDriverConfigKeepsSharedWIMAndDoesNotDeleteOnCleanupFailure(t *testing.T) {
	key := strings.Repeat("a", 64)
	s := &deletionStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}, configs: []DriverConfig{{ID: 2, Build: agent.DriverBuildResult{WIMKey: key}}, {ID: 3, Build: agent.DriverBuildResult{WIMKey: key}}}}
	c := &deletionCleaner{fail: true}
	server := &Server{Store: s, Cleanup: c}
	if res := submitDeletion(t, server, "/drivers/configs/2/delete"); res.Code != http.StatusServiceUnavailable || len(s.deleted) != 0 || c.removeWIM {
		t.Fatalf("cleanup failure status=%d deleted=%v removeWIM=%v", res.Code, s.deleted, c.removeWIM)
	}
	c.fail = false
	if res := submitDeletion(t, server, "/drivers/configs/2/delete"); res.Code != http.StatusSeeOther || c.removeWIM {
		t.Fatalf("shared WIM status=%d removeWIM=%v", res.Code, c.removeWIM)
	}
	if res := submitDeletion(t, server, "/drivers/configs/3/delete"); res.Code != http.StatusSeeOther || !c.removeWIM {
		t.Fatalf("last WIM status=%d removeWIM=%v", res.Code, c.removeWIM)
	}
}

func TestDeleteProfileAndAnswerBlockDependencies(t *testing.T) {
	s := &deletionStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}, profiles: []WindowsProfile{{ID: 17}}, answers: []WindowsAnswerVariant{{ID: 4, ProfileID: 17}}, configs: []DriverConfig{{ID: 3, ProfileID: 17, AnswerID: 4}}}
	c := &deletionCleaner{}
	server := &Server{Store: s, Cleanup: c}
	for _, path := range []string{"/images/windows-profiles/17/delete", "/images/windows-answers/4/delete"} {
		if res := submitDeletion(t, server, path); res.Code != http.StatusConflict {
			t.Fatalf("%s status=%d", path, res.Code)
		}
	}
	if len(c.calls) != 0 || len(s.deleted) != 0 {
		t.Fatalf("dependent assets changed: %v %v", c.calls, s.deleted)
	}
}
