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
	deleted bool
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
