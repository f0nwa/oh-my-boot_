package boot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadOrCreateSnapshot preserves the last menu that was successfully published.
func LoadOrCreateSnapshot(path, baseURL string) ([]byte, error) {
	contents, err := os.ReadFile(path)
	if err == nil {
		if !strings.HasPrefix(string(contents), "#!ipxe\n") {
			return nil, fmt.Errorf("invalid menu snapshot at %s", path)
		}
		return contents, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read menu snapshot: %w", err)
	}
	contents, err = Menu(baseURL)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create snapshot directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".menu-*")
	if err != nil {
		return nil, fmt.Errorf("create menu snapshot: %w", err)
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(contents); err != nil {
		file.Close()
		return nil, fmt.Errorf("write menu snapshot: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return nil, fmt.Errorf("set snapshot permissions: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return nil, fmt.Errorf("sync menu snapshot: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close menu snapshot: %w", err)
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return nil, fmt.Errorf("publish menu snapshot: %w", err)
	}
	return contents, nil
}
