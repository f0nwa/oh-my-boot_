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
