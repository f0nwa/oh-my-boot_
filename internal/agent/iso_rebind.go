package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Rebind updates the stored configuration and restarts the systemd unit for a Windows profile
// after its ISO image has been migrated to another storage.
func (m *WindowsISOManager) Rebind(ctx context.Context, req WindowsISORequest) (WindowsISOResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := resolveWindowsISOSourceRoots(m.allowedRoots(), req, m.LocalRoot); err != nil {
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

	// Atomically overwrite the existing config with the new source
	if err := writeAtomic(configPath, content, 0600); err != nil {
		return WindowsISOResult{}, fmt.Errorf("cannot write rebind configuration: %w", err)
	}

	unit := "omb-winiso@" + id + ".service"
	if err := m.Systemctl.Run(ctx, "restart", unit); err != nil {
		return WindowsISOResult{}, fmt.Errorf("cannot restart profile unit %s: %w", unit, err)
	}

	return m.inspectAndCheckProfile(ctx, filepath.Join(m.MountRoot, id), id, req.BootMode, req.ID)
}

// Rebind updates the registry entry for an Ubuntu ISO source after it has been migrated
// to another storage.
func (m *UbuntuISOManager) Rebind(ctx context.Context, req UbuntuISORequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !filepath.IsAbs(m.RegistryRoot) {
		return fmt.Errorf("Ubuntu ISO rebind unavailable: invalid registry root")
	}

	source, err := m.Validate(ctx, req)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(m.RegistryRoot, 0755); err != nil {
		return err
	}

	id := strconv.FormatInt(req.ID, 10)
	contents, err := json.Marshal(struct {
		ID      int64  `json:"id"`
		SHA256  string `json:"sha256"`
		ISOPath string `json:"iso_path"`
	}{req.ID, req.SHA256, source})
	if err != nil {
		return err
	}

	configPath := filepath.Join(m.RegistryRoot, id+".json")
	return writeAtomic(configPath, contents, 0644)
}
