package control

import (
	"testing"
	"time"

	"pxe-platform/internal/agent"
	"pxe-platform/internal/boot"
)

// A machine that reboots a few minutes after reaching Setup: the row must
// show the new boot, not carry the old choice and Setup state over.
func TestQuickRebootStartsAFreshRow(t *testing.T) {
	t0 := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	mac, ip := "bc:24:11:52:02:37", "10.30.0.17"
	dhcp := []agent.DHCPEvent{
		{At: t0, Kind: "PXE", MAC: mac},
		{At: t0.Add(2 * time.Second), Kind: "tftp-sent", IP: ip, Detail: "ipxeboot/x86_64-sb/ipxe-shim.efi"},
		{At: t0.Add(4 * time.Second), Kind: "PXE", MAC: mac},
		// The second boot, five minutes later.
		{At: t0.Add(5 * time.Minute), Kind: "PXE", MAC: mac},
		{At: t0.Add(5*time.Minute + 2*time.Second), Kind: "tftp-sent", IP: ip, Detail: "ipxeboot/x86_64-sb/ipxe-shim.efi"},
		{At: t0.Add(5*time.Minute + 4*time.Second), Kind: "PXE", MAC: mac},
	}
	httpEvents := []boot.Event{
		{At: t0.Add(5 * time.Second), Kind: "menu", MAC: mac, RemoteAddr: ip + ":1", Path: "/boot/menu.ipxe"},
		{At: t0.Add(20 * time.Second), Kind: "asset", MAC: mac, RemoteAddr: ip + ":2", Path: "/assets/windows-profiles/2/wimboot", Status: 200, Bytes: 10, Size: 10},
		{At: t0.Add(40 * time.Second), Kind: "asset", MAC: mac, RemoteAddr: ip + ":3", Path: "/assets/windows-profiles/2/boot.wim", Status: 200, Bytes: 10, Size: 10},
		{At: t0.Add(5*time.Minute + 5*time.Second), Kind: "menu", MAC: mac, RemoteAddr: ip + ":4", Path: "/boot/menu.ipxe"},
	}
	// The first boot's share session is still listed by Samba after the
	// reboot.
	now := t0.Add(5*time.Minute + 30*time.Second)
	session := agent.SMBSession{ID: "1", IP: ip, Profile: "2", Started: t0.Add(2 * time.Minute), LastSeen: now, Setup: true}
	install := installEvents(agent.SMBSessions{Since: t0.Add(-time.Hour), Sessions: []agent.SMBSession{session}}, httpEvents)
	rows := machineRowsWith(httpEvents, dhcp, install, map[string]string{"winiso_2": "Win11"}, now)
	if len(rows) != 1 {
		t.Fatalf("rows=%+v", rows)
	}
	row := rows[0]
	if row.Stage != "menu" || row.Choice != "" || row.Boots != 2 {
		t.Fatalf("row=%+v", row)
	}
	if got := trailStates(machineTrail(row, true)); got != "done,done,done,done,current,none" {
		t.Fatalf("trail=%s", got)
	}
	sessions := machineSessionsWith(mac, httpEvents, dhcp, install, map[string]string{"winiso_2": "Win11"}, now)
	if len(sessions) != 2 || sessions[0].Outcome != "menu" || sessions[1].Outcome != "winpe" || sessions[1].Choice != "Win11" {
		var got []string
		for _, s := range sessions {
			got = append(got, s.Outcome+"/"+s.Choice)
		}
		t.Fatalf("sessions=%v", got)
	}
}
