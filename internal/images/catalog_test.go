package images

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScanFindsISOsAndSkipsSymlinks(t *testing.T) {
	root := realTempDir(t)
	other := realTempDir(t)
	if err := os.Mkdir(filepath.Join(root, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		filepath.Join(root, "nested", "Ubuntu.ISO"): "iso-data",
		filepath.Join(root, "notes.txt"):            "notes",
		filepath.Join(root, ".omb-upload-123.part"): "partial-upload",
		filepath.Join(other, "secret.iso"):          "outside",
	} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(other, "secret.iso"), filepath.Join(root, "linked.iso")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(root, "linked-dir")); err != nil {
		t.Fatal(err)
	}

	got, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Images) != 1 || got.Images[0].Path != "nested/Ubuntu.ISO" || got.Images[0].Size != 8 || got.OtherBytes != 5 {
		t.Fatalf("unexpected catalog: %+v", got)
	}
}

func TestScanSkipsMacOSMetadataAndRecycleDirs(t *testing.T) {
	root := realTempDir(t)
	for _, dir := range []string{"#recycle", ".trash", ".hidden", "valid"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for path, content := range map[string]string{
		filepath.Join(root, "valid", "real.iso"):                "real-iso",
		filepath.Join(root, "valid", "._real.iso"):              "appledouble",
		filepath.Join(root, "valid", ".DS_Store"):               "ds-store",
		filepath.Join(root, "#recycle", "deleted.iso"):          "recycle-iso",
		filepath.Join(root, ".trash", "trashed.iso"):            "trash-iso",
		filepath.Join(root, ".hidden", "hidden.iso"):           "hidden-iso",
	} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Images) != 1 || got.Images[0].Path != "valid/real.iso" {
		t.Fatalf("expected only valid/real.iso, got: %+v", got.Images)
	}
	if got.OtherBytes != 0 {
		t.Fatalf("expected 0 otherBytes, got: %d", got.OtherBytes)
	}
}

func TestScanRejectsSymlinkedRoot(t *testing.T) {
	realRoot := realTempDir(t)
	link := filepath.Join(realTempDir(t), "linked-root")
	if err := os.Symlink(realRoot, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Scan(link); err == nil {
		t.Fatal("expected symlinked root to be rejected")
	}
	child := filepath.Join(realRoot, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Scan(filepath.Join(link, "child")); err == nil {
		t.Fatal("expected symlinked ancestor to be rejected")
	}
}

func TestScanIndexesISOWIMAndESD(t *testing.T) {
	root := realTempDir(t)
	files := map[string]string{
		"win11.iso":   "iso-bytes-1",
		"boot.wim":    "wim-bytes-22",
		"install.esd": "esd-bytes-333",
		"readme.txt":  "readme",
		"._boot.wim":  "appledouble",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Images) != 3 {
		t.Fatalf("expected 3 indexed images, got %d: %+v", len(got.Images), got.Images)
	}
	wantKinds := map[string]string{
		"boot.wim":    "wim",
		"install.esd": "esd",
		"win11.iso":   "iso",
	}
	var isoBytes int64
	for _, img := range got.Images {
		wantKind, ok := wantKinds[img.Path]
		if !ok {
			t.Fatalf("unexpected image path %q", img.Path)
		}
		if img.Kind != wantKind {
			t.Errorf("image %q kind = %q, want %q", img.Path, img.Kind, wantKind)
		}
		if !img.Present {
			t.Errorf("image %q should be present", img.Path)
		}
		isoBytes += img.Size
	}
	wantISOBytes := int64(len(files["win11.iso"]) + len(files["boot.wim"]) + len(files["install.esd"]))
	if isoBytes != wantISOBytes {
		t.Errorf("indexed bytes = %d, want %d", isoBytes, wantISOBytes)
	}
	if got.OtherBytes != int64(len(files["readme.txt"])) {
		t.Errorf("other_bytes = %d, want %d", got.OtherBytes, len(files["readme.txt"]))
	}
}

func TestCatalogReturnsImageKind(t *testing.T) {
	root := realTempDir(t)
	now := time.Now().UTC().Truncate(time.Second)
	repo := &httpRepository{memoryRepository: memoryRepository{
		storages: map[int64]Storage{
			1: {ID: 1, Name: "Local", Kind: "local", Root: root, State: "ready"},
		},
		images: map[int64]map[string]Image{
			1: {
				"win11.iso":   {StorageID: 1, Path: "win11.iso", Kind: "iso", Size: 100, Modified: now, Present: true},
				"boot.wim":    {StorageID: 1, Path: "boot.wim", Kind: "wim", Size: 200, Modified: now, Present: true},
				"install.esd": {StorageID: 1, Path: "install.esd", Kind: "esd", Size: 300, Modified: now, Present: true},
			},
		},
		state: map[int64]string{1: "ready"},
	}}
	server := NewServer(repo)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/catalog", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("GET /catalog status=%d body=%s", res.Code, res.Body.String())
	}
	var payload struct {
		Images []map[string]any `json:"images"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Images) != 3 {
		t.Fatalf("expected 3 images, got %d", len(payload.Images))
	}
	gotKinds := map[string]string{}
	for _, raw := range payload.Images {
		path, _ := raw["path"].(string)
		kind, ok := raw["kind"].(string)
		if !ok {
			t.Fatalf("image %q missing string kind field in JSON: %+v", path, raw)
		}
		gotKinds[path] = kind
	}
	for path, want := range map[string]string{
		"win11.iso":   "iso",
		"boot.wim":    "wim",
		"install.esd": "esd",
	} {
		if gotKinds[path] != want {
			t.Errorf("JSON kind for %q = %q, want %q", path, gotKinds[path], want)
		}
	}
}
