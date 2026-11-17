package nfsstorage

import (
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	keyPattern          = regexp.MustCompile(`^[0-9a-f]{32}$`)
	dnsLabelPattern     = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?$`)
	exportPathPattern   = regexp.MustCompile(`^[A-Za-z0-9._/@+-]+$`)
	componentPattern    = regexp.MustCompile(`^[A-Za-z0-9._@+-]+$`)
)

func Root(key string) string {
	return filepath.Join("/mnt/omb-nfs", key)
}

func UploadRoot(key string) string {
	return filepath.Join("/mnt/omb-nfs-upload", key)
}

// Source returns the mount source string for the NFS export.
// For IPv6 addresses, the host is enclosed in square brackets.
func Source(server, exportPath string) string {
	server = strings.TrimSpace(server)
	if strings.HasPrefix(server, "[") && strings.HasSuffix(server, "]") {
		return server + ":" + exportPath
	}
	if ip := net.ParseIP(server); ip != nil && ip.To4() == nil {
		return "[" + server + "]:" + exportPath
	}
	return server + ":" + exportPath
}

// NormalizeExportPath trims external whitespace, checks path validity, and normalizes
// any non-root trailing slash without distorting components.
func NormalizeExportPath(raw string) (string, error) {
	if strings.ContainsAny(raw, "\r\n\x00") {
		return "", fmt.Errorf("export path contains control characters")
	}
	trimmed := strings.Trim(raw, " \t")
	if err := validateExportPath(trimmed); err != nil {
		return "", err
	}
	if trimmed != "/" {
		trimmed = strings.TrimSuffix(trimmed, "/")
	}
	return trimmed, nil
}

// Validate validates key, server, export path, and NFS protocol version.
func Validate(key, server, exportPath, version string) error {
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("invalid storage key")
	}
	if err := validateServer(server); err != nil {
		return err
	}
	if err := validateExportPath(exportPath); err != nil {
		return err
	}
	if version != "4.1" && version != "3" {
		return fmt.Errorf("invalid NFS version: %q", version)
	}
	return nil
}

func validateServer(server string) error {
	if strings.HasPrefix(server, "[") && strings.HasSuffix(server, "]") {
		inner := server[1 : len(server)-1]
		ip := net.ParseIP(inner)
		if ip == nil || ip.To4() != nil {
			return fmt.Errorf("invalid NFS server")
		}
		return nil
	}
	if ip := net.ParseIP(server); ip != nil {
		return nil
	}
	if len(server) == 0 || len(server) > 253 {
		return fmt.Errorf("invalid NFS server")
	}
	for _, label := range strings.Split(server, ".") {
		if len(label) == 0 || len(label) > 63 || !dnsLabelPattern.MatchString(label) {
			return fmt.Errorf("invalid NFS server")
		}
	}
	return nil
}

func validateExportPath(path string) error {
	if path == "" {
		return fmt.Errorf("empty export path")
	}
	if len(path) > 512 {
		return fmt.Errorf("export path is too long")
	}
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("export path must be absolute")
	}
	if strings.Contains(path, "//") {
		return fmt.Errorf("export path cannot contain double slashes")
	}
	if !exportPathPattern.MatchString(path) {
		return fmt.Errorf("export path contains invalid characters")
	}
	if path == "/" {
		return nil
	}
	trimmed := strings.TrimSuffix(path, "/")
	for _, part := range strings.Split(trimmed[1:], "/") {
		if part == "" || part == "." || part == ".." || !componentPattern.MatchString(part) {
			return fmt.Errorf("invalid export path component: %q", part)
		}
	}
	return nil
}
