package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifySourceRequiresMatchingDigestAndValidCatalog(t *testing.T) {
	root := t.TempDir()
	iso := filepath.Join(root, "source.iso")
	os.WriteFile(iso, []byte("test-iso"), 0644)
	dir := filepath.Join(root, "source")
	os.Mkdir(dir, 0755)
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte("test-iso")))
	files := map[string]string{}
	for _, name := range []string{"vmlinuz", "initrd", "bootx64.efi", "install-sources.yaml"} {
		data := []byte("artifact")
		if name == "install-sources.yaml" {
			data = []byte("- id: ubuntu-desktop\n  name: Desktop\n")
		}
		os.WriteFile(filepath.Join(dir, name), data, 0644)
		files[name] = fmt.Sprintf("%x", sha256.Sum256(data))
	}
	manifest, _ := json.Marshal(map[string]any{"version": 1, "iso_sha256": digest, "iso_size": 8, "files": files})
	os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0644)
	if err := verifySource(iso, dir, digest); err != nil {
		t.Fatal(err)
	}
	if err := verifySource(iso, dir, fmt.Sprintf("%064d", 0)); err == nil {
		t.Fatal("incorrect digest accepted")
	}
	malformed := []byte("- id: ubuntu-desktop\n  name: [invalid\n")
	os.WriteFile(filepath.Join(dir, "install-sources.yaml"), malformed, 0644)
	files["install-sources.yaml"] = fmt.Sprintf("%x", sha256.Sum256(malformed))
	manifest, _ = json.Marshal(map[string]any{"version": 1, "iso_sha256": digest, "iso_size": 8, "files": files})
	os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0644)
	if err := verifySource(iso, dir, digest); err == nil {
		t.Fatal("malformed catalog accepted")
	}
}
