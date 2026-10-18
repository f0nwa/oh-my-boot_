package images

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type uploadRepository struct {
	httpRepository
	recorded []Image
}

type pausedUploadReader struct {
	started chan struct{}
	release chan struct{}
	read    bool
}

func (r *pausedUploadReader) Read(p []byte) (int, error) {
	if r.read {
		return 0, io.EOF
	}
	r.read = true
	close(r.started)
	<-r.release
	p[0] = 'x'
	return 1, nil
}

func TestImagesUploadRejectsConcurrentUploadToSameStorage(t *testing.T) {
	root := realTempDir(t)
	key := "0123456789abcdef0123456789abcdef"
	repo := &uploadRepository{httpRepository: httpRepository{memoryRepository: memoryRepository{
		storages: map[int64]Storage{7: {ID: 7, Kind: "smb", Root: root, Key: key, Server: "192.168.10.10", Share: "Downloads", State: "ready"}},
		images:   map[int64]map[string]Image{}, state: map[int64]string{7: "ready"},
	}}}
	server := NewServer(repo)
	server.uploadRoot = func(string) string { return root }
	server.mountIdentity = func(string) (string, bool) { return "mount-1", true }
	reader := &pausedUploadReader{started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		defer close(done)
		req := httptest.NewRequest(http.MethodPost, "/smb-storages/7/upload?name=first.iso", reader)
		req.ContentLength = 1
		server.Handler().ServeHTTP(httptest.NewRecorder(), req)
	}()
	<-reader.started
	second := httptest.NewRequest(http.MethodPost, "/smb-storages/7/upload?name=second.iso", strings.NewReader("y"))
	second.ContentLength = 1
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, second)
	close(reader.release)
	<-done
	if res.Code != http.StatusConflict {
		t.Fatalf("second upload status=%d body=%s", res.Code, res.Body.String())
	}
}

func (r *uploadRepository) RecordUpload(_ context.Context, image Image) error {
	r.recorded = append(r.recorded, image)
	return nil
}

func TestImagesUploadHTTPRecordsCompletedFileInSelectedFolder(t *testing.T) {
	root := realTempDir(t)
	if err := os.MkdirAll(filepath.Join(root, "Windows"), 0755); err != nil {
		t.Fatal(err)
	}
	key := "0123456789abcdef0123456789abcdef"
	repo := &uploadRepository{httpRepository: httpRepository{memoryRepository: memoryRepository{
		storages: map[int64]Storage{7: {ID: 7, Name: "NAS", Kind: "smb", Root: root, Key: key, Server: "192.168.10.10", Share: "Downloads", Subpath: "iso", State: "ready"}},
		images:   map[int64]map[string]Image{},
		state:    map[int64]string{7: "ready"},
	}}}
	server := NewServer(repo)
	server.uploadRoot = func(string) string { return root }
	server.mountIdentity = func(string) (string, bool) { return "mount-1", true }
	folders := httptest.NewRecorder()
	server.Handler().ServeHTTP(folders, httptest.NewRequest(http.MethodGet, "/smb-storages/7/folders", nil))
	var found []string
	if folders.Code != http.StatusOK || json.Unmarshal(folders.Body.Bytes(), &found) != nil || strings.Join(found, ",") != ",Windows" {
		t.Fatalf("folders status=%d body=%s", folders.Code, folders.Body.String())
	}
	req := httptest.NewRequest(http.MethodPost, "/smb-storages/7/upload?name=win.iso&folder=Windows", strings.NewReader("iso-data"))
	req.ContentLength = 8
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusCreated || len(repo.recorded) != 1 || repo.recorded[0].Path != "Windows/win.iso" || repo.recorded[0].Size != 8 || repo.recorded[0].Modified.Before(time.Now().Add(-time.Minute)) {
		t.Fatalf("upload status=%d body=%s recorded=%+v", res.Code, res.Body.String(), repo.recorded)
	}
	if _, err := os.Stat(filepath.Join(root, "Windows", "win.iso")); err != nil {
		t.Fatal(err)
	}
}
