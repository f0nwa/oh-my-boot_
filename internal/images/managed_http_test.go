package images

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type managedHTTPRepository struct {
	httpRepository
	deleted        bool
	deletedNFS     bool
	updatedVersion string
	updatedUploads *bool
}

func (r *managedHTTPRepository) CreateSMB(_ context.Context, input ManagedStorageInput) (Storage, error) {
	storage := Storage{ID: 9, Name: input.Name, Kind: "smb", Server: input.Server, Share: input.Share, Subpath: input.Subpath, Key: input.Key, State: "unavailable"}
	r.storages[9] = storage
	return storage, nil
}

func (r *managedHTTPRepository) DeleteSMB(_ context.Context, id int64) error {
	r.deleted = true
	delete(r.storages, id)
	return nil
}

func (r *managedHTTPRepository) CreateNFS(_ context.Context, input NFSStorageInput) (Storage, error) {
	storage := Storage{ID: 9, Name: input.Name, Kind: "nfs", Server: input.Server, ExportPath: input.ExportPath, NFSVersion: input.Version, Key: input.Key, State: "unavailable"}
	r.storages[9] = storage
	return storage, nil
}

func (r *managedHTTPRepository) UpdateNFSConfig(_ context.Context, id int64, version string) error {
	r.updatedVersion = version
	s := r.storages[id]
	s.NFSVersion = version
	r.storages[id] = s
	return nil
}

func (r *managedHTTPRepository) SetUploadsEnabled(_ context.Context, id int64, enabled bool) error {
	r.updatedUploads = &enabled
	s := r.storages[id]
	s.UploadsEnabled = enabled
	r.storages[id] = s
	return nil
}

func (r *managedHTTPRepository) DeleteNFS(_ context.Context, id int64) error {
	r.deletedNFS = true
	delete(r.storages, id)
	return nil
}

func TestManagedStorageAPIReservesAndReportsFailure(t *testing.T) {
	repo := &managedHTTPRepository{httpRepository: httpRepository{memoryRepository: memoryRepository{storages: map[int64]Storage{}, images: map[int64]map[string]Image{}, state: map[int64]string{}}}}
	handler := NewServer(repo).Handler()
	key := "0123456789abcdef0123456789abcdef"
	create := httptest.NewRecorder()
	handler.ServeHTTP(create, httptest.NewRequest("POST", "/smb-storages", strings.NewReader(`{"name":"NAS","key":"`+key+`","server":"192.168.10.10","share":"Downloads","subpath":"iso"}`)))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var storage Storage
	if err := json.Unmarshal(create.Body.Bytes(), &storage); err != nil || storage.ID != 9 || storage.Kind != "smb" {
		t.Fatalf("created=%+v err=%v", storage, err)
	}
	failure := httptest.NewRecorder()
	handler.ServeHTTP(failure, httptest.NewRequest("POST", "/smb-storages/9/error", nil))
	if failure.Code != http.StatusNoContent || repo.state[9] != "unavailable" {
		t.Fatalf("failure status=%d state=%s", failure.Code, repo.state[9])
	}
	repo.state[9] = "ready"
	scanFailure := httptest.NewRecorder()
	handler.ServeHTTP(scanFailure, httptest.NewRequest("POST", "/smb-storages/9/scan-error", nil))
	if scanFailure.Code != http.StatusNoContent || repo.state[9] != "unavailable" {
		t.Fatalf("scan-start failure status=%d state=%s", scanFailure.Code, repo.state[9])
	}
	removed := httptest.NewRecorder()
	handler.ServeHTTP(removed, httptest.NewRequest("DELETE", "/smb-storages/9", nil))
	if removed.Code != http.StatusNoContent || !repo.deleted {
		t.Fatalf("remove status=%d deleted=%v", removed.Code, repo.deleted)
	}
}

func TestManagedStorageAPIRejectsPasswordAndInvalidPath(t *testing.T) {
	repo := &managedHTTPRepository{httpRepository: httpRepository{memoryRepository: memoryRepository{storages: map[int64]Storage{}, images: map[int64]map[string]Image{}, state: map[int64]string{}}}}
	handler := NewServer(repo).Handler()
	for _, body := range []string{
		`{"name":"NAS","key":"0123456789abcdef0123456789abcdef","server":"192.168.10.10","share":"Downloads","subpath":"iso","password":"secret"}`,
		`{"name":"NAS","key":"0123456789abcdef0123456789abcdef","server":"192.168.10.10","share":"Downloads","subpath":"../iso"}`,
	} {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest("POST", "/smb-storages", strings.NewReader(body)))
		if res.Code != http.StatusBadRequest {
			t.Fatalf("invalid request status=%d body=%s", res.Code, res.Body.String())
		}
	}
}

func TestManagedNFSStorageAPIReservesAndReportsFailure(t *testing.T) {
	repo := &managedHTTPRepository{httpRepository: httpRepository{memoryRepository: memoryRepository{storages: map[int64]Storage{}, images: map[int64]map[string]Image{}, state: map[int64]string{}}}}
	handler := NewServer(repo).Handler()
	key := "0123456789abcdef0123456789abcdef"
	create := httptest.NewRecorder()
	handler.ServeHTTP(create, httptest.NewRequest("POST", "/nfs-storages", strings.NewReader(`{"name":"NFS","key":"`+key+`","server":"192.168.10.10","export_path":"/export/iso","version":"4.1"}`)))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var storage Storage
	if err := json.Unmarshal(create.Body.Bytes(), &storage); err != nil || storage.ID != 9 || storage.Kind != "nfs" {
		t.Fatalf("created=%+v err=%v", storage, err)
	}
	failure := httptest.NewRecorder()
	handler.ServeHTTP(failure, httptest.NewRequest("POST", "/nfs-storages/9/error", nil))
	if failure.Code != http.StatusNoContent || repo.state[9] != "unavailable" {
		t.Fatalf("failure status=%d state=%s", failure.Code, repo.state[9])
	}
	repo.state[9] = "ready"
	scanFailure := httptest.NewRecorder()
	handler.ServeHTTP(scanFailure, httptest.NewRequest("POST", "/nfs-storages/9/scan-error", nil))
	if scanFailure.Code != http.StatusNoContent || repo.state[9] != "unavailable" {
		t.Fatalf("scan-start failure status=%d state=%s", scanFailure.Code, repo.state[9])
	}
	removed := httptest.NewRecorder()
	handler.ServeHTTP(removed, httptest.NewRequest("DELETE", "/nfs-storages/9", nil))
	if removed.Code != http.StatusNoContent || !repo.deletedNFS {
		t.Fatalf("remove status=%d deleted=%v", removed.Code, repo.deletedNFS)
	}
}

func TestManagedNFSStorageAPIConfigAndUploads(t *testing.T) {
	repo := &managedHTTPRepository{httpRepository: httpRepository{memoryRepository: memoryRepository{
		storages: map[int64]Storage{9: {ID: 9, Kind: "nfs", State: "ready", NFSVersion: "4.1"}},
		images:   map[int64]map[string]Image{},
		state:    map[int64]string{9: "ready"},
	}}}
	handler := NewServer(repo).Handler()

	// Update version to 3
	resVer := httptest.NewRecorder()
	handler.ServeHTTP(resVer, httptest.NewRequest("POST", "/nfs-storages/9/version", strings.NewReader(`{"version":"3"}`)))
	if resVer.Code != http.StatusNoContent || repo.updatedVersion != "3" {
		t.Fatalf("version update status=%d version=%q", resVer.Code, repo.updatedVersion)
	}

	// Update version with invalid value rejected
	resVerBad := httptest.NewRecorder()
	handler.ServeHTTP(resVerBad, httptest.NewRequest("POST", "/nfs-storages/9/version", strings.NewReader(`{"version":"2"}`)))
	if resVerBad.Code != http.StatusBadRequest {
		t.Fatalf("bad version status=%d", resVerBad.Code)
	}

	// Enable uploads
	resUp := httptest.NewRecorder()
	handler.ServeHTTP(resUp, httptest.NewRequest("POST", "/nfs-storages/9/uploads", strings.NewReader(`{"uploads_enabled":true}`)))
	if resUp.Code != http.StatusNoContent || repo.updatedUploads == nil || !*repo.updatedUploads {
		t.Fatalf("uploads update status=%d uploads=%v", resUp.Code, repo.updatedUploads)
	}

	// Disable uploads
	resDown := httptest.NewRecorder()
	handler.ServeHTTP(resDown, httptest.NewRequest("POST", "/nfs-storages/9/uploads", strings.NewReader(`{"uploads_enabled":false}`)))
	if resDown.Code != http.StatusNoContent || repo.updatedUploads == nil || *repo.updatedUploads {
		t.Fatalf("uploads disable status=%d uploads=%v", resDown.Code, repo.updatedUploads)
	}
}

func TestManagedNFSStorageAPIRejectsInvalidInput(t *testing.T) {
	repo := &managedHTTPRepository{httpRepository: httpRepository{memoryRepository: memoryRepository{storages: map[int64]Storage{}, images: map[int64]map[string]Image{}, state: map[int64]string{}}}}
	handler := NewServer(repo).Handler()
	key := "0123456789abcdef0123456789abcdef"
	for _, body := range []string{
		`{"name":"","key":"` + key + `","server":"192.168.10.10","export_path":"/export/iso","version":"4.1"}`,
		`{"name":"NFS","key":"bad-key","server":"192.168.10.10","export_path":"/export/iso","version":"4.1"}`,
		`{"name":"NFS","key":"` + key + `","server":"","export_path":"/export/iso","version":"4.1"}`,
		`{"name":"NFS","key":"` + key + `","server":"192.168.10.10","export_path":"relative/path","version":"4.1"}`,
		`{"name":"NFS","key":"` + key + `","server":"192.168.10.10","export_path":"/export/iso","version":"2"}`,
		`{"name":"NFS","key":"` + key + `","server":"192.168.10.10","export_path":"/export/iso","version":"4.1","extra":"field"}`,
	} {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest("POST", "/nfs-storages", strings.NewReader(body)))
		if res.Code != http.StatusBadRequest {
			t.Fatalf("invalid request body=%s status=%d", body, res.Code)
		}
	}
}
