package setup

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"syscall"
	"testing"
)

func TestCheckTCPPortFree(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	addr := ln.Addr().String()

	err = CheckTCPPortFree(addr)
	if err == nil {
		_ = ln.Close()
		t.Fatalf("expected conflict error while TCP listener is active on %s", addr)
	}
	if !errors.Is(err, syscall.EADDRINUSE) {
		_ = ln.Close()
		t.Fatalf("expected EADDRINUSE, got: %v", err)
	}

	if err := ln.Close(); err != nil {
		t.Fatalf("close tcp listener: %v", err)
	}

	if err := CheckTCPPortFree(addr); err != nil {
		t.Fatalf("expected TCP %s to be free after close, got: %v", addr, err)
	}
}

func TestCheckUDPPortFree(t *testing.T) {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp4: %v", err)
	}
	addr := pc.LocalAddr().String()

	err = CheckUDPPortFree(addr)
	if err == nil {
		_ = pc.Close()
		t.Fatalf("expected conflict error while UDP listener is active on %s", addr)
	}
	if !errors.Is(err, syscall.EADDRINUSE) {
		_ = pc.Close()
		t.Fatalf("expected EADDRINUSE, got: %v", err)
	}

	if err := pc.Close(); err != nil {
		t.Fatalf("close udp listener: %v", err)
	}

	if err := CheckUDPPortFree(addr); err != nil {
		t.Fatalf("expected UDP %s to be free after close, got: %v", addr, err)
	}
}

func TestDetectUFWActive(t *testing.T) {
	tests := []struct {
		name       string
		out        string
		err        error
		wantActive bool
		wantKnown  bool
	}{
		{
			name:       "active",
			out:        "Status: active\n\nTo                         Action      From\n--                         ------      ----\n22/tcp                     ALLOW       Anywhere\n",
			wantActive: true,
			wantKnown:  true,
		},
		{
			name:       "inactive",
			out:        "Status: inactive\n",
			wantActive: false,
			wantKnown:  true,
		},
		{
			name:       "missing ufw binary",
			err:        errors.New("exec: \"ufw\": executable file not found in $PATH"),
			wantActive: false,
			wantKnown:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner := &mockRunner{
				handler: func(spec CmdSpec) ([]byte, error) {
					if spec.Name != "ufw" || len(spec.Args) != 1 || spec.Args[0] != "status" {
						t.Fatalf("unexpected command: %s %v", spec.Name, spec.Args)
					}
					return []byte(tc.out), tc.err
				},
			}
			active, known := DetectUFWActive(context.Background(), runner)
			if active != tc.wantActive || known != tc.wantKnown {
				t.Fatalf("DetectUFWActive() = (%v, %v), want (%v, %v)", active, known, tc.wantActive, tc.wantKnown)
			}
		})
	}
}

func TestPreflightFailsOnPortConflict(t *testing.T) {
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

	var gotCheckPXE bool
	called := false
	in := newPreflightInstaller(t, root, runner)
	in.PortCheck = func(checkPXE bool) error {
		called = true
		gotCheckPXE = checkPXE
		return errors.New("port conflict on TCP 8443: address already in use")
	}

	err := in.Preflight()
	if err == nil {
		t.Fatal("expected Preflight to fail on port conflict")
	}
	if !called {
		t.Fatal("expected PortCheck to be called during Preflight")
	}
	if gotCheckPXE {
		t.Fatal("expected Preflight to call PortCheck(false)")
	}
	if !strings.Contains(err.Error(), "TCP 8443") {
		t.Fatalf("expected error to mention TCP 8443, got: %v", err)
	}
}

func TestExecuteChecksPXEPortsBeforeChanges(t *testing.T) {
	root := t.TempDir()
	setupCleanAssets(t, root)
	runner := &mockRunner{}

	var gotCheckPXE bool
	in := &Installer{
		RootDir:    root,
		Runner:     runner,
		Euid:       func() int { return 0 },
		LookupUser: func(string) bool { return false },
		PortCheck: func(checkPXE bool) error {
			gotCheckPXE = checkPXE
			return errors.New("port conflict on UDP 67: address already in use")
		},
	}

	err := in.Execute(context.Background(), Options{
		Lang:          "ru",
		ManagementIP:  "192.168.10.135",
		PXEInterface:  "ens19",
		PXEServerIP:   "10.30.0.10",
		PXESubnet:     "10.30.0.0/24",
		DHCPMode:      "full",
		DHCPStart:     "10.30.0.100",
		DHCPEnd:       "10.30.0.200",
		AdminPassword: "SuperSecretPass123",
	})
	if err == nil {
		t.Fatal("expected Execute to fail on PXE port conflict")
	}
	if !gotCheckPXE {
		t.Fatal("expected Execute with DHCPMode=full to call PortCheck(true)")
	}
	if !strings.Contains(err.Error(), "UDP 67") {
		t.Fatalf("expected error to mention UDP 67, got: %v", err)
	}
	if len(runner.cmds) != 0 {
		t.Fatalf("expected no system commands to run after PXE port conflict, ran %d commands", len(runner.cmds))
	}
}

func TestUIUFWWarning(t *testing.T) {
	var ruBuf bytes.Buffer
	NewUI(&ruBuf, "ru", false, "0.2.1").UFWWarning()
	if !strings.Contains(ruBuf.String(), "UFW активен") || !strings.Contains(ruBuf.String(), "8443/tcp") || !strings.Contains(ruBuf.String(), "67,69,4011/udp") {
		t.Errorf("unexpected RU UFW warning: %q", ruBuf.String())
	}

	var enBuf bytes.Buffer
	NewUI(&enBuf, "en", false, "0.2.1").UFWWarning()
	if !strings.Contains(enBuf.String(), "UFW is active") || !strings.Contains(enBuf.String(), "8443/tcp") || !strings.Contains(enBuf.String(), "67,69,4011/udp") {
		t.Errorf("unexpected EN UFW warning: %q", enBuf.String())
	}
}
