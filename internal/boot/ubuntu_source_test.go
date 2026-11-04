package boot

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func sourceFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	assets := filepath.Join(root, "assets")
	os.MkdirAll(filepath.Join(assets, "ubuntu"), 0755)
	iso := filepath.Join(root, "desktop.iso")
	os.WriteFile(iso, []byte("test-iso"), 0644)
	files := map[string]string{}
	for _, n := range []string{"vmlinuz", "initrd", "bootx64.efi", "install-sources.yaml"} {
		data := []byte("artifact")
		if n == "install-sources.yaml" {
			data = []byte("- id: ubuntu-desktop\n  name:\n    en: Ubuntu Desktop\n  default: true\n")
		}
		os.WriteFile(filepath.Join(assets, "ubuntu", n), data, 0644)
		files[n] = fmt.Sprintf("%x", sha256.Sum256(data))
	}
	m := map[string]any{"version": 1, "iso_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte("test-iso"))), "iso_size": 8, "files": files}
	data, _ := json.Marshal(m)
	os.WriteFile(filepath.Join(assets, "ubuntu", "manifest.json"), data, 0644)
	return iso, assets
}
func TestUbuntuSourceRejectsReplacedFiles(t *testing.T) {
	for _, name := range []string{"iso", "vmlinuz", "install-sources.yaml"} {
		t.Run(name, func(t *testing.T) {
			iso, assets := sourceFixture(t)
			s := NewUbuntuSource(iso, assets)
			c, e := s.Catalog()
			if e != nil || len(c.Sources) != 1 {
				t.Fatalf("catalog: %#v %v", c, e)
			}
			path := iso
			if name != "iso" {
				path = filepath.Join(assets, "ubuntu", name)
			}
			os.Remove(path)
			os.WriteFile(path, []byte("replaced"), 0644)
			if _, e = s.Catalog(); e == nil {
				t.Fatal("replaced file accepted")
			}
		})
	}
}
func TestUbuntuSourceRejectsBadManifestAndSymlink(t *testing.T) {
	for _, kind := range []string{"missing-manifest", "manifest", "symlink", "contents"} {
		t.Run(kind, func(t *testing.T) {
			iso, assets := sourceFixture(t)
			switch kind {
			case "missing-manifest":
				os.Remove(filepath.Join(assets, "ubuntu", "manifest.json"))
			case "manifest":
				os.WriteFile(filepath.Join(assets, "ubuntu", "manifest.json"), []byte(`{"version":2}`), 0644)
			case "symlink":
				p := filepath.Join(assets, "ubuntu", "initrd")
				os.Remove(p)
				os.Symlink(iso, p)
			case "contents":
				os.WriteFile(iso, []byte("wrong-iso"), 0644)
			}
			if _, e := NewUbuntuSource(iso, assets).Catalog(); e == nil {
				t.Fatal("corrupt source accepted")
			}
		})
	}
}
func TestUbuntuSourceLocalAPI(t *testing.T) {
	iso, assets := sourceFixture(t)
	p, e := NewPublisher(filepath.Join(t.TempDir(), "menu"), []byte("#!ipxe\n"))
	if e != nil {
		t.Fatal(e)
	}
	handler := ControlHandlerWithUbuntuSource(p, nil, nil, nil, nil, NewUbuntuSource(iso, assets))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/ubuntu/source", nil))
	if res.Code != 200 {
		t.Fatalf("API %d %s", res.Code, res.Body.String())
	}
}

func TestUbuntuSourceReadCannotReplaceVerifiedSnapshot(t *testing.T) {
	iso, assets := sourceFixture(t)
	s := NewUbuntuSource(iso, assets)
	if _, err := s.Catalog(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(assets, "ubuntu", "install-sources.yaml")
	verified := s.info[path]
	changed := []byte("- id: ubuntu-desktop-changed\n  name: Changed\n")
	if err := os.WriteFile(path, changed, 0644); err != nil {
		t.Fatal(err)
	}
	later := verified.ModTime().Add(time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	if _, err := s.read(path, 64*1024); err == nil {
		t.Fatal("read replaced a previously verified snapshot")
	}
	if _, err := s.Catalog(); err == nil {
		t.Fatal("mutated catalog accepted under old manifest")
	}
}
