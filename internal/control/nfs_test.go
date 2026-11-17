package control

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"omb/internal/images"
)

type fakeNFSImages struct {
	fakeImagesAPI
	events          *[]string
	storage         images.Storage
	deleted         chan struct{}
	setUploadsCalls map[int64]bool
	updatedVersions map[int64]string
}

func (f *fakeNFSImages) CreateNFS(_ context.Context, input images.NFSStorageInput) (images.Storage, error) {
	if f.events != nil {
		*f.events = append(*f.events, "reserve")
	}
	f.storage = images.Storage{
		ID:             7,
		Key:            input.Key,
		Name:           input.Name,
		Kind:           "nfs",
		Server:         input.Server,
		ExportPath:     input.ExportPath,
		NFSVersion:     input.Version,
		State:          "ready",
		UploadsEnabled: false,
	}
	f.catalog.Storages = append(f.catalog.Storages, f.storage)
	return f.storage, f.err
}

func (f *fakeNFSImages) UpdateNFSVersion(_ context.Context, id int64, version string) error {
	if f.events != nil {
		*f.events = append(*f.events, "update-version:"+version)
	}
	if f.updatedVersions == nil {
		f.updatedVersions = make(map[int64]string)
	}
	f.updatedVersions[id] = version
	for i := range f.catalog.Storages {
		if f.catalog.Storages[i].ID == id {
			f.catalog.Storages[i].NFSVersion = version
		}
	}
	return f.err
}

func (f *fakeNFSImages) SetNFSUploads(_ context.Context, id int64, enabled bool) error {
	if f.events != nil {
		if enabled {
			*f.events = append(*f.events, "set-uploads:true")
		} else {
			*f.events = append(*f.events, "set-uploads:false")
		}
	}
	if f.setUploadsCalls == nil {
		f.setUploadsCalls = make(map[int64]bool)
	}
	f.setUploadsCalls[id] = enabled
	for i := range f.catalog.Storages {
		if f.catalog.Storages[i].ID == id {
			f.catalog.Storages[i].UploadsEnabled = enabled
			f.catalog.Storages[i].Uploads = enabled
		}
	}
	return f.err
}

func (f *fakeNFSImages) MarkNFSError(_ context.Context, _ int64) error {
	if f.events != nil {
		*f.events = append(*f.events, "mark-error")
	}
	return f.err
}

func (f *fakeNFSImages) MarkNFSScanError(_ context.Context, _ int64) error {
	if f.events != nil {
		*f.events = append(*f.events, "mark-scan-error")
	}
	return f.err
}

func (f *fakeNFSImages) DeleteNFS(_ context.Context, id int64) error {
	if f.events != nil {
		*f.events = append(*f.events, "delete")
	}
	if f.deleted != nil {
		close(f.deleted)
	}
	for i, s := range f.catalog.Storages {
		if s.ID == id {
			f.catalog.Storages = append(f.catalog.Storages[:i], f.catalog.Storages[i+1:]...)
			break
		}
	}
	return f.err
}

func (f *fakeNFSImages) Scan(ctx context.Context, id int64) error {
	if f.events != nil {
		*f.events = append(*f.events, "scan")
	}
	return f.fakeImagesAPI.Scan(ctx, id)
}

func (f *fakeNFSImages) DeleteImage(ctx context.Context, id int64, path string) error {
	if f.events != nil {
		*f.events = append(*f.events, "delete-image")
	}
	return f.fakeImagesAPI.DeleteImage(ctx, id, path)
}

type fakeNFSMountAPI struct {
	events        *[]string
	writable      bool
	mountErr      error
	uploadErr     error
	checkErr      error
	removeErr     error
	checkWritable bool
	mountStarted  chan struct{}
	mountRelease  <-chan struct{}
}

func (f *fakeNFSMountAPI) MountNFS(_ context.Context, _, _, _, _ string) (bool, error) {
	if f.events != nil {
		*f.events = append(*f.events, "mount")
	}
	if f.mountStarted != nil {
		close(f.mountStarted)
	}
	if f.mountRelease != nil {
		<-f.mountRelease
	}
	return f.writable, f.mountErr
}

func (f *fakeNFSMountAPI) EnableNFSUpload(_ context.Context, _, _, _, _ string) error {
	if f.events != nil {
		*f.events = append(*f.events, "enable-upload")
	}
	return f.uploadErr
}

func (f *fakeNFSMountAPI) CheckNFSUpload(_ context.Context, _ string) (bool, error) {
	if f.events != nil {
		*f.events = append(*f.events, "check-upload")
	}
	return f.checkWritable, f.checkErr
}

func (f *fakeNFSMountAPI) RemoveNFS(_ context.Context, _ string) error {
	if f.events != nil {
		*f.events = append(*f.events, "remove")
	}
	return f.removeErr
}

type nfsTestStore struct {
	authOnlyStore
	windowsProfiles []WindowsProfile
	bootProfiles    []BootProfile
	installations   []Installation
	audits          []string
}

func (s *nfsTestStore) WindowsProfiles(context.Context) ([]WindowsProfile, error) {
	return s.windowsProfiles, nil
}

func (s *nfsTestStore) BootProfiles(context.Context) ([]BootProfile, error) {
	return s.bootProfiles, nil
}

func (s *nfsTestStore) Installations(context.Context) ([]Installation, error) {
	return s.installations, nil
}

func (s *nfsTestStore) Audit(_ context.Context, _ int64, action, result, target string) error {
	s.audits = append(s.audits, action+":"+result+":"+target)
	return nil
}

func nfsForm() url.Values {
	return url.Values{
		"server":      {"192.168.10.10"},
		"export_path": {"/export/iso"},
		"version":     {"4.1"},
		"name":        {"Test NFS"},
		"csrf":        {CSRFToken("test-token")},
	}
}

func submitNFS(t *testing.T, s *Server, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	return res
}

func TestNFSRolePermissions(t *testing.T) {
	for _, tc := range []struct {
		role string
		want int
	}{
		{"observer", http.StatusForbidden},
		{"operator", http.StatusForbidden},
		{"admin", http.StatusSeeOther},
	} {
		events := []string{}
		api := &fakeNFSImages{events: &events}
		mount := &fakeNFSMountAPI{events: &events, writable: true}
		store := &nfsTestStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: tc.role, Enabled: true}}}
		s := &Server{Store: store, Images: api, NFS: mount}

		res := submitNFS(t, s, "/images/nfs", nfsForm())
		if res.Code != tc.want {
			t.Fatalf("role=%s status=%d body=%s", tc.role, res.Code, res.Body.String())
		}
		if tc.role == "admin" {
			if strings.Join(events, ",") != "reserve,mount,set-uploads:true,scan" {
				t.Fatalf("admin events=%v", events)
			}
		} else {
			if len(events) != 0 {
				t.Fatalf("non-admin got events: %v", events)
			}
		}
	}

	// CSRF check
	events := []string{}
	api := &fakeNFSImages{events: &events}
	mount := &fakeNFSMountAPI{events: &events, writable: true}
	store := &nfsTestStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
	s := &Server{Store: store, Images: api, NFS: mount}
	form := nfsForm()
	form.Set("csrf", "wrong")
	if res := submitNFS(t, s, "/images/nfs", form); res.Code != http.StatusForbidden || len(events) != 0 {
		t.Fatalf("CSRF status=%d events=%v", res.Code, events)
	}
}

func TestNFSCreateRWAndRO(t *testing.T) {
	// RW success
	{
		events := []string{}
		api := &fakeNFSImages{events: &events}
		mount := &fakeNFSMountAPI{events: &events, writable: true}
		store := &nfsTestStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
		s := &Server{Store: store, Images: api, NFS: mount}

		res := submitNFS(t, s, "/images/nfs", nfsForm())
		if res.Code != http.StatusSeeOther {
			t.Fatalf("RW create status=%d body=%s", res.Code, res.Body.String())
		}
		if strings.Join(events, ",") != "reserve,mount,set-uploads:true,scan" {
			t.Fatalf("RW events=%v", events)
		}
		if !api.setUploadsCalls[7] {
			t.Fatal("expected setUploads true for RW")
		}
	}

	// RO success (writable = false, no error)
	{
		events := []string{}
		api := &fakeNFSImages{events: &events}
		mount := &fakeNFSMountAPI{events: &events, writable: false}
		store := &nfsTestStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
		s := &Server{Store: store, Images: api, NFS: mount}

		res := submitNFS(t, s, "/images/nfs", nfsForm())
		if res.Code != http.StatusSeeOther {
			t.Fatalf("RO create status=%d body=%s", res.Code, res.Body.String())
		}
		if strings.Join(events, ",") != "reserve,mount,scan" {
			t.Fatalf("RO events=%v", events)
		}
		if api.setUploadsCalls[7] {
			t.Fatal("did not expect setUploads true for RO")
		}
	}

	// Default name and trailing slash normalization
	{
		events := []string{}
		api := &fakeNFSImages{events: &events}
		mount := &fakeNFSMountAPI{events: &events, writable: true}
		store := &nfsTestStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
		s := &Server{Store: store, Images: api, NFS: mount}

		form := nfsForm()
		form.Set("name", "")
		form.Set("export_path", "/export/iso/")
		res := submitNFS(t, s, "/images/nfs", form)
		if res.Code != http.StatusSeeOther {
			t.Fatalf("default name create status=%d body=%s", res.Code, res.Body.String())
		}
		if api.storage.Name != "192.168.10.10:/export/iso" {
			t.Fatalf("expected default name 192.168.10.10:/export/iso, got %q", api.storage.Name)
		}
		if api.storage.ExportPath != "/export/iso" {
			t.Fatalf("expected normalized export_path /export/iso, got %q", api.storage.ExportPath)
		}
	}
}

func TestNFSCreateValidation(t *testing.T) {
	for _, tc := range []struct {
		name        string
		server      string
		exportPath  string
		version     string
		storageName string
	}{
		{"empty server", "", "/export", "4.1", "NFS"},
		{"invalid server characters", "bad;server", "/export", "4.1", "NFS"},
		{"empty export path", "192.168.10.10", "", "4.1", "NFS"},
		{"relative export path", "192.168.10.10", "relative/path", "4.1", "NFS"},
		{"double slash export path", "192.168.10.10", "/export//iso", "4.1", "NFS"},
		{"invalid version", "192.168.10.10", "/export", "2", "NFS"},
		{"too long name", "192.168.10.10", "/export", "4.1", strings.Repeat("x", 121)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := []string{}
			api := &fakeNFSImages{events: &events}
			mount := &fakeNFSMountAPI{events: &events}
			store := &nfsTestStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
			s := &Server{Store: store, Images: api, NFS: mount}

			form := url.Values{
				"server":      {tc.server},
				"export_path": {tc.exportPath},
				"version":     {tc.version},
				"name":        {tc.storageName},
				"csrf":        {CSRFToken("test-token")},
			}
			res := submitNFS(t, s, "/images/nfs", form)
			if res.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for %s, got %d", tc.name, res.Code)
			}
			if len(events) != 0 {
				t.Fatalf("events triggered on validation failure: %v", events)
			}
		})
	}
}

func TestNFSCreateMountErrorRollback(t *testing.T) {
	events := []string{}
	api := &fakeNFSImages{events: &events}
	mount := &fakeNFSMountAPI{events: &events, mountErr: errors.New("mount connection refused")}
	store := &nfsTestStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
	s := &Server{Store: store, Images: api, NFS: mount}

	res := submitNFS(t, s, "/images/nfs", nfsForm())
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("mount error status=%d want 503", res.Code)
	}
	if strings.Join(events, ",") != "reserve,mount,mark-error" {
		t.Fatalf("events=%v", events)
	}
	var audited bool
	for _, a := range store.audits {
		if strings.HasPrefix(a, "nfs.create:error:7") {
			audited = true
			break
		}
	}
	if !audited {
		t.Fatalf("expected audit for nfs.create:error, got %v", store.audits)
	}
}

func TestNFSCreateScanError(t *testing.T) {
	events := []string{}
	api := &fakeNFSImages{events: &events, fakeImagesAPI: fakeImagesAPI{scanErr: errors.New("scan failure")}}
	mount := &fakeNFSMountAPI{events: &events, writable: true}
	store := &nfsTestStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
	s := &Server{Store: store, Images: api, NFS: mount}

	res := submitNFS(t, s, "/images/nfs", nfsForm())
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("scan error status=%d want 503", res.Code)
	}
	if strings.Join(events, ",") != "reserve,mount,set-uploads:true,scan,mark-scan-error" {
		t.Fatalf("events=%v", events)
	}
	var audited bool
	for _, a := range store.audits {
		if strings.HasPrefix(a, "nfs.create:scan-error:7") {
			audited = true
			break
		}
	}
	if !audited {
		t.Fatalf("expected audit for nfs.create:scan-error, got %v", store.audits)
	}
}

func TestNFSRetry(t *testing.T) {
	// Version change 4.1 -> 3
	{
		events := []string{}
		api := &fakeNFSImages{
			events: &events,
			fakeImagesAPI: fakeImagesAPI{
				catalog: images.Catalog{
					Storages: []images.Storage{
						{
							ID:         7,
							Kind:       "nfs",
							Key:        "0123456789abcdef0123456789abcdef",
							Server:     "192.168.10.10",
							ExportPath: "/export/iso",
							NFSVersion: "4.1",
							State:      "ready",
						},
					},
				},
			},
		}
		mount := &fakeNFSMountAPI{events: &events, writable: true}
		store := &nfsTestStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
		s := &Server{Store: store, Images: api, NFS: mount}

		form := url.Values{
			"version": {"3"},
			"csrf":    {CSRFToken("test-token")},
		}
		res := submitNFS(t, s, "/images/nfs/7/retry", form)
		if res.Code != http.StatusSeeOther {
			t.Fatalf("retry status=%d body=%s", res.Code, res.Body.String())
		}
		if strings.Join(events, ",") != "update-version:3,mount,set-uploads:true,scan" {
			t.Fatalf("retry events=%v", events)
		}
		if api.updatedVersions[7] != "3" {
			t.Fatalf("expected version updated to 3, got %q", api.updatedVersions[7])
		}
	}

	// 409 Conflict when scanning
	{
		events := []string{}
		api := &fakeNFSImages{
			events: &events,
			fakeImagesAPI: fakeImagesAPI{
				catalog: images.Catalog{
					Storages: []images.Storage{
						{
							ID:         7,
							Kind:       "nfs",
							Key:        "0123456789abcdef0123456789abcdef",
							Server:     "192.168.10.10",
							ExportPath: "/export/iso",
							NFSVersion: "4.1",
							State:      "scanning",
						},
					},
				},
			},
		}
		mount := &fakeNFSMountAPI{events: &events}
		store := &nfsTestStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
		s := &Server{Store: store, Images: api, NFS: mount}

		res := submitNFS(t, s, "/images/nfs/7/retry", nfsForm())
		if res.Code != http.StatusConflict {
			t.Fatalf("scanning retry status=%d want 409", res.Code)
		}
	}

	// Mount error on retry
	{
		events := []string{}
		api := &fakeNFSImages{
			events: &events,
			fakeImagesAPI: fakeImagesAPI{
				catalog: images.Catalog{
					Storages: []images.Storage{
						{
							ID:         7,
							Kind:       "nfs",
							Key:        "0123456789abcdef0123456789abcdef",
							Server:     "192.168.10.10",
							ExportPath: "/export/iso",
							NFSVersion: "4.1",
							State:      "unavailable",
						},
					},
				},
			},
		}
		mount := &fakeNFSMountAPI{events: &events, mountErr: errors.New("timeout")}
		store := &nfsTestStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
		s := &Server{Store: store, Images: api, NFS: mount}

		res := submitNFS(t, s, "/images/nfs/7/retry", nfsForm())
		if res.Code != http.StatusServiceUnavailable {
			t.Fatalf("retry mount error status=%d want 503", res.Code)
		}
		if strings.Join(events, ",") != "mount,mark-error" {
			t.Fatalf("events=%v", events)
		}
	}
}

func TestNFSRemove(t *testing.T) {
	nfsStorage := images.Storage{
		ID:         7,
		Kind:       "nfs",
		Key:        "0123456789abcdef0123456789abcdef",
		Server:     "192.168.10.10",
		ExportPath: "/export/iso",
		NFSVersion: "4.1",
		State:      "ready",
	}

	// Blocked when scanning (409)
	{
		events := []string{}
		api := &fakeNFSImages{
			events: &events,
			fakeImagesAPI: fakeImagesAPI{
				catalog: images.Catalog{
					Storages: []images.Storage{
						func() images.Storage {
							s := nfsStorage
							s.State = "scanning"
							return s
						}(),
					},
				},
			},
		}
		mount := &fakeNFSMountAPI{events: &events}
		store := &nfsTestStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
		s := &Server{Store: store, Images: api, NFS: mount}

		res := submitNFS(t, s, "/images/nfs/7/remove", nfsForm())
		if res.Code != http.StatusConflict {
			t.Fatalf("scanning remove status=%d want 409", res.Code)
		}
	}

	// Blocked by WindowsProfile (409)
	{
		events := []string{}
		api := &fakeNFSImages{events: &events, fakeImagesAPI: fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{nfsStorage}}}}
		mount := &fakeNFSMountAPI{events: &events}
		store := &nfsTestStore{
			authOnlyStore:   authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}},
			windowsProfiles: []WindowsProfile{{ID: 1, WindowsProfileSource: WindowsProfileSource{StorageID: 7, Path: "win.iso"}}},
		}
		s := &Server{Store: store, Images: api, NFS: mount}

		res := submitNFS(t, s, "/images/nfs/7/remove", nfsForm())
		if res.Code != http.StatusConflict {
			t.Fatalf("WindowsProfile remove status=%d want 409", res.Code)
		}
		if !strings.Contains(res.Body.String(), "Хранилище используется профилем Windows ISO") {
			t.Fatalf("unexpected body: %s", res.Body.String())
		}
	}

	// Blocked by active Ubuntu installation (409)
	{
		events := []string{}
		api := &fakeNFSImages{events: &events, fakeImagesAPI: fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{nfsStorage}}}}
		mount := &fakeNFSMountAPI{events: &events}
		store := &nfsTestStore{
			authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}},
			bootProfiles: []BootProfile{
				{ID: 10, Kind: BootProfileUbuntuISO, UbuntuStorageID: 7, UbuntuPath: "ubuntu.iso"},
			},
			installations: []Installation{
				{ID: 100, BootProfileID: 10},
			},
		}
		s := &Server{Store: store, Images: api, NFS: mount}

		res := submitNFS(t, s, "/images/nfs/7/remove", nfsForm())
		if res.Code != http.StatusConflict {
			t.Fatalf("active Ubuntu remove status=%d want 409", res.Code)
		}
		if !strings.Contains(res.Body.String(), "Хранилище используется действующей загрузкой Ubuntu") {
			t.Fatalf("unexpected body: %s", res.Body.String())
		}
	}

	// Allowed when Ubuntu installation is deleted (BootProfile exists, but no active installation)
	{
		events := []string{}
		api := &fakeNFSImages{events: &events, fakeImagesAPI: fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{nfsStorage}}}}
		mount := &fakeNFSMountAPI{events: &events}
		store := &nfsTestStore{
			authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}},
			bootProfiles: []BootProfile{
				{ID: 10, Kind: BootProfileUbuntuISO, UbuntuStorageID: 7, UbuntuPath: "ubuntu.iso"},
			},
			installations: []Installation{
				// No installation referencing BootProfileID 10
				{ID: 101, BootProfileID: 99},
			},
		}
		s := &Server{Store: store, Images: api, NFS: mount}

		res := submitNFS(t, s, "/images/nfs/7/remove", nfsForm())
		if res.Code != http.StatusSeeOther {
			t.Fatalf("historical Ubuntu remove status=%d want 303 body=%s", res.Code, res.Body.String())
		}
		if strings.Join(events, ",") != "remove,delete" {
			t.Fatalf("events=%v", events)
		}
	}

	// Clean removal
	{
		events := []string{}
		api := &fakeNFSImages{events: &events, fakeImagesAPI: fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{nfsStorage}}}}
		mount := &fakeNFSMountAPI{events: &events}
		store := &nfsTestStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
		s := &Server{Store: store, Images: api, NFS: mount}

		res := submitNFS(t, s, "/images/nfs/7/remove", nfsForm())
		if res.Code != http.StatusSeeOther {
			t.Fatalf("clean remove status=%d want 303", res.Code)
		}
		if strings.Join(events, ",") != "remove,delete" {
			t.Fatalf("events=%v", events)
		}
	}
}

func TestNFSScanUpdatesUploads(t *testing.T) {
	nfsStorage := images.Storage{
		ID:             7,
		Kind:           "nfs",
		Key:            "0123456789abcdef0123456789abcdef",
		Server:         "192.168.10.10",
		ExportPath:     "/export/iso",
		NFSVersion:     "4.1",
		State:          "ready",
		UploadsEnabled: false,
	}

	// CheckNFSUpload returns true -> updates uploads to true
	{
		events := []string{}
		api := &fakeNFSImages{events: &events, fakeImagesAPI: fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{nfsStorage}}}}
		mount := &fakeNFSMountAPI{events: &events, checkWritable: true}
		store := &nfsTestStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
		s := &Server{Store: store, Images: api, NFS: mount}

		form := url.Values{"id": {"7"}, "csrf": {CSRFToken("test-token")}}
		req := httptest.NewRequest(http.MethodPost, "/images/scan", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
		res := httptest.NewRecorder()
		s.Handler().ServeHTTP(res, req)

		if res.Code != http.StatusSeeOther {
			t.Fatalf("scan status=%d want 303", res.Code)
		}
		if strings.Join(events, ",") != "check-upload,set-uploads:true,scan" {
			t.Fatalf("events=%v", events)
		}
		if !api.setUploadsCalls[7] {
			t.Fatal("expected setUploads true")
		}
	}

	// CheckNFSUpload error -> still scans, does not fail
	{
		events := []string{}
		api := &fakeNFSImages{events: &events, fakeImagesAPI: fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{nfsStorage}}}}
		mount := &fakeNFSMountAPI{events: &events, checkErr: errors.New("check failed")}
		store := &nfsTestStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
		s := &Server{Store: store, Images: api, NFS: mount}

		form := url.Values{"id": {"7"}, "csrf": {CSRFToken("test-token")}}
		req := httptest.NewRequest(http.MethodPost, "/images/scan", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
		res := httptest.NewRecorder()
		s.Handler().ServeHTTP(res, req)

		if res.Code != http.StatusSeeOther {
			t.Fatalf("scan with checkErr status=%d want 303", res.Code)
		}
		if strings.Join(events, ",") != "check-upload,scan" {
			t.Fatalf("events=%v", events)
		}
	}
}

func TestNFSUsageAcrossControl(t *testing.T) {
	nfsRO := images.Storage{
		ID:             7,
		Kind:           "nfs",
		Key:            "0123456789abcdef0123456789abcdef",
		Server:         "192.168.10.10",
		ExportPath:     "/export/iso",
		NFSVersion:     "4.1",
		Root:           "/mnt/omb-nfs/0123456789abcdef0123456789abcdef",
		State:          "ready",
		UploadsEnabled: false,
		Uploads:        false,
	}
	nfsRW := images.Storage{
		ID:             8,
		Kind:           "nfs",
		Key:            "fedcba9876543210fedcba9876543210",
		Server:         "192.168.10.10",
		ExportPath:     "/export/iso-rw",
		NFSVersion:     "4.1",
		Root:           "/mnt/omb-nfs/fedcba9876543210fedcba9876543210",
		State:          "ready",
		UploadsEnabled: true,
		Uploads:        true,
	}
	catalog := images.Catalog{
		Storages: []images.Storage{nfsRO, nfsRW},
		Images: []images.Image{
			{StorageID: 7, Path: "win.iso", Size: 1000, Present: true},
			{StorageID: 8, Path: "ubuntu.iso", Size: 2000, Present: true},
		},
	}

	// 1. iso_upload: RO storage rejected (404/not found by uploadStorage), RW storage accepted
	{
		s := &Server{Images: &fakeNFSImages{fakeImagesAPI: fakeImagesAPI{catalog: catalog}}}
		_, okRO := s.uploadStorage(context.Background(), "7")
		if okRO {
			t.Fatal("expected RO NFS storage to be rejected for upload")
		}
		stRW, okRW := s.uploadStorage(context.Background(), "8")
		if !okRW || stRW.ID != 8 {
			t.Fatalf("expected RW NFS storage to be accepted for upload, got ok=%v", okRW)
		}
	}

	// 2. image_delete: RO storage forbidden
	{
		events := []string{}
		api := &fakeNFSImages{events: &events, fakeImagesAPI: fakeImagesAPI{catalog: catalog}}
		mount := &fakeNFSMountAPI{events: &events}
		store := &nfsTestStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
		s := &Server{Store: store, Images: api, NFS: mount}

		form := url.Values{
			"storage_id": {"7"},
			"path":       {"win.iso"},
			"csrf":       {CSRFToken("test-token")},
		}
		req := httptest.NewRequest(http.MethodPost, "/images/delete", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
		res := httptest.NewRecorder()
		s.Handler().ServeHTTP(res, req)

		if res.Code != http.StatusForbidden {
			t.Fatalf("image delete on RO NFS status=%d want 403", res.Code)
		}
	}

	// 3. image_delete: RW storage allowed
	{
		events := []string{}
		api := &fakeNFSImages{events: &events, fakeImagesAPI: fakeImagesAPI{catalog: catalog}}
		mount := &fakeNFSMountAPI{events: &events}
		store := &nfsTestStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
		s := &Server{Store: store, Images: api, NFS: mount}

		form := url.Values{
			"storage_id": {"8"},
			"path":       {"ubuntu.iso"},
			"csrf":       {CSRFToken("test-token")},
		}
		req := httptest.NewRequest(http.MethodPost, "/images/delete", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
		res := httptest.NewRecorder()
		s.Handler().ServeHTTP(res, req)

		if res.Code != http.StatusSeeOther {
			t.Fatalf("image delete on RW NFS status=%d want 303 body=%s", res.Code, res.Body.String())
		}
		if strings.Join(events, ",") != "enable-upload,delete-image" {
			t.Fatalf("events=%v", events)
		}
	}

	// 4. windows_profiles: SelectWindowsISO accepts NFS storage
	{
		src, err := SelectWindowsISO(catalog, 7, "win.iso", "Windows 10", "iso")
		if err != nil {
			t.Fatalf("SelectWindowsISO failed for NFS storage: %v", err)
		}
		if src.StorageID != 7 || src.Path != "win.iso" {
			t.Fatalf("SelectWindowsISO unexpected source: %+v", src)
		}
	}

	// 5. download_sources: lists ISOs from NFS storage
	{
		api := &fakeNFSImages{fakeImagesAPI: fakeImagesAPI{catalog: catalog}}
		store := &nfsTestStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
		s := &Server{Store: store, Images: api}
		choices, err := s.downloadSourceChoices(context.Background(), nil, nil, true)
		if err != nil {
			t.Fatalf("downloadSourceChoices failed: %v", err)
		}
		var found7, found8 bool
		for _, c := range choices {
			if c.Value == "iso:7:win.iso" {
				found7 = true
				if c.State == "unavailable" {
					t.Fatalf("NFS RO choice marked unavailable: %+v", c)
				}
			}
			if c.Value == "iso:8:ubuntu.iso" {
				found8 = true
				if c.State == "unavailable" {
					t.Fatalf("NFS RW choice marked unavailable: %+v", c)
				}
			}
		}
		if !found7 || !found8 {
			t.Fatalf("downloadSourceChoices missing choices: found7=%v found8=%v", found7, found8)
		}
	}

	// 6. image_inspections: matchingImageInspections includes NFS storage
	{
		records := map[string]ImageInspection{
			imageAuthorKey(7, "win.iso"): {
				StorageID:   7,
				Path:        "win.iso",
				StorageRoot: nfsRO.Root,
				Size:        1000,
				Modified:    catalog.Images[0].Modified,
				Kind:        "windows",
			},
		}
		matched := matchingImageInspections(catalog, records)
		if _, ok := matched[imageAuthorKey(7, "win.iso")]; !ok {
			t.Fatal("expected matchingImageInspections to include NFS image")
		}
	}
}
