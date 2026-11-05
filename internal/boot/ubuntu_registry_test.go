package boot

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUbuntuRegistryLoadingOneSourceDoesNotBlockAnother(t *testing.T) {
	assets, registry := t.TempDir(), t.TempDir()
	var digests [2]string
	var isos [2]string
	for i, id := range []int{11, 12} {
		iso, fixture := sourceFixture(t)
		isos[i] = iso
		data, err := os.ReadFile(iso)
		if err != nil {
			t.Fatal(err)
		}
		digests[i] = fmt.Sprintf("%x", sha256.Sum256(data))
		dest := filepath.Join(assets, "ubuntu-sources", fmt.Sprint(id), digests[i])
		if err := os.MkdirAll(dest, 0755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"manifest.json", "vmlinuz", "initrd", "bootx64.efi", "install-sources.yaml"} {
			content, err := os.ReadFile(filepath.Join(fixture, "ubuntu", name))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dest, name), content, 0644); err != nil {
				t.Fatal(err)
			}
		}
		config, err := json.Marshal(map[string]any{"id": id, "sha256": digests[i], "iso_path": iso})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(registry, fmt.Sprintf("%d.json", id)), config, 0600); err != nil {
			t.Fatal(err)
		}
	}
	started, release := make(chan struct{}), make(chan struct{})
	r := NewUbuntuRegistry(registry, assets)
	r.loadSource = func(iso, dir string) *UbuntuSource {
		if iso == isos[0] {
			close(started)
			<-release
		}
		return NewUbuntuSourceAt(iso, dir)
	}
	first := make(chan error, 1)
	go func() { _, err := r.source(11, digests[0]); first <- err }()
	<-started
	second := make(chan error, 1)
	go func() { _, err := r.source(12, digests[1]); second <- err }()
	select {
	case err := <-second:
		if err != nil {
			close(release)
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		close(release)
		<-first
		t.Fatal("one ISO blocked another source")
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
}

func TestUbuntuRegistryDefaultDoesNotResideUnderBootWritableState(t *testing.T) {
	if filepath.Dir(DefaultUbuntuRegistryRoot) != "/var/lib" {
		t.Fatal("registry parent must not be writable by the Boot service")
	}
}

func TestUbuntuRegistryServesOnlyVerifiedSourceAndAssets(t *testing.T) {
	assetsRoot := t.TempDir()
	registryRoot := t.TempDir()
	t.Setenv("OMB_BOOT_UBUNTU_REGISTRY", registryRoot)
	var urls []string
	var sourceDirs []string
	var isoFiles []string
	for _, id := range []int{11, 12} {
		iso, fixtureAssets := sourceFixture(t)
		isoFiles = append(isoFiles, iso)
		if id == 12 {
			contents := []byte("second-iso")
			if err := os.WriteFile(iso, contents, 0644); err != nil {
				t.Fatal(err)
			}
			manifestPath := filepath.Join(fixtureAssets, "ubuntu", "manifest.json")
			raw, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			var manifest map[string]any
			if err := json.Unmarshal(raw, &manifest); err != nil {
				t.Fatal(err)
			}
			manifest["iso_sha256"] = fmt.Sprintf("%x", sha256.Sum256(contents))
			manifest["iso_size"] = len(contents)
			raw, err = json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(manifestPath, raw, 0644); err != nil {
				t.Fatal(err)
			}
		}
		bytes, err := os.ReadFile(iso)
		if err != nil {
			t.Fatal(err)
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(bytes))
		dest := filepath.Join(assetsRoot, "ubuntu-sources", fmt.Sprint(id), digest)
		sourceDirs = append(sourceDirs, dest)
		if err := os.MkdirAll(dest, 0755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"manifest.json", "vmlinuz", "initrd", "bootx64.efi", "install-sources.yaml"} {
			content, err := os.ReadFile(filepath.Join(fixtureAssets, "ubuntu", name))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dest, name), content, 0644); err != nil {
				t.Fatal(err)
			}
		}
		config, err := json.Marshal(map[string]any{"id": id, "sha256": digest, "iso_path": iso})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(registryRoot, fmt.Sprintf("%d.json", id)), config, 0600); err != nil {
			t.Fatal(err)
		}
		urls = append(urls, fmt.Sprintf("/images/ubuntu/%d/%s.iso", id, digest), fmt.Sprintf("/assets/ubuntu/%d/%s/vmlinuz", id, digest))
	}
	publisher, err := NewPublisher(filepath.Join(t.TempDir(), "menu.ipxe"), []byte("#!ipxe\n"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewDynamicHandlerWithISO(assetsRoot, publisher, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range urls {
		res := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.RemoteAddr = "10.30.0.20:1234"
		h.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("source %s: status=%d", target, res.Code)
		}
		if target == urls[0] && res.Body.String() != "test-iso" || target == urls[2] && res.Body.String() != "second-iso" {
			t.Fatalf("source %s returned another ISO", target)
		}
	}
	strictHandler, err := NewDynamicHandlerWithTransfers(assetsRoot, publisher, nil, isoFiles[0], nil, NewUbuntuRegistry(registryRoot, assetsRoot))
	if err != nil {
		t.Fatal(err)
	}
	legacyRequest := httptest.NewRequest(http.MethodGet, "/images/ubuntu-desktop.iso", nil)
	legacyRequest.RemoteAddr = "10.30.0.20:1234"
	legacyResponse := httptest.NewRecorder()
	strictHandler.ServeHTTP(legacyResponse, legacyRequest)
	if legacyResponse.Code != http.StatusNotFound {
		t.Fatalf("unverified legacy alias was served: %d", legacyResponse.Code)
	}
	legacyDigest := strings.TrimSuffix(strings.Split(urls[0], "/")[4], ".iso")
	legacyConfig, err := json.Marshal(map[string]any{"id": 11, "sha256": legacyDigest})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(registryRoot, "legacy.json"), legacyConfig, 0600); err != nil {
		t.Fatal(err)
	}
	legacyResponse = httptest.NewRecorder()
	strictHandler.ServeHTTP(legacyResponse, legacyRequest)
	if legacyResponse.Code != http.StatusOK || legacyResponse.Body.String() != "test-iso" {
		t.Fatalf("verified legacy alias: status=%d body=%q", legacyResponse.Code, legacyResponse.Body.String())
	}
	legacyAsset := httptest.NewRecorder()
	legacyAssetRequest := httptest.NewRequest(http.MethodGet, "/assets/ubuntu/vmlinuz", nil)
	legacyAssetRequest.RemoteAddr = "10.30.0.20:1234"
	strictHandler.ServeHTTP(legacyAsset, legacyAssetRequest)
	if legacyAsset.Code != http.StatusOK || legacyAsset.Body.String() != "artifact" {
		t.Fatalf("verified legacy asset: status=%d body=%q", legacyAsset.Code, legacyAsset.Body.String())
	}
	rangeResult := httptest.NewRecorder()
	rangeRequest := httptest.NewRequest(http.MethodGet, urls[0], nil)
	rangeRequest.RemoteAddr = "10.30.0.20:1234"
	rangeRequest.Header.Set("Range", "bytes=0-3")
	h.ServeHTTP(rangeResult, rangeRequest)
	if rangeResult.Code != http.StatusPartialContent || rangeResult.Body.String() != "test" {
		t.Fatalf("ISO range status=%d body=%q", rangeResult.Code, rangeResult.Body.String())
	}
	parts := strings.Split(urls[0], "/")
	digest := strings.TrimSuffix(parts[len(parts)-1], ".iso")
	control := ControlHandlerWithUbuntuSource(publisher, nil, nil, nil, nil, NewUbuntuSource("", assetsRoot))
	legacyMetadata := httptest.NewRecorder()
	control.ServeHTTP(legacyMetadata, httptest.NewRequest(http.MethodGet, "/ubuntu/legacy", nil))
	if legacyMetadata.Code != http.StatusOK || !strings.Contains(legacyMetadata.Body.String(), legacyDigest) {
		t.Fatalf("verified legacy identity unavailable: status=%d", legacyMetadata.Code)
	}
	metadata := httptest.NewRecorder()
	control.ServeHTTP(metadata, httptest.NewRequest(http.MethodGet, "/ubuntu/sources/11/"+digest, nil))
	if metadata.Code != http.StatusOK || !strings.Contains(metadata.Body.String(), digest) || !strings.Contains(metadata.Body.String(), "ubuntu-desktop") {
		t.Fatalf("source-specific metadata unavailable: status=%d body=%s", metadata.Code, metadata.Body.String())
	}
	for _, target := range []string{strings.Replace(urls[0], "/11/", "/99/", 1), strings.Replace(urls[1], "/vmlinuz", "/manifest.json", 1)} {
		res := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.RemoteAddr = "10.30.0.20:1234"
		h.ServeHTTP(res, req)
		if res.Code != http.StatusNotFound {
			t.Fatalf("unexpected source access %s: status=%d", target, res.Code)
		}
	}
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, urls[0], nil)
	req.RemoteAddr = "192.168.10.20:1234"
	h.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("source exposed outside PXE subnet: %d", res.Code)
	}
	if err := os.WriteFile(filepath.Join(sourceDirs[0], "vmlinuz"), []byte("corrupted"), 0644); err != nil {
		t.Fatal(err)
	}
	res = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, urls[1], nil)
	req.RemoteAddr = "10.30.0.20:1234"
	h.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("changed asset was accepted: %d", res.Code)
	}
	if err := os.Symlink(sourceDirs[1], filepath.Join(assetsRoot, "alias")); err != nil {
		t.Fatal(err)
	}
	res = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/assets/alias/vmlinuz", nil)
	req.RemoteAddr = "192.168.10.20:1234"
	h.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("registry was exposed via an asset alias: %d", res.Code)
	}
	if err := os.WriteFile(isoFiles[1], []byte("altered ISO"), 0644); err != nil {
		t.Fatal(err)
	}
	res = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, urls[2], nil)
	req.RemoteAddr = "10.30.0.20:1234"
	h.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("changed ISO was accepted: %d", res.Code)
	}
}
