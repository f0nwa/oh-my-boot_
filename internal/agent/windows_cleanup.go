package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// removeManagedDirectory removes one agent-owned directory, never a link or root.
func removeManagedDirectory(root, child string) error {
	if !validManagedChild(child) {
		return fmt.Errorf("invalid managed asset name")
	}
	if err := requirePlainDirectory(root); err != nil {
		return err
	}
	target := filepath.Join(root, child)
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("managed asset is not a plain directory")
	}
	return os.RemoveAll(target)
}

func validManagedChild(name string) bool {
	if len(name) == 64 {
		for _, c := range name {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				return false
			}
		}
		return true
	}
	id, err := strconv.ParseInt(name, 10, 64)
	return err == nil && id > 0 && strconv.FormatInt(id, 10) == name
}

func requirePlainDirectory(root string) error {
	if root == "" || !filepath.IsAbs(root) {
		return fmt.Errorf("managed asset root is invalid")
	}
	for part := filepath.Clean(root); part != string(filepath.Separator); part = filepath.Dir(part) {
		info, err := os.Lstat(part)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("managed asset root has a non-directory or symlink component")
		}
	}
	return nil
}

func removeManagedFile(root, name string) error {
	if !validManagedChild(name) {
		return fmt.Errorf("invalid managed asset name")
	}
	if err := requirePlainDirectory(root); err != nil {
		return err
	}
	target := filepath.Join(root, name+".json")
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("managed asset is not a regular file")
	}
	return os.Remove(target)
}

func (m *WindowsISOManager) RemoveProfile(ctx context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id <= 0 {
		return fmt.Errorf("invalid Windows profile ID")
	}
	name := strconv.FormatInt(id, 10)
	for _, root := range []string{m.ConfigRoot, m.MountRoot, m.AssetRoot} {
		if err := requirePlainDirectory(root); err != nil {
			return err
		}
	}
	if m.Systemctl == nil {
		return fmt.Errorf("systemd control is unavailable")
	}
	target := filepath.Join(m.MountRoot, name)
	if info, err := os.Lstat(target); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Windows ISO mount target is unsafe")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := m.Systemctl.Run(ctx, "disable", "--now", "pxe-winiso@"+name+".service"); err != nil {
		return err
	}
	if info, err := os.Lstat(target); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Windows ISO mount target is unsafe")
		}
		mounter := m.Mounter
		if mounter == nil {
			mounter = CommandWindowsISOMounter{}
		}
		mounted, err := mounter.IsMounted(ctx, target)
		if err != nil {
			return err
		}
		if mounted {
			if err := mounter.Unmount(ctx, target); err != nil {
				return err
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := removeManagedDirectory(m.AssetRoot, name); err != nil {
		return err
	}
	if err := removeManagedDirectory(m.MountRoot, name); err != nil {
		return err
	}
	return removeManagedFile(m.ConfigRoot, name)
}

func (m *WindowsISOManager) RemoveAnswer(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id <= 0 {
		return fmt.Errorf("invalid Windows answer ID")
	}
	return removeManagedDirectory(m.AnswerRoot, strconv.FormatInt(id, 10))
}
