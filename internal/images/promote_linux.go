//go:build linux

package images

import (
	"os"

	"golang.org/x/sys/unix"
)

func promoteNoReplace(from, to string) error {
	err := unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE)
	if err == nil {
		return nil
	}
	if err == unix.EINVAL || err == unix.ENOSYS || err == unix.EOPNOTSUPP {
		if _, statErr := os.Lstat(to); statErr == nil {
			return os.ErrExist
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
		return os.Rename(from, to)
	}
	if err == unix.EEXIST {
		return os.ErrExist
	}
	return err
}
