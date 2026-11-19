package images

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type deleteRepository struct {
	httpRepository
	deleted []Image
}

func (r *deleteRepository) DeleteImage(_ context.Context, id int64, path string) error {
	delete(r.images[id], path)
	r.deleted = append(r.deleted, Image{StorageID: id, Path: path})
	return nil
}

func TestDeleteISOHTTPRemovesFileAndCatalogRecord(t *testing.T) {
	for _, kind := range []string{"local", "mounted", "smb", "nfs"} {
		t.Run(kind, func(t *testing.T) {
			root := realTempDir(t)
			if err := os.Mkdir(filepath.Join(root, "Windows"), 0755); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(root, "Windows", "win.iso")
			if err := os.WriteFile(target, []byte("ISO"), 0600); err != nil {
				t.Fatal(err)
			}
			key := "0123456789abcdef0123456789abcdef"
			storage := Storage{ID: 3, Name: "Storage", Kind: kind, Root: root, State: "ready"}
			if kind == "smb" {
				storage.Key = key
				storage.Server = "192.168.10.10"
				storage.Share = "Downloads"
			}
			if kind == "nfs" {
				storage.Key = key
				storage.Server = "192.168.10.10"
				storage.ExportPath = "/export/iso"
				storage.NFSVersion = "4.1"
				storage.UploadsEnabled = true
			}
			repo := &deleteRepository{httpRepository: httpRepository{memoryRepository: memoryRepository{
				storages: map[int64]Storage{3: storage},
				images:   map[int64]map[string]Image{3: {"Windows/win.iso": {StorageID: 3, Path: "Windows/win.iso", Present: true}}},
				state:    map[int64]string{3: "ready"},
			}}}
			server := NewServer(repo)
			server.uploadRoot = func(got string) string {
				if got != key {
					t.Fatalf("upload key = %q", got)
				}
				return root
			}
			server.nfsUploadRoot = func(got string) string {
				if got != key {
					t.Fatalf("nfs upload key = %q", got)
				}
				return root
			}
			server.mountIdentity = func(string) (string, bool) { return "mount-1", true }
			req := httptest.NewRequest(http.MethodDelete, "/storages/3/images", strings.NewReader(`{"path":"Windows/win.iso"}`))
			res := httptest.NewRecorder()
			server.Handler().ServeHTTP(res, req)
			if res.Code != http.StatusNoContent {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
			if _, err := os.Lstat(target); !os.IsNotExist(err) {
				t.Fatalf("source ISO remains: %v", err)
			}
			if _, err := os.Lstat(filepath.Join(root, "Windows")); !os.IsNotExist(err) {
				t.Fatalf("empty parent directory remains: %v", err)
			}
			if len(repo.deleted) != 1 || repo.deleted[0].StorageID != 3 || repo.deleted[0].Path != "Windows/win.iso" {
				t.Fatalf("deleted catalog records = %+v", repo.deleted)
			}
		})
	}
}

func TestDeleteISONFSRejectsWhenUploadsDisabled(t *testing.T) {
	root := realTempDir(t)
	key := "0123456789abcdef0123456789abcdef"
	storage := Storage{
		ID:             3,
		Name:           "NFS",
		Kind:           "nfs",
		Root:           root,
		Key:            key,
		Server:         "192.168.10.10",
		ExportPath:     "/export/iso",
		NFSVersion:     "4.1",
		State:          "ready",
		UploadsEnabled: false,
	}
	repo := &deleteRepository{httpRepository: httpRepository{memoryRepository: memoryRepository{
		storages: map[int64]Storage{3: storage},
		images:   map[int64]map[string]Image{3: {"win.iso": {StorageID: 3, Path: "win.iso", Present: true}}},
		state:    map[int64]string{3: "ready"},
	}}}
	server := NewServer(repo)
	server.mountIdentity = func(string) (string, bool) { return "mount-1", true }
	req := httptest.NewRequest(http.MethodDelete, "/storages/3/images", strings.NewReader(`{"path":"win.iso"}`))
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status=%d, want 403 Forbidden", res.Code)
	}
}

func TestDeleteISOHTTPRequiresCataloguedPresentImage(t *testing.T) {
	root := realTempDir(t)
	target := filepath.Join(root, "win.iso")
	if err := os.WriteFile(target, []byte("ISO"), 0600); err != nil {
		t.Fatal(err)
	}
	repo := &deleteRepository{httpRepository: httpRepository{memoryRepository: memoryRepository{
		storages: map[int64]Storage{3: {ID: 3, Kind: "local", Root: root, State: "ready"}},
		images:   map[int64]map[string]Image{3: {}},
		state:    map[int64]string{3: "ready"},
	}}}
	res := httptest.NewRecorder()
	NewServer(repo).Handler().ServeHTTP(res, httptest.NewRequest(http.MethodDelete, "/storages/3/images", strings.NewReader(`{"path":"win.iso"}`)))
	if res.Code != http.StatusNotFound || len(repo.deleted) != 0 {
		t.Fatalf("status=%d deleted=%+v", res.Code, repo.deleted)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("uncatalogued file changed: %v", err)
	}
}

func TestDeleteISOAbsentImageRemovesCatalogRecordWithoutFileOnDisk(t *testing.T) {
	root := realTempDir(t)
	repo := &deleteRepository{httpRepository: httpRepository{memoryRepository: memoryRepository{
		storages: map[int64]Storage{3: {ID: 3, Kind: "local", Root: root, State: "ready"}},
		images: map[int64]map[string]Image{
			3: {
				"absent.iso": {StorageID: 3, Path: "absent.iso", Present: false},
			},
		},
		state: map[int64]string{3: "ready"},
	}}}
	server := NewServer(repo)
	req := httptest.NewRequest(http.MethodDelete, "/storages/3/images", strings.NewReader(`{"path":"absent.iso"}`))
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if len(repo.deleted) != 1 || repo.deleted[0].Path != "absent.iso" {
		t.Fatalf("expected absent.iso to be deleted from repo, got: %+v", repo.deleted)
	}
}

func TestDeleteISORejectsTraversalAndSymlinks(t *testing.T) {
	root := realTempDir(t)
	outside := filepath.Join(realTempDir(t), "outside.iso")
	if err := os.WriteFile(outside, []byte("ISO"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.iso")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../outside.iso", "linked.iso", "notes.txt"} {
		if err := DeleteISO(root, path); err == nil {
			t.Fatalf("unsafe path accepted: %q", path)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside file changed: %v", err)
	}
}
