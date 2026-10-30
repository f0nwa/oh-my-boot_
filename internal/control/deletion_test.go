package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"pxe-platform/internal/agent"
)

type deletionStore struct {
	audits        []string
	installations []Installation
	authOnlyStore
	versions []MenuVersion
	profiles []WindowsProfile
	answers  []WindowsAnswerVariant
	configs  []DriverConfig
	deleted  []string
}

func (s *deletionStore) MenuVersions(context.Context) ([]MenuVersion, error) {
	if len(s.versions) == 0 {
		s.versions = []MenuVersion{{Active: true, Script: "#!ipxe\n"}}
	}
	versions := append([]MenuVersion(nil), s.versions...)
	for i := range versions {
		if versions[i].SHA256 == "" {
			sum := sha256.Sum256([]byte(versions[i].Script))
			versions[i].SHA256 = hex.EncodeToString(sum[:])
		}
	}
	return versions, nil
}

type deletionMenuReader struct {
	store    *deletionStore
	override []byte
}

func (m deletionMenuReader) CurrentMenu(ctx context.Context) ([]byte, error) {
	if m.override != nil {
		return m.override, nil
	}
	versions, err := m.store.MenuVersions(ctx)
	if err != nil {
		return nil, err
	}
	for _, version := range versions {
		if version.Active {
			return []byte(version.Script), nil
		}
	}
	return nil, fmt.Errorf("active menu missing")
}

func deletionTestServer(store *deletionStore, cleaner *deletionCleaner) *Server {
	return &Server{Store: store, Cleanup: cleaner, DeletionMenu: deletionMenuReader{store: store}}
}
func (s *deletionStore) WindowsProfiles(context.Context) ([]WindowsProfile, error) {
	return s.profiles, nil
}
func (s *deletionStore) WindowsAnswers(context.Context) ([]WindowsAnswerVariant, error) {
	return s.answers, nil
}
func (s *deletionStore) DriverConfigs(context.Context) ([]DriverConfig, error) { return s.configs, nil }
func (s *deletionStore) Audit(_ context.Context, _ int64, action, result, detail string) error {
	s.audits = append(s.audits, action+"|"+result+"|"+detail)
	return nil
}
func (s *deletionStore) DriverPackages(context.Context) ([]DriverPackage, error) { return nil, nil }
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
	calls       []string
	fail        bool
	removeWIM   bool
	manifestKey string
}

func (c *deletionCleaner) DriverConfigWIMKey(context.Context, int64) (string, error) {
	return c.manifestKey, nil
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
	server := deletionTestServer(s, c)
	s.installations = []Installation{{ID: 3003, DriverConfigID: 3}}
	s.versions = []MenuVersion{{Active: true, Script: "item install_3003 Windows test\n", Items: []MenuVersionItem{{InstallationID: 3003}}}}
	if res := submitDeletion(t, server, "/drivers/configs/3/delete"); res.Code != http.StatusConflict || len(c.calls) != 0 {
		t.Fatalf("active status=%d cleanup=%v", res.Code, c.calls)
	} else if body := res.Body.String(); !strings.Contains(body, `role="alert"`) || !strings.Contains(body, "Сначала снимите пункт с публикации меню") || !strings.Contains(body, "<h1>Драйверы") {
		t.Fatalf("conflict is not shown on the drivers page: %s", body)
	}
	s.versions = []MenuVersion{{Active: true, Script: "#!ipxe\n", Items: []MenuVersionItem{}}}
	s.installations = nil
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
	server := deletionTestServer(s, c)
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

func TestDeleteFailsClosedWhenBootMenuDiffersFromDatabase(t *testing.T) {
	s := &deletionStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}, configs: []DriverConfig{{ID: 3}}, versions: []MenuVersion{{Active: true, Script: "#!ipxe\n"}}}
	c := &deletionCleaner{}
	server := deletionTestServer(s, c)
	server.DeletionMenu = deletionMenuReader{store: s, override: []byte("#!ipxe\nitem install_3003 Test\n")}
	if res := submitDeletion(t, server, "/drivers/configs/3/delete"); res.Code != http.StatusServiceUnavailable || len(c.calls) != 0 || len(s.deleted) != 0 {
		t.Fatalf("mismatched menu status=%d cleanup=%v deleted=%v", res.Code, c.calls, s.deleted)
	}
}

func TestDeleteDriverConfigUsesAgentManifestWhenDatabaseMissedBuild(t *testing.T) {
	key := strings.Repeat("b", 64)
	s := &deletionStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}, configs: []DriverConfig{{ID: 3}}}
	c := &deletionCleaner{manifestKey: key}
	if res := submitDeletion(t, deletionTestServer(s, c), "/drivers/configs/3/delete"); res.Code != http.StatusSeeOther || !c.removeWIM {
		t.Fatalf("manifest recovery status=%d removeWIM=%v", res.Code, c.removeWIM)
	}
}

func TestDeleteProfileAndAnswerBlockDependencies(t *testing.T) {
	s := &deletionStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}, profiles: []WindowsProfile{{ID: 17}}, answers: []WindowsAnswerVariant{{ID: 4, ProfileID: 17}}, configs: []DriverConfig{{ID: 3, ProfileID: 17, AnswerID: 4}}}
	c := &deletionCleaner{}
	server := deletionTestServer(s, c)
	for _, path := range []string{"/images/windows-profiles/17/delete", "/images/windows-answers/4/delete"} {
		if res := submitDeletion(t, server, path); res.Code != http.StatusConflict {
			t.Fatalf("%s status=%d", path, res.Code)
		}
	}
	if len(c.calls) != 0 || len(s.deleted) != 0 {
		t.Fatalf("dependent assets changed: %v %v", c.calls, s.deleted)
	}
}

func TestMenuHistoryMarksDeletedRecordUnavailable(t *testing.T) {
	store := &deletionStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}, versions: []MenuVersion{
		{ID: 7, Script: "#!ipxe\nitem install_3099 Old test\n", Title: "Old"},
		{ID: 8, Active: true, Script: "#!ipxe\n", Title: "Current"},
	}}
	server := &Server{Store: store}
	req := httptest.NewRequest(http.MethodGet, "/menu", nil)
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "Удалённый пункт") || strings.Contains(res.Body.String(), `action="/menu/rollback"`) {
		t.Fatalf("history status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestDeleteEndpointsRequireAdminAndCSRF(t *testing.T) {
	for _, role := range []string{"operator", "observer"} {
		store := &deletionStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: role, Enabled: true}}}
		if res := submitDeletion(t, &Server{Store: store}, "/drivers/configs/3/delete"); res.Code != http.StatusForbidden {
			t.Fatalf("role=%s status=%d", role, res.Code)
		}
	}
	store := &deletionStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
	req := httptest.NewRequest(http.MethodPost, "/drivers/configs/3/delete", strings.NewReader("csrf=wrong"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	res := httptest.NewRecorder()
	(&Server{Store: store}).Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("bad CSRF status=%d", res.Code)
	}
}

func TestAdminPagesExposeRecordDeletionActions(t *testing.T) {
	data := pageData{Info: requestContext{User: User{ID: 1, Role: "admin"}, CSRF: "test"}, WindowsProfiles: []WindowsProfile{{ID: 17, WindowsProfileSource: WindowsProfileSource{Title: "Windows", Path: "win.iso"}, State: "ready"}}, WindowsAnswerVariants: []WindowsAnswerVariant{{ID: 4, ProfileID: 17, Title: "Lab"}}, DriverConfigs: []DriverConfig{{ID: 3, Title: "VirtIO"}}}
	for _, tc := range []struct{ page, action string }{
		{"images", `/images/windows-profiles/17/delete`},
		{"answers", `/images/windows-answers/4/delete`},
		{"drivers", `/drivers/configs/3/delete`},
	} {
		res := httptest.NewRecorder()
		render(res, tc.page, data)
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `action="`+tc.action+`"`) {
			t.Fatalf("%s missing %s: %d %s", tc.page, tc.action, res.Code, res.Body.String())
		}
	}
	data.WindowsProfiles[0].State = "unavailable"
	res := httptest.NewRecorder()
	render(res, "answers", data)
	if !strings.Contains(res.Body.String(), `action="/images/windows-answers/4/delete"`) || strings.Contains(res.Body.String(), `<option value="17"`) {
		t.Fatalf("unavailable parent hid deletion or allowed upload: %d %s", res.Code, res.Body.String())
	}
}

func TestMenuHistoryShowsVersionNumberAndTimeZone(t *testing.T) {
	msk := time.FixedZone("MSK", 3*60*60)
	store := &deletionStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}, versions: []MenuVersion{
		{ID: 21, At: time.Date(2026, 10, 19, 21, 23, 0, 0, msk), Script: "#!ipxe\n", Title: "Old"},
		{ID: 25, Active: true, At: time.Date(2026, 10, 19, 23, 17, 0, 0, msk), Script: "#!ipxe\n", Title: "Current"},
	}}
	req := httptest.NewRequest(http.MethodGet, "/menu", nil)
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	res := httptest.NewRecorder()
	(&Server{Store: store}).Handler().ServeHTTP(res, req)
	body := html.UnescapeString(res.Body.String())
	for _, want := range []string{"<th>№</th>", "<td>#21</td>", "<td>#25</td>", "19.10.2026 21:23 UTC+03:00"} {
		if !strings.Contains(body, want) {
			t.Fatalf("menu history lacks %q: status=%d body=%s", want, res.Code, body)
		}
	}
}

func TestRejectedDeletionsAreAudited(t *testing.T) {
	s := &deletionStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}},
		profiles: []WindowsProfile{{ID: 17}}, answers: []WindowsAnswerVariant{{ID: 4, ProfileID: 17}},
		configs: []DriverConfig{{ID: 3, ProfileID: 17, AnswerID: 4}}}
	server := deletionTestServer(s, &deletionCleaner{})
	s.installations = []Installation{{ID: 3003, DriverConfigID: 3}}
	s.versions = []MenuVersion{{Active: true, Script: "item install_3003 Windows test\n", Items: []MenuVersionItem{{InstallationID: 3003}}}}
	submitDeletion(t, server, "/drivers/configs/3/delete")
	s.installations = nil
	s.versions = []MenuVersion{{Active: true, Script: "#!ipxe\n"}}
	submitDeletion(t, server, "/images/windows-answers/4/delete")
	submitDeletion(t, server, "/images/windows-profiles/17/delete")
	want := []string{
		"driver-config.delete|rejected|id=3 published",
		"windows-answer.delete|rejected|id=4 used by driver config 3",
		"windows-profile.delete|rejected|id=17 used by answer 4",
	}
	if strings.Join(s.audits, "\n") != strings.Join(want, "\n") {
		t.Fatalf("audits = %q, want %q", s.audits, want)
	}
}

func (s *deletionStore) Installations(context.Context) ([]Installation, error) {
	return s.installations, nil
}
func (s *deletionStore) BootProfiles(context.Context) ([]BootProfile, error) { return nil, nil }
