package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"pxe-platform/internal/smbstorage"
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
	ImagesUID      string
	ImagesGID      string
	Systemctl      SMBSystemctl
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
	source := "//" + req.Server + "/" + req.Share
	if req.Subpath != "" {
		source += "/" + req.Subpath
	}
	unit := "[Unit]\nDescription=PXE Platform SMB ISO storage " + req.Key + "\nAfter=network-online.target\nWants=network-online.target\n\n" +
		"[Mount]\nWhat=" + source + "\nWhere=" + target + "\nType=cifs\nOptions=" + options + "\n\n" +
		"[Install]\nWantedBy=multi-user.target\n"
	if err := writeAtomic(unitPath, []byte(unit), 0644); err != nil {
		return err
	}
	if err := m.Systemctl.Run(ctx, "daemon-reload"); err != nil {
		return err
	}
	return m.Systemctl.Run(ctx, "enable", "--now", unitName)
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
