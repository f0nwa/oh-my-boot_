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

	"pxe-platform/internal/images"
)

type fakeManagedImages struct {
	fakeImagesAPI
	events  *[]string
	storage images.Storage
	deleted chan struct{}
}

func (f *fakeManagedImages) CreateSMB(_ context.Context, input images.ManagedStorageInput) (images.Storage, error) {
	*f.events = append(*f.events, "reserve")
	f.storage = images.Storage{ID: 7, Key: input.Key, Name: input.Name, Kind: "smb", Server: input.Server, Share: input.Share, Subpath: input.Subpath}
	f.catalog.Storages = append(f.catalog.Storages, f.storage)
	return f.storage, nil
}
func (f *fakeManagedImages) MarkSMBError(_ context.Context, _ int64) error {
	*f.events = append(*f.events, "mark-error")
	return nil
}
func (f *fakeManagedImages) MarkSMBScanError(_ context.Context, _ int64) error {
	*f.events = append(*f.events, "mark-scan-error")
	return nil
}
func (f *fakeManagedImages) DeleteSMB(_ context.Context, _ int64) error {
	*f.events = append(*f.events, "delete")
	if f.deleted != nil {
		close(f.deleted)
	}
	return nil
}

type fakeSMBMountAPI struct {
	events       *[]string
	mountErr     error
	removeErr    error
	mountStarted chan struct{}
	mountRelease <-chan struct{}
}

func (f *fakeSMBMountAPI) Mount(_ context.Context, _, _, _, _, _, _, _ string) error {
	*f.events = append(*f.events, "mount")
	if f.mountStarted != nil {
		close(f.mountStarted)
	}
	if f.mountRelease != nil {
		<-f.mountRelease
	}
	return f.mountErr
}

func TestSMBRetryCannotRemountAfterConcurrentRemoval(t *testing.T) {
	events := []string{}
	api := &fakeManagedImages{events: &events, deleted: make(chan struct{}), fakeImagesAPI: fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{{ID: 7, Kind: "smb", Key: "0123456789abcdef0123456789abcdef", Server: "192.168.10.10", Share: "Downloads", Subpath: "iso"}}}}}
	release := make(chan struct{})
	mount := &fakeSMBMountAPI{events: &events, mountStarted: make(chan struct{}), mountRelease: release}
	s := &Server{Store: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}, Images: api, SMB: mount}
	retryDone := make(chan struct{})
	go func() { submitSMB(t, s, "/images/smb/7/retry", smbForm()); close(retryDone) }()
	<-mount.mountStarted
	removeDone := make(chan struct{})
	go func() { submitSMB(t, s, "/images/smb/7/remove", smbForm()); close(removeDone) }()
	select {
	case <-api.deleted:
		close(release)
		<-retryDone
		<-removeDone
		t.Fatal("storage deleted while retry was still mounting")
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	<-retryDone
	<-removeDone
}
func (f *fakeSMBMountAPI) Remove(_ context.Context, _ string) error {
	*f.events = append(*f.events, "remove")
	return f.removeErr
}
func (f *fakeSMBMountAPI) EnableUpload(_ context.Context, _, _, _, _ string) error {
	if f.events != nil {
		*f.events = append(*f.events, "enable-upload")
	}
	return f.mountErr
}

func smbForm() url.Values {
	return url.Values{"path": {"//192.168.10.10/Downloads/iso"}, "username": {"reader"}, "password": {"secret"}, "csrf": {CSRFToken("test-token")}}
}

func submitSMB(t *testing.T, s *Server, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	return res
}

func TestSMBCreateRequiresAdminAndCSRF(t *testing.T) {
	for _, tc := range []struct {
		role string
		want int
	}{{"observer", http.StatusForbidden}, {"operator", http.StatusForbidden}, {"admin", http.StatusSeeOther}} {
		events := []string{}
		api := &fakeManagedImages{events: &events}
		mount := &fakeSMBMountAPI{events: &events}
		s := &Server{Store: authOnlyStore{user: User{ID: 1, Role: tc.role, Enabled: true}}, Images: api, SMB: mount}
		res := submitSMB(t, s, "/images/smb", smbForm())
		if res.Code != tc.want {
			t.Fatalf("role=%s status=%d body=%s", tc.role, res.Code, res.Body.String())
		}
		if tc.role == "admin" && strings.Join(events, ",") != "reserve,mount" || tc.role != "admin" && len(events) != 0 {
			t.Fatalf("role=%s events=%v", tc.role, events)
		}
		if tc.role == "admin" && (api.storage.Server != "192.168.10.10" || api.storage.Share != "Downloads" || api.storage.Subpath != "iso") {
			t.Fatalf("path parsed incorrectly: %+v", api.storage)
		}
	}
	events := []string{}
	s := &Server{Store: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}, Images: &fakeManagedImages{events: &events}, SMB: &fakeSMBMountAPI{events: &events}}
	form := smbForm()
	form.Set("csrf", "wrong")
	if res := submitSMB(t, s, "/images/smb", form); res.Code != http.StatusForbidden || len(events) != 0 {
		t.Fatalf("CSRF status=%d events=%v", res.Code, events)
	}
}

func TestSMBMountFailureRecordsErrorAndRemovalWaitsForCleanup(t *testing.T) {
	events := []string{}
	api := &fakeManagedImages{events: &events}
	mount := &fakeSMBMountAPI{events: &events, mountErr: errors.New("mount failed")}
	s := &Server{Store: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}, Images: api, SMB: mount}
	res := submitSMB(t, s, "/images/smb", smbForm())
	if res.Code != http.StatusServiceUnavailable || strings.Join(events, ",") != "reserve,mount,mark-error" || strings.Contains(res.Body.String(), "secret") {
		t.Fatalf("mount status=%d events=%v body=%s", res.Code, events, res.Body.String())
	}
	events = []string{}
	mount.removeErr = errors.New("cleanup failed")
	res = submitSMB(t, s, "/images/smb/7/remove", smbForm())
	if res.Code != http.StatusServiceUnavailable || strings.Join(events, ",") != "remove" {
		t.Fatalf("remove status=%d events=%v", res.Code, events)
	}
}

func TestSMBPageEscapesFieldsAndNeverShowsPassword(t *testing.T) {
	events := []string{}
	api := &fakeManagedImages{events: &events, fakeImagesAPI: fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{{ID: 7, Name: "<script>NAS</script>", Kind: "smb", Server: "192.168.10.10", Share: "Downloads", Subpath: "iso", Key: "0123456789abcdef0123456789abcdef"}}}}}
	s := &Server{Store: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}, Images: api}
	req := httptest.NewRequest("GET", "/storages", nil)
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK || strings.Contains(res.Body.String(), "<script>") || !strings.Contains(res.Body.String(), "&lt;script&gt;") || !strings.Contains(res.Body.String(), "name=\"password\"") {
		t.Fatalf("unsafe SMB page status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestSMBFormIsAdminOnly(t *testing.T) {
	for _, role := range []string{"admin", "operator", "observer"} {
		events := []string{}
		s := &Server{Store: authOnlyStore{user: User{ID: 1, Role: role, Enabled: true}}, Images: &fakeManagedImages{events: &events}}
		req := httptest.NewRequest("GET", "/storages", nil)
		req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
		res := httptest.NewRecorder()
		s.Handler().ServeHTTP(res, req)
		hasForm := strings.Contains(res.Body.String(), `action="/images/smb"`)
		if res.Code != http.StatusOK || hasForm != (role == "admin") {
			t.Fatalf("role=%s status=%d hasForm=%v", role, res.Code, hasForm)
		}
		if role == "admin" && (!strings.Contains(res.Body.String(), `name="path"`) || strings.Contains(res.Body.String(), `name="server"`) || strings.Contains(res.Body.String(), `name="share"`) || strings.Contains(res.Body.String(), `name="subpath"`)) {
			t.Fatal("SMB form must use one direct path field")
		}
	}
}

func TestSMBRetryAndRemoveOrder(t *testing.T) {
	events := []string{}
	api := &fakeManagedImages{events: &events, fakeImagesAPI: fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{{ID: 7, Kind: "smb", Key: "0123456789abcdef0123456789abcdef", Server: "192.168.10.10", Share: "Downloads", Subpath: "iso"}}}}}
	mount := &fakeSMBMountAPI{events: &events}
	s := &Server{Store: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}, Images: api, SMB: mount}
	if res := submitSMB(t, s, "/images/smb/7/retry", smbForm()); res.Code != http.StatusSeeOther || strings.Join(events, ",") != "mount" || len(api.scans) != 1 || api.scans[0] != 7 {
		t.Fatalf("retry status=%d events=%v scans=%v", res.Code, events, api.scans)
	}
	events = []string{}
	if res := submitSMB(t, s, "/images/smb/7/remove", smbForm()); res.Code != http.StatusSeeOther || strings.Join(events, ",") != "remove,delete" {
		t.Fatalf("remove status=%d events=%v", res.Code, events)
	}
}

func TestSMBChangeWaitsForRunningScan(t *testing.T) {
	events := []string{}
	api := &fakeManagedImages{events: &events, fakeImagesAPI: fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{{ID: 7, Kind: "smb", State: "scanning", Key: "0123456789abcdef0123456789abcdef", Server: "192.168.10.10", Share: "Downloads", Subpath: "iso"}}}}}
	s := &Server{Store: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}, Images: api, SMB: &fakeSMBMountAPI{events: &events}}
	for _, path := range []string{"/images/smb/7/retry", "/images/smb/7/remove"} {
		res := submitSMB(t, s, path, smbForm())
		if res.Code != http.StatusConflict || len(events) != 0 {
			t.Fatalf("path=%s status=%d events=%v", path, res.Code, events)
		}
	}
}

func TestSMBCreateReportsScanStartFailure(t *testing.T) {
	events := []string{}
	api := &fakeManagedImages{events: &events, fakeImagesAPI: fakeImagesAPI{scanErr: errors.New("offline")}}
	s := &Server{Store: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}, Images: api, SMB: &fakeSMBMountAPI{events: &events}}
	res := submitSMB(t, s, "/images/smb", smbForm())
	if res.Code != http.StatusServiceUnavailable || strings.Contains(res.Body.String(), "secret") || strings.Join(events, ",") != "reserve,mount,mark-scan-error" {
		t.Fatalf("scan failure status=%d events=%v body=%s", res.Code, events, res.Body.String())
	}
}
