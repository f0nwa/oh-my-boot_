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

func TestSafeImagesReturnURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "/images"},
		{"/images", "/images"},
		{"/images?show=attention", "/images?show=attention"},
		{"/images?show=attention&q=Win11&sort=size&dir=desc&message=old&extra=1", "/images?dir=desc&q=Win11&show=attention&sort=size"},
		{"/storages?show=attention", "/images"},
		{"https://evil.example/images?show=attention", "/images"},
		{"//evil.example/images", "/images"},
	}
	for _, tc := range cases {
		if got := safeImagesReturnURL(tc.in); got != tc.want {
			t.Errorf("safeImagesReturnURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestImageDeletePreservesFilterReturnTo(t *testing.T) {
	storage := images.Storage{ID: 7, Name: "Storage", Kind: "local", State: "ready"}
	api := &fakeImagesAPI{catalog: images.Catalog{
		Storages: []images.Storage{storage},
		Images:   []images.Image{{StorageID: 7, Path: "absent.iso", Present: false}},
	}}
	store := &deletionStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
	server := deletionTestServer(store, &deletionCleaner{})
	server.Images = api

	form := imageDeletionForm("7", "absent.iso")
	form.Set("return_to", "/images?show=attention&q=absent&sort=size&dir=desc&message=stale")
	res := submitImageDeletion(t, server, form)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	loc, err := url.Parse(res.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Path != "/images" {
		t.Fatalf("path=%q, want /images", loc.Path)
	}
	q := loc.Query()
	if q.Get("show") != "attention" || q.Get("q") != "absent" || q.Get("sort") != "size" || q.Get("dir") != "desc" {
		t.Fatalf("lost filter params in redirect: %s", res.Header().Get("Location"))
	}
	if q.Get("message") != "Запись образа «absent.iso» удалена из библиотеки" {
		t.Fatalf("message=%q", q.Get("message"))
	}
}

func submitImageBatchDeletion(t *testing.T, server *Server, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	form.Set("csrf", CSRFToken("test-token"))
	req := httptest.NewRequest(http.MethodPost, "/images/delete-batch", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	return res
}

func TestImageDeleteBatchAbsentAndMixed(t *testing.T) {
	t.Run("absent_only_preserves_filter_and_skips_smb_upload", func(t *testing.T) {
		storage := images.Storage{ID: 7, Name: "NAS", Kind: "smb", Key: "nas1", Server: "10.0.0.5", Share: "ISO", State: "ready"}
		api := &fakeImagesAPI{catalog: images.Catalog{
			Storages: []images.Storage{storage},
			Images: []images.Image{
				{StorageID: 7, Path: "a.iso", Present: false},
				{StorageID: 7, Path: "b.iso", Present: false},
			},
		}}
		store := &deletionStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
		events := []string{}
		server := deletionTestServer(store, &deletionCleaner{})
		server.Images = api
		server.SMB = &fakeSMBMountAPI{events: &events}

		form := url.Values{
			"item":      {"7:a.iso", "7:b.iso", "7:a.iso"},
			"return_to": {"/images?show=attention"},
		}
		res := submitImageBatchDeletion(t, server, form)
		if res.Code != http.StatusSeeOther {
			t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
		}
		if len(api.deleted) != 2 {
			t.Fatalf("deleted=%+v, want 2", api.deleted)
		}
		if len(events) != 0 {
			t.Fatalf("SMB EnableUpload called for absent images: %v", events)
		}
		loc, _ := url.Parse(res.Header().Get("Location"))
		if loc.Path != "/images" || loc.Query().Get("show") != "attention" {
			t.Fatalf("redirect=%q", res.Header().Get("Location"))
		}
		if got, want := loc.Query().Get("message"), "Удалено записей из библиотеки: 2"; got != want {
			t.Fatalf("message=%q, want %q", got, want)
		}
		if got := translate(langEN, loc.Query().Get("message")); got != "Removed library records: 2" {
			t.Fatalf("en message=%q", got)
		}
	})

	t.Run("mixed_with_partial_failure", func(t *testing.T) {
		storage := images.Storage{ID: 7, Name: "NAS", Kind: "smb", Key: "nas1", Server: "10.0.0.5", Share: "ISO", State: "ready"}
		api := &fakeImagesAPI{catalog: images.Catalog{
			Storages: []images.Storage{storage},
			Images: []images.Image{
				{StorageID: 7, Path: "present.iso", Present: true},
				{StorageID: 7, Path: "absent.iso", Present: false},
				{StorageID: 7, Path: "used.iso", Present: true},
			},
		}}
		base := &deletionStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
		store := &referencedDeletionStore{
			deletionStore: base,
			source: InstallationMenuSources{
				BootProfiles:  []BootProfile{{ID: 90, Kind: BootProfileUbuntuISO, UbuntuStorageID: 7, UbuntuPath: "used.iso"}},
				Installations: []Installation{{ID: 91, BootProfileID: 90}},
			},
		}
		events := []string{}
		server := deletionTestServer(base, &deletionCleaner{})
		server.Store = store
		server.Images = api
		server.SMB = &fakeSMBMountAPI{events: &events}

		form := url.Values{
			"item":      {"7:present.iso", "7:absent.iso", "7:used.iso"},
			"return_to": {"/images?show=free&q=iso"},
		}
		res := submitImageBatchDeletion(t, server, form)
		if res.Code != http.StatusSeeOther {
			t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
		}
		if len(api.deleted) != 2 {
			t.Fatalf("deleted=%+v, want 2", api.deleted)
		}
		if strings.Join(events, ",") != "enable-upload" {
			t.Fatalf("SMB events=%v, want [enable-upload]", events)
		}
		loc, _ := url.Parse(res.Header().Get("Location"))
		if loc.Query().Get("show") != "free" || loc.Query().Get("q") != "iso" {
			t.Fatalf("redirect=%q", res.Header().Get("Location"))
		}
		if !strings.Contains(loc.Query().Get("message"), "Удалено образов: 2; не удалось удалить: 1") {
			t.Fatalf("message=%q", loc.Query().Get("message"))
		}
	})

	t.Run("validation_and_role_checks", func(t *testing.T) {
		store := &deletionStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
		server := deletionTestServer(store, &deletionCleaner{})
		server.Images = &fakeImagesAPI{}
		if res := submitImageBatchDeletion(t, server, url.Values{}); res.Code != http.StatusBadRequest {
			t.Fatalf("empty batch status=%d, want 400", res.Code)
		}
		if res := submitImageBatchDeletion(t, server, url.Values{"item": {"bad-item"}}); res.Code != http.StatusBadRequest {
			t.Fatalf("bad item status=%d, want 400", res.Code)
		}
		store.user.Role = "operator"
		if res := submitImageBatchDeletion(t, server, url.Values{"item": {"7:a.iso"}}); res.Code != http.StatusForbidden {
			t.Fatalf("operator batch status=%d, want 403", res.Code)
		}
	})
}

func TestImagesPageRendersBulkCheckboxesForAdminOnly(t *testing.T) {
	for _, role := range []string{"admin", "operator", "observer"} {
		s, _ := setupImagesTableServer(t, role, langRU)
		body := libraryGet(t, s, "/images?show=attention")
		hasSelectAll := strings.Contains(body, `id="image-select-all"`)
		hasBulkBar := strings.Contains(body, `id="image-bulk-bar"`)
		hasBulkDialog := strings.Contains(body, `id="image-bulk-delete-dialog"`)
		want := role == "admin"
		if hasSelectAll != want || hasBulkBar != want || hasBulkDialog != want {
			t.Fatalf("role=%s selectAll=%v bulkBar=%v bulkDialog=%v, want %v", role, hasSelectAll, hasBulkBar, hasBulkDialog, want)
		}
		if role == "admin" {
			if !strings.Contains(body, `name="return_to" value="/images?show=attention"`) {
				t.Fatal("missing preserved return_to in /images?show=attention forms")
			}
		}
	}
}
