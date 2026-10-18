package smbstorage

import (
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	keyPattern       = regexp.MustCompile(`^[0-9a-f]{32}$`)
	componentPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	dnsLabelPattern  = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?$`)
)

func Root(key string) string { return filepath.Join("/mnt/pxe-platform-smb", key) }

func UploadRoot(key string) string { return filepath.Join("/mnt/pxe-platform-smb-upload", key) }

// ParsePath splits a share path entered in the dashboard without normalizing
// dot segments or empty components that could change the requested location.
func ParsePath(value string) (server, share, subpath string, err error) {
	value = strings.ReplaceAll(strings.TrimSpace(value), `\`, "/")
	value = strings.TrimPrefix(value, "//")
	parts := strings.Split(value, "/")
	if len(parts) < 2 {
		return "", "", "", fmt.Errorf("invalid SMB path")
	}
	for _, part := range parts {
		if part == "" {
			return "", "", "", fmt.Errorf("invalid SMB path")
		}
	}
	server, share, subpath = parts[0], parts[1], strings.Join(parts[2:], "/")
	if err := Validate("0123456789abcdef0123456789abcdef", server, share, subpath); err != nil {
		return "", "", "", err
	}
	return server, share, subpath, nil
}

func Validate(key, server, share, subpath string) error {
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("invalid storage key")
	}
	if ip := net.ParseIP(server); ip == nil || ip.To4() == nil {
		if len(server) == 0 || len(server) > 253 {
			return fmt.Errorf("invalid SMB server")
		}
		for _, label := range strings.Split(server, ".") {
			if len(label) > 63 || !dnsLabelPattern.MatchString(label) {
				return fmt.Errorf("invalid SMB server")
			}
		}
	}
	if !validComponent(share) {
		return fmt.Errorf("invalid SMB share")
	}
	if subpath != "" {
		for _, part := range strings.Split(subpath, "/") {
			if !validComponent(part) {
				return fmt.Errorf("invalid SMB subpath")
			}
		}
	}
	return nil
}

func validComponent(value string) bool {
	return value != "." && value != ".." && componentPattern.MatchString(value)
}
