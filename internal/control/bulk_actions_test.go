package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"omb/internal/boot"
)

func TestBulkSelectScriptEndpoint(t *testing.T) {
	s := &Server{Store: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/bulk-select.js", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "data-bulk-select-row") {
		t.Fatalf("GET /assets/bulk-select.js = %d len=%d", rec.Code, rec.Body.Len())
	}
}

func TestSafeSectionReturnURL(t *testing.T) {
	for _, tc := range []struct {
		raw, base, want string
		params          []string
	}{
		{"/answers?q=win&sort=title&message=old", "/answers", "/answers?q=win&sort=title", []string{"q", "sort", "dir"}},
		{"/machines?q=dell&rule=aa:bb:cc&evil=1", "/machines", "/machines?q=dell&rule=aa%3Abb%3Acc", []string{"q", "sort", "rule"}},
		{"https://evil.example/answers", "/answers", "/answers", []string{"q"}},
		{"/other?q=1", "/answers", "/answers", []string{"q"}},
	} {
		if got := safeSectionReturnURL(tc.raw, tc.base, tc.params...); got != tc.want {
			t.Errorf("safeSectionReturnURL(%q, %q) = %q, want %q", tc.raw, tc.base, got, tc.want)
		}
	}
}

type bulkUbuntuStore struct {
	*downloadsStore
	deletedUbuntu  []int64
	deletedWindows []int64
	answers        map[int64]WindowsAnswerVariant
}

func (s *bulkUbuntuStore) DeleteUbuntuAutoinstall(_ context.Context, id int64) error {
	s.deletedUbuntu = append(s.deletedUbuntu, id)
	return nil
}

func (s *bulkUbuntuStore) WindowsAnswer(_ context.Context, id int64) (WindowsAnswerVariant, error) {
	if a, ok := s.answers[id]; ok {
		return a, nil
	}
	return WindowsAnswerVariant{}, errNotFoundForTest
}

func (s *bulkUbuntuStore) DeleteWindowsAnswer(_ context.Context, id int64) error {
	s.deletedWindows = append(s.deletedWindows, id)
	return nil
}

var errNotFoundForTest = errInstallationPublished

type bulkCleanupStub struct{}

func (bulkCleanupStub) RemoveWindowsProfile(context.Context, int64) error          { return nil }
func (bulkCleanupStub) RemoveWindowsAnswer(context.Context, int64) error           { return nil }
func (bulkCleanupStub) RemoveDriverPackage(context.Context, string) error          { return nil }
func (bulkCleanupStub) RemoveDriverConfig(context.Context, int64, string, bool) error { return nil }
func (bulkCleanupStub) DriverConfigWIMKey(context.Context, int64) (string, error)  { return "", nil }
func (bulkCleanupStub) RemoveUbuntuSource(context.Context, int64) error            { return nil }

type bulkInitialMenuStub struct {
	baseURL string
}

func (m bulkInitialMenuStub) CurrentMenu(context.Context) ([]byte, error) {
	return boot.Menu(m.baseURL)
}

func TestUbuntuAutoinstallSingleAndBatchDelete(t *testing.T) {
	s, base, _ := downloadsServer(t)
	base.user = User{ID: 1, Username: "admin", Role: "admin", Enabled: true}
	base.installations = []Installation{
		{ID: 10, Title: "Ubuntu Prod", BootProfileID: 1, AutoinstallID: 20},
	}
	store := &bulkUbuntuStore{
		downloadsStore: base,
		answers: map[int64]WindowsAnswerVariant{
			5: {ID: 5, Title: "Win11 XML"},
		},
	}
	s.Store = store
	s.Cleanup = bulkCleanupStub{}
	s.DeletionMenu = bulkInitialMenuStub{baseURL: s.BootBaseURL}

	// Used Ubuntu autoinstall (20) must be rejected with 409.
	resUsed := menuPost(t, s, "/ubuntu-autoinstall/20/delete", url.Values{"return_to": {"/answers?q=ubu"}})
	if resUsed.Code != http.StatusConflict {
		t.Fatalf("expected 409 for used ubuntu script, got %d body=%s", resUsed.Code, resUsed.Body.String())
	}

	// Unused Ubuntu autoinstall (21) is deleted and redirects preserving return_to.
	resOK := menuPost(t, s, "/ubuntu-autoinstall/21/delete", url.Values{"return_to": {"/answers?q=ubu"}})
	if resOK.Code != http.StatusSeeOther || !strings.HasPrefix(resOK.Header().Get("Location"), "/answers?q=ubu") {
		t.Fatalf("single delete = %d loc=%q body=%s", resOK.Code, resOK.Header().Get("Location"), resOK.Body.String())
	}

	// Batch delete: Windows 5 (free), Ubuntu 22 (free), Ubuntu 20 (used -> skipped).
	resBatch := menuPost(t, s, "/answers/delete-batch", url.Values{
		"item":      {"windows:5", "ubuntu:22", "ubuntu:20"},
		"return_to": {"/answers?sort=title"},
	})
	if resBatch.Code != http.StatusSeeOther || !strings.HasPrefix(resBatch.Header().Get("Location"), "/answers?sort=title") {
		t.Fatalf("batch delete = %d loc=%q body=%s", resBatch.Code, resBatch.Header().Get("Location"), resBatch.Body.String())
	}
	if len(store.deletedWindows) != 1 || store.deletedWindows[0] != 5 {
		t.Fatalf("deletedWindows = %v", store.deletedWindows)
	}
	if len(store.deletedUbuntu) != 2 || store.deletedUbuntu[1] != 22 {
		t.Fatalf("deletedUbuntu = %v", store.deletedUbuntu)
	}

	// Operator gets 403 on delete-batch.
	base.user.Role = "operator"
	resOp := menuPost(t, s, "/answers/delete-batch", url.Values{"item": {"ubuntu:22"}})
	if resOp.Code != http.StatusForbidden {
		t.Fatalf("operator delete-batch = %d, want 403", resOp.Code)
	}
}

func TestInstallationsBulkRoleVisibilityAndDeleteSkipsPublished(t *testing.T) {
	s, store, _ := downloadsServer(t)
	store.user = User{ID: 2, Username: "op", Role: "operator", Enabled: true}
	opPage := libraryGet(t, s, "/installations")
	if !strings.Contains(opPage, `data-bulk-bar="installations"`) || !strings.Contains(opPage, `data-bulk-action-val="add"`) {
		t.Fatalf("operator must see bulk menu bar on /installations")
	}
	if strings.Contains(opPage, `installations-bulk-delete-dialog`) {
		t.Fatalf("operator must not see bulk delete dialog on /installations")
	}

	store.user.Role = "observer"
	obsPage := libraryGet(t, s, "/installations")
	if strings.Contains(obsPage, `data-bulk-bar="installations"`) {
		t.Fatalf("observer must not see bulk bar on /installations")
	}

	store.user.Role = "admin"
	adminPage := libraryGet(t, s, "/installations")
	if !strings.Contains(adminPage, `installations-bulk-delete-dialog`) {
		t.Fatalf("admin must see bulk delete dialog on /installations")
	}
}

func TestBootAndInstallWIMDeleteBatch(t *testing.T) {
	s, store, _ := newFakeWIMStore(t, "admin")
	s.DeletionMenu = bulkInitialMenuStub{baseURL: s.BootBaseURL}
	store.bootImages = []WindowsBootImage{
		{ID: 1, SourceKind: WIMSourceKindISO, WindowsProfileID: 10, Title: "ISO boot", State: "ready"},
		{ID: 2, SourceKind: WIMSourceKindFile, StorageID: 7, Path: "wim/boot.wim", Title: "Standalone boot", State: "ready"},
	}
	store.installImages = []WindowsInstallImage{
		{ID: 3, StorageID: 7, Path: "wim/install.wim", Title: "Standalone install", State: "ready"},
	}

	resBoot := wimPost(t, s, "/images/boot/delete-batch", url.Values{
		"item":      {"id:1", "id:2"},
		"return_to": {"/images?tab=boot"},
	})
	if resBoot.Code != http.StatusSeeOther || !strings.HasPrefix(resBoot.Header().Get("Location"), "/images?tab=boot") {
		t.Fatalf("boot delete-batch = %d loc=%q body=%s", resBoot.Code, resBoot.Header().Get("Location"), resBoot.Body.String())
	}
	if len(store.bootImages) != 1 || store.bootImages[0].ID != 1 {
		t.Fatalf("expected only ISO boot 1 remaining, got %+v", store.bootImages)
	}

	resInst := wimPost(t, s, "/images/install/delete-batch", url.Values{
		"item":      {"id:3"},
		"return_to": {"/images?tab=install"},
	})
	if resInst.Code != http.StatusSeeOther || !strings.HasPrefix(resInst.Header().Get("Location"), "/images?tab=install") {
		t.Fatalf("install delete-batch = %d loc=%q body=%s", resInst.Code, resInst.Header().Get("Location"), resInst.Body.String())
	}
	if len(store.installImages) != 0 {
		t.Fatalf("expected install 3 removed, got %+v", store.installImages)
	}
}
