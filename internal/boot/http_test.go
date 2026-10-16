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

func TestWinPEStartupAssetIsOnlyServedToPXESubnet(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "windows11"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "windows11", "pxe-setup.cmd"), []byte("limited-secret"), 0644); err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(root, []byte("#!ipxe\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		remote string
		status int
	}{
		{"10.30.0.7:12345", http.StatusOK},
		{"192.168.10.55:12345", http.StatusNotFound},
		{"[::1]:12345", http.StatusNotFound},
	} {
		req := httptest.NewRequest(http.MethodGet, "/assets/windows11/pxe-setup.cmd", nil)
		req.RemoteAddr = tc.remote
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != tc.status {
			t.Errorf("%s: got status %d, want %d", tc.remote, res.Code, tc.status)
		}
		if tc.status != http.StatusOK && strings.Contains(res.Body.String(), "limited-secret") {
			t.Fatalf("secret reached %s", tc.remote)
		}
	}
	for _, path := range []string{
		"/assets/windows11//pxe-setup.cmd",
		"/assets/windows11/../windows11/pxe-setup.cmd",
		"/assets/windows11%2fpxe-setup.cmd",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "192.168.10.55:12345"
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code == http.StatusOK || strings.Contains(res.Body.String(), "limited-secret") {
			t.Fatalf("alternate path %q exposed startup asset", path)
		}
	}
}

func TestUbuntuISOEndpointServesOnlySelectedFile(t *testing.T) {
	assets := t.TempDir()
	storage := t.TempDir()
	iso := filepath.Join(storage, "ubuntu.iso")
	if err := os.WriteFile(iso, []byte("abcdefgh"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := NewPublisher(filepath.Join(t.TempDir(), "menu.ipxe"), []byte("#!ipxe\n"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewDynamicHandlerWithISO(assets, p, nil, iso)
	if err != nil {
		t.Fatal(err)
	}
	rangeResult := httptest.NewRecorder()
	rangeRequest := httptest.NewRequest(http.MethodGet, "/images/ubuntu-desktop.iso", nil)
	rangeRequest.Header.Set("Range", "bytes=2-5")
	h.ServeHTTP(rangeResult, rangeRequest)
	if rangeResult.Code != http.StatusPartialContent || rangeResult.Body.String() != "cdef" {
		t.Fatalf("unexpected ISO range: %d %q", rangeResult.Code, rangeResult.Body.String())
	}
	for _, path := range []string{"/images/other.iso", "/images/../secret"} {
		res := httptest.NewRecorder()
		h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		if res.Code != http.StatusNotFound && res.Code != http.StatusTemporaryRedirect {
			t.Fatalf("unexpected access to %s: %d", path, res.Code)
		}
		if strings.Contains(res.Body.String(), "abcdefgh") {
			t.Fatalf("other path exposed ISO: %s", path)
		}
	}
	if err := os.Remove(iso); err != nil {
		t.Fatal(err)
	}
	missing := httptest.NewRecorder()
	h.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/images/ubuntu-desktop.iso", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing ISO returned %d", missing.Code)
	}
}

func TestUbuntuISOEndpointRejectsSymlink(t *testing.T) {
	assets := t.TempDir()
	storage := t.TempDir()
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(storage, "ubuntu.iso")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	p, err := NewPublisher(filepath.Join(t.TempDir(), "menu.ipxe"), []byte("#!ipxe\n"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewDynamicHandlerWithISO(assets, p, nil, link)
	if err != nil {
		t.Fatal(err)
	}
	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/images/ubuntu-desktop.iso", nil))
	if res.Code != http.StatusNotFound || strings.Contains(res.Body.String(), "private") {
		t.Fatalf("symlink exposed: %d %q", res.Code, res.Body.String())
	}
}
