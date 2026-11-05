package agent

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// Only a structural mismatch is unsupported; a failed filesystem operation
// must remain an inspection failure so it can be retried.
var errUnsupportedISO = errors.New("unsupported ISO layout")

func unsupportedISO(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errUnsupportedISO, fmt.Sprintf(format, args...))
}
func isoPathError(err error) error {
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return fmt.Errorf("%w: %v", errUnsupportedISO, err)
	}
	return err
}
