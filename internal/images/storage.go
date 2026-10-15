package images

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ValidateStorage(name, kind, root string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 120 || (kind != "local" && kind != "mounted") || !filepath.IsAbs(root) {
		return "", fmt.Errorf("invalid storage name, kind or path")
	}
	root = filepath.Clean(root)
	if err := validateRootPath(root); err != nil {
		return "", err
	}
	if kind == "mounted" && !IsMountpoint(root) {
		return "", fmt.Errorf("path is not a mountpoint")
	}
	return root, nil
}

// validateRootPath rejects symlinks in every component, including parent dirs.
func validateRootPath(root string) error {
	if !filepath.IsAbs(root) {
		return fmt.Errorf("storage root must be absolute")
	}
	current := string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Clean(root), current), current) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("storage root must contain only real directories: %s", current)
		}
	}
	return nil
}
