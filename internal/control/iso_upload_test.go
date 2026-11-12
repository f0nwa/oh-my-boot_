package control

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omb/internal/images"
)

type countedReader struct {
	read int
}

func (r *countedReader) Read(p []byte) (int, error) {
	r.read++
	return 0, io.EOF
}

func uploadStorage() images.Storage {
	return images.Storage{ID: 7, Name: "DSM", Kind: "smb", Key: "0123456789abcdef0123456789abcdef", Server: "192.168.10.10", Share: "Downloads", Subpath: "iso", State: "ready", Uploads: true, Total: 2 << 40, Free: 566 << 30}
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
			req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
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
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
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
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "iso-upload-form") || !strings.Contains(res.Body.String(), "DSM") || !strings.Contains(res.Body.String(), `<select id="iso-folder" name="folder"></select>`) {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestISOPageUploadIsInDialogForEditorsOnly(t *testing.T) {
	for _, tc := range []struct {
		role string
		want bool
	}{{"operator", true}, {"observer", false}} {
		t.Run(tc.role, func(t *testing.T) {
			api := &fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{uploadStorage()}}}
			s := &Server{Store: authOnlyStore{user: User{ID: 1, Role: tc.role, Enabled: true}}, Images: api}
			req := httptest.NewRequest(http.MethodGet, "/images", nil)
			req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
			res := httptest.NewRecorder()
			s.Handler().ServeHTTP(res, req)
			body := res.Body.String()
			open := strings.Index(body, `id="iso-upload-dialog"`)
			form := strings.Index(body, `id="iso-upload-form"`)
			trigger := strings.Contains(body, `id="iso-upload-open"`)
			if tc.want && (open < 0 || form < open || !trigger || !strings.Contains(body[form:], `</dialog>`)) {
				t.Fatal("upload trigger and form inside dialog are required")
			}
			if !tc.want && (open >= 0 || form >= 0 || trigger) {
				t.Fatal("observer must not see upload controls")
			}
		})
	}
}

func localStorage(uploads bool) images.Storage {
	return images.Storage{ID: 3, Name: "Local", Kind: "local", Root: "/var/lib/omb-images/iso", State: "ready", Uploads: uploads, Total: 39 << 30, Free: 26 << 30}
}

func TestISOUploadIntoLocalStorageNeedsNoSMBMount(t *testing.T) {
	api := &fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{localStorage(true)}}}
	events := []string{}
	s := &Server{Store: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}, Images: api, SMB: &fakeSMBMountAPI{events: &events}}
	req := httptest.NewRequest(http.MethodPost, "/images/upload/3?name=win.iso", strings.NewReader("iso-data"))
	req.ContentLength = 8
	req.Header.Set("X-CSRF-Token", CSRFToken("test-token"))
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusCreated || api.uploaded != "iso-data" || len(events) != 0 {
		t.Fatalf("status=%d body=%s events=%v", res.Code, res.Body.String(), events)
	}
	// A read-only local storage takes no uploads.
	api = &fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{localStorage(false)}}}
	s.Images = api
	req = httptest.NewRequest(http.MethodPost, "/images/upload/3?name=win.iso", strings.NewReader("iso-data"))
	req.ContentLength = 8
	req.Header.Set("X-CSRF-Token", CSRFToken("test-token"))
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res = httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNotFound || api.uploaded != "" {
		t.Fatalf("read-only local: status=%d", res.Code)
	}
}

func TestISOPageListsUploadTargetsWithFreeSpace(t *testing.T) {
	api := &fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{uploadStorage(), localStorage(true)}}}
	s := &Server{Store: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}, Images: api}
	for _, page := range []string{"/images", "/storages", "/system"} {
		req := httptest.NewRequest(http.MethodGet, page, nil)
		req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
		res := httptest.NewRecorder()
		s.Handler().ServeHTTP(res, req)
		body := res.Body.String()
		want := []string{"свободно 566,0 ГБ из 2,0 ТБ", "свободно 26,0 ГБ из 39,0 ГБ"}
		if page == "/images" {
			want = []string{`<option value="7" data-free="607737872384">DSM · сетевая папка · свободно 566,0 ГБ</option>`, `<option value="3" data-free="27917287424">Local · на этом сервере · свободно 26,0 ГБ</option>`}
		}
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Fatalf("%s lacks %q: %s", page, w, body)
			}
		}
	}
}

func TestLocalISOStorageMustBeUnderTheAgentRoot(t *testing.T) {
	for root, want := range map[string]bool{"/var/lib/omb-images/iso": true, "/var/lib/omb-images": true, "/srv/iso": false, "/var/lib/omb-images/../x": false} {
		if got := localISOStorage(root); got != want {
			t.Errorf("%s: %v", root, got)
		}
	}
}
