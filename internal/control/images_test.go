package control

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"omb/internal/images"
)

type fakeImagesAPI struct {
	catalog        images.Catalog
	err            error
	scanErr        error
	scans          []int64
	uploaded       string
	selectedFolder string
	folders        []string
	deleted        []images.Image
	deleteErr      error
	settings       images.CatalogSettings
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
func (f *fakeImagesAPI) CreateNFS(context.Context, images.NFSStorageInput) (images.Storage, error) {
	return images.Storage{}, f.err
}
func (f *fakeImagesAPI) UpdateNFSVersion(context.Context, int64, string) error { return f.err }
func (f *fakeImagesAPI) SetNFSUploads(context.Context, int64, bool) error      { return f.err }
func (f *fakeImagesAPI) MarkNFSError(context.Context, int64) error             { return f.err }
func (f *fakeImagesAPI) MarkNFSScanError(context.Context, int64) error         { return f.err }
func (f *fakeImagesAPI) DeleteNFS(context.Context, int64) error                { return f.err }
func (f *fakeImagesAPI) RenameStorage(_ context.Context, id int64, name string) error {
	if f.err != nil {
		return f.err
	}
	for _, s := range f.catalog.Storages {
		if s.ID != id && s.Name == name {
			return images.ErrStorageNameTaken
		}
	}
	for i, s := range f.catalog.Storages {
		if s.ID == id {
			f.catalog.Storages[i].Name = name
			return nil
		}
	}
	return errors.New("storage not found")
}
func (f *fakeImagesAPI) Folders(context.Context, int64) ([]string, error) { return f.folders, f.err }
func (f *fakeImagesAPI) CatalogSettings(context.Context) (images.CatalogSettings, error) {
	if f.err != nil {
		return images.CatalogSettings{}, f.err
	}
	if f.settings.RescanIntervalMinutes == 0 {
		return images.DefaultCatalogSettings(), nil
	}
	return f.settings, nil
}
func (f *fakeImagesAPI) SetCatalogSettings(_ context.Context, s images.CatalogSettings) (images.CatalogSettings, error) {
	if f.err != nil {
		return images.CatalogSettings{}, f.err
	}
	f.settings = s
	return s, nil
}
func (f *fakeImagesAPI) DeleteImage(_ context.Context, id int64, path string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, images.Image{StorageID: id, Path: path})
	return nil
}
func (f *fakeImagesAPI) UploadISO(_ context.Context, _ int64, folder, name string, body io.Reader, _ int64) (images.UploadResult, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return images.UploadResult{}, err
	}
	f.uploaded = string(data)
	f.selectedFolder = folder
	return images.UploadResult{Path: folder + "/" + name, Size: int64(len(data))}, f.err
}
func (f *fakeImagesAPI) StartMigration(context.Context, int64, string, int64) (*images.MigrationJob, error) {
	return &images.MigrationJob{ID: "test-mig"}, f.err
}
func (f *fakeImagesAPI) ListMigrations(context.Context) ([]*images.MigrationJob, error) {
	return nil, f.err
}
func (f *fakeImagesAPI) GetMigration(context.Context, string) (*images.MigrationJob, error) {
	return nil, f.err
}
func (f *fakeImagesAPI) CancelMigration(context.Context, string) error {
	return f.err
}
func (f *fakeImagesAPI) CommitDeleteSource(context.Context, string) error {
	return f.err
}

func TestImagesPageEscapesISONameAndShowsStorage(t *testing.T) {
	api := &fakeImagesAPI{catalog: images.Catalog{
		Storages: []images.Storage{{ID: 5, Name: "NAS", Kind: "mounted", State: "ready"}},
		Images:   []images.Image{{StorageID: 5, Path: `<script>alert(1)</script>.iso`, Present: true}},
	}}
	s := &Server{Store: authOnlyStore{user: User{ID: 1, Username: "viewer", Role: "observer", Enabled: true}}, Images: api}
	req := httptest.NewRequest("GET", "/images", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "NAS") || strings.Contains(res.Body.String(), "<script>") || !strings.Contains(res.Body.String(), "&lt;script&gt;") {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestImagesPageShowsServiceOutage(t *testing.T) {
	s := &Server{Store: authOnlyStore{user: User{ID: 1, Role: "observer", Enabled: true}}, Images: &fakeImagesAPI{err: errors.New("offline")}}
	req := httptest.NewRequest("GET", "/images", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
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
			req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
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
	catalog := images.Catalog{Storages: []images.Storage{{ID: 5, Name: "NAS-Archive", State: "ready"}}}
	store := &auditRecordingStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}}
	s := &Server{Store: store, Images: &fakeImagesAPI{catalog: catalog}}
	req := httptest.NewRequest("POST", "/images/scan", strings.NewReader("id=5&csrf="+CSRFToken("test-token")))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther || store.action != "images.scan.requested" || store.result != "accepted" {
		t.Fatalf("status=%d audit=%s/%s", res.Code, store.action, store.result)
	}
	loc, _ := url.Parse(res.Header().Get("Location"))
	if got, want := loc.Query().Get("message"), "Сканирование хранилища «NAS-Archive» запущено"; got != want {
		t.Fatalf("message=%q, want %q", got, want)
	}
	if got := translate(langEN, loc.Query().Get("message")); got != "Scanning storage “NAS-Archive” started" {
		t.Fatalf("english message=%q", got)
	}
}
