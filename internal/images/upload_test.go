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

func TestReceiveISOIntoExistingFolderAndRejectsReplacement(t *testing.T) {
	root := realTempDir(t)
	if err := os.MkdirAll(filepath.Join(root, "Windows", "Test"), 0755); err != nil {
		t.Fatal(err)
	}
	got, err := ReceiveISO(context.Background(), root, "Windows/Test", "installer.iso", strings.NewReader("iso-data"), int64(len("iso-data")))
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != "Windows/Test/installer.iso" || got.Size != 8 || got.SHA256 != "d4fdbb796c321761669fd0ca447caa175f74addbcff636d98016ab8f4f36ab91" {
		t.Fatalf("unexpected result: %+v", got)
	}
	content, err := os.ReadFile(filepath.Join(root, "Windows", "Test", "installer.iso"))
	if err != nil || string(content) != "iso-data" {
		t.Fatalf("uploaded content=%q err=%v", content, err)
	}
	if _, err := ReceiveISO(context.Background(), root, "Windows/Test", "installer.iso", strings.NewReader("replacement"), 11); err == nil {
		t.Fatal("existing ISO was replaced")
	}
	content, err = os.ReadFile(filepath.Join(root, "Windows", "Test", "installer.iso"))
	if err != nil || string(content) != "iso-data" {
		t.Fatalf("existing content changed: %q err=%v", content, err)
	}
}

func TestReceiveISORejectsTraversalAndSymlinkedFolder(t *testing.T) {
	root := realTempDir(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	for _, folder := range []string{"../outside", "link", "missing", "/absolute", `back\\slash`} {
		if _, err := ReceiveISO(context.Background(), root, folder, "test.iso", strings.NewReader("a"), 1); err == nil {
			t.Errorf("accepted folder %q", folder)
		}
	}
	for _, name := range []string{"../test.iso", ".hidden.iso", "bad.txt", "bad\\name.iso", "trail .iso ", ""} {
		if _, err := ReceiveISO(context.Background(), root, "", name, strings.NewReader("a"), 1); err == nil {
			t.Errorf("accepted name %q", name)
		}
	}
}

func TestReceiveISODeletesIncompleteFile(t *testing.T) {
	root := realTempDir(t)
	if _, err := ReceiveISO(context.Background(), root, "", "short.iso", strings.NewReader("short"), 10); err == nil {
		t.Fatal("accepted short stream")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("partial upload remains: %v err=%v", entries, err)
	}
}

func TestListFoldersSkipsSymlinksAndIncludesEmptyFolders(t *testing.T) {
	root := realTempDir(t)
	if err := os.MkdirAll(filepath.Join(root, "Windows", "Empty"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	folders, err := ListFolders(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(folders, ",") != ",Windows,Windows/Empty" {
		t.Fatalf("folders=%q", folders)
	}
}

func TestReceiveISOSetsReadableMode(t *testing.T) {
	root := realTempDir(t)
	got, err := ReceiveISO(context.Background(), root, "", "readable.iso", strings.NewReader("iso-content"), int64(len("iso-content")))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, got.Path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0644 {
		t.Fatalf("expected perm 0644, got %#o", perm)
	}
}

func TestUploadAcceptsWIMAndESDWhenExpectedKindMatches(t *testing.T) {
	root := realTempDir(t)
	repo := &uploadRepository{httpRepository: httpRepository{memoryRepository: memoryRepository{
		storages: map[int64]Storage{3: {ID: 3, Name: "Local", Kind: "local", Root: root, State: "ready"}},
		images:   map[int64]map[string]Image{},
		state:    map[int64]string{3: "ready"},
	}}}
	server := NewServer(repo)
	server.writable = func(string) bool { return true }

	upload := func(query, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/storages/3/upload?"+query, strings.NewReader(body))
		req.ContentLength = int64(len(body))
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		return res
	}

	// expected_kind=wim accepts .wim and rejects .iso
	if res := upload("name=boot.wim&expected_kind=wim", "wim-data"); res.Code != http.StatusCreated {
		t.Fatalf("expected_kind=wim with boot.wim: status=%d body=%s", res.Code, res.Body.String())
	}
	if res := upload("name=win.iso&expected_kind=wim", "iso-data"); res.Code != http.StatusBadRequest {
		t.Fatalf("expected_kind=wim with win.iso: status=%d, want 400", res.Code)
	}

	// expected_kind=esd accepts .esd and rejects .wim
	if res := upload("name=install.esd&expected_kind=esd", "esd-data"); res.Code != http.StatusCreated {
		t.Fatalf("expected_kind=esd with install.esd: status=%d body=%s", res.Code, res.Body.String())
	}
	if res := upload("name=other.wim&expected_kind=esd", "wim-data"); res.Code != http.StatusBadRequest {
		t.Fatalf("expected_kind=esd with other.wim: status=%d, want 400", res.Code)
	}

	// expected_kind=wim_or_esd accepts both .wim and .esd, rejects .iso
	if res := upload("name=install.wim&expected_kind=wim_or_esd", "wim-install"); res.Code != http.StatusCreated {
		t.Fatalf("expected_kind=wim_or_esd with install.wim: status=%d body=%s", res.Code, res.Body.String())
	}
	if res := upload("name=install2.esd&expected_kind=wim_or_esd", "esd-install"); res.Code != http.StatusCreated {
		t.Fatalf("expected_kind=wim_or_esd with install2.esd: status=%d body=%s", res.Code, res.Body.String())
	}
	if res := upload("name=win2.iso&expected_kind=wim_or_esd", "iso-data"); res.Code != http.StatusBadRequest {
		t.Fatalf("expected_kind=wim_or_esd with win2.iso: status=%d, want 400", res.Code)
	}

	// empty expected_kind defaults to iso: accepts .iso, rejects .wim
	if res := upload("name=win.iso", "iso-data"); res.Code != http.StatusCreated {
		t.Fatalf("default expected_kind with win.iso: status=%d body=%s", res.Code, res.Body.String())
	}
	if res := upload("name=extra.wim", "wim-data"); res.Code != http.StatusBadRequest {
		t.Fatalf("default expected_kind with extra.wim: status=%d, want 400", res.Code)
	}

	// invalid expected_kind is rejected
	if res := upload("name=win3.iso&expected_kind=txt", "iso-data"); res.Code != http.StatusBadRequest {
		t.Fatalf("invalid expected_kind=txt: status=%d, want 400", res.Code)
	}

	// Verify recorded images carry expected Kind values
	gotKinds := map[string]string{}
	for _, img := range repo.recorded {
		gotKinds[img.Path] = img.Kind
	}
	for path, want := range map[string]string{
		"boot.wim":     "wim",
		"install.esd":  "esd",
		"install.wim":  "wim",
		"install2.esd": "esd",
		"win.iso":      "iso",
	} {
		if gotKinds[path] != want {
			t.Errorf("recorded kind for %q = %q, want %q", path, gotKinds[path], want)
		}
	}
}
