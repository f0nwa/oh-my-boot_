package nfsstorage

import (
	"strings"
	"testing"
)

func TestRootAndUploadRoot(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef"
	if got := Root(key); got != "/mnt/omb-nfs/"+key {
		t.Fatalf("Root(%q) = %q, want %q", key, got, "/mnt/omb-nfs/"+key)
	}
	if got := UploadRoot(key); got != "/mnt/omb-nfs-upload/"+key {
		t.Fatalf("UploadRoot(%q) = %q, want %q", key, got, "/mnt/omb-nfs-upload/"+key)
	}
}

func TestSource(t *testing.T) {
	tests := []struct {
		server     string
		exportPath string
		want       string
	}{
		{"192.168.10.10", "/volume1/iso", "192.168.10.10:/volume1/iso"},
		{"2001:db8::1", "/iso", "[2001:db8::1]:/iso"},
		{"[2001:db8::1]", "/iso", "[2001:db8::1]:/iso"},
		{"::1", "/export", "[::1]:/export"},
		{"nas.local", "/srv/nfs", "nas.local:/srv/nfs"},
		{"nas", "/", "nas:/"},
	}
	for _, tt := range tests {
		if got := Source(tt.server, tt.exportPath); got != tt.want {
			t.Errorf("Source(%q, %q) = %q, want %q", tt.server, tt.exportPath, got, tt.want)
		}
	}
}

func TestNormalizeExportPath(t *testing.T) {
	valid := []struct {
		raw  string
		want string
	}{
		{"/volume1/iso", "/volume1/iso"},
		{"  /volume1/iso  ", "/volume1/iso"},
		{"  /volume1/iso/  ", "/volume1/iso"},
		{"/", "/"},
		{"  /  ", "/"},
		{"/export", "/export"},
		{"/a/b/c-d_e@f+g", "/a/b/c-d_e@f+g"},
	}
	for _, tt := range valid {
		got, err := NormalizeExportPath(tt.raw)
		if err != nil {
			t.Errorf("NormalizeExportPath(%q) unexpected error: %v", tt.raw, err)
		} else if got != tt.want {
			t.Errorf("NormalizeExportPath(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}

	invalid := []string{
		"",
		"   ",
		"relative/path",
		"//",
		"///",
		"/volume1//iso",
		"/volume1/iso//",
		"/.",
		"/..",
		"/volume1/./iso",
		"/volume1/../iso",
		"/volume1/iso/.",
		"/volume1/iso/..",
		"/volume1/iso name",
		"/volume1/iso%20",
		"/volume1\\iso",
		"/volume1/iso\r\n",
		"/volume1/iso\x00",
		"/" + strings.Repeat("a", 513),
	}
	for _, raw := range invalid {
		if got, err := NormalizeExportPath(raw); err == nil {
			t.Errorf("NormalizeExportPath(%q) = %q, expected error", raw, got)
		}
	}
}

func TestValidateSuccess(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef"
	validServers := []string{
		"192.168.10.10",
		"10.0.0.1",
		"2001:db8::1",
		"[2001:db8::1]",
		"::1",
		"nas",
		"nas.example.com",
		"nfs-server-01.local",
	}
	validPaths := []string{
		"/volume1/iso",
		"/",
		"/export",
		"/srv/nfs4/iso",
	}
	validVersions := []string{"4.1", "3"}

	for _, server := range validServers {
		for _, path := range validPaths {
			for _, version := range validVersions {
				if err := Validate(key, server, path, version); err != nil {
					t.Errorf("Validate(%q, %q, %q, %q) unexpected error: %v", key, server, path, version, err)
				}
			}
		}
	}
}

func TestValidateRejections(t *testing.T) {
	validKey := "0123456789abcdef0123456789abcdef"
	validServer := "192.168.10.10"
	validPath := "/volume1/iso"
	validVersion := "4.1"

	tests := []struct {
		name    string
		key     string
		server  string
		path    string
		version string
	}{
		// Key errors
		{"empty key", "", validServer, validPath, validVersion},
		{"short key", "0123456789abcdef", validServer, validPath, validVersion},
		{"31 char key", "0123456789abcdef0123456789abcde", validServer, validPath, validVersion},
		{"33 char key", "0123456789abcdef0123456789abcdef0", validServer, validPath, validVersion},
		{"uppercase key", "0123456789ABCDEF0123456789ABCDEF", validServer, validPath, validVersion},
		{"non-hex key", "0123456789abcdef0123456789abcdeg", validServer, validPath, validVersion},
		{"key with spaces", " 0123456789abcdef0123456789abcdef ", validServer, validPath, validVersion},

		// Server errors
		{"empty server", validKey, "", validPath, validVersion},
		{"whitespace server", validKey, "   ", validPath, validVersion},
		{"server with comma", validKey, "192.168.10.10,foo", validPath, validVersion},
		{"server with double dot", validKey, "nas..local", validPath, validVersion},
		{"server with leading dot", validKey, ".nas.local", validPath, validVersion},
		{"server with trailing dot", validKey, "nas.local.", validPath, validVersion},
		{"invalid IPv6 extra colons", validKey, "2001:db8:::1", validPath, validVersion},
		{"bracketed IPv4", validKey, "[192.168.1.1]", validPath, validVersion},
		{"bracketed invalid text", validKey, "[invalid]", validPath, validVersion},
		{"server with space", validKey, "server name", validPath, validVersion},
		{"server with slash", validKey, "server/path", validPath, validVersion},
		{"server too long", validKey, strings.Repeat("a", 254), validPath, validVersion},
		{"server label too long", validKey, strings.Repeat("a", 64) + ".com", validPath, validVersion},

		// Export path errors
		{"empty export path", validKey, validServer, "", validVersion},
		{"relative export path", validKey, validServer, "relative/path", validVersion},
		{"double slash root", validKey, validServer, "//", validVersion},
		{"double slash component", validKey, validServer, "/volume1//iso", validVersion},
		{"export path dot", validKey, validServer, "/.", validVersion},
		{"export path dot-dot", validKey, validServer, "/..", validVersion},
		{"export path middle dot", validKey, validServer, "/volume1/./iso", validVersion},
		{"export path middle dot-dot", validKey, validServer, "/volume1/../iso", validVersion},
		{"export path with space", validKey, validServer, "/volume1/iso name", validVersion},
		{"export path with leading space", validKey, validServer, " /volume1/iso", validVersion},
		{"export path with percent", validKey, validServer, "/volume1/iso%20", validVersion},
		{"export path with backslash", validKey, validServer, "/volume1\\iso", validVersion},
		{"export path with crlf", validKey, validServer, "/volume1/iso\r\n", validVersion},
		{"export path with null", validKey, validServer, "/volume1/iso\x00", validVersion},
		{"export path too long", validKey, validServer, "/" + strings.Repeat("x", 513), validVersion},

		// Version errors
		{"empty version", validKey, validServer, validPath, ""},
		{"version 4", validKey, validServer, validPath, "4"},
		{"version 4.2", validKey, validServer, validPath, "4.2"},
		{"version 2", validKey, validServer, validPath, "2"},
		{"version 3.0", validKey, validServer, validPath, "3.0"},
		{"nfs prefix version", validKey, validServer, validPath, "nfs4.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Validate(tt.key, tt.server, tt.path, tt.version); err == nil {
				t.Fatalf("Validate(%q, %q, %q, %q) accepted invalid input", tt.key, tt.server, tt.path, tt.version)
			}
		})
	}
}
