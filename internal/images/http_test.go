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
func (r *httpRepository) SaveScan(ctx context.Context, id int64, found []Image) error {
	err := r.memoryRepository.SaveScan(ctx, id, found)
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
