package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// WindowsISOManager persists one immutable source reference per profile and
// starts a dedicated systemd unit to mount and prepare it.
type WindowsISOManager struct {
	mu                sync.Mutex
	SourceRoot        string
	SourceRoots       []string
	LocalRoot         string
	ConfigRoot        string
	MountRoot         string
	AssetRoot         string
	AnswerRoot        string
	BootImageRoot     string
	InstallAssetRoot  string
	InstallConfigRoot string
	InstallMountRoot  string
	Template          []byte
	Winpeshl          []byte
	Password          string
	Systemctl         SMBSystemctl
	Mounter           WindowsISOMounter
	InstallMounter    InstallImageMounter
	WIMCommands       DriverCommandRunner
}

func (m *WindowsISOManager) allowedRoots() []string {
	if len(m.SourceRoots) > 0 {
		return m.SourceRoots
	}
	if m.SourceRoot != "" {
		return []string{m.SourceRoot}
	}
	return nil
}

func (m *WindowsISOManager) Check(ctx context.Context, req WindowsISORequest) (WindowsISOResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := resolveWindowsISOSourceRoots(m.allowedRoots(), req, m.LocalRoot); err != nil {
		return WindowsISOResult{}, err
	}
	id := strconv.FormatInt(req.ID, 10)
	content, err := os.ReadFile(filepath.Join(m.ConfigRoot, id+".json"))
	if err != nil {
		return WindowsISOResult{}, err
	}
	if !sameStoredWindowsISORequest(content, req) {
		return WindowsISOResult{}, fmt.Errorf("Windows ISO profile source differs from prepared configuration")
	}
	target := filepath.Join(m.MountRoot, id)
	mounter := m.Mounter
	if mounter == nil {
		mounter = CommandWindowsISOMounter{}
	}
	mounted, err := mounter.IsMounted(ctx, target)
	if err != nil {
		return WindowsISOResult{}, err
	}
	if !mounted {
		return WindowsISOResult{}, fmt.Errorf("Windows ISO is not mounted")
	}
	return m.inspectAndCheckProfile(ctx, target, id, req.BootMode, req.ID)
}

func (m *WindowsISOManager) Prepare(ctx context.Context, req WindowsISORequest) (WindowsISOResult, error) {
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
	unit := "omb-winiso@" + id + ".service"
	if err := m.Systemctl.Run(ctx, "enable", "--now", unit); err != nil {
		return WindowsISOResult{}, err
	}
	return m.inspectAndCheckProfile(ctx, filepath.Join(m.MountRoot, id), id, req.BootMode, req.ID)
}

func (m *WindowsISOManager) inspectAndCheckProfile(ctx context.Context, target, id, mode string, profileID int64) (WindowsISOResult, error) {
	installFile, err := inspectMountedISO(target, mode)
	if err != nil {
		return WindowsISOResult{}, err
	}
	if err := m.checkWindowsISOAssets(id, mode); err != nil {
		return WindowsISOResult{}, err
	}
	if mode != "" {
		return WindowsISOResult{InstallFile: installFile}, nil
	}
	if err := ensureAnswerStartupAsset(filepath.Join(m.AssetRoot, id), profileID, nil); err != nil {
		return WindowsISOResult{}, err
	}
	wimInfo, wimErr := inspectWindowsWIMInfo(ctx, m.WIMCommands, filepath.Join(target, "sources", installFile))
	if wimErr != nil {
		log.Printf("windows-iso %s: wim info check unavailable (%v); continuing with wim_info.known=false", id, wimErr)
		wimInfo = WindowsWIMInfo{}
	}
	bootWIMInfo, bootErr := inspectWindowsWIMInfo(ctx, m.WIMCommands, filepath.Join(m.AssetRoot, id, "boot.wim"))
	if bootErr != nil {
		log.Printf("windows-iso %s: boot.wim info check unavailable (%v); continuing with boot_wim_info.known=false", id, bootErr)
		bootWIMInfo = WindowsWIMInfo{}
	}
	return WindowsISOResult{InstallFile: installFile, WIMInfo: wimInfo, BootWIMInfo: bootWIMInfo}, nil
}

// InspectProfileBootWIM reads WIM metadata directly from a prepared Windows ISO
// profile's cached boot.wim under AssetRoot/<profileID>/boot.wim.
func (m *WindowsISOManager) InspectProfileBootWIM(ctx context.Context, profileID int64) (WindowsWIMInfo, error) {
	if profileID <= 0 {
		return WindowsWIMInfo{}, fmt.Errorf("invalid Windows profile ID")
	}
	if m.AssetRoot == "" {
		return WindowsWIMInfo{}, fmt.Errorf("Windows profile asset root is unavailable")
	}
	dir := filepath.Join(m.AssetRoot, strconv.FormatInt(profileID, 10))
	if err := requireRegularISOFile(dir, "boot.wim"); err != nil {
		return WindowsWIMInfo{}, err
	}
	return inspectWindowsWIMInfo(ctx, m.WIMCommands, filepath.Join(dir, "boot.wim"))
}

func (m *WindowsISOManager) checkWindowsISOAssets(id, mode string) error {
	for _, name := range isoAssetFiles(mode) {
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
	return stored.BootMode == req.BootMode && stored.ID == req.ID && stored.StorageRoot == req.StorageRoot && stored.RelativePath == req.RelativePath && stored.Size == req.Size && stored.Modified.Equal(req.Modified)
}
