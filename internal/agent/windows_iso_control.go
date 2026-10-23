package agent

import (
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
	LocalRoot  string
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
	if _, err := resolveWindowsISOSource(m.SourceRoot, req, m.LocalRoot); err != nil {
		return err
	}
	id := strconv.FormatInt(req.ID, 10)
	content, err := os.ReadFile(filepath.Join(m.ConfigRoot, id+".json"))
	if err != nil {
		return err
	}
	if !sameStoredWindowsISORequest(content, req) {
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
	if _, err := resolveWindowsISOSource(m.SourceRoot, req, m.LocalRoot); err != nil {
		return WindowsISOResult{}, err
	}
	if m.Systemctl == nil {
		return WindowsISOResult{}, fmt.Errorf("systemd control is unavailable")
	}
	stored := req
	stored.Modified = req.Modified.UTC()
	content, err := json.Marshal(stored)
	if err != nil {
		return WindowsISOResult{}, err
	}
	if err := os.MkdirAll(m.ConfigRoot, 0700); err != nil {
		return WindowsISOResult{}, err
	}
	id := strconv.FormatInt(req.ID, 10)
	configPath := filepath.Join(m.ConfigRoot, id+".json")
	if existing, err := os.ReadFile(configPath); err == nil {
		if !sameStoredWindowsISORequest(existing, req) {
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

// sameStoredWindowsISORequest compares a prepared profile configuration with a
// request by value. The modification time is compared as an instant, so the
// same timestamp serialized in another time zone is accepted.
func sameStoredWindowsISORequest(content []byte, req WindowsISORequest) bool {
	var stored WindowsISORequest
	if err := json.Unmarshal(content, &stored); err != nil {
		return false
	}
	return stored.ID == req.ID && stored.StorageRoot == req.StorageRoot && stored.RelativePath == req.RelativePath && stored.Size == req.Size && stored.Modified.Equal(req.Modified)
}
