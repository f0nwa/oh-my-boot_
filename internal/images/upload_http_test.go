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

func TestImagesUploadIntoWritableLocalStorage(t *testing.T) {
	root := realTempDir(t)
	repo := &uploadRepository{httpRepository: httpRepository{memoryRepository: memoryRepository{
		storages: map[int64]Storage{3: {ID: 3, Name: "Local", Kind: "local", Root: root, State: "ready"}},
		images:   map[int64]map[string]Image{},
		state:    map[int64]string{3: "ready"},
	}}}
	server := NewServer(repo)
	// A local upload never uses the SMB upload mount.
	server.uploadRoot = func(string) string { t.Fatal("SMB upload root used for a local storage"); return "" }
	server.mountIdentity = func(string) (string, bool) { return "", false }
	writable := false
	server.writable = func(string) bool { return writable }
	req := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/storages/3/upload?name=win.iso", strings.NewReader("iso-data"))
		r.ContentLength = 8
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, r)
		return res
	}
	if res := req(); res.Code != http.StatusForbidden || len(repo.recorded) != 0 {
		t.Fatalf("read-only local storage: status=%d", res.Code)
	}
	writable = true
	if res := req(); res.Code != http.StatusCreated || len(repo.recorded) != 1 || repo.recorded[0].Path != "win.iso" || repo.recorded[0].StorageID != 3 {
		t.Fatalf("local upload status=%d body=%s recorded=%+v", res.Code, res.Body.String(), repo.recorded)
	}
	if _, err := os.Stat(filepath.Join(root, "win.iso")); err != nil {
		t.Fatal(err)
	}
}

func TestImagesCatalogReportsSpaceAndUploads(t *testing.T) {
	root := realTempDir(t)
	repo := &uploadRepository{httpRepository: httpRepository{memoryRepository: memoryRepository{
		storages: map[int64]Storage{
			1: {ID: 1, Name: "NAS", Kind: "smb", Root: root, State: "ready"},
			2: {ID: 2, Name: "Local", Kind: "local", Root: root, State: "ready"},
			3: {ID: 3, Name: "Off", Kind: "local", Root: root, State: "unavailable"},
			4: {ID: 4, Name: "NFS-RO", Kind: "nfs", Root: root, State: "ready", UploadsEnabled: false},
			5: {ID: 5, Name: "NFS-RW", Kind: "nfs", Root: root, State: "ready", UploadsEnabled: true},
		},
		images: map[int64]map[string]Image{},
		state:  map[int64]string{1: "ready", 2: "ready", 3: "unavailable", 4: "ready", 5: "ready"},
	}}}
	server := NewServer(repo)
	server.space = func(string) (int64, int64, bool) { return 100 << 30, 40 << 30, true }
	server.writable = func(string) bool { return false }
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/catalog", nil))
	var catalog Catalog
	if err := json.Unmarshal(res.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	got := map[string]Storage{}
	for _, s := range catalog.Storages {
		got[s.Name] = s
	}
	if s := got["NAS"]; s.Total != 100<<30 || s.Free != 40<<30 || !s.Uploads {
		t.Fatalf("NAS = %+v", s)
	}
	if s := got["Local"]; s.Total != 100<<30 || s.Uploads {
		t.Fatalf("read-only local = %+v", s)
	}
	if s := got["Off"]; s.Total != 0 || s.Uploads {
		t.Fatalf("unavailable = %+v", s)
	}
	if s := got["NFS-RO"]; s.Total != 100<<30 || s.Uploads {
		t.Fatalf("NFS-RO = %+v", s)
	}
	if s := got["NFS-RW"]; s.Total != 100<<30 || !s.Uploads {
		t.Fatalf("NFS-RW = %+v", s)
	}
}

func TestSpaceAndWritableOnARealDirectory(t *testing.T) {
	dir := t.TempDir()
	total, free, ok := Space(dir)
	if !ok || total <= 0 || free < 0 || free > total {
		t.Fatalf("space = %d %d %v", total, free, ok)
	}
	if !Writable(dir) {
		t.Fatal("temp dir not writable")
	}
	if _, _, ok := Space(filepath.Join(dir, "missing")); ok {
		t.Fatal("space of a missing path")
	}
}

func TestImagesUploadIntoNFSStorage(t *testing.T) {
	root := realTempDir(t)
	uploadDir := realTempDir(t)
	key := "0123456789abcdef0123456789abcdef"
	nfsStorage := Storage{
		ID:             8,
		Name:           "NFS",
		Kind:           "nfs",
		Root:           root,
		Key:            key,
		Server:         "192.168.10.10",
		ExportPath:     "/export/iso",
		NFSVersion:     "4.1",
		State:          "ready",
		UploadsEnabled: true,
	}
	repo := &uploadRepository{httpRepository: httpRepository{memoryRepository: memoryRepository{
		storages: map[int64]Storage{8: nfsStorage},
		images:   map[int64]map[string]Image{},
		state:    map[int64]string{8: "ready"},
	}}}
	server := NewServer(repo)
	server.uploadRoot = func(string) string { t.Fatal("SMB upload root used for NFS storage"); return "" }
	server.nfsUploadRoot = func(got string) string {
		if got != key {
			t.Fatalf("nfs upload key = %q, want %q", got, key)
		}
		return uploadDir
	}
	mounted := true
	server.mountIdentity = func(path string) (string, bool) {
		if !mounted {
			return "", false
		}
		return "mount-nfs", true
	}

	req := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/storages/8/upload?name=win.iso", strings.NewReader("iso-data"))
		r.ContentLength = 8
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, r)
		return res
	}

	// 1. Success when ready, uploads_enabled, mounted
	res := req()
	if res.Code != http.StatusCreated || len(repo.recorded) != 1 || repo.recorded[0].Path != "win.iso" || repo.recorded[0].StorageID != 8 {
		t.Fatalf("upload status=%d body=%s recorded=%+v", res.Code, res.Body.String(), repo.recorded)
	}
	if _, err := os.Stat(filepath.Join(uploadDir, "win.iso")); err != nil {
		t.Fatalf("uploaded file missing: %v", err)
	}

	// 2. Reject when state != ready
	s := repo.storages[8]
	s.State = "unavailable"
	repo.storages[8] = s
	if res := req(); res.Code != http.StatusConflict {
		t.Fatalf("unavailable NFS upload status=%d, want 409", res.Code)
	}

	// 3. Reject when !UploadsEnabled
	s.State = "ready"
	s.UploadsEnabled = false
	repo.storages[8] = s
	if res := req(); res.Code != http.StatusForbidden {
		t.Fatalf("read-only NFS upload status=%d, want 403", res.Code)
	}

	// 4. Reject when mountIdentity fails
	s.UploadsEnabled = true
	repo.storages[8] = s
	mounted = false
	if res := req(); res.Code != http.StatusServiceUnavailable {
		t.Fatalf("unmounted NFS upload status=%d, want 503", res.Code)
	}
}
