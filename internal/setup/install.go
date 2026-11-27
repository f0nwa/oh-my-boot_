package setup

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"omb/internal/winpe"
)

const (
	defaultStateDir = "/var/lib/omb-deployment"
	bootDSN         = "host=/var/run/postgresql dbname=omb_boot user=omb-boot"
	controlDSN      = "host=/var/run/postgresql dbname=omb_control user=omb-control"
	imagesDSN       = "host=/var/run/postgresql dbname=omb_images user=omb-images"
)

var (
	migrationIDRe  = regexp.MustCompile(`^[0-9]{4}_[a-z0-9_]+$`)
	migrationSHARe = regexp.MustCompile(`^[0-9a-f]{64}$`)
	smbIfacesRe    = regexp.MustCompile(`(?m)^ *interfaces *=.*$`)
	smbHostsRe     = regexp.MustCompile(`(?m)^ *hosts allow *=.*$`)
)

// Options holds all parameters required to initialize a fresh OMB server.
type Options struct {
	Lang              string
	ManagementIP      string
	PXEInterface      string
	PXEServerIP       string
	PXESubnet         string
	DHCPMode          string
	DHCPStart         string
	DHCPEnd           string
	AdminPassword     string
	AllowedInterfaces string
	StateDir          string
	InstallMode       string
	Channel           string
	SkipPXE           bool
	Version           string
}

// ResolveChannel validates an explicit release channel ("stable" or "dev") or,
// when explicit is empty, infers it from releaseJSONPath (falling back to "stable").
func ResolveChannel(explicit, releaseJSONPath string) (string, error) {
	if explicit != "" {
		if explicit != "stable" && explicit != "dev" {
			return "", fmt.Errorf("unsupported channel: %q (expected stable or dev)", explicit)
		}
		return explicit, nil
	}
	if releaseJSONPath != "" {
		if raw, err := os.ReadFile(releaseJSONPath); err == nil {
			var meta struct {
				Channel string `json:"channel"`
			}
			if json.Unmarshal(raw, &meta) == nil && (meta.Channel == "stable" || meta.Channel == "dev") {
				return meta.Channel, nil
			}
		}
	}
	return "stable", nil
}

// CmdSpec describes an external command execution.
type CmdSpec struct {
	Name  string
	Args  []string
	Env   []string
	Stdin []byte
}

// CmdRunner executes external commands.
type CmdRunner interface {
	Run(ctx context.Context, spec CmdSpec) ([]byte, error)
}

// ExecRunner is the default CmdRunner backed by os/exec.
type ExecRunner struct{}

// Run executes spec using os/exec and includes stderr in any returned error.
func (ExecRunner) Run(ctx context.Context, spec CmdSpec) ([]byte, error) {
	cmd := exec.CommandContext(ctx, spec.Name, spec.Args...)
	if len(spec.Env) > 0 {
		cmd.Env = append(os.Environ(), spec.Env...)
	}
	if spec.Stdin != nil {
		cmd.Stdin = bytes.NewReader(spec.Stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return stdout.Bytes(), fmt.Errorf("%s %s: %w: %s", spec.Name, strings.Join(spec.Args, " "), err, msg)
		}
		return stdout.Bytes(), fmt.Errorf("%s %s: %w", spec.Name, strings.Join(spec.Args, " "), err)
	}
	return stdout.Bytes(), nil
}

// Installer performs preflight validation and initial setup of an OMB server.
type Installer struct {
	RootDir     string
	Runner      CmdRunner
	Euid        func() int
	Exists      func(path string) bool
	LookupUser  func(name string) bool
	Chown       func(path, userName, groupName string) error
	PortCheck   func(checkPXE bool) error
	HealthCheck func(ctx context.Context, managementIP, bootURL string) error
	Progress    func(step, msg string)
}

func (in *Installer) rootDir() string {
	if in.RootDir == "" {
		return "/"
	}
	return filepath.Clean(in.RootDir)
}

func (in *Installer) resolve(p string) string {
	root := in.rootDir()
	if root == "/" {
		return p
	}
	if p == root || strings.HasPrefix(p, root+string(filepath.Separator)) {
		return p
	}
	return filepath.Join(root, strings.TrimPrefix(p, "/"))
}

func (in *Installer) runner() CmdRunner {
	if in.Runner != nil {
		return in.Runner
	}
	return ExecRunner{}
}

func (in *Installer) euid() int {
	if in.Euid != nil {
		return in.Euid()
	}
	return os.Geteuid()
}

func (in *Installer) exists(p string) bool {
	if in.Exists != nil {
		return in.Exists(p)
	}
	_, err := os.Lstat(in.resolve(p))
	return err == nil
}

func (in *Installer) lookupUser(name string) bool {
	if in.LookupUser != nil {
		return in.LookupUser(name)
	}
	_, err := user.Lookup(name)
	return err == nil
}

func (in *Installer) chown(path, userName, groupName string) error {
	if in.Chown != nil {
		return in.Chown(path, userName, groupName)
	}
	if in.rootDir() != "/" {
		return nil
	}
	u, err := user.Lookup(userName)
	if err != nil {
		return fmt.Errorf("lookup user %s: %w", userName, err)
	}
	g, err := user.LookupGroup(groupName)
	if err != nil {
		return fmt.Errorf("lookup group %s: %w", groupName, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return fmt.Errorf("parse uid for %s: %w", userName, err)
	}
	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return fmt.Errorf("parse gid for %s: %w", groupName, err)
	}
	return os.Chown(in.resolve(path), uid, gid)
}

func (in *Installer) checkPorts(checkPXE bool) error {
	if in.PortCheck != nil {
		return in.PortCheck(checkPXE)
	}
	return CheckPortConflicts(checkPXE)
}

func (in *Installer) progress(step, msg string) {
	if in.Progress != nil {
		in.Progress(step, msg)
	}
}

func (in *Installer) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return in.runner().Run(ctx, CmdSpec{Name: name, Args: args})
}

func (in *Installer) runWithStdin(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	return in.runner().Run(ctx, CmdSpec{Name: name, Args: args, Stdin: stdin})
}

// Preflight verifies that the host is clean and ready for initial OMB setup.
// Active smbd/nmbd services are intentionally allowed because online apt installation
// of samba starts them before omb-setup runs.
func (in *Installer) Preflight() error {
	if in.euid() != 0 {
		return fmt.Errorf("omb-setup must be run as root")
	}

	forbiddenPaths := []string{
		"/etc/omb",
		"/srv/omb/tftp",
		"/usr/local/bin/omb-control",
	}
	for _, p := range forbiddenPaths {
		if in.exists(p) {
			return fmt.Errorf("refusing to overwrite existing path: %s", p)
		}
	}

	for _, account := range []string{"omb-boot", "omb-control", "omb-images", "omb-iso"} {
		if in.lookupUser(account) {
			return fmt.Errorf("system account already exists: %s", account)
		}
	}

	if _, err := in.run(context.Background(), "systemctl", "is-active", "--quiet", "omb-dnsmasq"); err == nil {
		return fmt.Errorf("unexpected active service: omb-dnsmasq")
	}

	requiredAssets := []string{
		"/usr/share/omb/bootstrap",
		"/usr/share/omb/assets",
		"/usr/share/omb/samba",
		"/usr/share/omb/migrations/manifest.json",
		"/usr/lib/omb/fetch-ipxe",
	}
	for _, p := range requiredAssets {
		if !in.exists(p) {
			return fmt.Errorf("required package asset is missing: %s", p)
		}
	}

	if err := in.checkPorts(false); err != nil {
		return err
	}

	return nil
}

// Execute performs the full initial configuration of OMB on the host.
func (in *Installer) Execute(ctx context.Context, opts Options) error {
	lang := opts.Lang
	if lang == "" {
		lang = "ru"
	}
	if lang != "ru" && lang != "en" {
		return fmt.Errorf("unsupported language: %s", lang)
	}

	mgmtAddr, err := netip.ParseAddr(opts.ManagementIP)
	if err != nil || !mgmtAddr.Is4() {
		return fmt.Errorf("invalid management IPv4 address: %q", opts.ManagementIP)
	}
	if utf8.RuneCountInString(opts.AdminPassword) < 12 {
		return fmt.Errorf("admin password must be at least 12 characters")
	}

	stateDir := opts.StateDir
	if stateDir == "" {
		stateDir = defaultStateDir
	}
	installMode := opts.InstallMode
	if installMode == "" {
		installMode = "online"
	}
	if installMode != "online" && installMode != "offline" {
		return fmt.Errorf("unsupported install mode: %s", installMode)
	}
	channel, err := ResolveChannel(opts.Channel, in.resolve("/usr/share/omb/release.json"))
	if err != nil {
		return err
	}

	configurePXE := !opts.SkipPXE && (opts.DHCPMode == "full" || opts.DHCPMode == "proxy")
	var pxeCfgBytes []byte
	if configurePXE {
		cfg, err := BuildNetworkConfig(opts)
		if err != nil {
			return fmt.Errorf("invalid PXE network configuration: %w", err)
		}
		pxeCfgBytes, err = json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return fmt.Errorf("encode PXE network config: %w", err)
		}
		if err := in.checkPorts(true); err != nil {
			return err
		}
	}

	bootHost := opts.ManagementIP
	if !opts.SkipPXE && opts.PXEServerIP != "" {
		bootHost = opts.PXEServerIP
	}
	bootURL := fmt.Sprintf("http://%s:8080", bootHost)

	allowedIfaces := opts.AllowedInterfaces
	if allowedIfaces == "" {
		if !opts.SkipPXE && opts.PXEInterface != "" && opts.PXEInterface != "none" {
			allowedIfaces = opts.PXEInterface
		} else {
			allowedIfaces = "omb-pxe-off"
		}
	}

	// 1. Start PostgreSQL and ensure no OMB databases exist.
	in.progress("postgres", "Starting PostgreSQL")
	if _, err := in.run(ctx, "systemctl", "start", "postgresql.service"); err != nil {
		return fmt.Errorf("start postgresql: %w", err)
	}
	existingDBs, err := in.run(ctx, "runuser", "-u", "postgres", "--", "psql", "-Xqw", "-v", "ON_ERROR_STOP=1",
		"-d", "postgres", "-Atc", "SELECT datname FROM pg_database WHERE datname IN ('omb_boot','omb_control','omb_images')")
	if err != nil {
		return fmt.Errorf("check existing databases: %w", err)
	}
	if strings.TrimSpace(string(existingDBs)) != "" {
		return fmt.Errorf("an OMB database already exists: %s", strings.TrimSpace(string(existingDBs)))
	}

	// 2. Create system users, PostgreSQL roles, and databases.
	in.progress("databases", "Creating PostgreSQL roles and databases")
	for _, svc := range []string{"boot", "control", "images"} {
		account := "omb-" + svc
		dbName := "omb_" + svc
		if _, err := in.run(ctx, "useradd", "--system", "--user-group", "--home-dir", "/var/lib/"+account, "--shell", "/usr/sbin/nologin", account); err != nil {
			return fmt.Errorf("create user %s: %w", account, err)
		}
		if _, err := in.run(ctx, "runuser", "-u", "postgres", "--", "psql", "-Xqw", "-v", "ON_ERROR_STOP=1",
			"-d", "postgres", "-c", fmt.Sprintf(`CREATE ROLE "%s" LOGIN`, account)); err != nil {
			return fmt.Errorf("create role %s: %w", account, err)
		}
		if _, err := in.run(ctx, "runuser", "-u", "postgres", "--", "createdb", "--owner="+account, dbName); err != nil {
			return fmt.Errorf("create database %s: %w", dbName, err)
		}
	}

	// 3. Apply bootstrap SQL and seed schema_migrations.
	in.progress("bootstrap", "Initializing database schemas")
	for _, svc := range []string{"boot", "images", "control"} {
		sqlBytes, err := os.ReadFile(in.resolve("/usr/share/omb/bootstrap/" + svc + ".sql"))
		if err != nil {
			return fmt.Errorf("read bootstrap SQL for %s: %w", svc, err)
		}
		if _, err := in.runWithStdin(ctx, sqlBytes, "runuser", "-u", "omb-"+svc, "--",
			"psql", "-Xw", "-v", "ON_ERROR_STOP=1", "-d", "omb_"+svc); err != nil {
			return fmt.Errorf("apply bootstrap SQL for %s: %w", svc, err)
		}
	}
	if err := in.seedSchemaMigrations(ctx, "/usr/share/omb/migrations/manifest.json"); err != nil {
		return err
	}

	// 4. Create omb-iso system user.
	if _, err := in.run(ctx, "useradd", "--system", "--user-group", "--home-dir", "/var/lib/omb-iso", "--shell", "/usr/sbin/nologin", "omb-iso"); err != nil {
		return fmt.Errorf("create user omb-iso: %w", err)
	}

	// 5. Create directories and unpack boot assets.
	in.progress("assets", "Preparing directories and boot assets")
	if err := in.ensureDir("/srv/omb", 0o755, "", ""); err != nil {
		return err
	}
	if err := in.ensureDir("/etc/omb", 0o750, "root", "omb-control"); err != nil {
		return err
	}
	if err := in.ensureDir("/etc/omb/winiso", 0o750, "", ""); err != nil {
		return err
	}
	for _, dir := range []string{
		"/mnt/omb-smb",
		"/mnt/omb-smb-upload",
		"/mnt/omb-nfs",
		"/mnt/omb-nfs-upload",
		"/mnt/omb-iso",
		"/mnt/omb-iso/profiles",
		"/var/lib/omb-ubuntu-registry",
	} {
		if err := in.ensureDir(dir, 0o755, "", ""); err != nil {
			return err
		}
	}

	ipxeSHA, err := sha256File(in.resolve("/usr/share/omb/assets/ipxeboot.tar.gz"))
	if err != nil {
		return fmt.Errorf("hash ipxeboot.tar.gz: %w", err)
	}
	shimSHA, err := sha256File(in.resolve("/usr/share/omb/assets/ipxe-shimx64.efi"))
	if err != nil {
		return fmt.Errorf("hash ipxe-shimx64.efi: %w", err)
	}
	if _, err := in.run(ctx, "sh", "/usr/lib/omb/fetch-ipxe",
		"file:///usr/share/omb/assets/ipxeboot.tar.gz", ipxeSHA,
		"file:///usr/share/omb/assets/ipxe-shimx64.efi", shimSHA,
		"/srv/omb/tftp"); err != nil {
		return fmt.Errorf("unpack iPXE assets: %w", err)
	}
	if err := in.ensureDir("/srv/omb/tftp", 0o755, "", ""); err != nil {
		return err
	}
	if _, err := in.run(ctx, "runuser", "-u", "omb-boot", "--", "test", "-r", "/srv/omb/tftp/ipxeboot/x86_64/undionly.kpxe"); err != nil {
		return fmt.Errorf("verify iPXE boot file readability: %w", err)
	}

	for _, sub := range []string{
		"/srv/omb/tftp/windows",
		"/srv/omb/tftp/windows-profiles",
		"/srv/omb/tftp/windows-answers",
		"/srv/omb/tftp/windows-driver-wims",
		"/srv/omb/tftp/windows-driver-configs",
		"/srv/omb/tftp/ubuntu-sources",
	} {
		if err := in.ensureDir(sub, 0o755, "", ""); err != nil {
			return err
		}
	}
	if err := in.ensureDir("/srv/omb/tftp/ubuntu-autoinstall", 0o750, "omb-boot", "omb-control"); err != nil {
		return err
	}
	if err := copyFile(in.resolve("/usr/share/omb/assets/wimboot"), in.resolve("/srv/omb/tftp/windows/wimboot"), 0o644); err != nil {
		return fmt.Errorf("install wimboot: %w", err)
	}

	if err := in.ensureDir("/var/lib/omb-images", 0o750, "omb-images", "omb-images"); err != nil {
		return err
	}
	if err := in.ensureDir("/var/lib/omb-images/iso", os.ModeSetgid|0o770, "root", "omb-images"); err != nil {
		return err
	}
	if err := in.ensureDir("/var/lib/omb-control", 0o700, "omb-control", "omb-control"); err != nil {
		return err
	}
	if err := in.ensureDir(stateDir, 0o755, "", ""); err != nil {
		return err
	}

	// 6. Write service environment files.
	in.progress("config", "Writing service configuration")
	bootEnv := fmt.Sprintf("OMB_BOOT_BASE_URL=%s\nOMB_BOOT_DATABASE_URL=%q\n", bootURL, bootDSN)
	if !opts.SkipPXE && opts.PXESubnet != "" {
		bootEnv += fmt.Sprintf("OMB_BOOT_ALLOWED_SUBNETS=%s\n", opts.PXESubnet)
	}
	if err := writeFileAtomic(in.resolve("/etc/omb/boot.env"), []byte(bootEnv), 0o600); err != nil {
		return fmt.Errorf("write boot.env: %w", err)
	}
	if !opts.SkipPXE && opts.PXEServerIP != "" {
		if rawTmpl, err := os.ReadFile(in.resolve("/usr/share/omb/windows-profiles/omb-setup.cmd.in")); err == nil {
			if updatedTmpl, _, err := winpe.UpdateStartupServerIP(rawTmpl, opts.PXEServerIP); err == nil {
				if err := writeFileAtomic(in.resolve("/etc/omb/windows-profiles/omb-setup.cmd.in"), updatedTmpl, 0o644); err != nil {
					return fmt.Errorf("write omb-setup.cmd.in: %w", err)
				}
			}
		}
	}
	controlEnv := fmt.Sprintf("OMB_CONTROL_LISTEN=%s:8443\nOMB_CONTROL_INTERFACES=%s\nOMB_CONTROL_BOOT_BASE_URL=%s\nOMB_CONTROL_DATABASE_URL=%q\n",
		opts.ManagementIP, allowedIfaces, bootURL, controlDSN)
	if err := writeFileAtomic(in.resolve("/etc/omb/control.env"), []byte(controlEnv), 0o600); err != nil {
		return fmt.Errorf("write control.env: %w", err)
	}
	imagesEnv := fmt.Sprintf("OMB_IMAGES_DATABASE_URL=%q\n", imagesDSN)
	if err := writeFileAtomic(in.resolve("/etc/omb/images.env"), []byte(imagesEnv), 0o600); err != nil {
		return fmt.Errorf("write images.env: %w", err)
	}
	updateEnv := fmt.Sprintf("OMB_UPDATE_MANAGEMENT_IP=%s\n", opts.ManagementIP)
	if err := writeFileAtomic(in.resolve("/etc/omb/update.env"), []byte(updateEnv), 0o600); err != nil {
		return fmt.Errorf("write update.env: %w", err)
	}
	etcPubKey := in.resolve("/etc/omb/release-public.pem")
	if _, err := os.Stat(etcPubKey); os.IsNotExist(err) {
		bundledPubKey := in.resolve("/usr/share/omb/release-public.pem")
		if info, err := os.Stat(bundledPubKey); err == nil && info.Mode().IsRegular() {
			if err := copyFile(bundledPubKey, etcPubKey, 0o644); err != nil {
				return fmt.Errorf("install default release-public.pem: %w", err)
			}
		}
	}

	// 7. Generate secrets.key, self-signed TLS certificate, and omb-iso Samba password.
	in.progress("secrets", "Generating TLS certificate and keys")
	secretHex, err := randomHex(32)
	if err != nil {
		return fmt.Errorf("generate secrets.key: %w", err)
	}
	if err := writeFileAtomic(in.resolve("/etc/omb/secrets.key"), []byte(secretHex+"\n"), 0o600); err != nil {
		return fmt.Errorf("write secrets.key: %w", err)
	}

	keyPath := in.resolve("/var/lib/omb-control/control.key")
	crtPath := in.resolve("/var/lib/omb-control/control.crt")
	if _, err := in.run(ctx, "openssl", "req", "-x509", "-newkey", "rsa:3072", "-sha256", "-nodes", "-days", "90",
		"-subj", "/CN=omb-control", "-addext", "subjectAltName=IP:"+opts.ManagementIP,
		"-keyout", keyPath, "-out", crtPath); err != nil {
		return fmt.Errorf("generate TLS certificate: %w", err)
	}
	if err := in.chown("/etc/omb/secrets.key", "omb-control", "omb-control"); err != nil {
		return err
	}
	if err := in.chown("/var/lib/omb-control/control.key", "omb-control", "omb-control"); err != nil {
		return err
	}
	if err := in.chown("/var/lib/omb-control/control.crt", "omb-control", "omb-control"); err != nil {
		return err
	}
	if err := os.Chmod(in.resolve("/etc/omb/secrets.key"), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(keyPath, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(crtPath, 0o644); err != nil {
		return err
	}
	if _, err := in.run(ctx, "runuser", "-u", "omb-control", "--", "test", "-r", "/etc/omb/secrets.key"); err != nil {
		return fmt.Errorf("verify secrets.key readability: %w", err)
	}
	if err := copyFile(crtPath, filepath.Join(in.resolve(stateDir), "control.crt"), 0o644); err != nil {
		return fmt.Errorf("copy control.crt to state dir: %w", err)
	}

	isoPass, err := randomHex(24)
	if err != nil {
		return fmt.Errorf("generate omb-iso password: %w", err)
	}
	if err := writeFileAtomic(in.resolve("/etc/omb/omb-iso-password"), []byte(isoPass+"\n"), 0o600); err != nil {
		return fmt.Errorf("write omb-iso-password: %w", err)
	}
	if _, err := in.runWithStdin(ctx, []byte(isoPass+"\n"+isoPass+"\n"), "smbpasswd", "-s", "-a", "omb-iso"); err != nil {
		return fmt.Errorf("configure Samba password for omb-iso: %w", err)
	}

	// 8. Backup distribution smb.conf, write safe lo-only smb.conf, disable smbd/nmbd.
	in.progress("samba", "Configuring initial local-only Samba")
	etcSMB := in.resolve("/etc/samba/smb.conf")
	if _, err := os.Stat(etcSMB); err == nil {
		if err := copyFile(etcSMB, filepath.Join(in.resolve(stateDir), "smb.conf.distribution"), 0o600); err != nil {
			return fmt.Errorf("backup distribution smb.conf: %w", err)
		}
	}
	smbTemplate, err := os.ReadFile(in.resolve("/usr/share/omb/samba/smb.conf"))
	if err != nil {
		return fmt.Errorf("read smb.conf template: %w", err)
	}
	safeSMB := smbIfacesRe.ReplaceAll(smbTemplate, []byte("   interfaces = lo"))
	safeSMB = smbHostsRe.ReplaceAll(safeSMB, []byte("   hosts allow = 127.0.0.1"))

	tmpSMB, err := os.CreateTemp("", "omb-smb-*.conf")
	if err != nil {
		return fmt.Errorf("create temp smb.conf: %w", err)
	}
	tmpSMBPath := tmpSMB.Name()
	defer os.Remove(tmpSMBPath)
	if _, err := tmpSMB.Write(safeSMB); err != nil {
		tmpSMB.Close()
		return fmt.Errorf("write temp smb.conf: %w", err)
	}
	if err := tmpSMB.Close(); err != nil {
		return err
	}
	if _, err := in.run(ctx, "testparm", "-s", tmpSMBPath); err != nil {
		return fmt.Errorf("validate smb.conf with testparm: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(etcSMB), 0o755); err != nil {
		return err
	}
	if err := writeFileAtomic(etcSMB, safeSMB, 0o644); err != nil {
		return fmt.Errorf("write /etc/samba/smb.conf: %w", err)
	}
	if _, err := in.run(ctx, "systemctl", "disable", "--now", "smbd", "nmbd"); err != nil {
		return fmt.Errorf("disable smbd/nmbd: %w", err)
	}

	// 9. Register Local ISO storage and create initial admin.
	in.progress("admin", "Creating local storage and initial administrator")
	if _, err := in.run(ctx, "runuser", "-u", "omb-images", "--", "env",
		"OMB_IMAGES_DATABASE_URL="+imagesDSN,
		"/usr/bin/omb-imagesctl", "add-path", "-name", "Local", "-kind", "local", "-root", "/var/lib/omb-images/iso"); err != nil {
		return fmt.Errorf("register local ISO storage: %w", err)
	}
	if _, err := in.runWithStdin(ctx, []byte(opts.AdminPassword+"\n"), "runuser", "-u", "omb-control", "--", "env",
		"OMB_CONTROL_DATABASE_URL="+controlDSN,
		"/usr/bin/omb-controlctl", "create-admin", "-username", "admin", "-language", lang); err != nil {
		return fmt.Errorf("create initial administrator: %w", err)
	}

	// 10. Reload systemd, enable and start core services, verify health.
	in.progress("services", "Starting OMB services")
	if _, err := in.run(ctx, "systemctl", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w", err)
	}
	if _, err := in.run(ctx, "systemctl", "enable", "--now",
		"omb-boot.service", "omb-agent.service", "omb-images.service", "omb-control.service", "omb-update.socket"); err != nil {
		return fmt.Errorf("enable and start OMB services: %w", err)
	}
	if err := in.checkHealth(ctx, opts.ManagementIP, bootURL); err != nil {
		return fmt.Errorf("service health check failed: %w", err)
	}

	// 11. Optionally apply PXE network configuration via omb-agentctl (configures omb-dnsmasq and Samba).
	if configurePXE {
		in.progress("pxe", "Applying PXE network and Samba configuration")
		tmpNet, err := os.CreateTemp("", "omb-network-*.json")
		if err != nil {
			return fmt.Errorf("create temp network config: %w", err)
		}
		tmpNetPath := tmpNet.Name()
		defer os.Remove(tmpNetPath)
		if err := tmpNet.Chmod(0o600); err != nil {
			tmpNet.Close()
			return err
		}
		if _, err := tmpNet.Write(pxeCfgBytes); err != nil {
			tmpNet.Close()
			return fmt.Errorf("write temp network config: %w", err)
		}
		if err := tmpNet.Close(); err != nil {
			return err
		}
		if _, err := in.run(ctx, "/usr/bin/omb-agentctl", "-config", tmpNetPath); err != nil {
			return fmt.Errorf("apply PXE network via omb-agentctl: %w", err)
		}
	}

	// 12. Record deployment install mode and release channel.
	in.progress("state", "Recording installation metadata")
	if err := writeFileAtomic(filepath.Join(in.resolve(stateDir), "INSTALL_MODE"), []byte(installMode+"\n"), 0o644); err != nil {
		return fmt.Errorf("write INSTALL_MODE: %w", err)
	}
	channelPath := filepath.Join(in.resolve(stateDir), "CHANNEL")
	_, channelStatErr := os.Stat(channelPath)
	if installMode != "offline" || os.IsNotExist(channelStatErr) {
		if err := writeFileAtomic(channelPath, []byte(channel+"\n"), 0o600); err != nil {
			return fmt.Errorf("write CHANNEL: %w", err)
		}
	}

	return nil
}

type migrationManifest struct {
	Format     int              `json:"format"`
	Migrations []migrationEntry `json:"migrations"`
}

type migrationEntry struct {
	Database string `json:"database"`
	ID       string `json:"id"`
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
}

func (in *Installer) seedSchemaMigrations(ctx context.Context, manifestPath string) error {
	raw, err := os.ReadFile(in.resolve(manifestPath))
	if err != nil {
		return fmt.Errorf("read migrations manifest: %w", err)
	}
	var manifest migrationManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return fmt.Errorf("parse migrations manifest: %w", err)
	}
	if manifest.Format != 1 || manifest.Migrations == nil {
		return fmt.Errorf("invalid migrations manifest format")
	}

	const insertSQL = "INSERT INTO public.schema_migrations (id, sha256) VALUES (:'mig_id', :'mig_sha') ON CONFLICT (id) DO NOTHING;\n"
	for _, entry := range manifest.Migrations {
		switch entry.Database {
		case "omb_boot", "omb_images", "omb_control":
		default:
			return fmt.Errorf("unsupported migration database in manifest: %s", entry.Database)
		}
		if !migrationIDRe.MatchString(entry.ID) {
			return fmt.Errorf("invalid migration id in manifest: %s", entry.ID)
		}
		if !migrationSHARe.MatchString(entry.SHA256) {
			return fmt.Errorf("invalid migration sha256 in manifest: %s/%s", entry.Database, entry.ID)
		}
		dbUser := strings.ReplaceAll(entry.Database, "_", "-")
		if _, err := in.runWithStdin(ctx, []byte(insertSQL), "runuser", "-u", dbUser, "--",
			"psql", "-Xw", "-v", "ON_ERROR_STOP=1", "-d", entry.Database,
			"-v", "mig_id="+entry.ID, "-v", "mig_sha="+entry.SHA256); err != nil {
			return fmt.Errorf("seed migration %s/%s: %w", entry.Database, entry.ID, err)
		}
	}
	return nil
}

func (in *Installer) checkHealth(ctx context.Context, managementIP, bootURL string) error {
	if in.HealthCheck != nil {
		return in.HealthCheck(ctx, managementIP, bootURL)
	}

	crtPEM, err := os.ReadFile(in.resolve("/var/lib/omb-control/control.crt"))
	if err != nil {
		return fmt.Errorf("read control.crt for health check: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(crtPEM) {
		return fmt.Errorf("parse control.crt for health check")
	}

	httpsClient := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			Proxy:           nil,
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
	}
	httpClient := &http.Client{
		Timeout:   4 * time.Second,
		Transport: &http.Transport{Proxy: nil},
	}
	unixClient := func(sock string) *http.Client {
		return &http.Client{
			Timeout: 3 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", sock)
				},
			},
		}
	}

	var lastErr error
	for attempt := 0; attempt < 20; attempt++ {
		if err := in.probeOnce(ctx, httpsClient, httpClient, unixClient, managementIP, bootURL); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
	return lastErr
}

func (in *Installer) probeOnce(
	ctx context.Context,
	httpsClient, httpClient *http.Client,
	unixClient func(string) *http.Client,
	managementIP, bootURL string,
) error {
	for _, svc := range []string{"omb-boot.service", "omb-agent.service", "omb-images.service", "omb-control.service"} {
		if _, err := in.run(ctx, "systemctl", "is-active", "--quiet", svc); err != nil {
			return fmt.Errorf("service %s is not active: %w", svc, err)
		}
	}

	if err := checkHTTPStatus(ctx, httpsClient, fmt.Sprintf("https://%s:8443/login", managementIP), ""); err != nil {
		return fmt.Errorf("omb-control health: %w", err)
	}
	if err := checkHTTPStatus(ctx, unixClient("/run/omb-images/images.sock"), "http://localhost/catalog", ""); err != nil {
		return fmt.Errorf("omb-images health: %w", err)
	}
	if err := checkHTTPStatus(ctx, unixClient("/run/omb/agent.sock"), "http://localhost/status", ""); err != nil {
		return fmt.Errorf("omb-agent health: %w", err)
	}
	if err := checkHTTPStatus(ctx, httpClient, "http://127.0.0.1:8080/boot/menu.ipxe", "#!ipxe"); err != nil {
		return fmt.Errorf("omb-boot menu health: %w", err)
	}
	if err := checkHTTPStatus(ctx, httpClient, bootURL+"/assets/windows/wimboot", ""); err != nil {
		return fmt.Errorf("omb-boot wimboot health: %w", err)
	}
	return nil
}

func checkHTTPStatus(ctx context.Context, client *http.Client, rawURL, wantPrefix string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned HTTP %d", rawURL, resp.StatusCode)
	}
	if wantPrefix != "" {
		body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
		if err != nil {
			return err
		}
		if !strings.HasPrefix(string(body), wantPrefix) {
			return fmt.Errorf("%s did not start with %q", rawURL, wantPrefix)
		}
	}
	return nil
}

func (in *Installer) ensureDir(path string, mode os.FileMode, owner, group string) error {
	resolved := in.resolve(path)
	if err := os.MkdirAll(resolved, mode.Perm()); err != nil {
		return fmt.Errorf("mkdir %s: %w", path, err)
	}
	if owner != "" && group != "" {
		if err := in.chown(path, owner, group); err != nil {
			return err
		}
	}
	if err := os.Chmod(resolved, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func randomHex(numBytes int) (string, error) {
	b := make([]byte, numBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return writeFileAtomic(dst, data, mode)
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".omb-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}
