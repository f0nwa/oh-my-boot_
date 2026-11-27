package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func updateArchive(t *testing.T, members ...tar.Header) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	for _, h := range members {
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(bytes.Repeat([]byte{'x'}, int(h.Size))); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestExtractUpdate(t *testing.T) {
	valid := tar.Header{Name: "apt-repo/repo-manifest.json", Mode: 0777, Size: 1}
	for _, tc := range []struct {
		name    string
		members []tar.Header
		limit   int64
		ok      bool
	}{
		{"verified layout", []tar.Header{valid}, 16384, true},
		{"sibling path", []tar.Header{{Name: "apt-repo/../repo-manifest.json", Mode: 0600}}, 16384, false},
		{"absolute path", []tar.Header{{Name: "/apt-repo/evil.deb", Mode: 0600}}, 16384, false},
		{"symbolic link", []tar.Header{{Name: "apt-repo/evil.deb", Typeflag: tar.TypeSymlink, Linkname: "/etc/omb"}}, 16384, false},
		{"hard link", []tar.Header{{Name: "apt-repo/evil.deb", Typeflag: tar.TypeLink, Linkname: "/etc/omb"}}, 16384, false},
		{"unrecognized file", []tar.Header{{Name: "apt-repo/setup.sh", Mode: 0600}}, 16384, false},
		{"duplicate entry", []tar.Header{valid, valid}, 16384, false},
		{"oversize", []tar.Header{valid}, 1024, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			err := extractUpdate(bytes.NewReader(updateArchive(t, tc.members...)), repo, tc.limit)
			if (err == nil) != tc.ok {
				t.Fatalf("extract = %v, want success %v", err, tc.ok)
			}
			if tc.ok {
				info, err := os.Stat(filepath.Join(repo, "repo-manifest.json"))
				if err != nil || info.Mode().Perm() != 0600 {
					t.Fatalf("file = %v, %v", info, err)
				}
			}
		})
	}
}
