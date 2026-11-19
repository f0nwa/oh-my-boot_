package boot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// MaintenanceState lists the installations and boot asset namespaces currently
// paused for maintenance. New PXE clients see a [MAINTENANCE] marker and pause
// screen in menu.ipxe, and new HTTP downloads for blocked assets return 503.
// In-flight HTTP transfers and SMB sessions continue uninterrupted (Drain).
type MaintenanceState struct {
	InstallationIDs   []int64 `json:"installation_ids,omitempty"`
	WindowsProfileIDs []int64 `json:"windows_profile_ids,omitempty"`
	DriverConfigIDs   []int64 `json:"driver_config_ids,omitempty"`
	UbuntuSourceIDs   []int64 `json:"ubuntu_source_ids,omitempty"`
	BlockLegacyUbuntu bool    `json:"block_legacy_ubuntu,omitempty"`
}

func normalizeIDs(ids []int64) []int64 {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[int64]bool, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (s MaintenanceState) normalized() MaintenanceState {
	return MaintenanceState{
		InstallationIDs:   normalizeIDs(s.InstallationIDs),
		WindowsProfileIDs: normalizeIDs(s.WindowsProfileIDs),
		DriverConfigIDs:   normalizeIDs(s.DriverConfigIDs),
		UbuntuSourceIDs:   normalizeIDs(s.UbuntuSourceIDs),
		BlockLegacyUbuntu: s.BlockLegacyUbuntu,
	}
}

func (s MaintenanceState) Empty() bool {
	return len(s.InstallationIDs) == 0 && len(s.WindowsProfileIDs) == 0 && len(s.DriverConfigIDs) == 0 && len(s.UbuntuSourceIDs) == 0 && !s.BlockLegacyUbuntu
}

// MaintenanceManager stores the active maintenance state in memory and
// optionally persists it to disk so it survives omb-boot restarts.
type MaintenanceManager struct {
	mu    sync.RWMutex
	path  string
	state MaintenanceState
	inst  map[int64]bool
	win   map[int64]bool
	drv   map[int64]bool
	ubu   map[int64]bool
}

func NewMaintenanceManager(path string) *MaintenanceManager {
	m := &MaintenanceManager{path: path}
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			var saved MaintenanceState
			if json.Unmarshal(data, &saved) == nil {
				m.applyLocked(saved.normalized())
			}
		}
	}
	return m
}

func (m *MaintenanceManager) applyLocked(s MaintenanceState) {
	m.state = s
	m.inst = make(map[int64]bool, len(s.InstallationIDs))
	for _, id := range s.InstallationIDs {
		m.inst[id] = true
	}
	m.win = make(map[int64]bool, len(s.WindowsProfileIDs))
	for _, id := range s.WindowsProfileIDs {
		m.win[id] = true
	}
	m.drv = make(map[int64]bool, len(s.DriverConfigIDs))
	for _, id := range s.DriverConfigIDs {
		m.drv[id] = true
	}
	m.ubu = make(map[int64]bool, len(s.UbuntuSourceIDs))
	for _, id := range s.UbuntuSourceIDs {
		m.ubu[id] = true
	}
}

func (m *MaintenanceManager) Current() MaintenanceState {
	if m == nil {
		return MaintenanceState{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return MaintenanceState{
		InstallationIDs:   append([]int64(nil), m.state.InstallationIDs...),
		WindowsProfileIDs: append([]int64(nil), m.state.WindowsProfileIDs...),
		DriverConfigIDs:   append([]int64(nil), m.state.DriverConfigIDs...),
		UbuntuSourceIDs:   append([]int64(nil), m.state.UbuntuSourceIDs...),
		BlockLegacyUbuntu: m.state.BlockLegacyUbuntu,
	}
}

func (m *MaintenanceManager) Set(state MaintenanceState) error {
	if m == nil {
		return nil
	}
	clean := state.normalized()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.path != "" {
		data, err := json.Marshal(clean)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(m.path), 0o750); err != nil {
			return err
		}
		tmp := m.path + ".tmp"
		if err := os.WriteFile(tmp, data, 0o640); err != nil {
			return err
		}
		if err := os.Rename(tmp, m.path); err != nil {
			return err
		}
	}
	m.applyLocked(clean)
	return nil
}

// IsPathBlocked reports whether a new HTTP boot file request hits an asset
// currently in maintenance mode.
func (m *MaintenanceManager) IsPathBlocked(urlPath string) bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.win) == 0 && len(m.drv) == 0 && len(m.ubu) == 0 && !m.state.BlockLegacyUbuntu {
		return false
	}
	if rest, ok := strings.CutPrefix(urlPath, "/assets/windows-profiles/"); ok {
		if id, ok := leadingPositiveID(rest); ok && m.win[id] {
			return true
		}
	}
	if rest, ok := strings.CutPrefix(urlPath, "/assets/windows-driver-configs/"); ok {
		if id, ok := leadingPositiveID(rest); ok && m.drv[id] {
			return true
		}
	}
	if rest, ok := strings.CutPrefix(urlPath, "/assets/ubuntu/"); ok {
		if id, ok := leadingPositiveID(rest); ok && m.ubu[id] {
			return true
		}
	}
	if rest, ok := strings.CutPrefix(urlPath, "/images/ubuntu/"); ok {
		if id, ok := leadingPositiveID(rest); ok && m.ubu[id] {
			return true
		}
	}
	if m.state.BlockLegacyUbuntu && (urlPath == "/images/ubuntu-desktop.iso" || strings.HasPrefix(urlPath, "/assets/ubuntu/")) {
		return true
	}
	return false
}

// IsUbuntuSourceBlocked reports whether an Ubuntu source ID is in maintenance mode.
func (m *MaintenanceManager) IsUbuntuSourceBlocked(id int64) bool {
	if m == nil || id <= 0 {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.ubu[id]
}

func leadingPositiveID(rest string) (int64, bool) {
	seg, _, _ := strings.Cut(rest, "/")
	if seg == "" {
		return 0, false
	}
	id, err := strconv.ParseInt(seg, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != seg {
		return 0, false
	}
	return id, true
}

// RewriteMenu applies Approach 1 dynamically on GET /boot/menu.ipxe:
// blocked items get a [MAINTENANCE] suffix and their :install_N boot section
// is replaced with a pause notice returning to menu_root.
func (m *MaintenanceManager) RewriteMenu(raw []byte) []byte {
	if m == nil {
		return raw
	}
	m.mu.RLock()
	if len(m.inst) == 0 {
		m.mu.RUnlock()
		return raw
	}
	blocked := make(map[int64]bool, len(m.inst))
	for id := range m.inst {
		blocked[id] = true
	}
	m.mu.RUnlock()

	lines := strings.Split(string(raw), "\n")
	var out strings.Builder
	out.Grow(len(raw) + len(blocked)*160)
	skippingSection := false
	for i, line := range lines {
		if skippingSection {
			if strings.HasPrefix(line, ":") {
				skippingSection = false
			} else {
				continue
			}
		}
		if strings.HasPrefix(line, ":install_") {
			rawID := strings.TrimPrefix(line, ":install_")
			if id, err := strconv.ParseInt(rawID, 10, 64); err == nil && blocked[id] {
				fmt.Fprintf(&out, ":install_%d\necho\necho [MAINTENANCE] This installation is temporarily paused for maintenance.\nprompt Press any key to return to the menu... || echo -n\ngoto menu_root\n", id)
				skippingSection = true
				continue
			}
		}
		if strings.HasPrefix(line, "item install_") {
			rest := strings.TrimPrefix(line, "item install_")
			idStr, title, hasSpace := strings.Cut(rest, " ")
			if id, err := strconv.ParseInt(idStr, 10, 64); err == nil && blocked[id] {
				if !strings.Contains(line, "[MAINTENANCE]") {
					if hasSpace && title != "" {
						line = "item install_" + idStr + " " + title + " [MAINTENANCE]"
					} else {
						line = "item install_" + idStr + " [MAINTENANCE]"
					}
				}
			}
		}
		out.WriteString(line)
		if i < len(lines)-1 {
			out.WriteByte('\n')
		}
	}
	return []byte(out.String())
}
