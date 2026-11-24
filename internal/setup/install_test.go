package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omb/internal/network"
)

type recordedCmd struct {
	Name  string
	Args  []string
	Env   []string
	Stdin string
}

func (c recordedCmd) String() string {
	if len(c.Args) == 0 {
		return c.Name
	}
	return c.Name + " " + strings.Join(c.Args, " ")
}

type mockRunner struct {
	cmds    []recordedCmd
	handler func(spec CmdSpec) ([]byte, error)
}

func (m *mockRunner) Run(_ context.Context, spec CmdSpec) ([]byte, error) {
	m.cmds = append(m.cmds, recordedCmd{
		Name:  spec.Name,
		Args:  append([]string(nil), spec.Args...),
		Env:   append([]string(nil), spec.Env...),
		Stdin: string(spec.Stdin),
	})
	if m.handler != nil {
		return m.handler(spec)
	}
	return nil, nil
}

func setupCleanAssets(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{
		"usr/share/omb/bootstrap/boot.sql":    "-- boot schema\n",
		"usr/share/omb/bootstrap/images.sql":  "-- images schema\n",
		"usr/share/omb/bootstrap/control.sql": "-- control schema\n",
		"usr/share/omb/assets/ipxeboot.tar.gz": "fake-ipxe-tarball",
		"usr/share/omb/assets/ipxe-shimx64.efi": "fake-shim-efi",
		"usr/share/omb/assets/wimboot":         "fake-wimboot",
		"usr/share/omb/samba/smb.conf": "[global]\n   interfaces = ens19\n   hosts allow = 10.30.0.0/24\n\n[ombisos]\n   path = /mnt/omb-iso\n",
		"usr/share/omb/migrations/manifest.json": `{
  "format": 1,
  "migrations": [
    {"database": "omb_boot", "id": "0001_initial", "path": "omb_boot/0001_initial.sql", "sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
    {"database": "omb_images", "id": "0001_initial", "path": "omb_images/0001_initial.sql", "sha256": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
    {"database": "omb_control", "id": "0001_initial", "path": "omb_control/0001_initial.sql", "sha256": "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}
  ]
}`,
		"usr/lib/omb/fetch-ipxe": "#!/bin/sh\nexit 0\n",
		"etc/samba/smb.conf":     "[global]\n   workgroup = WORKGROUP\n",
	}
	for rel, content := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
}

func newPreflightInstaller(t *testing.T, root string, runner CmdRunner) *Installer {
	t.Helper()
	return &Installer{
		RootDir:    root,
		Runner:     runner,
		Euid:       func() int { return 0 },
		LookupUser: func(string) bool { return false },
	}
}

func TestPreflightRejectsNonRoot(t *testing.T) {
	root := t.TempDir()
	setupCleanAssets(t, root)
	runner := &mockRunner{
		handler: func(spec CmdSpec) ([]byte, error) {
			if spec.Name == "systemctl" && len(spec.Args) >= 3 && spec.Args[0] == "is-active" {
				return nil, errors.New("inactive")
			}
			return nil, nil
		},
	}
	in := newPreflightInstaller(t, root, runner)
	in.Euid = func() int { return 1000 }

	if err := in.Preflight(); err == nil {
		t.Fatal("expected Preflight to reject non-root")
	}
}

func TestPreflightRejectsExistingEtcOMB(t *testing.T) {
	root := t.TempDir()
	setupCleanAssets(t, root)
	if err := os.MkdirAll(filepath.Join(root, "etc/omb"), 0o750); err != nil {
		t.Fatal(err)
	}
	runner := &mockRunner{
		handler: func(spec CmdSpec) ([]byte, error) {
			if spec.Name == "systemctl" && len(spec.Args) >= 3 && spec.Args[0] == "is-active" {
				return nil, errors.New("inactive")
			}
			return nil, nil
		},
	}
	in := newPreflightInstaller(t, root, runner)

	if err := in.Preflight(); err == nil {
		t.Fatal("expected Preflight to reject existing /etc/omb")
	}
}

func TestPreflightRejectsActiveDnsmasq(t *testing.T) {
	root := t.TempDir()
	setupCleanAssets(t, root)
	runner := &mockRunner{
		handler: func(spec CmdSpec) ([]byte, error) {
			if spec.Name == "systemctl" && len(spec.Args) >= 3 && spec.Args[0] == "is-active" && strings.HasPrefix(spec.Args[2], "omb-dnsmasq") {
				return nil, nil // active
			}
			return nil, errors.New("inactive")
		},
	}
	in := newPreflightInstaller(t, root, runner)

	if err := in.Preflight(); err == nil {
		t.Fatal("expected Preflight to reject active omb-dnsmasq")
	}
}

func TestPreflightAllowsActiveSmbdAndNmbd(t *testing.T) {
	root := t.TempDir()
	setupCleanAssets(t, root)
	runner := &mockRunner{
		handler: func(spec CmdSpec) ([]byte, error) {
			if spec.Name == "systemctl" && len(spec.Args) >= 3 && spec.Args[0] == "is-active" {
				svc := spec.Args[2]
				if strings.HasPrefix(svc, "smbd") || strings.HasPrefix(svc, "nmbd") {
					return nil, nil // active from apt install ./omb_*.deb
				}
				return nil, errors.New("inactive")
			}
			return nil, nil
		},
	}
	in := newPreflightInstaller(t, root, runner)

	if err := in.Preflight(); err != nil {
		t.Fatalf("expected Preflight to allow active smbd/nmbd, got: %v", err)
	}
}

func TestPreflightRejectsMissingAssets(t *testing.T) {
	root := t.TempDir()
	setupCleanAssets(t, root)
	if err := os.Remove(filepath.Join(root, "usr/share/omb/migrations/manifest.json")); err != nil {
		t.Fatal(err)
	}
	runner := &mockRunner{
		handler: func(spec CmdSpec) ([]byte, error) {
			if spec.Name == "systemctl" && len(spec.Args) >= 3 && spec.Args[0] == "is-active" {
				return nil, errors.New("inactive")
			}
			return nil, nil
		},
	}
	in := newPreflightInstaller(t, root, runner)

	if err := in.Preflight(); err == nil {
		t.Fatal("expected Preflight to reject missing migrations/manifest.json")
	}
}

func TestExecuteDeferredPXE(t *testing.T) {
	root := t.TempDir()
	setupCleanAssets(t, root)

	var healthCalled bool
	var healthMgmtIP, healthBootURL string

	runner := &mockRunner{
		handler: func(spec CmdSpec) ([]byte, error) {
			// Simulate openssl creating key and cert files so copy to StateDir works.
			if spec.Name == "openssl" {
				for i := 0; i < len(spec.Args)-1; i++ {
					if spec.Args[i] == "-keyout" || spec.Args[i] == "-out" {
						if err := os.WriteFile(spec.Args[i+1], []byte("dummy-pem"), 0o600); err != nil {
							return nil, err
						}
					}
				}
			}
			return nil, nil
		},
	}

	in := &Installer{
		RootDir:    root,
		Runner:     runner,
		Euid:       func() int { return 0 },
		LookupUser: func(string) bool { return false },
		HealthCheck: func(_ context.Context, mgmtIP, bootURL string) error {
			healthCalled = true
			healthMgmtIP = mgmtIP
			healthBootURL = bootURL
			return nil
		},
	}

	opts := Options{
		Lang:              "en",
		ManagementIP:      "192.168.10.135",
		PXEInterface:      "none",
		DHCPMode:          "skip",
		SkipPXE:           true,
		AllowedInterfaces: "ens19",
		AdminPassword:     "SuperSecretPass123",
		InstallMode:       "online",
	}

	if err := in.Execute(context.Background(), opts); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Verify exact ordering of key commands.
	wantSubstrings := []string{
		"systemctl start postgresql.service",
		"CREATE ROLE \"omb-boot\" LOGIN",
		"createdb --owner=omb-boot omb_boot",
		"CREATE ROLE \"omb-control\" LOGIN",
		"createdb --owner=omb-control omb_control",
		"CREATE ROLE \"omb-images\" LOGIN",
		"createdb --owner=omb-images omb_images",
		"runuser -u omb-boot -- psql -Xw -v ON_ERROR_STOP=1 -d omb_boot",
		"runuser -u omb-images -- psql -Xw -v ON_ERROR_STOP=1 -d omb_images",
		"runuser -u omb-control -- psql -Xw -v ON_ERROR_STOP=1 -d omb_control",
		"mig_id=0001_initial",
		"useradd --system --user-group --home-dir /var/lib/omb-iso --shell /usr/sbin/nologin omb-iso",
		"sh /usr/lib/omb/fetch-ipxe file:///usr/share/omb/assets/ipxeboot.tar.gz",
		"openssl req -x509",
		"smbpasswd -s -a omb-iso",
		"testparm -s",
		"systemctl disable --now smbd nmbd",
		"/usr/bin/omb-imagesctl add-path -name Local -kind local -root /var/lib/omb-images/iso",
		"/usr/bin/omb-controlctl create-admin -username admin -language en",
		"systemctl daemon-reload",
		"systemctl enable --now omb-boot.service omb-agent.service omb-images.service omb-control.service",
	}

	lastIdx := -1
	for _, want := range wantSubstrings {
		found := -1
		for i := lastIdx + 1; i < len(runner.cmds); i++ {
			if strings.Contains(runner.cmds[i].String(), want) {
				found = i
				break
			}
		}
		if found == -1 {
			var all []string
			for i, c := range runner.cmds {
				all = append(all, fmt.Sprintf("[%d] %s", i, c.String()))
			}
			t.Fatalf("expected command containing %q after index %d\ncommands:\n%s", want, lastIdx, strings.Join(all, "\n"))
		}
		lastIdx = found
	}

	// Ensure omb-agentctl was NOT called in deferred PXE mode.
	for _, c := range runner.cmds {
		if strings.Contains(c.String(), "omb-agentctl") {
			t.Fatalf("unexpected omb-agentctl call in deferred PXE mode: %s", c.String())
		}
	}

	// Verify health check was invoked with management IP and management boot URL.
	if !healthCalled {
		t.Fatal("expected HealthCheck to be called")
	}
	if healthMgmtIP != "192.168.10.135" || healthBootURL != "http://192.168.10.135:8080" {
		t.Errorf("HealthCheck got (%q, %q), want (%q, %q)", healthMgmtIP, healthBootURL, "192.168.10.135", "http://192.168.10.135:8080")
	}

	// Verify generated files.
	assertFileContains(t, filepath.Join(root, "srv/omb/tftp/windows/wimboot"), "fake-wimboot")
	assertFileContains(t, filepath.Join(root, "etc/omb/boot.env"), "OMB_BOOT_BASE_URL=http://192.168.10.135:8080")
	assertFileContains(t, filepath.Join(root, "etc/omb/control.env"), "OMB_CONTROL_LISTEN=192.168.10.135:8443")
	assertFileContains(t, filepath.Join(root, "etc/omb/control.env"), "OMB_CONTROL_INTERFACES=ens19")
	assertFileContains(t, filepath.Join(root, "etc/omb/control.env"), "OMB_CONTROL_BOOT_BASE_URL=http://192.168.10.135:8080")
	assertFileContains(t, filepath.Join(root, "etc/omb/images.env"), `OMB_IMAGES_DATABASE_URL="host=/var/run/postgresql dbname=omb_images user=omb-images"`)
	assertFileExists(t, filepath.Join(root, "etc/omb/secrets.key"))
	assertFileContains(t, filepath.Join(root, "var/lib/omb-deployment/control.crt"), "dummy-pem")
	assertFileContains(t, filepath.Join(root, "var/lib/omb-deployment/smb.conf.distribution"), "workgroup = WORKGROUP")
	assertFileContains(t, filepath.Join(root, "etc/samba/smb.conf"), "interfaces = lo")
	assertFileContains(t, filepath.Join(root, "etc/samba/smb.conf"), "hosts allow = 127.0.0.1")
	assertFileContains(t, filepath.Join(root, "var/lib/omb-deployment/INSTALL_MODE"), "online")
}

func TestExecuteWithPXEFullDHCP(t *testing.T) {
	root := t.TempDir()
	setupCleanAssets(t, root)

	var agentCfg network.Config
	var agentCalled bool

	runner := &mockRunner{
		handler: func(spec CmdSpec) ([]byte, error) {
			if spec.Name == "openssl" {
				for i := 0; i < len(spec.Args)-1; i++ {
					if spec.Args[i] == "-keyout" || spec.Args[i] == "-out" {
						if err := os.WriteFile(spec.Args[i+1], []byte("dummy-pem"), 0o600); err != nil {
							return nil, err
						}
					}
				}
			}
			if spec.Name == "/usr/bin/omb-agentctl" {
				agentCalled = true
				for i := 0; i < len(spec.Args)-1; i++ {
					if spec.Args[i] == "-config" {
						raw, err := os.ReadFile(spec.Args[i+1])
						if err != nil {
							return nil, fmt.Errorf("read agentctl config: %w", err)
						}
						if err := json.Unmarshal(raw, &agentCfg); err != nil {
							return nil, fmt.Errorf("unmarshal agentctl config: %w", err)
						}
					}
				}
			}
			return nil, nil
		},
	}

	in := &Installer{
		RootDir:     root,
		Runner:      runner,
		Euid:        func() int { return 0 },
		LookupUser:  func(string) bool { return false },
		HealthCheck: func(context.Context, string, string) error { return nil },
	}

	opts := Options{
		Lang:          "ru",
		ManagementIP:  "192.168.10.135",
		PXEInterface:  "ens19",
		PXEServerIP:   "10.30.0.10",
		PXESubnet:     "10.30.0.0/24",
		DHCPMode:      "full",
		DHCPStart:     "10.30.0.100",
		DHCPEnd:       "10.30.0.200",
		AdminPassword: "SuperSecretPass123",
		InstallMode:   "offline",
	}

	if err := in.Execute(context.Background(), opts); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Verify OMB_BOOT_BASE_URL uses PXE IP.
	assertFileContains(t, filepath.Join(root, "etc/omb/boot.env"), "OMB_BOOT_BASE_URL=http://10.30.0.10:8080")
	assertFileContains(t, filepath.Join(root, "etc/omb/control.env"), "OMB_CONTROL_BOOT_BASE_URL=http://10.30.0.10:8080")
	assertFileContains(t, filepath.Join(root, "etc/omb/control.env"), "OMB_CONTROL_INTERFACES=ens19")
	assertFileContains(t, filepath.Join(root, "var/lib/omb-deployment/INSTALL_MODE"), "offline")

	// Verify omb-agentctl -config was invoked with valid network.Config JSON.
	if !agentCalled {
		t.Fatal("expected /usr/bin/omb-agentctl -config to be called")
	}
	if err := network.Validate(agentCfg); err != nil {
		t.Fatalf("agentctl config failed network.Validate: %v", err)
	}
	if agentCfg.Mode != "full" || agentCfg.Interface != "ens19" || agentCfg.ServerIP != "10.30.0.10" ||
		agentCfg.Subnet != "10.30.0.0/24" || agentCfg.DHCPStart != "10.30.0.100" || agentCfg.DHCPEnd != "10.30.0.200" ||
		agentCfg.BootURL != "http://10.30.0.10:8080/boot/menu.ipxe" || agentCfg.TFTPRoot != "/srv/omb/tftp" {
		t.Fatalf("unexpected agentctl config: %+v", agentCfg)
	}
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file %s to exist: %v", path, err)
	}
}

func assertFileContains(t *testing.T, path, sub string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(data), sub) {
		t.Errorf("file %s does not contain %q; got:\n%s", path, sub, string(data))
	}
}
