package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"omb/internal/network"
)

var validLocalSMBUsername = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,32}$`)

type LocalSMBUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Mode     string `json:"mode"`
}

type LocalSMBConfig struct {
	Enabled     bool           `json:"enabled"`
	ListenIP    string         `json:"listen_ip"`
	ReadOnlyAll bool           `json:"read_only_all"`
	Users       []LocalSMBUser `json:"users"`
}

type LocalSMBPasswordRequest struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type CommandRunner interface {
	Run(ctx context.Context, input, name string, args ...string) ([]byte, error)
}

type LocalSMBManager struct {
	mu               sync.Mutex
	SMBConfPath      string
	UserMapPath      string
	NetworkStatePath string
	StatePath        string
	LocalISORoot     string
	Runner           CommandRunner
}

func (m *LocalSMBManager) runner() CommandRunner {
	if m.Runner != nil {
		return m.Runner
	}
	return ExecDriverCommandRunner{}
}

func (m *LocalSMBManager) smbConfPath() string {
	if m.SMBConfPath != "" {
		return m.SMBConfPath
	}
	return "/etc/samba/smb.conf"
}

func (m *LocalSMBManager) userMapPath() string {
	if m.UserMapPath != "" {
		return m.UserMapPath
	}
	return "/etc/omb/samba-usermap.conf"
}

func (m *LocalSMBManager) networkStatePath() string {
	if m.NetworkStatePath != "" {
		return m.NetworkStatePath
	}
	return "/var/lib/omb-agent/network.json"
}

func (m *LocalSMBManager) statePath() string {
	if m.StatePath != "" {
		return m.StatePath
	}
	return "/var/lib/omb-agent/local-smb.json"
}

func (m *LocalSMBManager) saveState(cfg LocalSMBConfig) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal local smb state: %w", err)
	}
	_ = os.MkdirAll(filepath.Dir(m.statePath()), 0o750)
	if err := writeAtomic(m.statePath(), data, 0o600); err != nil {
		return fmt.Errorf("write local smb state: %w", err)
	}
	return nil
}

func (m *LocalSMBManager) loadState() (LocalSMBConfig, error) {
	data, err := os.ReadFile(m.statePath())
	if err != nil {
		if os.IsNotExist(err) {
			return LocalSMBConfig{Enabled: false}, nil
		}
		return LocalSMBConfig{}, fmt.Errorf("read local smb state: %w", err)
	}
	var cfg LocalSMBConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return LocalSMBConfig{}, fmt.Errorf("parse local smb state: %w", err)
	}
	return cfg, nil
}

func (m *LocalSMBManager) localISORoot() string {
	if m.LocalISORoot != "" {
		return m.LocalISORoot
	}
	return "/var/lib/omb-images/iso"
}

func (m *LocalSMBManager) readNetworkConfig() (iface, subnet string) {
	iface = "ens19"
	subnet = "10.30.0.0/24"

	data, err := os.ReadFile(m.networkStatePath())
	if err != nil {
		return iface, subnet
	}
	var cfg network.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return iface, subnet
	}
	if cfg.Interface != "" {
		iface = cfg.Interface
	}
	if cfg.Subnet != "" {
		subnet = cfg.Subnet
	}
	return iface, subnet
}

func (m *LocalSMBManager) ApplyConfig(ctx context.Context, cfg LocalSMBConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pxeIface, pxeSubnet := m.readNetworkConfig()
	runner := m.runner()

	if !cfg.Enabled {
		conf := renderDisabledSMBConf(pxeIface, pxeSubnet)
		if err := writeAtomic(m.smbConfPath(), []byte(conf), 0644); err != nil {
			return fmt.Errorf("write smb.conf: %w", err)
		}
		if err := m.saveState(cfg); err != nil {
			return err
		}

		_, _ = runner.Run(ctx, "", "smbcontrol", "smbd", "close-share", "iso")
		_, _ = runner.Run(ctx, "", "systemctl", "try-restart", "smbd")
		_, _ = runner.Run(ctx, "", "smbcontrol", "smbd", "reload-config")
		return nil
	}

	users := append([]LocalSMBUser(nil), cfg.Users...)
	sort.Slice(users, func(i, j int) bool {
		return users[i].ID < users[j].ID
	})

	if err := os.MkdirAll(m.localISORoot(), 0770); err != nil && !os.IsPermission(err) {
		return fmt.Errorf("create local iso directory: %w", err)
	}

	for _, u := range users {
		unixUser := fmt.Sprintf("omb-fs-%d", u.ID)
		_, _ = runner.Run(ctx, "", "useradd", "--system", "--no-create-home", "--shell", "/usr/sbin/nologin", "--comment", "omb-fs-user", unixUser)
	}

	var usermap strings.Builder
	validCount := 0
	for _, u := range users {
		if u.Mode == "ro" || u.Mode == "rw" {
			validCount++
			fmt.Fprintf(&usermap, "!omb-fs-%d = \"%s\"\n", u.ID, u.Username)
		}
	}
	if err := os.MkdirAll(filepath.Dir(m.userMapPath()), 0755); err != nil {
		return fmt.Errorf("create usermap directory: %w", err)
	}
	if err := writeAtomic(m.userMapPath(), []byte(usermap.String()), 0600); err != nil {
		return fmt.Errorf("write samba-usermap.conf: %w", err)
	}

	conf := renderEnabledSMBConf(renderSMBOptions{
		pxeIface:    pxeIface,
		pxeSubnet:   pxeSubnet,
		listenIP:    cfg.ListenIP,
		userMapPath: m.userMapPath(),
		isoRoot:     m.localISORoot(),
		readOnlyAll: cfg.ReadOnlyAll,
		users:       users,
	})

	if err := writeAtomic(m.smbConfPath(), []byte(conf), 0644); err != nil {
		return fmt.Errorf("write smb.conf: %w", err)
	}
	savedCfg := cfg
	savedCfg.Users = users
	if err := m.saveState(savedCfg); err != nil {
		return err
	}

	if validCount == 0 {
		_, _ = runner.Run(ctx, "", "smbcontrol", "smbd", "close-share", "iso")
	}
	_, _ = runner.Run(ctx, "", "systemctl", "try-restart", "smbd")
	_, _ = runner.Run(ctx, "", "smbcontrol", "smbd", "reload-config")
	return nil
}

func (m *LocalSMBManager) SyncPXENetwork(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.loadState()
	if err != nil {
		return err
	}
	pxeIface, pxeSubnet := m.readNetworkConfig()

	var conf string
	if cfg.Enabled {
		users := append([]LocalSMBUser(nil), cfg.Users...)
		sort.Slice(users, func(i, j int) bool {
			return users[i].ID < users[j].ID
		})
		conf = renderEnabledSMBConf(renderSMBOptions{
			pxeIface:    pxeIface,
			pxeSubnet:   pxeSubnet,
			listenIP:    cfg.ListenIP,
			userMapPath: m.userMapPath(),
			isoRoot:     m.localISORoot(),
			readOnlyAll: cfg.ReadOnlyAll,
			users:       users,
		})
	} else {
		conf = renderDisabledSMBConf(pxeIface, pxeSubnet)
	}

	candidate, err := writeCandidate(m.smbConfPath(), []byte(conf), 0o644)
	if err != nil {
		return fmt.Errorf("write smb.conf candidate: %w", err)
	}
	defer os.Remove(candidate)

	runner := m.runner()
	if out, err := runner.Run(ctx, "", "testparm", "-s", candidate); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("validate smb.conf with testparm: %w: %s", err, msg)
		}
		return fmt.Errorf("validate smb.conf with testparm: %w", err)
	}
	if err := os.Rename(candidate, m.smbConfPath()); err != nil {
		return fmt.Errorf("activate smb.conf: %w", err)
	}

	if out, err := runner.Run(ctx, "", "systemctl", "enable", "--now", "smbd"); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("enable smbd: %w: %s", err, msg)
		}
		return fmt.Errorf("enable smbd: %w", err)
	}
	if out, err := runner.Run(ctx, "", "systemctl", "try-restart", "smbd"); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("restart smbd: %w: %s", err, msg)
		}
		return fmt.Errorf("restart smbd: %w", err)
	}
	if out, err := runner.Run(ctx, "", "smbcontrol", "smbd", "reload-config"); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("reload smbd config: %w: %s", err, msg)
		}
		return fmt.Errorf("reload smbd config: %w", err)
	}
	return nil
}

func (m *LocalSMBManager) SetPassword(ctx context.Context, req LocalSMBPasswordRequest) error {
	if req.ID <= 0 {
		return fmt.Errorf("invalid user ID")
	}
	if !validLocalSMBUsername.MatchString(req.Username) {
		return fmt.Errorf("invalid username")
	}
	if len(req.Password) < 12 || strings.ContainsAny(req.Password, "\r\n") {
		return fmt.Errorf("password must be at least 12 characters and contain no newlines")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	runner := m.runner()
	unixUser := fmt.Sprintf("omb-fs-%d", req.ID)

	_, _ = runner.Run(ctx, "", "useradd", "--system", "--no-create-home", "--shell", "/usr/sbin/nologin", "--comment", "omb-fs-user", unixUser)

	input := fmt.Sprintf("%s\n%s\n", req.Password, req.Password)
	if _, err := runner.Run(ctx, input, "smbpasswd", "-a", "-s", unixUser); err != nil {
		return fmt.Errorf("set smb password: %w", err)
	}

	return nil
}

func renderDisabledSMBConf(pxeIface, pxeSubnet string) string {
	var b strings.Builder
	b.WriteString("[global]\n")
	b.WriteString("   server role = standalone server\n")
	b.WriteString("   workgroup = WORKGROUP\n")
	b.WriteString("   security = user\n")
	b.WriteString("   map to guest = Never\n")
	fmt.Fprintf(&b, "   interfaces = lo %s\n", pxeIface)
	b.WriteString("   bind interfaces only = yes\n")
	fmt.Fprintf(&b, "   hosts allow = 127.0.0.1 %s\n", pxeSubnet)
	b.WriteString("   smb ports = 445\n")
	b.WriteString("   server min protocol = SMB2_02\n")
	b.WriteString("   server signing = mandatory\n")
	b.WriteString("   load printers = no\n")
	b.WriteString("   printing = bsd\n")
	b.WriteString("   printcap name = /dev/null\n")
	b.WriteString("   disable spoolss = yes\n")
	b.WriteString("\n")
	b.WriteString("[ombisos]\n")
	b.WriteString("   path = /mnt/omb-iso/profiles\n")
	b.WriteString("   browseable = no\n")
	b.WriteString("   read only = yes\n")
	b.WriteString("   guest ok = no\n")
	b.WriteString("   valid users = omb-iso\n")
	fmt.Fprintf(&b, "   hosts allow = 127.0.0.1 %s\n", pxeSubnet)
	return b.String()
}

type renderSMBOptions struct {
	pxeIface    string
	pxeSubnet   string
	listenIP    string
	userMapPath string
	isoRoot     string
	readOnlyAll bool
	users       []LocalSMBUser
}

func renderEnabledSMBConf(opts renderSMBOptions) string {
	var b strings.Builder
	b.WriteString("[global]\n")
	b.WriteString("   server role = standalone server\n")
	b.WriteString("   workgroup = WORKGROUP\n")
	b.WriteString("   security = user\n")
	b.WriteString("   map to guest = Never\n")

	if opts.listenIP != "" {
		fmt.Fprintf(&b, "   interfaces = lo %s %s\n", opts.pxeIface, opts.listenIP)
		b.WriteString("   bind interfaces only = yes\n")
	} else {
		fmt.Fprintf(&b, "   interfaces = lo %s\n", opts.pxeIface)
		b.WriteString("   bind interfaces only = no\n")
	}

	fmt.Fprintf(&b, "   username map = %s\n", opts.userMapPath)
	b.WriteString("   smb ports = 445\n")
	b.WriteString("   server min protocol = SMB2_02\n")
	b.WriteString("   server signing = mandatory\n")
	b.WriteString("   load printers = no\n")
	b.WriteString("   printing = bsd\n")
	b.WriteString("   printcap name = /dev/null\n")
	b.WriteString("   disable spoolss = yes\n")
	b.WriteString("\n")
	b.WriteString("[ombisos]\n")
	b.WriteString("   path = /mnt/omb-iso/profiles\n")
	b.WriteString("   browseable = no\n")
	b.WriteString("   read only = yes\n")
	b.WriteString("   guest ok = no\n")
	b.WriteString("   valid users = omb-iso\n")
	fmt.Fprintf(&b, "   hosts allow = 127.0.0.1 %s\n", opts.pxeSubnet)
	b.WriteString("\n")
	b.WriteString("[iso]\n")
	fmt.Fprintf(&b, "   path = %s\n", opts.isoRoot)
	b.WriteString("   browseable = yes\n")
	if opts.readOnlyAll {
		b.WriteString("   read only = yes\n")
	} else {
		b.WriteString("   read only = no\n")
	}
	b.WriteString("   guest ok = no\n")

	var validUsers []string
	var readList []string
	var writeList []string

	for _, u := range opts.users {
		unixUser := fmt.Sprintf("omb-fs-%d", u.ID)
		if u.Mode == "ro" {
			validUsers = append(validUsers, unixUser)
			readList = append(readList, unixUser)
		} else if u.Mode == "rw" {
			validUsers = append(validUsers, unixUser)
			if opts.readOnlyAll {
				readList = append(readList, unixUser)
			} else {
				writeList = append(writeList, unixUser)
			}
		}
	}

	if len(validUsers) == 0 {
		b.WriteString("   available = no\n")
	} else {
		fmt.Fprintf(&b, "   valid users = %s\n", strings.Join(validUsers, " "))
		if len(readList) > 0 {
			fmt.Fprintf(&b, "   read list = %s\n", strings.Join(readList, " "))
		}
		if len(writeList) > 0 {
			fmt.Fprintf(&b, "   write list = %s\n", strings.Join(writeList, " "))
		}
	}

	b.WriteString("   force user = omb-images\n")
	b.WriteString("   force group = omb-images\n")
	b.WriteString("   create mask = 0664\n")
	b.WriteString("   force create mode = 0664\n")
	b.WriteString("   directory mask = 2770\n")
	b.WriteString("   force directory mode = 2770\n")
	b.WriteString("   veto files = /._*/.DS_Store/Thumbs.db/desktop.ini/\n")
	b.WriteString("   delete veto files = yes\n")

	return b.String()
}

