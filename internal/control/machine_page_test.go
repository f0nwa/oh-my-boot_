package control

import (
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omb/internal/agent"
	"omb/internal/boot"
)

func testBootClient(t *testing.T, handler http.Handler) *BootClient {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "boot.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: handler}
	go srv.Serve(ln)
	t.Cleanup(func() { _ = srv.Close() })
	return NewBootClient(socket)
}

func TestMachineSessionsSplitBootsAndNameOutcome(t *testing.T) {
	t0 := time.Date(2026, 10, 20, 22, 47, 0, 0, time.UTC)
	mac := "52:54:00:b2:44:0d"
	dhcp := []agent.DHCPEvent{
		{At: t0, Kind: "PXE", MAC: mac, IP: "10.30.0.19", Detail: "proxy"},
		{At: t0.Add(time.Second), Kind: "tftp-sent", IP: "10.30.0.19", Detail: "ipxeboot/x86_64-sb/ipxe-shim.efi"},
		{At: t0.Add(2 * time.Hour), Kind: "PXE", MAC: mac, IP: "10.30.0.19", Detail: "proxy"},
		{At: t0.Add(2*time.Hour + time.Second), Kind: "tftp-failed", IP: "10.30.0.19", Detail: "ipxeboot/x86_64/undionly.kpxe"},
		{At: t0.Add(5 * time.Second), Kind: "PXE", MAC: "bc:24:11:00:00:01"},
	}
	httpEvents := []boot.Event{
		{At: t0.Add(10 * time.Second), Kind: "choice", Path: "/boot/choice?id=7", RemoteAddr: "10.30.0.19:1", MAC: mac},
		{At: t0.Add(3 * time.Second), Kind: "menu", Path: "/boot/menu.ipxe", RemoteAddr: "10.30.0.19:1", MAC: mac},
		{At: t0.Add(20 * time.Second), Kind: "asset", Path: "/assets/windows-profiles/3/boot.wim", RemoteAddr: "10.30.0.19:1", MAC: mac},
		{At: t0.Add(21 * time.Second), Kind: "asset", Path: "/assets/windows-driver-configs/7/omb-setup.cmd", RemoteAddr: "10.30.0.19:1", MAC: mac},
	}
	sessions := machineSessions(mac, httpEvents, dhcp, map[string]string{"install_7": "Win11 VirtIO"})
	if len(sessions) != 2 {
		t.Fatalf("sessions=%+v", sessions)
	}
	latest, first := sessions[0], sessions[1]
	if latest.Outcome != "loader_failed" || !latest.Failed() || len(latest.Steps) != 2 || !latest.Steps[1].Failed || latest.Steps[1].Stage != "TFTP" {
		t.Fatalf("latest=%+v", latest)
	}
	if first.Outcome != "files" || !first.OK() || first.Choice != "Win11 VirtIO" || len(first.Steps) != 6 {
		t.Fatalf("first=%+v", first)
	}
	stages := []string{}
	for _, step := range first.Steps {
		stages = append(stages, step.Stage)
	}
	if strings.Join(stages, ",") != "DHCP,TFTP,Меню,Выбор,Файлы,Файлы" {
		t.Fatalf("stages=%v", stages)
	}
}

func TestMachinePageRendersTimelineAndSessions(t *testing.T) {
	t0 := time.Date(2026, 10, 20, 22, 47, 0, 0, time.UTC)
	data := samplePageData(langRU)
	data.Info.Path = "/events/machines/52:54:00:b2:44:0d"
	data.MACFilter = "52:54:00:b2:44:0d"
	data.Machines = []machineRow{{MAC: data.MACFilter, IP: "10.30.0.19", Firmware: "UEFI", Boots: 3}}
	data.Sessions = []bootSession{
		{Start: t0, End: t0, Outcome: "loader_failed", Steps: []machineStep{{At: t0, Stage: "TFTP", Text: "Загрузчик не отдан", Raw: "undionly.kpxe", Failed: true}}},
		{Start: t0.Add(-time.Hour), End: t0.Add(-time.Hour), Outcome: "files", Choice: "Win11 VirtIO"},
	}
	data.SessionIndex = 1
	body := renderPage("machine", data)
	for _, want := range []string{`class="outcome outcome-ok"`, "Получила файлы установки · Win11 VirtIO", `class="mono boots-count">3</strong>`, `href="/events/machines/52:54:00:b2:44:0d?boot=0"`, `?boot=1" class="active"`, `href="/events?tab=dhcp&mac=52%3a54%3a00%3ab2%3a44%3a0d"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("machine page lacks %q: %s", want, body)
		}
	}
	data.SessionIndex = 0
	if body := renderPage("machine", data); !strings.Contains(body, `class="outcome outcome-bad"`) || !strings.Contains(body, `class="timeline-step failed"`) || !strings.Contains(body, "TFTP не отдал загрузчик") {
		t.Fatalf("failed boot: %s", body)
	}
	data.Machines[0].Boots = 0
	if body := renderPage("machine", data); !strings.Contains(body, `<span class="mono muted">0</span>`) {
		t.Fatalf("zero boots should be muted, got: %s", body)
	}
	data.Machines[0].Boots = 3
	data.Lang = langEN
	if found := cyrillic.FindAllString(renderPage("machine", data), 5); len(found) > 0 {
		t.Fatalf("English machine page has Russian text: %v", found)
	}
}

func TestMachinePageRejectsBadMACAndLinksFromEvents(t *testing.T) {
	s := &Server{Store: authOnlyStore{user: User{ID: 1, Role: "observer", Enabled: true}}}
	req := httptest.NewRequest(http.MethodGet, "/events/machines/not-a-mac", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("bad MAC status=%d", res.Code)
	}
	if body := libraryGet(t, s, "/events/machines/52:54:00:B2:44:0D"); !strings.Contains(body, "52:54:00:b2:44:0d") || !strings.Contains(body, "Записей об этой машине пока нет.") {
		t.Fatalf("empty machine page: %s", body)
	}
	data := samplePageData(langRU)
	data.Tab = "machines"
	data.Machines = []machineRow{{MAC: "bc:24:11:04:2a:ed", LastSeen: time.Now()}}
	if body := renderPage("events", data); !strings.Contains(body, `href="/events/machines/bc:24:11:04:2a:ed"`) {
		t.Fatalf("events page does not link machines: %s", body)
	}
}

func TestMachinePageTargetedBootEvents(t *testing.T) {
	var requestedMAC string
	var requestedLimit string
	bootSrv := testBootClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedMAC = r.URL.Query().Get("mac")
		requestedLimit = r.URL.Query().Get("limit")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]\n"))
	}))

	s := &Server{
		Store: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}},
		Boot:  bootSrv,
	}

	targetMAC := "52:54:00:b2:44:0d"
	body := libraryGet(t, s, "/events/machines/"+targetMAC)
	if requestedMAC != targetMAC {
		t.Fatalf("expected Boot to be queried with mac=%q, got %q", targetMAC, requestedMAC)
	}
	if requestedLimit != "500" {
		t.Fatalf("expected Boot to be queried with limit=500, got %q", requestedLimit)
	}
	if !strings.Contains(body, targetMAC) {
		t.Fatalf("body should contain target MAC: %s", body)
	}
}

func TestMachinePageHasNoDetails(t *testing.T) {
	data := samplePageData(langRU)
	data.MACFilter = "52:54:00:b2:44:0d"
	data.Machine = &machineCard{
		MachineInfo: MachineInfo{
			MAC:                  data.MACFilter,
			Name:                 "Тестовая машина",
			Manufacturer:         "QEMU",
			Product:              "Standard PC",
			ManufacturerOverride: "OMB Maker",
			ProductOverride:      "OMB Model",
			Note:                 "Заметка тестовой машины",
		},
		Known: true,
	}
	body := renderPage("machine", data)
	if strings.Contains(body, "<details") || strings.Contains(body, "</details>") {
		t.Fatalf("machine page must not contain <details>: %s", body)
	}
	if strings.Contains(body, "<summary") || strings.Contains(body, "</summary>") {
		t.Fatalf("machine page must not contain <summary>: %s", body)
	}
	for _, want := range []string{
		`id="machine-edit-dialog"`,
		`id="machine-delete-dialog"`,
		"data-machine-edit-open",
		"data-machine-delete-open",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("machine page lacks %q: %s", want, body)
		}
	}
}
