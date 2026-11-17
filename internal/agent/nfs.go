package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"omb/internal/nfsstorage"
)

type NFSMountRequest struct {
	Key        string `json:"key"`
	Server     string `json:"server"`
	ExportPath string `json:"export_path"`
	Version    string `json:"version"`
}

type NFSMountResult struct {
	Writable bool `json:"writable"`
}

type NFSManager struct {
	mu         sync.Mutex
	ConfigDir  string
	UnitDir    string
	MountRoot  string
	UploadRoot string
	ImagesUID  string
	ImagesGID  string
	Systemctl  SMBSystemctl
	Probe      func(path string) error
	WriteProbe func(path string, uid, gid string) error
}

func (m *NFSManager) configDir() string {
	if m.ConfigDir != "" {
		return m.ConfigDir
	}
	return "/etc/omb/nfs"
}

func (m *NFSManager) unitDir() string {
	if m.UnitDir != "" {
		return m.UnitDir
	}
	return "/etc/systemd/system"
}

func (m *NFSManager) mountRoot() string {
	if m.MountRoot != "" {
		return m.MountRoot
	}
	return "/mnt/omb-nfs"
}

func (m *NFSManager) uploadRoot() string {
	if m.UploadRoot != "" {
		return m.UploadRoot
	}
	return "/mnt/omb-nfs-upload"
}

func systemdMountUnitName(target string) string {
	var name strings.Builder
	for _, c := range []byte(strings.TrimPrefix(filepath.Clean(target), "/")) {
		switch {
		case c == '/':
			name.WriteByte('-')
		case c == '-':
			name.WriteString(`\x2d`)
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '.', c == ':':
			name.WriteByte(c)
		default:
			fmt.Fprintf(&name, `\x%02x`, c)
		}
	}
	return name.String() + ".mount"
}

func nfsUnitOptions(version string, rw bool) (string, error) {
	mode := "ro"
	if rw {
		mode = "rw"
	}
	switch version {
	case "4.1":
		return mode + ",nosuid,nodev,noexec,hard,proto=tcp,vers=4.1", nil
	case "3":
		return mode + ",nosuid,nodev,noexec,hard,proto=tcp,vers=3,nolock", nil
	default:
		return "", fmt.Errorf("unsupported NFS version: %q", version)
	}
}

func renderNFSUnit(desc, source, target, options string) string {
	return "[Unit]\nDescription=" + desc + "\nAfter=network-online.target\nWants=network-online.target\n\n" +
		"[Mount]\nWhat=" + source + "\nWhere=" + target + "\nType=nfs\nOptions=" + options + "\nTimeoutSec=15s\n\n" +
		"[Install]\nWantedBy=multi-user.target\n"
}

func (m *NFSManager) mountUploadUnit(ctx context.Context, req NFSMountRequest) error {
	options, err := nfsUnitOptions(req.Version, true)
	if err != nil {
		return err
	}
	target := filepath.Join(m.uploadRoot(), req.Key)
	unitName := systemdMountUnitName(target)
	unitPath := filepath.Join(m.unitDir(), unitName)
	source := nfsstorage.Source(req.Server, req.ExportPath)

	if info, err := os.Lstat(unitPath); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to replace non-regular upload mount unit")
		}
		_ = m.Systemctl.Run(ctx, "stop", unitName)
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := os.MkdirAll(m.unitDir(), 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(target, 0755); err != nil {
		return err
	}

	unitContent := renderNFSUnit("Oh My Boot NFS ISO upload "+req.Key, source, target, options)
	if err := writeAtomic(unitPath, []byte(unitContent), 0644); err != nil {
		return err
	}
	if err := m.Systemctl.Run(ctx, "daemon-reload"); err != nil {
		return err
	}
	return m.Systemctl.Run(ctx, "enable", "--now", unitName)
}

func (m *NFSManager) cleanupUploadUnit(ctx context.Context, key string) {
	uploadTarget := filepath.Join(m.uploadRoot(), key)
	uploadUnitName := systemdMountUnitName(uploadTarget)
	uploadUnitPath := filepath.Join(m.unitDir(), uploadUnitName)

	_ = m.Systemctl.Run(ctx, "disable", "--now", uploadUnitName)
	_ = os.Remove(uploadTarget)
	_ = os.Remove(uploadUnitPath)
	_ = m.Systemctl.Run(ctx, "daemon-reload")
}

func (m *NFSManager) Mount(ctx context.Context, req NFSMountRequest) (NFSMountResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := nfsstorage.Validate(req.Key, req.Server, req.ExportPath, req.Version); err != nil {
		return NFSMountResult{}, err
	}
	if m.Systemctl == nil || m.ImagesUID == "" || m.ImagesGID == "" {
		return NFSMountResult{}, fmt.Errorf("NFS mount manager is not configured")
	}

	target := filepath.Join(m.mountRoot(), req.Key)
	unitName := systemdMountUnitName(target)
	unitPath := filepath.Join(m.unitDir(), unitName)
	source := nfsstorage.Source(req.Server, req.ExportPath)

	uploadTarget := filepath.Join(m.uploadRoot(), req.Key)
	uploadUnitName := systemdMountUnitName(uploadTarget)
	uploadUnitPath := filepath.Join(m.unitDir(), uploadUnitName)
	if info, err := os.Lstat(uploadUnitPath); err == nil {
		if !info.Mode().IsRegular() {
			return NFSMountResult{}, fmt.Errorf("refusing to replace non-regular upload mount unit")
		}
		if err := m.Systemctl.Run(ctx, "stop", uploadUnitName); err != nil {
			return NFSMountResult{}, err
		}
	} else if !os.IsNotExist(err) {
		return NFSMountResult{}, err
	}

	options, err := nfsUnitOptions(req.Version, false)
	if err != nil {
		return NFSMountResult{}, err
	}

	if err := os.MkdirAll(m.configDir(), 0700); err != nil {
		return NFSMountResult{}, err
	}
	if err := os.MkdirAll(m.unitDir(), 0755); err != nil {
		return NFSMountResult{}, err
	}
	if err := os.MkdirAll(target, 0755); err != nil {
		return NFSMountResult{}, err
	}

	if info, err := os.Lstat(unitPath); err == nil {
		if !info.Mode().IsRegular() {
			return NFSMountResult{}, fmt.Errorf("refusing to replace non-regular mount unit")
		}
		if err := m.Systemctl.Run(ctx, "stop", unitName); err != nil {
			return NFSMountResult{}, err
		}
	} else if !os.IsNotExist(err) {
		return NFSMountResult{}, err
	}

	unitContent := renderNFSUnit("Oh My Boot NFS ISO storage "+req.Key, source, target, options)
	if err := writeAtomic(unitPath, []byte(unitContent), 0644); err != nil {
		return NFSMountResult{}, err
	}
	if err := m.Systemctl.Run(ctx, "daemon-reload"); err != nil {
		return NFSMountResult{}, err
	}
	if err := m.Systemctl.Run(ctx, "enable", "--now", unitName); err != nil {
		return NFSMountResult{}, err
	}

	probe := m.Probe
	if probe == nil {
		probe = probeDir
	}
	if err := probe(target); err != nil {
		return NFSMountResult{}, fmt.Errorf("NFS export is not readable: %w", err)
	}

	cfgPath := filepath.Join(m.configDir(), req.Key+".json")
	cfgBytes, err := json.Marshal(req)
	if err != nil {
		return NFSMountResult{}, err
	}
	if err := writeAtomic(cfgPath, cfgBytes, 0600); err != nil {
		return NFSMountResult{}, err
	}

	// Auto-detect RW upload support
	if err := m.mountUploadUnit(ctx, req); err != nil {
		m.cleanupUploadUnit(ctx, req.Key)
		return NFSMountResult{Writable: false}, nil
	}

	writeProbe := m.WriteProbe
	if writeProbe == nil {
		writeProbe = defaultNFSWriteProbe
	}
	if err := writeProbe(uploadTarget, m.ImagesUID, m.ImagesGID); err != nil {
		m.cleanupUploadUnit(ctx, req.Key)
		return NFSMountResult{Writable: false}, nil
	}

	return NFSMountResult{Writable: true}, nil
}

func (m *NFSManager) EnableUpload(ctx context.Context, req NFSMountRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := nfsstorage.Validate(req.Key, req.Server, req.ExportPath, req.Version); err != nil {
		return err
	}
	if m.Systemctl == nil || m.UploadRoot == "" || m.ImagesUID == "" || m.ImagesGID == "" {
		return fmt.Errorf("NFS upload mount manager is not configured")
	}

	readUnitPath := filepath.Join(m.unitDir(), systemdMountUnitName(filepath.Join(m.mountRoot(), req.Key)))
	readUnit, err := os.ReadFile(readUnitPath)
	if err != nil {
		return err
	}
	source := nfsstorage.Source(req.Server, req.ExportPath)
	if !strings.Contains(string(readUnit), "What="+source+"\n") || !strings.Contains(string(readUnit), "vers="+req.Version) {
		return fmt.Errorf("NFS upload source or version does not match existing storage")
	}

	if err := m.mountUploadUnit(ctx, req); err != nil {
		m.cleanupUploadUnit(ctx, req.Key)
		return err
	}

	writeProbe := m.WriteProbe
	if writeProbe == nil {
		writeProbe = defaultNFSWriteProbe
	}
	uploadTarget := filepath.Join(m.uploadRoot(), req.Key)
	if err := writeProbe(uploadTarget, m.ImagesUID, m.ImagesGID); err != nil {
		m.cleanupUploadUnit(ctx, req.Key)
		return fmt.Errorf("NFS export is not writable: %w", err)
	}

	return nil
}

func (m *NFSManager) CheckUpload(ctx context.Context, key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.Systemctl == nil || m.ImagesUID == "" || m.ImagesGID == "" {
		return false, fmt.Errorf("NFS mount manager is not configured")
	}

	cfgPath := filepath.Join(m.configDir(), key+".json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return false, fmt.Errorf("failed to read NFS storage config: %w", err)
	}
	var req NFSMountRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return false, fmt.Errorf("invalid NFS storage config: %w", err)
	}
	if err := nfsstorage.Validate(req.Key, req.Server, req.ExportPath, req.Version); err != nil {
		return false, err
	}

	uploadTarget := filepath.Join(m.uploadRoot(), key)
	uploadUnitName := systemdMountUnitName(uploadTarget)

	writeProbe := m.WriteProbe
	if writeProbe == nil {
		writeProbe = defaultNFSWriteProbe
	}

	isActive := m.Systemctl.Run(ctx, "is-active", "--quiet", uploadUnitName) == nil

	if isActive {
		if err := writeProbe(uploadTarget, m.ImagesUID, m.ImagesGID); err != nil {
			m.cleanupUploadUnit(ctx, key)
			return false, nil
		}
		return true, nil
	}

	if err := m.mountUploadUnit(ctx, req); err != nil {
		m.cleanupUploadUnit(ctx, key)
		return false, nil
	}
	if err := writeProbe(uploadTarget, m.ImagesUID, m.ImagesGID); err != nil {
		m.cleanupUploadUnit(ctx, key)
		return false, nil
	}

	return true, nil
}

func (m *NFSManager) Remove(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := nfsstorage.Validate(key, "127.0.0.1", "/placeholder", "4.1"); err != nil {
		return err
	}
	if m.Systemctl == nil {
		return fmt.Errorf("NFS mount manager is not configured")
	}

	uploadTarget := filepath.Join(m.uploadRoot(), key)
	uploadUnitPath := filepath.Join(m.unitDir(), systemdMountUnitName(uploadTarget))
	if _, err := os.Lstat(uploadUnitPath); err == nil {
		if err := m.Systemctl.Run(ctx, "disable", "--now", filepath.Base(uploadUnitPath)); err != nil {
			return err
		}
		if err := os.Remove(uploadTarget); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.Remove(uploadUnitPath); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	target := filepath.Join(m.mountRoot(), key)
	unitPath := filepath.Join(m.unitDir(), systemdMountUnitName(target))
	if _, err := os.Lstat(unitPath); err == nil {
		if err := m.Systemctl.Run(ctx, "disable", "--now", filepath.Base(unitPath)); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	if err := m.Systemctl.Run(ctx, "daemon-reload"); err != nil {
		return err
	}

	cfgPath := filepath.Join(m.configDir(), key+".json")
	if err := os.Remove(cfgPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

func (m *NFSManager) RecoverMounts(ctx context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.Systemctl == nil {
		return nil, fmt.Errorf("NFS mount manager is not configured")
	}

	configs, err := filepath.Glob(filepath.Join(m.configDir(), "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(configs)

	var actions []string
	var problems []error
	for _, cfg := range configs {
		key := strings.TrimSuffix(filepath.Base(cfg), ".json")
		if key == "" || strings.ContainsAny(key, "/\\") {
			continue
		}
		roots := []string{m.mountRoot()}
		if m.uploadRoot() != "" {
			roots = append(roots, m.uploadRoot())
		}
		for _, root := range roots {
			unit := systemdMountUnitName(filepath.Join(root, key))
			if info, err := os.Lstat(filepath.Join(m.unitDir(), unit)); err != nil || !info.Mode().IsRegular() {
				continue
			}
			done, err := restartIfInactive(ctx, m.Systemctl, unit)
			if done {
				actions = append(actions, "restarted "+unit)
			}
			if err != nil {
				problems = append(problems, err)
			}
		}
	}
	return actions, errors.Join(problems...)
}

func (m *NFSManager) StaleMounts() []staleMount {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.Systemctl == nil {
		return nil
	}
	probe := m.Probe
	if probe == nil {
		probe = probeDir
	}
	var stale []staleMount
	for _, target := range m.mountTargets() {
		unit := systemdMountUnitName(target)
		if m.Systemctl.Run(context.Background(), "is-active", "--quiet", unit) != nil {
			continue
		}
		if err := probe(target); err != nil {
			stale = append(stale, staleMount{Unit: unit, Target: target, Err: err})
		}
	}
	return stale
}

func (m *NFSManager) RemountStale(ctx context.Context, stale []staleMount) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var actions []string
	var problems []error
	for _, mount := range stale {
		if err := m.Systemctl.Run(ctx, "restart", mount.Unit); err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", mount.Unit, err))
			continue
		}
		actions = append(actions, fmt.Sprintf("remounted %s (was unreadable: %v)", mount.Unit, mount.Err))
	}
	return actions, errors.Join(problems...)
}

func (m *NFSManager) mountTargets() []string {
	configs, err := filepath.Glob(filepath.Join(m.configDir(), "*.json"))
	if err != nil {
		return nil
	}
	sort.Strings(configs)
	roots := []string{m.mountRoot()}
	if m.uploadRoot() != "" {
		roots = append(roots, m.uploadRoot())
	}
	var targets []string
	for _, cfg := range configs {
		key := strings.TrimSuffix(filepath.Base(cfg), ".json")
		if key == "" || strings.ContainsAny(key, "/\\") {
			continue
		}
		for _, root := range roots {
			target := filepath.Join(root, key)
			if info, err := os.Lstat(filepath.Join(m.unitDir(), systemdMountUnitName(target))); err == nil && info.Mode().IsRegular() {
				targets = append(targets, target)
			}
		}
	}
	return targets
}

func ProbeWriteDir(path string) error {
	done := make(chan error, 1)
	go func() {
		file, err := os.CreateTemp(path, ".omb-write-check-*")
		if err != nil {
			done <- err
			return
		}
		name := file.Name()
		defer os.Remove(name)
		payload := []byte("omb-write-check")
		if _, err := file.Write(payload); err != nil {
			_ = file.Close()
			done <- err
			return
		}
		if err := file.Close(); err != nil {
			done <- err
			return
		}
		file, err = os.Open(name)
		if err != nil {
			done <- err
			return
		}
		actual := make([]byte, len(payload))
		_, err = io.ReadFull(file, actual)
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil && string(actual) != string(payload) {
			err = fmt.Errorf("write probe content differs")
		}
		if err != nil {
			done <- err
			return
		}
		done <- os.Remove(name)
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(probeTimeout):
		return fmt.Errorf("no answer in %s", probeTimeout)
	}
}

func defaultNFSWriteProbe(path string, uidStr, gidStr string) error {
	if os.Geteuid() == 0 {
		u64, errU := strconv.ParseUint(uidStr, 10, 32)
		if errU != nil {
			return fmt.Errorf("invalid UID %q: %w", uidStr, errU)
		}
		g64, errG := strconv.ParseUint(gidStr, 10, 32)
		if errG != nil {
			return fmt.Errorf("invalid GID %q: %w", gidStr, errG)
		}
		selfExe, err := os.Executable()
		if err != nil || selfExe == "" {
			return fmt.Errorf("cannot determine executable path: %w", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, selfExe, "--probe-write", path)
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Credential: &syscall.Credential{
				Uid: uint32(u64),
				Gid: uint32(g64),
			},
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("probe-write failed: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	return ProbeWriteDir(path)
}
