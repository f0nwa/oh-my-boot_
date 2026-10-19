package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"pxe-platform/internal/drivers"
)

type DriverManager struct {
	mu          sync.Mutex
	Root        string
	WIMRoot     string
	ConfigRoot  string
	ProfileRoot string
	AnswerRoot  string
	Runner      DriverCommandRunner
	FreeBytes   func(string) (uint64, error)
}

func (m *DriverManager) Import(ctx context.Context, src io.Reader, size int64) (drivers.Inventory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if size <= 0 || size > drivers.MaxArchiveBytes {
		return drivers.Inventory{}, fmt.Errorf("driver archive size is invalid")
	}
	if err := safeDriverRoot(m.Root); err != nil {
		return drivers.Inventory{}, err
	}
	archive, err := os.CreateTemp(m.Root, ".driver-upload-")
	if err != nil {
		return drivers.Inventory{}, err
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	if err := archive.Chmod(0600); err != nil {
		return drivers.Inventory{}, err
	}
	written, err := io.Copy(archive, io.LimitReader(src, size+1))
	if err != nil {
		return drivers.Inventory{}, fmt.Errorf("read driver archive: %w", err)
	}
	if written != size {
		return drivers.Inventory{}, fmt.Errorf("driver archive length differs")
	}
	if err := ctx.Err(); err != nil {
		return drivers.Inventory{}, err
	}
	stage, err := os.MkdirTemp(m.Root, ".driver-package-")
	if err != nil {
		return drivers.Inventory{}, err
	}
	defer os.RemoveAll(stage)
	files := filepath.Join(stage, "files")
	if err := os.Mkdir(files, 0700); err != nil {
		return drivers.Inventory{}, err
	}
	inventory, err := drivers.ExtractZIP(archive, size, files)
	if err != nil {
		return drivers.Inventory{}, err
	}
	final := filepath.Join(m.Root, inventory.SHA256)
	if _, err := os.Lstat(final); err == nil {
		if err := m.checkPackage(inventory.SHA256); err != nil {
			return drivers.Inventory{}, err
		}
		return inventory, nil
	} else if !os.IsNotExist(err) {
		return drivers.Inventory{}, err
	}
	data, err := json.Marshal(inventory)
	if err != nil {
		return drivers.Inventory{}, err
	}
	if err := os.WriteFile(filepath.Join(stage, "inventory.json"), data, 0600); err != nil {
		return drivers.Inventory{}, err
	}
	if err := os.Rename(stage, final); err != nil {
		return drivers.Inventory{}, err
	}
	return inventory, nil
}

func (m *DriverManager) CheckPackage(ctx context.Context, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return m.checkPackage(hash)
}

func (m *DriverManager) checkPackage(hash string) error {
	if !validDriverHash(hash) {
		return fmt.Errorf("invalid driver package hash")
	}
	root := filepath.Join(m.Root, hash)
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("driver package is unavailable")
	}
	manifestPath := filepath.Join(root, "inventory.json")
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil || !manifestInfo.Mode().IsRegular() || manifestInfo.Size() > 1<<20 {
		return fmt.Errorf("driver package inventory is unavailable")
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var inventory drivers.Inventory
	if err := json.Unmarshal(data, &inventory); err != nil {
		return err
	}
	if inventory.SHA256 != hash || drivers.CanonicalHash(inventory.Files) != hash || len(inventory.INFs) == 0 {
		return fmt.Errorf("driver package inventory differs")
	}
	want := make(map[string]drivers.File, len(inventory.Files))
	for _, file := range inventory.Files {
		if file.Path == "" || filepath.IsAbs(file.Path) || strings.Contains(file.Path, "..") || want[file.Path].Path != "" {
			return fmt.Errorf("invalid driver package inventory path")
		}
		want[file.Path] = file
	}
	for _, inf := range inventory.INFs {
		if _, ok := want[inf.Path]; !ok {
			return fmt.Errorf("driver package INF is missing from inventory")
		}
	}
	fileRoot := filepath.Join(root, "files")
	seen := map[string]bool{}
	err = filepath.WalkDir(fileRoot, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("driver package contains non-regular file")
		}
		rel, err := filepath.Rel(fileRoot, name)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		expected, ok := want[rel]
		if !ok {
			return fmt.Errorf("driver package contains unexpected file")
		}
		f, err := os.Open(name)
		if err != nil {
			return err
		}
		h := sha256.New()
		size, copyErr := io.Copy(h, f)
		closeErr := f.Close()
		if copyErr != nil || closeErr != nil {
			return fmt.Errorf("driver package file read failed")
		}
		if size != expected.Size || hex.EncodeToString(h.Sum(nil)) != expected.SHA256 {
			return fmt.Errorf("driver package file differs: %s", rel)
		}
		seen[rel] = true
		return nil
	})
	if err != nil {
		return err
	}
	if len(seen) != len(want) {
		return fmt.Errorf("driver package is incomplete")
	}
	return nil
}

func safeDriverRoot(root string) error {
	if !filepath.IsAbs(root) {
		return fmt.Errorf("driver root must be absolute")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("driver root is unsafe")
	}
	return nil
}

func validDriverHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
