package control

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"pxe-platform/internal/agent"
	"pxe-platform/internal/images"
)

type fakeRecoveryAPI struct {
	status agent.RecoveryStatus
	set    []agent.RecoveryConfig
	runs   int
	err    error
}

func (f *fakeRecoveryAPI) StorageRecoveryStatus(context.Context) (agent.RecoveryStatus, error) {
	return f.status, f.err
}
func (f *fakeRecoveryAPI) SetStorageRecovery(_ context.Context, cfg agent.RecoveryConfig) (agent.RecoveryStatus, error) {
	f.set = append(f.set, cfg)
	f.status.RecoveryConfig = cfg
	return f.status, nil
}
func (f *fakeRecoveryAPI) RunStorageRecovery(context.Context) (agent.RecoveryStatus, error) {
	f.runs++
	return f.status, nil
}

type recoveryPageStore struct {
	answerPageStore
	audits []string
}

func (s *recoveryPageStore) Audit(_ context.Context, _ int64, action, result, detail string) error {
	s.audits = append(s.audits, action+"|"+result+"|"+detail)
	return nil
}

func recoveryServer(role string) (*Server, *recoveryPageStore, *fakeRecoveryAPI) {
	store := &recoveryPageStore{answerPageStore: answerPageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: role, Enabled: true}}}}
	api := &fakeRecoveryAPI{status: agent.RecoveryStatus{
		RecoveryConfig: agent.RecoveryConfig{Enabled: true, IntervalSeconds: 120},
		LastRun:        time.Date(2026, 10, 20, 12, 7, 0, 0, time.UTC),
		Actions:        []string{"restarted pxe-winiso@1.service"},
		LastError:      "mnt-x.mount: start failed",
	}}
	return &Server{Store: store, Images: &fakeImagesAPI{catalog: images.Catalog{}}, StorageRecovery: api}, store, api
}

func TestImagesPageShowsStorageRecovery(t *testing.T) {
	for _, tc := range []struct {
		role     string
		editable bool
	}{{"admin", true}, {"operator", false}, {"observer", false}} {
		s, _, _ := recoveryServer(tc.role)
		res := httptest.NewRecorder()
		s.Handler().ServeHTTP(res, accountRequest(http.MethodGet, "/storages", nil))
		body := res.Body.String()
		for _, want := range []string{"Автоматическое восстановление подключений", "restarted pxe-winiso@1.service", "mnt-x.mount: start failed", "каждые 2 мин"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: images page lacks %q", tc.role, want)
			}
		}
		hasForm := strings.Contains(body, `action="/images/storage-recovery"`) && strings.Contains(body, `name="interval_minutes" value="2"`) && strings.Contains(body, `action="/images/storage-recovery/run"`)
		if hasForm != tc.editable {
			t.Errorf("%s: recovery form shown=%v, want %v", tc.role, hasForm, tc.editable)
		}
	}
}

func TestStorageRecoverySettings(t *testing.T) {
	s, store, api := recoveryServer("admin")
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, accountRequest(http.MethodPost, "/images/storage-recovery", url.Values{"enabled": {"on"}, "interval_minutes": {"5"}}))
	if res.Code != http.StatusSeeOther || len(api.set) != 1 || api.set[0] != (agent.RecoveryConfig{Enabled: true, IntervalSeconds: 300}) {
		t.Fatalf("save: %d %+v", res.Code, api.set)
	}
	res = httptest.NewRecorder()
	s.Handler().ServeHTTP(res, accountRequest(http.MethodPost, "/images/storage-recovery", url.Values{"interval_minutes": {"10"}}))
	if res.Code != http.StatusSeeOther || api.set[1] != (agent.RecoveryConfig{Enabled: false, IntervalSeconds: 600}) {
		t.Fatalf("disable: %d %+v", res.Code, api.set)
	}
	for _, bad := range []string{"0", "61", "abc"} {
		res = httptest.NewRecorder()
		s.Handler().ServeHTTP(res, accountRequest(http.MethodPost, "/images/storage-recovery", url.Values{"enabled": {"on"}, "interval_minutes": {bad}}))
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `role="alert"`) {
			t.Fatalf("interval %q: %d", bad, res.Code)
		}
	}
	if len(api.set) != 2 {
		t.Fatalf("invalid settings reached Agent: %+v", api.set)
	}
	res = httptest.NewRecorder()
	s.Handler().ServeHTTP(res, accountRequest(http.MethodPost, "/images/storage-recovery/run", url.Values{}))
	if res.Code != http.StatusSeeOther || api.runs != 1 {
		t.Fatalf("run: %d runs=%d", res.Code, api.runs)
	}
	want := "storage-recovery.update|ok|enabled=true interval=300s,storage-recovery.update|ok|enabled=false interval=600s,storage-recovery.run|error|actions=1"
	if strings.Join(store.audits, ",") != want {
		t.Fatalf("audits = %q", store.audits)
	}
}

func TestStorageRecoveryRequiresAdmin(t *testing.T) {
	s, _, api := recoveryServer("operator")
	for _, path := range []string{"/images/storage-recovery", "/images/storage-recovery/run"} {
		res := httptest.NewRecorder()
		s.Handler().ServeHTTP(res, accountRequest(http.MethodPost, path, url.Values{"enabled": {"on"}, "interval_minutes": {"5"}}))
		if res.Code != http.StatusForbidden {
			t.Fatalf("%s by operator: %d", path, res.Code)
		}
	}
	if len(api.set) != 0 || api.runs != 0 {
		t.Fatal("operator changed recovery")
	}
}

func TestImagesPageSurvivesUnavailableRecovery(t *testing.T) {
	s, _, api := recoveryServer("admin")
	api.err = errors.New("agent down")
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, accountRequest(http.MethodGet, "/storages", nil))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "Состояние восстановления недоступно") {
		t.Fatalf("page with unavailable recovery: %d", res.Code)
	}
}
