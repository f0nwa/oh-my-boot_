package boot

import (
	"fmt"
	"io"
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
	if err := os.MkdirAll(filepath.Join(root, "windows-profiles", "1"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "windows-profiles", "1", "omb-setup.cmd"), []byte("limited-secret"), 0644); err != nil {
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
		req := httptest.NewRequest(http.MethodGet, "/assets/windows-profiles/1/omb-setup.cmd", nil)
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
		"/assets/windows-profiles/1//omb-setup.cmd",
		"/assets/windows-profiles/2/../1/omb-setup.cmd",
		"/assets/windows-profiles%2f1%2fomb-setup.cmd",
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

func TestProfileWinPEStartupAssetIsOnlyServedToPXESubnet(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "windows-profiles", "17")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "omb-setup.cmd"), []byte("limited-secret"), 0644); err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(root, []byte("#!ipxe\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		remote string
		want   int
	}{{"10.30.0.7:12345", http.StatusOK}, {"192.168.10.55:12345", http.StatusNotFound}} {
		req := httptest.NewRequest(http.MethodGet, "/assets/windows-profiles/17/omb-setup.cmd", nil)
		req.RemoteAddr = tc.remote
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != tc.want {
			t.Fatalf("remote=%s status=%d want=%d", tc.remote, res.Code, tc.want)
		}
		if tc.want != http.StatusOK && strings.Contains(res.Body.String(), "limited-secret") {
			t.Fatalf("startup script leaked to %s", tc.remote)
		}
	}
}

func TestWindowsAnswerAssetsAreLimitedToPXESubnet(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "windows-answers", "4")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"omb-answer.xml", "omb-setup.cmd"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("sensitive"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	h, err := NewHandler(root, []byte("#!ipxe\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"omb-answer.xml", "omb-setup.cmd"} {
		for _, tc := range []struct {
			addr string
			want int
		}{{"10.30.0.7:1234", http.StatusOK}, {"192.168.10.55:1234", http.StatusNotFound}} {
			req := httptest.NewRequest(http.MethodGet, "/assets/windows-answers/4/"+name, nil)
			req.RemoteAddr = tc.addr
			res := httptest.NewRecorder()
			h.ServeHTTP(res, req)
			if res.Code != tc.want || (tc.want == http.StatusOK && res.Header().Get("Cache-Control") != "no-store") {
				t.Fatalf("%s %s: status=%d cache=%q", name, tc.addr, res.Code, res.Header().Get("Cache-Control"))
			}
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

func TestHandlerRecordsInstallationChoice(t *testing.T) {
	var events []Event
	h, err := NewHandler(t.TempDir(), []byte("#!ipxe\n"), func(e Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"", "0", "-1", "abc", "1.5", "+1", "01", "9223372036854775808", "1&id=2"} {
		res := httptest.NewRecorder()
		h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/boot/choice?id="+raw, nil))
		if res.Code != http.StatusBadRequest {
			t.Errorf("id=%q: status=%d", raw, res.Code)
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodHead} {
		res := httptest.NewRecorder()
		h.ServeHTTP(res, httptest.NewRequest(method, "/boot/choice?id=73", nil))
		if res.Code != http.StatusMethodNotAllowed || res.Header().Get("Allow") != "GET" {
			t.Errorf("method=%s: status=%d allow=%q", method, res.Code, res.Header().Get("Allow"))
		}
	}
	if len(events) != 0 {
		t.Fatalf("invalid requests recorded: %+v", events)
	}
	req := httptest.NewRequest(http.MethodGet, "/boot/choice?id=73&ignored=value", nil)
	req.RemoteAddr = "10.30.0.19:1234"
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.String() != "#!ipxe\n" || res.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response=%d %q headers=%v", res.Code, res.Body.String(), res.Header())
	}
	if len(events) != 1 || events[0].Kind != "choice" || events[0].Path != "/boot/choice?id=73" || events[0].RemoteAddr != req.RemoteAddr || events[0].At.IsZero() || events[0].Status != 0 || events[0].Size != 0 || events[0].Bytes != 0 {
		t.Fatalf("events=%+v", events)
	}
}

func TestGenericAssetStableAliasesRemainAvailable(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "public"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "public", "kernel"), []byte("public kernel"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("public/kernel", filepath.Join(root, "kernel-alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("public", filepath.Join(root, "directory-alias")); err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(root, []byte("#!ipxe\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/assets/kernel-alias", "/assets/directory-alias/kernel"} {
		req := httptest.NewRequest("GET", path, nil)
		req.RemoteAddr = "192.168.10.7:1234"
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != 200 || res.Body.String() != "public kernel" {
			t.Fatalf("stable alias %s: %d %q", path, res.Code, res.Body.String())
		}
	}
}

func TestGenericAssetOpenRejectsSubstitutionAfterResolution(t *testing.T) {
	for _, directory := range []bool{false, true} {
		t.Run(fmt.Sprintf("directory=%v", directory), func(t *testing.T) {
			root := t.TempDir()
			assets, err := NewAutoinstallAssets(root)
			if err != nil {
				t.Fatal(err)
			}
			digest := autoinstallDigest(autoinstallTestData)
			if _, err := assets.Stage(7, digest, autoinstallTestData); err != nil {
				t.Fatal(err)
			}
			public := filepath.Join(root, "public")
			if err := os.Mkdir(public, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(public, "user-data"), []byte("public"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("public/user-data", filepath.Join(root, "alias")); err != nil {
				t.Fatal(err)
			}
			// Resolve a safe stable alias, then substitute either resolved component
			// before the exact open operation the HTTP handler uses.
			resolved, err := filepath.EvalSymlinks(filepath.Join(root, "alias"))
			if err != nil {
				t.Fatal(err)
			}
			relative, err := filepath.Rel(root, resolved)
			if err != nil {
				t.Fatal(err)
			}
			substituted := resolved
			target := filepath.Join(root, "ubuntu-autoinstall", "7", digest, "user-data")
			if directory {
				substituted = public
				target = filepath.Dir(target)
			}
			if err := os.Rename(substituted, substituted+"-old"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, substituted); err != nil {
				t.Fatal(err)
			}
			file, err := openResolvedAsset(root, relative)
			if err == nil {
				defer file.Close()
				data, _ := io.ReadAll(file)
				t.Fatalf("post-resolution substitution opened private data: %q", data)
			}
		})
	}
}

func TestWinPEDriverStartupIsLimitedToPXESubnet(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "windows-driver-configs", "17")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "omb-setup.cmd"), []byte("limited-secret"), 0644); err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(root, []byte("#!ipxe\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		remote string
		status int
	}{{"10.30.0.7:12345", http.StatusOK}, {"192.168.10.55:12345", http.StatusNotFound}} {
		req := httptest.NewRequest(http.MethodGet, "/assets/windows-driver-configs/17/omb-setup.cmd", nil)
		req.RemoteAddr = tc.remote
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != tc.status {
			t.Fatalf("remote=%s status=%d want=%d", tc.remote, res.Code, tc.status)
		}
		if tc.status == http.StatusOK && res.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("startup script can be cached")
		}
		if tc.status != http.StatusOK && strings.Contains(res.Body.String(), "limited-secret") {
			t.Fatal("driver script leaked outside PXE subnet")
		}
	}
}
