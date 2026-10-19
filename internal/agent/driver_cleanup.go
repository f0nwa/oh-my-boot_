package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

func (m *DriverManager) ConfigWIMKey(id int64) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id <= 0 || requirePlainDirectory(m.ConfigRoot) != nil {
		return "", fmt.Errorf("invalid driver configuration root or ID")
	}
	dir := filepath.Join(m.ConfigRoot, strconv.FormatInt(id, 10))
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("driver configuration directory is unsafe")
	}
	file := filepath.Join(dir, "build.json")
	info, err = os.Lstat(file)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("driver configuration manifest is unsafe")
	}
	content, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	var manifest driverConfigManifest
	if err := json.Unmarshal(content, &manifest); err != nil || manifest.Request.ConfigID != id || len(manifest.Result.WIMKey) != 64 || !validManagedChild(manifest.Result.WIMKey) {
		return "", fmt.Errorf("driver configuration manifest is invalid")
	}
	return manifest.Result.WIMKey, nil
}

func (m *DriverManager) RemoveConfig(_ context.Context, id int64, wimKey string, removeWIM bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id <= 0 || (wimKey != "" && !validManagedChild(wimKey)) || (removeWIM && len(wimKey) != 64) {
		return fmt.Errorf("invalid driver configuration identity")
	}
	if err := removeManagedDirectory(m.ConfigRoot, strconv.FormatInt(id, 10)); err != nil {
		return err
	}
	if removeWIM {
		return removeManagedDirectory(m.WIMRoot, wimKey)
	}
	return nil
}
