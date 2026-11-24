package setup

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
)

// CheckTCPPortFree verifies that the given TCP address (e.g. ":8443" or "127.0.0.1:8443") can be bound.
func CheckTCPPortFree(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return fmt.Errorf("TCP %s is already in use: %w", addr, err)
		}
		return fmt.Errorf("check TCP %s: %w", addr, err)
	}
	_ = ln.Close()
	return nil
}

// CheckUDPPortFree verifies that the given UDPv4 address (e.g. ":67" or "127.0.0.1:67") can be bound.
func CheckUDPPortFree(addr string) error {
	pc, err := net.ListenPacket("udp4", addr)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return fmt.Errorf("UDP %s is already in use: %w", addr, err)
		}
		return fmt.Errorf("check UDP %s: %w", addr, err)
	}
	_ = pc.Close()
	return nil
}

// CheckPortConflicts verifies that required OMB ports are not already bound by another process.
// Core ports TCP 8443 (HTTPS control panel) and TCP 8080 (iPXE HTTP) are always checked.
// When checkPXE is true, PXE/DHCP/TFTP ports UDP 67, UDP 69, and UDP 4011 are also checked.
func CheckPortConflicts(checkPXE bool) error {
	for _, port := range []int{8443, 8080} {
		if err := CheckTCPPortFree(fmt.Sprintf(":%d", port)); err != nil {
			return fmt.Errorf("port conflict on TCP %d: %w", port, err)
		}
	}
	if checkPXE {
		for _, port := range []int{67, 69, 4011} {
			if err := CheckUDPPortFree(fmt.Sprintf(":%d", port)); err != nil {
				return fmt.Errorf("port conflict on UDP %d: %w", port, err)
			}
		}
	}
	return nil
}

// DetectUFWActive checks whether UFW firewall is currently active on the host.
// If ufw is not installed or the command fails, it returns (false, false).
func DetectUFWActive(ctx context.Context, runner CmdRunner) (active bool, known bool) {
	if runner == nil {
		runner = ExecRunner{}
	}
	out, err := runner.Run(ctx, CmdSpec{
		Name: "ufw",
		Args: []string{"status"},
		Env:  []string{"LC_ALL=C", "LANG=C"},
	})
	if err != nil {
		return false, false
	}
	if strings.Contains(string(out), "Status: active") {
		return true, true
	}
	return false, true
}
