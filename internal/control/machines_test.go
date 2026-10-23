package control

import (
	"strings"
	"testing"
	"time"

	"pxe-platform/internal/agent"
	"pxe-platform/internal/boot"
)

func TestMachineRowsJoinDHCPTFTPAndHTTP(t *testing.T) {
	t0 := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	dhcp := []agent.DHCPEvent{
		{At: t0.Add(2 * time.Second), Kind: "tftp-sent", IP: "10.30.0.19", Detail: "ipxeboot/x86_64-sb/ipxe-shim.efi"},
		{At: t0, Kind: "PXE", MAC: "bc:24:11:04:2a:ed", Detail: "proxy"},
		{At: t0.Add(time.Minute), Kind: "PXE", MAC: "bc:24:11:52:02:39", Detail: "proxy"},
		{At: t0.Add(61 * time.Second), Kind: "tftp-failed", IP: "10.30.0.20", Detail: "ipxeboot/x86_64/undionly.kpxe"},
	}
	httpEvents := []boot.Event{
		{At: t0.Add(30 * time.Second), Kind: "asset", Path: "/assets/windows-driver-wims/abc/boot.wim", RemoteAddr: "10.30.0.19:5000", MAC: "bc:24:11:04:2a:ed"},
		{At: t0.Add(20 * time.Second), Kind: "asset", Path: "/assets/windows-driver-configs/4/pxe-setup.cmd", RemoteAddr: "10.30.0.19:5000", MAC: "bc:24:11:04:2a:ed"},
		{At: t0.Add(10 * time.Second), Kind: "menu", Path: "/boot/menu.ipxe", RemoteAddr: "10.30.0.19:5000", MAC: "bc:24:11:04:2a:ed"},
	}
	names := map[string]string{"windriver_4": "Windows 11 VirtIO Net SCSI test"}
	rows := machineRows(httpEvents, dhcp, names)
	if len(rows) != 2 {
		t.Fatalf("rows=%+v", rows)
	}
	var a, b machineRow
	for _, r := range rows {
		if r.MAC == "bc:24:11:04:2a:ed" {
			a = r
		} else {
			b = r
		}
	}
	if a.IP != "10.30.0.19" || a.Firmware != "UEFI" || a.Stage != "files" || a.Choice != "Windows 11 VirtIO Net SCSI test" || !a.LastSeen.Equal(t0.Add(30*time.Second)) {
		t.Fatalf("machine a %+v", a)
	}
	// b got the only PXE answer a second before the failed TFTP transfer, so
	// that transfer is b's even though its line names only an IP.
	if b.MAC != "bc:24:11:52:02:39" || b.Stage != "loader_failed" || b.IP != "10.30.0.20" || b.Firmware != "BIOS" {
		t.Fatalf("machine b %+v", b)
	}
}

func TestResolveDHCPMACsFromHTTP(t *testing.T) {
	t0 := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	dhcp := []agent.DHCPEvent{{At: t0, Kind: "tftp-sent", IP: "10.30.0.19", Detail: "ipxeboot/x86_64/undionly.kpxe"}, {At: t0.Add(2 * time.Hour), Kind: "tftp-sent", IP: "10.30.0.19"}}
	httpEvents := []boot.Event{{At: t0.Add(time.Minute), RemoteAddr: "10.30.0.19:1", MAC: "bc:24:11:04:2a:ed"}}
	resolved := resolveDHCPMACs(dhcp, httpEvents)
	if resolved[0].MAC != "bc:24:11:04:2a:ed" || resolved[1].MAC != "" {
		t.Fatalf("%+v", resolved)
	}
}

func TestEventsPageTabs(t *testing.T) {
	data := samplePageData(langRU)
	data.Tab = "machines"
	data.Machines = []machineRow{{MAC: "bc:24:11:04:2a:ed", IP: "10.30.0.19", Firmware: "UEFI", Stage: "files", Choice: "Windows 11", LastSeen: time.Now()}}
	body := renderPage("events", data)
	for _, want := range []string{"bc:24:11:04:2a:ed", `href="/events/machines/bc:24:11:04:2a:ed"`, "Загрузка файлов", `href="/events?tab=dhcp"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("machines tab lacks %q: %s", want, body)
		}
	}
	data.Tab, data.MACFilter = "dhcp", "bc:24:11:04:2a:ed"
	data.DHCPEvents = []agent.DHCPEvent{{At: time.Now(), Kind: "tftp-failed", IP: "10.30.0.20", MAC: "bc:24:11:04:2a:ed", Detail: "ipxeboot/x86_64/undionly.kpxe"}}
	body = renderPage("events", data)
	if !strings.Contains(body, "Ошибка отправки по TFTP") || !strings.Contains(body, "undionly.kpxe") {
		t.Fatalf("dhcp tab: %s", body)
	}
}

func TestWideTablesFitTheWindow(t *testing.T) {
	css, _ := templateFS.ReadFile("templates/style.css")
	if strings.Contains(string(css), "max-width:1040px") {
		t.Fatal("content column still capped at 1040px")
	}
	data := samplePageData(langRU)
	data.Tab, data.EventError = "requests", ""
	body := renderPage("events", data)
	if !strings.Contains(body, `<td class="nowrap">20.10.2026 01:00:00</td>`) || !strings.Contains(body, "Время, UTC&#43;00:00") {
		t.Fatalf("time column: %s", body)
	}
}

func TestEveryTableScrollsInsideItsBox(t *testing.T) {
	for _, name := range []string{"installations", "menu", "images", "answers", "drivers", "storages", "network", "network_preview", "users", "account", "audit", "events"} {
		data := samplePageData(langRU)
		data.ImageError, data.EventError, data.Tab = "", "", "requests"
		body := renderPage(name, data)
		if n, w := strings.Count(body, "<table"), strings.Count(body, `<div class="table-wrap"><table`); n != w {
			t.Errorf("%s: %d tables, %d in a scroll box", name, n, w)
		}
	}
}

func TestResolveTFTPByTheOnlyRecentPXEClient(t *testing.T) {
	t0 := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	dhcp := []agent.DHCPEvent{
		{At: t0, Kind: "PXE", MAC: "bc:24:11:04:2a:ed", Detail: "proxy"},
		{At: t0.Add(2 * time.Second), Kind: "tftp-failed", IP: "10.30.0.19", Detail: "ipxeboot/x86_64/undionly.kpxe"},
		{At: t0.Add(time.Minute), Kind: "PXE", MAC: "bc:24:11:04:2a:ed"},
		{At: t0.Add(time.Minute + time.Second), Kind: "PXE", MAC: "bc:24:11:52:02:39"},
		{At: t0.Add(time.Minute + 3*time.Second), Kind: "tftp-sent", IP: "10.30.0.20"},
		{At: t0.Add(5 * time.Minute), Kind: "tftp-sent", IP: "10.30.0.21"},
	}
	resolved := resolveDHCPMACs(dhcp, nil)
	if resolved[1].MAC != "bc:24:11:04:2a:ed" {
		t.Fatalf("single client not linked: %+v", resolved[1])
	}
	if resolved[4].MAC != "" || resolved[5].MAC != "" {
		t.Fatalf("ambiguous or stale TFTP linked: %+v %+v", resolved[4], resolved[5])
	}
}

// The real dnsmasq sequence of a UEFI VM on 20.10.2026: two firmware PXE
// answers, the shim's deliberate abort, the shim and iPXE sent, then iPXE's
// own DHCP. The furthest step is iPXE, and the abort is not an error.
func TestMachineProgressFollowsTheFurthestStepOfABoot(t *testing.T) {
	t0 := time.Date(2026, 10, 20, 0, 40, 49, 0, time.UTC)
	mac := "bc:24:11:52:02:39"
	dhcp := []agent.DHCPEvent{
		{At: t0, Kind: "PXE", MAC: mac, Detail: "proxy"},
		{At: t0.Add(3 * time.Second), Kind: "PXE", MAC: mac, Detail: "proxy"},
		{At: t0.Add(4 * time.Second), Kind: "tftp-failed", IP: "10.30.0.19", Detail: "ipxeboot/x86_64-sb/ipxe-shim.efi"},
		{At: t0.Add(4 * time.Second), Kind: "tftp-sent", IP: "10.30.0.19", Detail: "ipxeboot/x86_64-sb/ipxe-shim.efi"},
		{At: t0.Add(4 * time.Second), Kind: "tftp-sent", IP: "10.30.0.19", Detail: "ipxeboot/x86_64-sb/ipxe.efi"},
		{At: t0.Add(11 * time.Second), Kind: "PXE", MAC: mac, Detail: "proxy"},
	}
	rows := machineRows(nil, dhcp, nil)
	if len(rows) != 1 || rows[0].Stage != "ipxe" || rows[0].Firmware != "UEFI" {
		t.Fatalf("rows=%+v", rows)
	}
	if got := trailStates(machineTrail(rows[0], false)); got != "done,done,done,none,none,none,none" {
		t.Fatalf("trail=%s", got)
	}
	sessions := machineSessions(mac, nil, dhcp, nil)
	if len(sessions) != 1 || sessions[0].Failed() || sessions[0].Outcome != "loader" {
		t.Fatalf("session=%+v", sessions)
	}
	var stages []string
	for _, step := range sessions[0].Steps {
		stages = append(stages, step.Stage)
		if step.Failed {
			t.Fatalf("shim abort shown as a failure: %+v", step)
		}
	}
	if strings.Join(stages, ",") != "DHCP,DHCP,TFTP,TFTP,TFTP,iPXE" {
		t.Fatalf("stages=%v", stages)
	}
	// A new boot hours later starts from scratch.
	later := append(dhcp, agent.DHCPEvent{At: t0.Add(5 * time.Hour), Kind: "PXE", MAC: mac})
	if rows := machineRows(nil, later, nil); rows[0].Stage != "pxe" {
		t.Fatalf("new boot stage=%s", rows[0].Stage)
	}
}
