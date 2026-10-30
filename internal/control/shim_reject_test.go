package control

import (
	"testing"
	"time"

	"pxe-platform/internal/agent"
	"pxe-platform/internal/boot"
)

// shimLog is what dnsmasq logged for the test VM on 23.10.2026: proxy-DHCP
// answers (MAC, no IP), the shim's size probe and the shim itself, and, when
// the firmware let the shim run, ipxe.efi right after it.
func shimLog(t0 time.Time, accepted bool, retries int) []agent.DHCPEvent {
	const mac, ip = "bc:24:11:0c:5a:d0", "10.30.0.11"
	shim := "ipxeboot/x86_64-sb/ipxe-shim.efi"
	var out []agent.DHCPEvent
	at := t0
	for i := 0; i <= retries; i++ {
		out = append(out,
			agent.DHCPEvent{At: at, Kind: "PXE", MAC: mac, Detail: "proxy"},
			agent.DHCPEvent{At: at.Add(4 * time.Second), Kind: "PXE", MAC: mac, Detail: "proxy"},
			agent.DHCPEvent{At: at.Add(5 * time.Second), Kind: "tftp-failed", IP: ip, Detail: shim},
			agent.DHCPEvent{At: at.Add(5 * time.Second), Kind: "tftp-sent", IP: ip, Detail: shim},
		)
		at = at.Add(45 * time.Second) // the firmware tries IPv6, then IPv4 again
	}
	if accepted {
		end := out[len(out)-1].At
		out = append(out,
			agent.DHCPEvent{At: end, Kind: "tftp-sent", IP: ip, Detail: "ipxeboot/x86_64-sb/ipxe.efi"},
			agent.DHCPEvent{At: end.Add(8 * time.Second), Kind: "PXE", MAC: mac, Detail: "proxy"}, // iPXE's own DHCP
		)
	}
	return out
}

func TestShimRejectedByFirmware(t *testing.T) {
	t0 := time.Date(2026, 10, 23, 18, 15, 34, 0, time.UTC)
	const mac = "bc:24:11:0c:5a:d0"
	for _, retries := range []int{0, 2} {
		dhcp := shimLog(t0, false, retries)
		last := dhcp[len(dhcp)-1].At
		if rows := machineRowsWith(nil, dhcp, installLog{}, nil, last.Add(10*time.Second)); len(rows) != 1 || rows[0].Stage != "loader" {
			t.Fatalf("retries=%d: too early to tell: %+v", retries, rows)
		}
		rows := machineRowsWith(nil, dhcp, installLog{}, nil, last.Add(shimRejectAfter+time.Second))
		if len(rows) != 1 || rows[0].Stage != "loader_rejected" || rows[0].Firmware != "UEFI" {
			t.Fatalf("retries=%d: rows=%+v", retries, rows)
		}
		if got := trailStates(machineTrail(rows[0], true)); got != "done,done,failed,none,none,none,none" {
			t.Fatalf("retries=%d: trail=%s", retries, got)
		}
		sessions := machineSessionsWith(mac, nil, dhcp, installLog{}, nil, last.Add(time.Minute))
		if len(sessions) != 1 || sessions[0].Outcome != "loader_rejected" || !sessions[0].Failed() {
			t.Fatalf("retries=%d: sessions=%+v", retries, sessions)
		}
	}
}

func TestShimAcceptedByFirmware(t *testing.T) {
	t0 := time.Date(2026, 10, 23, 18, 20, 4, 0, time.UTC)
	const mac = "bc:24:11:0c:5a:d0"
	dhcp := shimLog(t0, true, 0)
	last := dhcp[len(dhcp)-1].At
	rows := machineRowsWith(nil, dhcp, installLog{}, nil, last.Add(time.Minute))
	if len(rows) != 1 || rows[0].Stage != "ipxe" {
		t.Fatalf("rows=%+v", rows)
	}
	menu := []boot.Event{{At: last.Add(time.Second), Kind: "menu", MAC: mac, RemoteAddr: "10.30.0.11:5000", Path: "/menu.ipxe"}}
	if rows := machineRowsWith(menu, dhcp, installLog{}, nil, last.Add(time.Hour)); rows[0].Stage != "menu" {
		t.Fatalf("menu: %+v", rows)
	}
	if s := machineSessionsWith(mac, nil, dhcp, installLog{}, nil, last.Add(time.Hour)); len(s) != 1 || s[0].Outcome == "loader_rejected" || s[0].Failed() {
		t.Fatalf("sessions=%+v", s)
	}
}

// A firmware answer after a shim it refused is not iPXE asking for an
// address: the shim never ran.
func TestFirmwareRetryIsNotIPXE(t *testing.T) {
	t0 := time.Date(2026, 10, 23, 18, 15, 34, 0, time.UTC)
	dhcp := shimLog(t0, false, 0)
	dhcp = append(dhcp, agent.DHCPEvent{At: t0.Add(43 * time.Second), Kind: "PXE", MAC: "bc:24:11:0c:5a:d0", Detail: "proxy"})
	rows := machineRowsWith(nil, dhcp, installLog{}, nil, t0.Add(44*time.Second))
	if rows[0].Stage == "ipxe" {
		t.Fatalf("stage=%s", rows[0].Stage)
	}
}
