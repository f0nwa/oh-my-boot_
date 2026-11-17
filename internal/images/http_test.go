package images

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type httpRepository struct {
	memoryRepository
	done chan struct{}
}

func (r *httpRepository) Catalog(context.Context) (Catalog, error) {
	result := Catalog{Storages: []Storage{}, Images: []Image{}}
	for _, storage := range r.storages {
		storage.State = r.state[storage.ID]
		result.Storages = append(result.Storages, storage)
	}
	for _, byPath := range r.images {
		for _, image := range byPath {
			result.Images = append(result.Images, image)
		}
	}
	return result, nil
}
func (r *httpRepository) MarkScanning(_ context.Context, id int64) error {
	r.state[id] = "scanning"
	return nil
}
func (r *httpRepository) CreateSMB(_ context.Context, _ ManagedStorageInput) (Storage, error) {
	return Storage{}, nil
}
func (r *httpRepository) DeleteSMB(_ context.Context, _ int64) error { return nil }
func (r *httpRepository) CreateNFS(_ context.Context, _ NFSStorageInput) (Storage, error) {
	return Storage{}, nil
}
func (r *httpRepository) UpdateNFSConfig(_ context.Context, _ int64, _ string) error {
	return nil
}
func (r *httpRepository) SetUploadsEnabled(_ context.Context, _ int64, _ bool) error {
	return nil
}
func (r *httpRepository) DeleteNFS(_ context.Context, _ int64) error    { return nil }
func (r *httpRepository) RenameStorage(_ context.Context, _ int64, _ string) error {
	return nil
}
func (r *httpRepository) RecordUpload(_ context.Context, _ Image) error { return nil }
func (r *httpRepository) SaveScan(ctx context.Context, id int64, found []Image, otherBytes int64) error {
	err := r.memoryRepository.SaveScan(ctx, id, found, otherBytes)
	close(r.done)
	return err
}

func TestHandlerListsCatalogAndStartsScan(t *testing.T) {
	root := realTempDir(t)
	repo := &httpRepository{memoryRepository: memoryRepository{
		storages: map[int64]Storage{7: {ID: 7, Name: "NAS", Kind: "local", Root: root}},
		images:   map[int64]map[string]Image{},
		state:    map[int64]string{7: "unknown"},
	}, done: make(chan struct{})}
	server := NewServer(repo)
	get := httptest.NewRecorder()
	server.Handler().ServeHTTP(get, httptest.NewRequest("GET", "/catalog", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("GET status=%d", get.Code)
	}
	var catalog Catalog
	if err := json.Unmarshal(get.Body.Bytes(), &catalog); err != nil || len(catalog.Storages) != 1 || catalog.Storages[0].Name != "NAS" {
		t.Fatalf("catalog=%+v err=%v", catalog, err)
	}
	post := httptest.NewRecorder()
	server.Handler().ServeHTTP(post, httptest.NewRequest("POST", "/scan", strings.NewReader(`{"storage_id":7}`)))
	if post.Code != http.StatusAccepted {
		t.Fatalf("POST status=%d body=%s", post.Code, post.Body.String())
	}
	<-repo.done
	if repo.state[7] != "ready" {
		t.Fatalf("scan did not finish: %s", repo.state[7])
	}
}

func TestHandlerRejectsUnknownScan(t *testing.T) {
	repo := &httpRepository{memoryRepository: memoryRepository{storages: map[int64]Storage{}, images: map[int64]map[string]Image{}, state: map[int64]string{}}}
	server := NewServer(repo)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequest("POST", "/scan", strings.NewReader(`{"storage_id":99}`)))
	if res.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestHandlerCatalogSettings(t *testing.T) {
	repo := &httpRepository{memoryRepository: memoryRepository{storages: map[int64]Storage{}, images: map[int64]map[string]Image{}, state: map[int64]string{}}}
	server := NewServer(repo)

	// 1. GET returns default settings
	getRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(getRes, httptest.NewRequest("GET", "/settings", nil))
	if getRes.Code != http.StatusOK {
		t.Fatalf("GET /settings status = %d", getRes.Code)
	}
	var got CatalogSettings
	if err := json.Unmarshal(getRes.Body.Bytes(), &got); err != nil || !got.RescanEnabled || got.RescanIntervalMinutes != 30 {
		t.Fatalf("unexpected defaults: %+v err=%v", got, err)
	}

	// 2. PUT updates settings
	putRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(putRes, httptest.NewRequest("PUT", "/settings", strings.NewReader(`{"rescan_enabled":false,"rescan_interval_minutes":45}`)))
	if putRes.Code != http.StatusOK {
		t.Fatalf("PUT /settings status = %d body = %s", putRes.Code, putRes.Body.String())
	}
	var updated CatalogSettings
	if err := json.Unmarshal(putRes.Body.Bytes(), &updated); err != nil || updated.RescanEnabled || updated.RescanIntervalMinutes != 45 {
		t.Fatalf("unexpected updated: %+v", updated)
	}

	// 3. PUT rejects invalid interval
	badRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(badRes, httptest.NewRequest("PUT", "/settings", strings.NewReader(`{"rescan_enabled":true,"rescan_interval_minutes":0}`)))
	if badRes.Code != http.StatusBadRequest {
		t.Fatalf("PUT /settings with 0 minutes status = %d, want 400", badRes.Code)
	}
}
