package control

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pxe-platform/internal/images"
)

type countedReader struct {
	read int
}

func (r *countedReader) Read(p []byte) (int, error) {
	r.read++
	return 0, io.EOF
}

func uploadStorage() images.Storage {
	return images.Storage{ID: 7, Name: "DSM", Kind: "smb", Key: "0123456789abcdef0123456789abcdef", Server: "192.168.10.10", Share: "Downloads", Subpath: "iso", State: "ready"}
}

func TestISOUploadRejectsObserverAndBadCSRFBeforeReadingBody(t *testing.T) {
	for _, tc := range []struct {
		role string
		csrf string
	}{
		{role: "observer", csrf: CSRFToken("test-token")},
		{role: "operator", csrf: "bad"},
	} {
		t.Run(tc.role+tc.csrf, func(t *testing.T) {
			body := &countedReader{}
			api := &fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{uploadStorage()}}}
			events := []string{}
			s := &Server{Store: authOnlyStore{user: User{ID: 1, Role: tc.role, Enabled: true}}, Images: api, SMB: &fakeSMBMountAPI{events: &events}}
			req := httptest.NewRequest(http.MethodPost, "/images/upload/7?name=test.iso", io.NopCloser(body))
			req.ContentLength = 1
			req.Header.Set("X-CSRF-Token", tc.csrf)
			req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
			res := httptest.NewRecorder()
			s.Handler().ServeHTTP(res, req)
			if res.Code != http.StatusForbidden || body.read != 0 || len(events) != 0 || api.uploaded != "" {
				t.Fatalf("status=%d reads=%d events=%v", res.Code, body.read, events)
			}
		})
	}
}

func TestISOUploadStreamsToSelectedStorageAndFolder(t *testing.T) {
	api := &fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{uploadStorage()}}}
	events := []string{}
	s := &Server{Store: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}, Images: api, SMB: &fakeSMBMountAPI{events: &events}}
	req := httptest.NewRequest(http.MethodPost, "/images/upload/7?name=win.iso&folder=Windows/Test", strings.NewReader("iso-data"))
	req.ContentLength = 8
	req.Header.Set("X-CSRF-Token", CSRFToken("test-token"))
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusCreated || api.uploaded != "iso-data" || api.selectedFolder != "Windows/Test" || strings.Join(events, ",") != "enable-upload" {
		t.Fatalf("status=%d body=%s uploaded=%q folder=%q events=%v", res.Code, res.Body.String(), api.uploaded, api.selectedFolder, events)
	}
}

func TestISOPageOffersUploadForReadySMBStorage(t *testing.T) {
	api := &fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{uploadStorage()}}}
	s := &Server{Store: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}, Images: api}
	req := httptest.NewRequest(http.MethodGet, "/images", nil)
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "iso-upload-form") || !strings.Contains(res.Body.String(), "DSM") || !strings.Contains(res.Body.String(), `<select id="iso-folder" name="folder"></select>`) {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}
