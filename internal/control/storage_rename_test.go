package control

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"omb/internal/images"
)

func submitRename(t *testing.T, s *Server, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	return res
}

func renameForm(name string) url.Values {
	return url.Values{
		"name": {name},
		"csrf": {CSRFToken("test-token")},
	}
}

func TestStorageRenameSuccess(t *testing.T) {
	store := &auditRecordingStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
	api := &fakeImagesAPI{
		catalog: images.Catalog{
			Storages: []images.Storage{
				{ID: 18, Name: "192.168.10.10:/volume3/Downloads", Kind: "nfs", State: "ready"},
			},
		},
	}
	s := &Server{Store: store, Images: api}

	res := submitRename(t, s, "/images/storages/18/rename", renameForm("Synology Downloads"))
	if res.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 SeeOther, got %d (body: %s)", res.Code, res.Body.String())
	}
	if loc := res.Header().Get("Location"); !strings.Contains(loc, "/storages?message=") {
		t.Fatalf("unexpected redirect location: %s", loc)
	}
	if api.catalog.Storages[0].Name != "Synology Downloads" {
		t.Fatalf("expected storage renamed to 'Synology Downloads', got %q", api.catalog.Storages[0].Name)
	}
	if store.action != "storage.rename" || store.result != "ok" {
		t.Fatalf("expected audit storage.rename/ok, got %s/%s", store.action, store.result)
	}
}

func TestStorageRenameRolesAndCSRF(t *testing.T) {
	for _, tc := range []struct {
		role string
		want int
	}{
		{"observer", http.StatusForbidden},
		{"operator", http.StatusForbidden},
		{"admin", http.StatusSeeOther},
	} {
		api := &fakeImagesAPI{
			catalog: images.Catalog{
				Storages: []images.Storage{
					{ID: 1, Name: "NAS", Kind: "smb"},
				},
			},
		}
		s := &Server{
			Store:  authOnlyStore{user: User{ID: 1, Role: tc.role, Enabled: true}},
			Images: api,
		}
		res := submitRename(t, s, "/images/storages/1/rename", renameForm("New NAS"))
		if res.Code != tc.want {
			t.Fatalf("role=%s expected status %d, got %d", tc.role, tc.want, res.Code)
		}
	}

	// CSRF invalid
	api := &fakeImagesAPI{
		catalog: images.Catalog{
			Storages: []images.Storage{{ID: 1, Name: "NAS", Kind: "smb"}},
		},
	}
	s := &Server{
		Store:  authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}},
		Images: api,
	}
	form := renameForm("New NAS")
	form.Set("csrf", "invalid-csrf-token")
	res := submitRename(t, s, "/images/storages/1/rename", form)
	if res.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for bad CSRF, got %d", res.Code)
	}
}

func TestStorageRenameDuplicateConflict(t *testing.T) {
	api := &fakeImagesAPI{
		catalog: images.Catalog{
			Storages: []images.Storage{
				{ID: 1, Name: "First Storage", Kind: "smb"},
				{ID: 2, Name: "Second Storage", Kind: "nfs"},
			},
		},
	}
	s := &Server{
		Store:  authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}},
		Images: api,
	}

	// Rename storage 1 to "Second Storage" -> conflict
	res := submitRename(t, s, "/images/storages/1/rename", renameForm("Second Storage"))
	if res.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict, got %d (body: %s)", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "Хранилище с таким названием уже существует") {
		t.Fatalf("expected duplicate error message in body, got: %s", res.Body.String())
	}
}

func TestStorageRenameValidation(t *testing.T) {
	api := &fakeImagesAPI{
		catalog: images.Catalog{
			Storages: []images.Storage{
				{ID: 1, Name: "NAS", Kind: "smb"},
			},
		},
	}
	s := &Server{
		Store:  authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}},
		Images: api,
	}

	tooLong := strings.Repeat("x", 121)
	for _, invalidName := range []string{"", "   ", tooLong} {
		res := submitRename(t, s, "/images/storages/1/rename", renameForm(invalidName))
		if res.Code != http.StatusBadRequest {
			t.Fatalf("name=%q expected 400 Bad Request, got %d", invalidName, res.Code)
		}
	}
}

func TestStorageRenameNotFound(t *testing.T) {
	api := &fakeImagesAPI{
		catalog: images.Catalog{
			Storages: []images.Storage{
				{ID: 1, Name: "NAS", Kind: "smb"},
			},
		},
	}
	s := &Server{
		Store:  authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}},
		Images: api,
	}

	res := submitRename(t, s, "/images/storages/999/rename", renameForm("New Name"))
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found, got %d", res.Code)
	}
}
