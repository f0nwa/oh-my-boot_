package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// WindowsISOManager persists one immutable source reference per profile and
// starts a dedicated systemd unit to mount and prepare it.
type WindowsISOManager struct {
	mu         sync.Mutex
	SourceRoot string
	ConfigRoot string
	MountRoot  string
	AssetRoot  string
	AnswerRoot string
	Systemctl  SMBSystemctl
	Mounter    WindowsISOMounter
}

func (m *WindowsISOManager) Check(ctx context.Context, req WindowsISORequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := resolveWindowsISOSource(m.SourceRoot, req); err != nil {
		return err
	}
	id := strconv.FormatInt(req.ID, 10)
	content, err := os.ReadFile(filepath.Join(m.ConfigRoot, id+".json"))
	if err != nil {
		return err
	}
	expected, err := json.Marshal(req)
	if err != nil || !bytes.Equal(content, expected) {
		return fmt.Errorf("Windows ISO profile source differs from prepared configuration")
	}
	target := filepath.Join(m.MountRoot, id)
	mounter := m.Mounter
	if mounter == nil {
		mounter = CommandWindowsISOMounter{}
	}
	mounted, err := mounter.IsMounted(ctx, target)
	if err != nil {
		return err
	}
	if !mounted {
		return fmt.Errorf("Windows ISO is not mounted")
	}
	if _, err := inspectMountedWindowsISO(target); err != nil {
		return err
	}
	return m.checkWindowsISOAssets(id)
}

func (m *WindowsISOManager) Prepare(ctx context.Context, req WindowsISORequest) (WindowsISOResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := resolveWindowsISOSource(m.SourceRoot, req); err != nil {
		return WindowsISOResult{}, err
	}
	if m.Systemctl == nil {
		return WindowsISOResult{}, fmt.Errorf("systemd control is unavailable")
	}
	content, err := json.Marshal(req)
	if err != nil {
		return WindowsISOResult{}, err
	}
	if err := os.MkdirAll(m.ConfigRoot, 0700); err != nil {
		return WindowsISOResult{}, err
	}
	id := strconv.FormatInt(req.ID, 10)
	configPath := filepath.Join(m.ConfigRoot, id+".json")
	if existing, err := os.ReadFile(configPath); err == nil {
		if !bytes.Equal(existing, content) {
			return WindowsISOResult{}, fmt.Errorf("Windows ISO profile source is immutable")
		}
	} else if os.IsNotExist(err) {
		if err := writeAtomic(configPath, content, 0600); err != nil {
			return WindowsISOResult{}, err
		}
	} else {
		return WindowsISOResult{}, err
	}
	unit := "pxe-winiso@" + id + ".service"
	if err := m.Systemctl.Run(ctx, "enable", "--now", unit); err != nil {
		return WindowsISOResult{}, err
	}
	installFile, err := inspectMountedWindowsISO(filepath.Join(m.MountRoot, id))
	if err != nil {
		return WindowsISOResult{}, err
	}
	if err := m.checkWindowsISOAssets(id); err != nil {
		return WindowsISOResult{}, err
	}
	return WindowsISOResult{InstallFile: installFile}, nil
}

func (m *WindowsISOManager) checkWindowsISOAssets(id string) error {
	for _, name := range []string{"boot.wim", "winpeshl.ini", "pxe-setup.cmd"} {
		if err := requireRegularISOFile(filepath.Join(m.AssetRoot, id), name); err != nil {
			return err
		}
	}
	return nil
}
