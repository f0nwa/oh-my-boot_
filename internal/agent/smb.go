package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"omb/internal/smbstorage"
)

type SMBMountRequest struct {
	Key      string `json:"key"`
	Server   string `json:"server"`
	Share    string `json:"share"`
	Subpath  string `json:"subpath"`
	Username string `json:"username"`
	Password string `json:"password"`
	Domain   string `json:"domain"`
}

type SMBAccountInfo struct {
	Username string `json:"username"`
	Domain   string `json:"domain,omitempty"`
}

type SMBSystemctl interface {
	Run(context.Context, ...string) error
}

type SMBCommandSystemctl struct{}

func (SMBCommandSystemctl) Run(ctx context.Context, args ...string) error {
	return run(ctx, "systemctl", args...)
}

type SMBManager struct {
	mu             sync.Mutex
	CredentialsDir string
	UnitDir        string
	MountRoot      string
	UploadRoot     string
	ImagesUID      string
	ImagesGID      string
	Systemctl      SMBSystemctl
	// Probe checks that a mounted share can be read. Nil uses probeDir.
	Probe      func(path string) error
	WriteProbe func(path string) error
}

func (m *SMBManager) EnableUpload(ctx context.Context, key, server, share, subpath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := smbstorage.Validate(key, server, share, subpath); err != nil {
		return err
	}
	if m.Systemctl == nil || m.UploadRoot == "" || m.ImagesUID == "" || m.ImagesGID == "" {
		return fmt.Errorf("SMB upload mount manager is not configured")
	}
	readUnitPath := filepath.Join(m.UnitDir, smbUnitName(filepath.Join(m.MountRoot, key)))
	readUnit, err := os.ReadFile(readUnitPath)
	if err != nil {
		return err
	}
	source := "//" + server + "/" + share
	if subpath != "" {
		source += "/" + subpath
	}
	if !strings.Contains(string(readUnit), "What="+source+"\n") {
		return fmt.Errorf("SMB upload source does not match existing storage")
	}
	credPath := filepath.Join(m.CredentialsDir, key+".cred")
	if info, err := os.Lstat(credPath); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("SMB credentials unavailable")
	}
	target := filepath.Join(m.UploadRoot, key)
	unitName := smbUnitName(target)
	unitPath := filepath.Join(m.UnitDir, unitName)
	if info, err := os.Lstat(unitPath); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to replace non-regular upload mount unit")
		}
	} else if !os.IsNotExist(err) {
		return err
	} else {
		if err := os.MkdirAll(target, 0755); err != nil {
			return err
		}
		options := "credentials=" + credPath + ",rw,nosuid,nodev,noexec,uid=" + m.ImagesUID + ",gid=" + m.ImagesGID + ",file_mode=0640,dir_mode=0750"
		unit := "[Unit]\nDescription=Oh My Boot SMB ISO upload " + key + "\nAfter=network-online.target\nWants=network-online.target\n\n" +
			"[Mount]\nWhat=" + source + "\nWhere=" + target + "\nType=cifs\nOptions=" + options + "\n\n" +
			"[Install]\nWantedBy=multi-user.target\n"
		if err := writeAtomic(unitPath, []byte(unit), 0644); err != nil {
			return err
		}
	}
	if err := m.Systemctl.Run(ctx, "daemon-reload"); err != nil {
		return err
	}
	if err := m.Systemctl.Run(ctx, "enable", "--now", unitName); err != nil {
		return err
	}
	probe := m.WriteProbe
	if probe == nil {
		probe = probeSMBWrite
	}
	if err := probe(target); err != nil {
		return fmt.Errorf("SMB share is not writable: %w", err)
	}
	return nil
}

func (m *SMBManager) Mount(ctx context.Context, req SMBMountRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := smbstorage.Validate(req.Key, req.Server, req.Share, req.Subpath); err != nil {
		return err
	}
	if !validCredential(req.Username, false) || !validCredential(req.Password, false) || !validCredential(req.Domain, true) {
		return fmt.Errorf("invalid SMB credentials")
	}
	if m.Systemctl == nil || m.ImagesUID == "" || m.ImagesGID == "" {
		return fmt.Errorf("SMB mount manager is not configured")
	}
	target := filepath.Join(m.MountRoot, req.Key)
	unitName := smbUnitName(target)
	unitPath := filepath.Join(m.UnitDir, unitName)
	credPath := filepath.Join(m.CredentialsDir, req.Key+".cred")
	source := "//" + req.Server + "/" + req.Share
	if req.Subpath != "" {
		source += "/" + req.Subpath
	}
	var uploadUnitName string
	if m.UploadRoot != "" {
		uploadUnitName = smbUnitName(filepath.Join(m.UploadRoot, req.Key))
		uploadUnitPath := filepath.Join(m.UnitDir, uploadUnitName)
		if info, err := os.Lstat(uploadUnitPath); err == nil {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("refusing to replace non-regular upload mount unit")
			}
			unit, err := os.ReadFile(uploadUnitPath)
			if err != nil || !strings.Contains(string(unit), "What="+source+"\n") {
				return fmt.Errorf("existing upload mount source differs from SMB storage")
			}
			if err := m.Systemctl.Run(ctx, "stop", uploadUnitName); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		} else {
			uploadUnitName = ""
		}
	}
	if err := os.MkdirAll(m.CredentialsDir, 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(m.UnitDir, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(target, 0755); err != nil {
		return err
	}
	if info, err := os.Lstat(unitPath); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to replace non-regular mount unit")
		}
		if err := m.Systemctl.Run(ctx, "stop", unitName); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := writeAtomic(credPath, []byte("username="+req.Username+"\npassword="+req.Password+"\n"+optionalDomain(req.Domain)), 0600); err != nil {
		return err
	}
	options := "credentials=" + credPath + ",ro,nosuid,nodev,noexec,uid=" + m.ImagesUID + ",gid=" + m.ImagesGID + ",file_mode=0440,dir_mode=0550"
	unit := "[Unit]\nDescription=Oh My Boot SMB ISO storage " + req.Key + "\nAfter=network-online.target\nWants=network-online.target\n\n" +
		"[Mount]\nWhat=" + source + "\nWhere=" + target + "\nType=cifs\nOptions=" + options + "\n\n" +
		"[Install]\nWantedBy=multi-user.target\n"
	if err := writeAtomic(unitPath, []byte(unit), 0644); err != nil {
		return err
	}
	if err := m.Systemctl.Run(ctx, "daemon-reload"); err != nil {
		return err
	}
	if err := m.Systemctl.Run(ctx, "enable", "--now", unitName); err != nil {
		return err
	}
	probe := m.Probe
	if probe == nil {
		probe = probeDir
	}
	if err := probe(target); err != nil {
		return fmt.Errorf("SMB share is not readable: %w", err)
	}
	if uploadUnitName != "" {
		return m.Systemctl.Run(ctx, "enable", "--now", uploadUnitName)
	}
	return nil
}

func (m *SMBManager) Remove(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := smbstorage.Validate(key, "127.0.0.1", "placeholder", ""); err != nil {
		return err
	}
	if m.Systemctl == nil {
		return fmt.Errorf("SMB mount manager is not configured")
	}
	target := filepath.Join(m.MountRoot, key)
	unitPath := filepath.Join(m.UnitDir, smbUnitName(target))
	credPath := filepath.Join(m.CredentialsDir, key+".cred")
	if m.UploadRoot != "" {
		uploadTarget := filepath.Join(m.UploadRoot, key)
		uploadUnitPath := filepath.Join(m.UnitDir, smbUnitName(uploadTarget))
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
	}
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
	if err := os.Remove(credPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (m *SMBManager) ListAccounts() map[string]SMBAccountInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]SMBAccountInfo)
	if m.CredentialsDir == "" {
		return out
	}
	files, err := filepath.Glob(filepath.Join(m.CredentialsDir, "*.cred"))
	if err != nil {
		return out
	}
	for _, path := range files {
		key := strings.TrimSuffix(filepath.Base(path), ".cred")
		if key == "" || strings.ContainsAny(key, "/\\") {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var info SMBAccountInfo
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimRight(line, "\r")
			if strings.HasPrefix(line, "username=") {
				info.Username = strings.TrimPrefix(line, "username=")
			} else if strings.HasPrefix(line, "domain=") {
				info.Domain = strings.TrimPrefix(line, "domain=")
			}
		}
		if info.Username != "" {
			out[key] = info
		}
	}
	return out
}

func optionalDomain(domain string) string {
	if domain == "" {
		return ""
	}
	return "domain=" + domain + "\n"
}

func validCredential(value string, optional bool) bool {
	if value == "" {
		return optional
	}
	return len(value) <= 1024 && !strings.HasPrefix(value, " ") && !strings.ContainsAny(value, "\x00\r\n")
}

func smbUnitName(target string) string {
	return systemdMountUnitName(target)
}
