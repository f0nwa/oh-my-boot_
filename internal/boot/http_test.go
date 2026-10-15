package boot

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandlerServesMenuAndAssetRange(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ipxe.efi"), []byte("abcdefgh"), 0o644); err != nil {
		t.Fatal(err)
	}
	var events []Event
	h, err := NewHandler(dir, []byte("#!ipxe\necho ready\n"), func(event Event) { events = append(events, event) })
	if err != nil {
		t.Fatal(err)
	}

	menu := httptest.NewRecorder()
	h.ServeHTTP(menu, httptest.NewRequest(http.MethodGet, "/boot/menu.ipxe", nil))
	if menu.Code != http.StatusOK || menu.Body.String() != "#!ipxe\necho ready\n" {
		t.Fatalf("unexpected menu response: %d %q", menu.Code, menu.Body.String())
	}
	if ct := menu.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Fatalf("wrong menu content type: %q", ct)
	}

	asset := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/ipxe.efi", nil)
	req.Header.Set("Range", "bytes=0-3")
	h.ServeHTTP(asset, req)
	if asset.Code != http.StatusPartialContent || asset.Body.String() != "abcd" {
		t.Fatalf("unexpected range response: %d %q", asset.Code, asset.Body.String())
	}
	if len(events) != 2 || events[0].Kind != "menu" || events[1].Kind != "asset" {
		t.Fatalf("unexpected events: %+v", events)
	}
}

func TestHandlerKeepsAssetsInsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("private"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(root, []byte("#!ipxe\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/assets/../secret", "/assets/linked"} {
		res := httptest.NewRecorder()
		h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		if strings.Contains(res.Body.String(), "private") {
			t.Fatalf("asset route exposed file outside root via %q", path)
		}
	}
}
