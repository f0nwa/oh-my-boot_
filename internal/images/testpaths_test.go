package images

import (
	"path/filepath"
	"testing"
)

// macOS exposes temporary directories below /var, which is itself a symlink.
func realTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}
