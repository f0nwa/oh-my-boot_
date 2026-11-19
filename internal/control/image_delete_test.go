package control

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"omb/internal/images"
)

func TestImagesDeletionButtonTracksSavedDownloads(t *testing.T) {
	for _, kind := range []string{BootProfileWindowsISO, BootProfileWinPEISO, BootProfileUbuntuISO, BootProfileUbuntuBuiltin} {
		t.Run(string(kind), func(t *testing.T) {
			s, store, _ := downloadsServer(t)
			store.user.Role = "admin"
			image := s.Images.(*fakeImagesAPI).catalog.Images[0]
			store.bootProfiles = []BootProfile{{ID: 90, Kind: kind, WindowsProfileID: store.profiles[0].ID, UbuntuStorageID: image.StorageID, UbuntuPath: image.Path}}
			store.installations = []Installation{{ID: 91, Title: "Used ISO", BootProfileID: 90}}
			body := libraryGet(t, s, "/images")
			if !regexp.MustCompile(`data-image-delete-open="image-delete-0"[^>]*disabled`).MatchString(body) {
				t.Fatal("referenced ISO has active deletion")
			}
			if !strings.Contains(body, `data-image-delete-open="image-delete-1"`) || regexp.MustCompile(`data-image-delete-open="image-delete-1"[^>]*disabled`).MatchString(body) {
				t.Fatal("unreferenced ISO has no active deletion")
			}
			store.installations = nil
			body = libraryGet(t, s, "/images")
			if regexp.MustCompile(`data-image-delete-open="image-delete-0"[^>]*disabled`).MatchString(body) {
				t.Fatal("ISO remains blocked after the last download is removed")
			}
		})
	}
}

type imageUsageFailureStore struct {
	*downloadsStore
}

func (s *imageUsageFailureStore) BootProfiles(context.Context) ([]BootProfile, error) {
	return nil, errors.New("database unavailable")
}

func TestImagesDeletionDisabledWhenUsageCannotBeChecked(t *testing.T) {
	s, store, _ := downloadsServer(t)
	store.user.Role = "admin"
	s.Store = &imageUsageFailureStore{downloadsStore: store}
	body := libraryGet(t, s, "/images")
	for _, id := range []string{"0", "1"} {
		if !regexp.MustCompile(`data-image-delete-open="image-delete-` + id + `"[^>]*disabled`).MatchString(body) {
			t.Fatalf("ISO %s can be deleted without checking usage", id)
		}
	}
	if !strings.Contains(body, "Не удалось проверить загрузки") {
		t.Fatal("usage failure is hidden")
	}
}

func TestImageDeleteBlocksUbuntuSourceUsedByDownload(t *testing.T) {
	for _, kind := range []string{BootProfileUbuntuISO, BootProfileUbuntuBuiltin} {
		t.Run(string(kind), func(t *testing.T) {
			api := &fakeImagesAPI{catalog: imageDeletionCatalog("local")}
			base := &deletionStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
			store := &referencedDeletionStore{deletionStore: base, source: InstallationMenuSources{
				BootProfiles:  []BootProfile{{ID: 90, Kind: kind, UbuntuStorageID: 7, UbuntuPath: "Windows/win.iso"}},
				Installations: []Installation{{ID: 91, BootProfileID: 90}},
			}}
			cleaner := &deletionCleaner{}
			server := deletionTestServer(base, cleaner)
			server.Store, server.Images = store, api
			res := submitImageDeletion(t, server, imageDeletionForm("7", "Windows/win.iso"))
			if res.Code != http.StatusConflict || len(api.deleted) != 0 || len(cleaner.calls) != 0 || len(base.deleted) != 0 {
				t.Fatalf("used Ubuntu ISO removed: status=%d deleted=%v cleanup=%v", res.Code, api.deleted, cleaner.calls)
			}
		})
	}
}

type imageDeletionUsageFailureStore struct {
	*deletionStore
}

func (s *imageDeletionUsageFailureStore) Installations(context.Context) ([]Installation, error) {
	return nil, errors.New("database unavailable")
}

func TestImageDeleteRejectsUnverifiedUsageBeforeRemovingSource(t *testing.T) {
	api := &fakeImagesAPI{catalog: imageDeletionCatalog("local")}
	base := &deletionStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
	cleaner := &deletionCleaner{}
	server := deletionTestServer(base, cleaner)
	server.Store = &imageDeletionUsageFailureStore{deletionStore: base}
	server.Images = api
	res := submitImageDeletion(t, server, imageDeletionForm("7", "Windows/win.iso"))
	if res.Code != http.StatusServiceUnavailable || len(api.deleted) != 0 || len(cleaner.calls) != 0 {
		t.Fatalf("unverified source removed: status=%d deleted=%v cleanup=%v", res.Code, api.deleted, cleaner.calls)
	}
}

func imageDeletionForm(storageID, path string) url.Values {
	return url.Values{"storage_id": {storageID}, "path": {path}, "csrf": {CSRFToken("test-token")}}
}

func submitImageDeletion(t *testing.T, server *Server, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/images/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	return res
}

func imageDeletionCatalog(kind string) images.Catalog {
	storage := images.Storage{ID: 7, Name: "Storage", Kind: kind, State: "ready"}
	if kind == "smb" {
		storage.Key = "0123456789abcdef0123456789abcdef"
		storage.Server = "192.168.10.10"
		storage.Share = "Downloads"
	}
	return images.Catalog{Storages: []images.Storage{storage}, Images: []images.Image{{StorageID: 7, Path: "Windows/win.iso", Present: true}}}
}

func TestImageDeleteRemovesLocalSourceAndCatalogEntry(t *testing.T) {
	api := &fakeImagesAPI{catalog: imageDeletionCatalog("local")}
	store := &deletionStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
	server := deletionTestServer(store, &deletionCleaner{})
	server.Images = api
	res := submitImageDeletion(t, server, imageDeletionForm("7", "Windows/win.iso"))
	if res.Code != http.StatusSeeOther || len(api.deleted) != 1 || api.deleted[0].Path != "Windows/win.iso" {
		t.Fatalf("status=%d body=%s deleted=%+v", res.Code, res.Body.String(), api.deleted)
	}
	if len(store.audits) != 1 || store.audits[0] != "iso.delete|ok|7 Windows/win.iso" {
		t.Fatalf("audit=%v", store.audits)
	}
	location, _ := url.Parse(res.Header().Get("Location"))
	if got, want := location.Query().Get("message"), "ISO «Windows/win.iso» удалён из хранилища «Storage»"; got != want {
		t.Fatalf("message=%q, want %q", got, want)
	}
	if got := translate(langEN, location.Query().Get("message")); got != "ISO “Windows/win.iso” deleted from storage “Storage”" {
		t.Fatalf("english message=%q", got)
	}
}

func TestImageDeleteCleansUnusedProfileAndUsesSMBWriteMount(t *testing.T) {
	api := &fakeImagesAPI{catalog: imageDeletionCatalog("smb")}
	store := &deletionStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}, profiles: []WindowsProfile{{ID: 17, WindowsProfileSource: WindowsProfileSource{StorageID: 7, Path: "Windows/win.iso"}}}}
	cleaner := &deletionCleaner{}
	server := deletionTestServer(store, cleaner)
	server.Images = api
	events := []string{}
	server.SMB = &fakeSMBMountAPI{events: &events}
	res := submitImageDeletion(t, server, imageDeletionForm("7", "Windows/win.iso"))
	if res.Code != http.StatusSeeOther || strings.Join(events, ",") != "enable-upload" || strings.Join(cleaner.calls, ",") != "profile" || strings.Join(store.deleted, ",") != "profile" || len(api.deleted) != 1 {
		t.Fatalf("status=%d events=%v cleanup=%v database=%v images=%+v", res.Code, events, cleaner.calls, store.deleted, api.deleted)
	}
}

func TestImageDeleteBlocksImageUsedByInstallation(t *testing.T) {
	api := &fakeImagesAPI{catalog: imageDeletionCatalog("local")}
	base := &deletionStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}, profiles: []WindowsProfile{{ID: 17, WindowsProfileSource: WindowsProfileSource{StorageID: 7, Path: "Windows/win.iso"}}}}
	store := &referencedDeletionStore{deletionStore: base, source: InstallationMenuSources{BootProfiles: []BootProfile{{ID: 1, Kind: BootProfileWindowsISO, WindowsProfileID: 17}}, Installations: []Installation{{ID: 9, BootProfileID: 1, Title: "Used"}}}}
	cleaner := &deletionCleaner{}
	server := deletionTestServer(base, cleaner)
	server.Store = store
	server.Images = api
	res := submitImageDeletion(t, server, imageDeletionForm("7", "Windows/win.iso"))
	if res.Code != http.StatusConflict || len(api.deleted) != 0 || len(cleaner.calls) != 0 || len(base.deleted) != 0 {
		t.Fatalf("status=%d deleted=%+v cleanup=%v database=%v", res.Code, api.deleted, cleaner.calls, base.deleted)
	}
}

func TestImageDeleteRequiresAdminAndCSRF(t *testing.T) {
	for _, role := range []string{"observer", "operator", "admin"} {
		t.Run(role, func(t *testing.T) {
			api := &fakeImagesAPI{catalog: imageDeletionCatalog("local")}
			store := &deletionStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: role, Enabled: true}}}
			server := deletionTestServer(store, &deletionCleaner{})
			server.Images = api
			res := submitImageDeletion(t, server, imageDeletionForm("7", "Windows/win.iso"))
			want := http.StatusForbidden
			if role == "admin" {
				want = http.StatusSeeOther
			}
			if res.Code != want || (role != "admin" && len(api.deleted) != 0) {
				t.Fatalf("status=%d deleted=%+v", res.Code, api.deleted)
			}
		})
	}
	api := &fakeImagesAPI{catalog: imageDeletionCatalog("local")}
	store := &deletionStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
	server := deletionTestServer(store, &deletionCleaner{})
	server.Images = api
	form := imageDeletionForm("7", "Windows/win.iso")
	form.Set("csrf", "wrong")
	if res := submitImageDeletion(t, server, form); res.Code != http.StatusForbidden || len(api.deleted) != 0 {
		t.Fatalf("CSRF status=%d deleted=%+v", res.Code, api.deleted)
	}
}

func TestImageDeleteReportsStorageFailure(t *testing.T) {
	api := &fakeImagesAPI{catalog: imageDeletionCatalog("local"), deleteErr: errors.New("read-only")}
	store := &deletionStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
	server := deletionTestServer(store, &deletionCleaner{})
	server.Images = api
	res := submitImageDeletion(t, server, imageDeletionForm("7", "Windows/win.iso"))
	if res.Code != http.StatusServiceUnavailable || len(store.audits) != 1 || !strings.Contains(store.audits[0], "iso.delete|error") {
		t.Fatalf("status=%d audit=%v", res.Code, store.audits)
	}
}

func TestImagesPageShowsSourceDeletionOnlyToAdmin(t *testing.T) {
	for _, role := range []string{"admin", "operator", "observer"} {
		data := pageData{Info: requestContext{User: User{Role: role}, CSRF: "test"}, ISOImages: []imageRow{{Image: images.Image{StorageID: 7, Path: "Windows/win.iso", Present: true}}}}
		res := httptest.NewRecorder()
		render(res, "images", data)
		hasAction := strings.Contains(res.Body.String(), `action="/images/delete"`)
		if hasAction != (role == "admin") || role == "admin" && (!strings.Contains(res.Body.String(), `name="storage_id" value="7"`) || !strings.Contains(res.Body.String(), `name="path" value="Windows/win.iso"`)) {
			t.Fatalf("role=%s action=%v body=%s", role, hasAction, res.Body.String())
		}
	}
}

func TestImageDeleteRemovesAbsentCatalogEntry(t *testing.T) {
	storage := images.Storage{ID: 7, Name: "Storage", Kind: "local", State: "ready"}
	api := &fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{storage}, Images: []images.Image{{StorageID: 7, Path: "absent.iso", Present: false}}}}
	store := &deletionStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
	server := deletionTestServer(store, &deletionCleaner{})
	server.Images = api
	res := submitImageDeletion(t, server, imageDeletionForm("7", "absent.iso"))
	if res.Code != http.StatusSeeOther || len(api.deleted) != 1 || api.deleted[0].Path != "absent.iso" {
		t.Fatalf("status=%d body=%s deleted=%+v", res.Code, res.Body.String(), api.deleted)
	}
	if len(store.audits) != 1 || store.audits[0] != "iso.delete|ok|7 absent.iso" {
		t.Fatalf("audit=%v", store.audits)
	}
	location, _ := url.Parse(res.Header().Get("Location"))
	if got, want := location.Query().Get("message"), "Запись образа «absent.iso» удалена из библиотеки"; got != want {
		t.Fatalf("message=%q, want %q", got, want)
	}
	if got := translate(langEN, location.Query().Get("message")); got != "Image record “absent.iso” removed from library" {
		t.Fatalf("english message=%q", got)
	}
}

func TestImageDeleteBlocksAbsentImageUsedByInstallation(t *testing.T) {
	storage := images.Storage{ID: 7, Name: "Storage", Kind: "local", State: "ready"}
	api := &fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{storage}, Images: []images.Image{{StorageID: 7, Path: "absent.iso", Present: false}}}}
	base := &deletionStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}, profiles: []WindowsProfile{{ID: 17, WindowsProfileSource: WindowsProfileSource{StorageID: 7, Path: "absent.iso"}}}}
	store := &referencedDeletionStore{deletionStore: base, source: InstallationMenuSources{BootProfiles: []BootProfile{{ID: 1, Kind: BootProfileWindowsISO, WindowsProfileID: 17}}, Installations: []Installation{{ID: 9, BootProfileID: 1, Title: "Used"}}}}
	cleaner := &deletionCleaner{}
	server := deletionTestServer(base, cleaner)
	server.Store = store
	server.Images = api
	res := submitImageDeletion(t, server, imageDeletionForm("7", "absent.iso"))
	if res.Code != http.StatusConflict || len(api.deleted) != 0 || len(cleaner.calls) != 0 || len(base.deleted) != 0 {
		t.Fatalf("status=%d deleted=%+v cleanup=%v database=%v", res.Code, api.deleted, cleaner.calls, base.deleted)
	}
}

func TestImagesPageShowsAbsentImageDeletionOnlyToAdmin(t *testing.T) {
	for _, role := range []string{"admin", "operator", "observer"} {
		data := pageData{
			Info: requestContext{User: User{Role: role}, CSRF: "test"},
			ISOImages: []imageRow{{
				Image:       images.Image{StorageID: 7, Path: "absent.iso", Present: false},
				StorageName: "Local",
			}},
		}
		res := httptest.NewRecorder()
		render(res, "images", data)
		body := res.Body.String()
		hasButton := strings.Contains(body, `data-image-delete-open="image-delete-absent-0"`)
		hasDialog := strings.Contains(body, `id="image-delete-absent-0"`)
		if hasButton != (role == "admin") {
			t.Fatalf("role=%s hasButton=%v", role, hasButton)
		}
		if hasDialog != (role == "admin") {
			t.Fatalf("role=%s hasDialog=%v", role, hasDialog)
		}
		if role == "admin" {
			if !strings.Contains(body, "Удалить из библиотеки") {
				t.Fatal("missing 'Удалить из библиотеки' menu item")
			}
			if !strings.Contains(body, "Файл отсутствует на диске хранилища") {
				t.Fatal("missing absent description in dialog")
			}
		}
	}
}
