package control

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pxe-platform/internal/images"
)

type fakeImagesAPI struct {
	catalog images.Catalog
	err     error
	scanErr error
	scans   []int64
}

type auditRecordingStore struct {
	authOnlyStore
	action string
	result string
}

func (s *auditRecordingStore) Audit(_ context.Context, _ int64, action, result, _ string) error {
	s.action, s.result = action, result
	return nil
}

func (f *fakeImagesAPI) Catalog(context.Context) (images.Catalog, error) { return f.catalog, f.err }
func (f *fakeImagesAPI) Scan(_ context.Context, id int64) error {
	f.scans = append(f.scans, id)
	if f.scanErr != nil {
		return f.scanErr
	}
	return f.err
}
func (f *fakeImagesAPI) CreateSMB(context.Context, images.ManagedStorageInput) (images.Storage, error) {
	return images.Storage{}, f.err
}
func (f *fakeImagesAPI) MarkSMBError(context.Context, int64) error     { return f.err }
func (f *fakeImagesAPI) MarkSMBScanError(context.Context, int64) error { return f.err }
func (f *fakeImagesAPI) DeleteSMB(context.Context, int64) error        { return f.err }

func TestImagesPageEscapesISONameAndShowsStorage(t *testing.T) {
	api := &fakeImagesAPI{catalog: images.Catalog{
		Storages: []images.Storage{{ID: 5, Name: "NAS", Kind: "mounted", State: "ready"}},
		Images:   []images.Image{{StorageID: 5, Path: `<script>alert(1)</script>.iso`, Present: true}},
	}}
	s := &Server{Store: authOnlyStore{user: User{ID: 1, Username: "viewer", Role: "observer", Enabled: true}}, Images: api}
	req := httptest.NewRequest("GET", "/images", nil)
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "NAS") || !strings.Contains(res.Body.String(), "Смонтированный каталог") || !strings.Contains(res.Body.String(), "Доступно") || strings.Contains(res.Body.String(), "<script>") || !strings.Contains(res.Body.String(), "&lt;script&gt;") {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestImagesPageShowsServiceOutage(t *testing.T) {
	s := &Server{Store: authOnlyStore{user: User{ID: 1, Role: "observer", Enabled: true}}, Images: &fakeImagesAPI{err: errors.New("offline")}}
	req := httptest.NewRequest("GET", "/images", nil)
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "недоступен") {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestImagesScanRoleAndCSRF(t *testing.T) {
	for _, tc := range []struct {
		role string
		want int
	}{
		{"observer", http.StatusForbidden},
		{"operator", http.StatusSeeOther},
		{"admin", http.StatusSeeOther},
	} {
		t.Run(tc.role, func(t *testing.T) {
			api := &fakeImagesAPI{}
			s := &Server{Store: authOnlyStore{user: User{ID: 1, Role: tc.role, Enabled: true}}, Images: api}
			body := "id=5&csrf=" + CSRFToken("test-token")
			req := httptest.NewRequest("POST", "/images/scan", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
			res := httptest.NewRecorder()
			s.Handler().ServeHTTP(res, req)
			if res.Code != tc.want {
				t.Fatalf("role=%s status=%d body=%s", tc.role, res.Code, res.Body.String())
			}
			if tc.role == "observer" && len(api.scans) != 0 || tc.role != "observer" && (len(api.scans) != 1 || api.scans[0] != 5) {
				t.Fatalf("role=%s scans=%v", tc.role, api.scans)
			}
		})
	}
}

func TestImagesScanAuditsAcceptedRequestNotCompletion(t *testing.T) {
	store := &auditRecordingStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}}
	s := &Server{Store: store, Images: &fakeImagesAPI{}}
	req := httptest.NewRequest("POST", "/images/scan", strings.NewReader("id=5&csrf="+CSRFToken("test-token")))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther || store.action != "images.scan.requested" || store.result != "accepted" {
		t.Fatalf("status=%d audit=%s/%s", res.Code, store.action, store.result)
	}
}
