package images

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func DeleteISO(root, relative string) error {
	if relative == "" || path.IsAbs(relative) || path.Clean(relative) != relative || strings.Contains(relative, `\`) {
		return fmt.Errorf("%w: invalid path", ErrInvalidISO)
	}
	folder := path.Dir(relative)
	if folder == "." {
		folder = ""
	}
	name := path.Base(relative)
	if !ValidISOName(name) {
		return ErrInvalidISO
	}
	directory, err := existingFolder(root, folder)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidFolder, err)
	}
	target := filepath.Join(directory, name)
	info, err := os.Lstat(target)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: not a regular file", ErrInvalidISO)
	}
	if err := os.Remove(target); err != nil {
		return err
	}
	_ = cleanEmptyParentDirs(target, root)
	return nil
}
