package drivers

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testZIP(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for name, content := range entries {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

const testINF = `[Version]
Signature="$Windows NT$"
Class=Net
CatalogFile=net.cat
[Manufacturer]
%Vendor%=Models,NTamd64
[Models.NTamd64]
%Device%=Install,PCI\VEN_1AF4&DEV_1000
[Install]
CopyFiles=net.sys
`

func TestExtractCanonicalPackage(t *testing.T) {
	a := testZIP(t, map[string]string{"selected/net/driver.inf": testINF, "selected/net/net.cat": "cat", "selected/net/net.sys": "sys"})
	b := testZIP(t, map[string]string{"net.sys": "sys", "driver.inf": testINF, "net.cat": "cat"})
	left, err := ExtractZIP(bytes.NewReader(a), int64(len(a)), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	right, err := InspectZIP(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	if left.SHA256 != right.SHA256 {
		t.Fatalf("canonical hash differs: %s != %s", left.SHA256, right.SHA256)
	}
	if len(left.INFs) != 1 || left.INFs[0].Architecture != "amd64" || left.INFs[0].Class != "Net" {
		t.Fatalf("wrong INF metadata: %+v", left.INFs)
	}
	if len(left.INFs[0].HardwareIDs) != 1 || left.INFs[0].HardwareIDs[0] != `PCI\VEN_1AF4&DEV_1000` {
		t.Fatalf("wrong hardware IDs: %+v", left.INFs[0].HardwareIDs)
	}
}

func TestInspectINFClassWithSpaces(t *testing.T) {
	archive := testZIP(t, map[string]string{
		"netkvm.inf": strings.Replace(testINF, "Class=Net", "Class = Net", 1),
		"net.cat":    "cat",
		"net.sys":    "sys",
	})
	inventory, err := InspectZIP(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	if got := inventory.INFs[0].Class; got != "Net" {
		t.Fatalf("driver class = %q, want Net", got)
	}
}

func TestExtractRejectsUnsafePaths(t *testing.T) {
	for _, name := range []string{"../driver.inf", "/driver.inf", "C:/driver.inf", "foo\\driver.inf", "a/./driver.inf", "a//driver.inf"} {
		t.Run(name, func(t *testing.T) {
			archive := testZIP(t, map[string]string{name: testINF})
			_, err := InspectZIP(bytes.NewReader(archive), int64(len(archive)))
			if err == nil {
				t.Fatal("accepted unsafe path")
			}
		})
	}
}

func TestExtractRejectsCaseCollision(t *testing.T) {
	archive := testZIP(t, map[string]string{"Driver.inf": testINF, "driver.INF": testINF})
	if _, err := InspectZIP(bytes.NewReader(archive), int64(len(archive))); err == nil {
		t.Fatal("accepted case collision")
	}
}

func TestExtractRejectsSymlink(t *testing.T) {
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	header := &zip.FileHeader{Name: "driver.inf"}
	header.SetMode(os.ModeSymlink | 0777)
	f, err := w.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("target")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectZIP(bytes.NewReader(out.Bytes()), int64(out.Len())); err == nil {
		t.Fatal("accepted symlink")
	}
}

func TestExtractRejectsDeclaredExpansionBomb(t *testing.T) {
	archive := testZIP(t, map[string]string{"driver.inf": testINF})
	central := bytes.Index(archive, []byte{'P', 'K', 1, 2})
	if central < 0 {
		t.Fatal("central directory missing")
	}
	binary.LittleEndian.PutUint32(archive[central+24:], uint32(MaxExtractedBytes+1))
	if _, err := InspectZIP(bytes.NewReader(archive), int64(len(archive))); err == nil {
		t.Fatal("accepted excessive declared size")
	}
}

func TestExtractRejectsMissingReferencedFile(t *testing.T) {
	archive := testZIP(t, map[string]string{"driver.inf": testINF, "net.cat": "cat"})
	if _, err := InspectZIP(bytes.NewReader(archive), int64(len(archive))); err == nil {
		t.Fatal("accepted missing SYS")
	}
}

func TestExtractRejectsNoINF(t *testing.T) {
	archive := testZIP(t, map[string]string{"driver.sys": "sys"})
	if _, err := InspectZIP(bytes.NewReader(archive), int64(len(archive))); err == nil {
		t.Fatal("accepted package without INF")
	}
}

func TestExtractCleansPartialOutput(t *testing.T) {
	archive := testZIP(t, map[string]string{"driver.inf": testINF, "net.cat": "cat"})
	dest := t.TempDir()
	if _, err := ExtractZIP(bytes.NewReader(archive), int64(len(archive)), dest); err == nil {
		t.Fatal("accepted missing SYS")
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("partial extraction remains: %v", entries)
	}
}

func TestExtractRejectsExcessiveDepth(t *testing.T) {
	path := strings.Repeat("x/", MaxPathComponents) + "driver.inf"
	archive := testZIP(t, map[string]string{path: testINF})
	if _, err := InspectZIP(bytes.NewReader(archive), int64(len(archive))); err == nil {
		t.Fatal("accepted deep path")
	}
}

func TestExtractFilesHavePrivatePermissions(t *testing.T) {
	// Use a minimal INF without external file references.
	archive := testZIP(t, map[string]string{"driver.inf": "[Version]\nClass=Net\n[Models.NTamd64]\n"})
	dest := t.TempDir()
	_, err := ExtractZIP(bytes.NewReader(archive), int64(len(archive)), dest)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dest, "driver.inf"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %v", info.Mode())
	}
}
