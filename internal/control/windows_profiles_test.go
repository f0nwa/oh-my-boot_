package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"pxe-platform/internal/images"
)

type recordingWindowsProfileStore struct {
	authOnlyStore
	created []WindowsProfileSource
}

func (s *recordingWindowsProfileStore) CreateWindowsProfile(_ context.Context, source WindowsProfileSource) (WindowsProfile, error) {
	s.created = append(s.created, source)
	return WindowsProfile{ID: 11, WindowsProfileSource: source}, nil
}

func TestWindowsProfileCreateUsesCatalogRatherThanFormPath(t *testing.T) {
	modified := time.Date(2026, 10, 16, 15, 0, 0, 0, time.UTC)
	store := &recordingWindowsProfileStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}}
	api := &fakeImagesAPI{catalog: images.Catalog{
		Storages: []images.Storage{{ID: 7, Kind: "smb", Root: "/mnt/pxe-platform-smb/key7", State: "ready"}},
		Images:   []images.Image{{StorageID: 7, Path: "Win10/RU-RU.ISO", Size: 7304607744, Modified: modified, Present: true}},
	}}
	s := &Server{Store: store, Images: api}
	form := url.Values{"storage_id": {"7"}, "path": {"Win10/RU-RU.ISO"}, "title": {"Windows 10 x64"}, "folder": {"Windows"}, "storage_root": {"/etc"}, "csrf": {CSRFToken("test-token")}}
	req := httptest.NewRequest(http.MethodPost, "/images/windows-profiles", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther || len(store.created) != 1 || store.created[0].StorageRoot != "/mnt/pxe-platform-smb/key7" {
		t.Fatalf("status=%d created=%+v body=%s", res.Code, store.created, res.Body.String())
	}
}

func TestSelectWindowsISOUsesReadyCatalogRecord(t *testing.T) {
	modified := time.Date(2026, 10, 16, 15, 0, 0, 0, time.UTC)
	catalog := images.Catalog{
		Storages: []images.Storage{{ID: 7, Kind: "smb", Root: "/mnt/pxe-platform-smb/key7", State: "ready"}},
		Images:   []images.Image{{StorageID: 7, Path: "Win10/RU-RU.ISO", Size: 7304607744, Modified: modified, Present: true}},
	}
	got, err := SelectWindowsISO(catalog, 7, "Win10/RU-RU.ISO", "Windows 10 x64", "Windows")
	if err != nil {
		t.Fatal(err)
	}
	if got.StorageID != 7 || got.Path != "Win10/RU-RU.ISO" || got.StorageRoot != "/mnt/pxe-platform-smb/key7" || got.Size != 7304607744 || !got.Modified.Equal(modified) || got.Title != "Windows 10 x64" || got.Folder != "Windows" {
		t.Fatalf("selected source differs from catalog: %+v", got)
	}
}

func TestSelectWindowsISORejectsUnlistedOrUnavailableSource(t *testing.T) {
	base := images.Catalog{
		Storages: []images.Storage{{ID: 7, Kind: "smb", Root: "/mnt/pxe-platform-smb/key7", State: "ready"}},
		Images:   []images.Image{{StorageID: 7, Path: "Win10/RU-RU.ISO", Size: 7304607744, Present: true}},
	}
	for _, tc := range []struct {
		name    string
		catalog images.Catalog
		path    string
		title   string
		folder  string
	}{
		{"unlisted path", base, "Win10/other.ISO", "Windows 10", "Windows"},
		{"path traversal", base, "../Win10/RU-RU.ISO", "Windows 10", "Windows"},
		{"unsupported file", base, "Win10/RU-RU.exe", "Windows 10", "Windows"},
		{"unsafe title", base, "Win10/RU-RU.ISO", "Win\ngoto local", "Windows"},
		{"unsafe folder", base, "Win10/RU-RU.ISO", "Windows 10", "Windows/../Admin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := SelectWindowsISO(tc.catalog, 7, tc.path, tc.title, tc.folder); err == nil {
				t.Fatal("accepted an unlisted or unsafe ISO selection")
			}
		})
	}
	unavailable := base
	unavailable.Storages = []images.Storage{{ID: 7, Kind: "smb", Root: "/mnt/pxe-platform-smb/key7", State: "unavailable"}}
	if _, err := SelectWindowsISO(unavailable, 7, "Win10/RU-RU.ISO", "Windows 10", "Windows"); err == nil {
		t.Fatal("accepted unavailable storage")
	}
	missing := base
	missing.Images = []images.Image{{StorageID: 7, Path: "Win10/RU-RU.ISO", Size: 7304607744, Present: false}}
	if _, err := SelectWindowsISO(missing, 7, "Win10/RU-RU.ISO", "Windows 10", "Windows"); err == nil {
		t.Fatal("accepted missing image")
	}
	poisoned := base
	poisoned.Images = []images.Image{{StorageID: 7, Path: "../outside.iso", Size: 100, Present: true}}
	if _, err := SelectWindowsISO(poisoned, 7, "../outside.iso", "Windows 10", "Windows"); err == nil {
		t.Fatal("accepted traversal path copied from catalog")
	}
}
