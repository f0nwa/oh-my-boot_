package control

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omb/internal/agent"
)

type packageDeleteStore struct {
	*driverConfigStore
	configErr error
}

func (s *packageDeleteStore) DriverConfigs(ctx context.Context) ([]DriverConfig, error) {
	if s.configErr != nil {
		return nil, s.configErr
	}
	return s.driverConfigStore.DriverConfigs(ctx)
}
func (s *packageDeleteStore) DeleteDriverPackage(_ context.Context, hash string) error {
	for i, p := range s.packages {
		if p.SHA256 == hash {
			s.packages = append(s.packages[:i], s.packages[i+1:]...)
			return nil
		}
	}
	return os.ErrNotExist
}

func TestDeleteDriverPackageProtectsDependenciesAndRemovesUnusedFiles(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	used, free := strings.Repeat("a", 64), strings.Repeat("b", 64)
	for _, hash := range []string{used, free} {
		if err := os.Mkdir(filepath.Join(root, hash), 0700); err != nil {
			t.Fatal(err)
		}
	}
	store := &packageDeleteStore{driverConfigStore: configFixture()}
	store.user.Role = "admin"
	store.packages = []DriverPackage{{SHA256: used}, {SHA256: free}}
	store.configs = []DriverConfig{{ID: 1, PackageINFs: []agent.SelectedINF{{PackageSHA256: used, INFPath: "net.inf"}}}}
	client := inMemoryAgentClient(agent.HandlerWithDriverManager(nil, nil, nil, &agent.DriverManager{Root: root}))
	server := &Server{Store: store, Cleanup: client}
	if res := submitDeletion(t, server, "/drivers/packages/"+used+"/delete"); res.Code != http.StatusConflict {
		t.Fatalf("used package status=%d", res.Code)
	}
	if _, err := os.Stat(filepath.Join(root, used)); err != nil {
		t.Fatal("used files removed", err)
	}
	store.configErr = errors.New("database unavailable")
	if res := submitDeletion(t, server, "/drivers/packages/"+free+"/delete"); res.Code != http.StatusServiceUnavailable {
		t.Fatalf("failed dependency lookup status=%d", res.Code)
	}
	store.configErr = nil

	// Cleanup failure must leave the catalog record available for a safe retry.
	if err := os.Remove(filepath.Join(root, free)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, used), filepath.Join(root, free)); err != nil {
		t.Fatal(err)
	}
	if res := submitDeletion(t, server, "/drivers/packages/"+free+"/delete"); res.Code != http.StatusServiceUnavailable || len(store.packages) != 2 {
		t.Fatalf("cleanup failure: status=%d packages=%v", res.Code, store.packages)
	}
	if err := os.Remove(filepath.Join(root, free)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, free), 0700); err != nil {
		t.Fatal(err)
	}
	if res := submitDeletion(t, server, "/drivers/packages/"+free+"/delete"); res.Code != http.StatusSeeOther {
		t.Fatalf("free package status=%d body=%s", res.Code, res.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, free)); !os.IsNotExist(err) {
		t.Fatal("unused package files remain", err)
	}
	if len(store.packages) != 1 || store.packages[0].SHA256 != used {
		t.Fatalf("catalog=%+v", store.packages)
	}
}

func TestDeleteDriverPackageRequiresAdminAndCSRF(t *testing.T) {
	for _, role := range []string{"operator", "observer", "admin"} {
		store := configFixture()
		store.user.Role = role
		csrf := CSRFToken("test-token")
		if role == "admin" {
			csrf = "wrong"
		}
		req := httptest.NewRequest(http.MethodPost, "/drivers/packages/"+strings.Repeat("a", 64)+"/delete", strings.NewReader("csrf="+csrf))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
		res := httptest.NewRecorder()
		(&Server{Store: store}).Handler().ServeHTTP(res, req)
		if res.Code != http.StatusForbidden {
			t.Errorf("%s status=%d", role, res.Code)
		}
	}
}
