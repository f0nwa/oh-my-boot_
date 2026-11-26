package control

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"omb/internal/agent"
	"omb/internal/boot"
	"omb/internal/network"
)

type fakeSMBDisconnectAPI struct {
	calls []struct{ id, ip, pid string }
	err   error
}

func (f *fakeSMBDisconnectAPI) DisconnectSMBSession(_ context.Context, id, ip, pid string) error {
	f.calls = append(f.calls, struct{ id, ip, pid string }{id: id, ip: ip, pid: pid})
	return f.err
}

type fakeTransferCancelAPI struct {
	ids []int
	err error
}

func (f *fakeTransferCancelAPI) CancelTransfer(_ context.Context, id int) error {
	f.ids = append(f.ids, id)
	return f.err
}

type dhcpIgnoreTestStore struct {
	*newInstallStore
	machines map[string]MachineInfo
}

func (s *dhcpIgnoreTestStore) Machines(context.Context) ([]MachineInfo, error) {
	out := make([]MachineInfo, 0, len(s.machines))
	for _, m := range s.machines {
		out = append(out, m)
	}
	return out, nil
}
func (s *dhcpIgnoreTestStore) SeeMachines(context.Context, []MachineSeen) error         { return nil }
func (s *dhcpIgnoreTestStore) AddMachine(context.Context, string, MachineEdit) (bool, error) {
	return true, nil
}
func (s *dhcpIgnoreTestStore) ApplyMachineImport(context.Context, []machineImportChange) error {
	return nil
}
func (s *dhcpIgnoreTestStore) EditMachine(context.Context, string, MachineEdit) error { return nil }
func (s *dhcpIgnoreTestStore) DeleteMachine(_ context.Context, mac string) error {
	delete(s.machines, mac)
	return nil
}
func (s *dhcpIgnoreTestStore) SetMachineNetworkDriver(_ context.Context, mac, driver string) error {
	m := s.machines[mac]
	m.MAC = mac
	m.NetworkDriver = driver
	s.machines[mac] = m
	return nil
}
func (s *dhcpIgnoreTestStore) SetMachineDHCPIgnored(_ context.Context, mac string, ignored bool) error {
	m := s.machines[mac]
	m.MAC = mac
	m.DHCPIgnored = ignored
	s.machines[mac] = m
	return nil
}
func (s *dhcpIgnoreTestStore) BootDriverRules(context.Context) ([]network.BootDriverRule, error) {
	var rules []network.BootDriverRule
	for _, m := range s.machines {
		if m.DHCPIgnored {
			rules = append(rules, network.BootDriverRule{Match: m.MAC, Driver: "ignore"})
		} else if m.NetworkDriver != "" {
			rules = append(rules, network.BootDriverRule{Match: m.MAC, Driver: m.NetworkDriver})
		}
	}
	return rules, nil
}

type recordingBootDrivers struct {
	snapshots [][]network.BootDriverRule
}

func (r *recordingBootDrivers) SetBootDrivers(_ context.Context, rules []network.BootDriverRule) error {
	cp := append([]network.BootDriverRule(nil), rules...)
	r.snapshots = append(r.snapshots, cp)
	return nil
}

func TestStaleSMBHTTPAndDHCPFloodDetection(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 30, 0, 0, time.UTC)
	started := now.Add(-20 * time.Minute)
	copyingAt := now.Add(-15 * time.Minute)
	lastIOStale := now.Add(-8 * time.Minute)
	lastIOFresh := now.Add(-30 * time.Second)

	httpEvents := []boot.Event{
		{At: started.Add(-1 * time.Minute), Kind: "asset", Path: "/assets/windows-profiles/17/boot.wim", RemoteAddr: "10.30.0.42:1234", MAC: "a4:bb:6d:14:cb:f7", Status: 200},
		{At: started.Add(-1 * time.Minute), Kind: "asset", Path: "/assets/windows-profiles/12/boot.wim", RemoteAddr: "10.30.0.63:1234", MAC: "3c:52:82:11:90:ab", Status: 200},
		{At: now.Add(-5 * time.Minute), Kind: "choice", Path: "/boot/choice?id=100", RemoteAddr: "10.30.0.51:1234", MAC: "54:ee:75:aa:12:04"},
	}
	smb := agent.SMBSessions{
		Since: started.Add(-time.Hour),
		Sessions: []agent.SMBSession{
			{ID: "1001", IP: "10.30.0.42", PID: "48192", Profile: "17", Started: started, LastSeen: now, LastIOAt: &lastIOStale, Copying: &copyingAt, CopyRead: 1 << 30},
			{ID: "1002", IP: "10.30.0.63", PID: "48200", Profile: "12", Started: started, LastSeen: now, LastIOAt: &lastIOFresh, Copying: &copyingAt, CopyRead: 2 << 30},
		},
	}
	transfers := []boot.Transfer{
		{ID: 7, Path: "/images/ubuntu-desktop.iso", RemoteAddr: "10.30.0.51:5000", Started: now.Add(-5 * time.Minute), LastActivity: now.Add(-3 * time.Minute), Size: 5 << 30, Bytes: 1 << 30},
	}
	var dhcp []agent.DHCPEvent
	for i := 0; i < 12; i++ {
		dhcp = append(dhcp, agent.DHCPEvent{At: now.Add(-time.Duration(i*5) * time.Second), Kind: "DHCPDISCOVER", MAC: "de:ad:be:ef:00:99", IP: "10.30.0.88"})
	}
	dhcp = append(dhcp, agent.DHCPEvent{At: now.Add(-10 * time.Second), Kind: "DHCPDISCOVER", MAC: "aa:bb:cc:dd:ee:ff", IP: "10.30.0.90"})

	install := installEvents(smb, httpEvents)
	rows := machineRowsWith(httpEvents, dhcp, install, nil, now)
	attachDownloads(rows, transfers, now)
	attachActiveHTTPTransfers(rows, transfers, now)
	attachActiveSMBSessions(rows, smb, httpEvents, now)
	detectDHCPFlood(rows, dhcp, now)

	byMAC := map[string]machineRow{}
	for _, r := range rows {
		byMAC[r.MAC] = r
	}
	if smbRow := byMAC["a4:bb:6d:14:cb:f7"]; smbRow.ActiveSMB == nil || !smbRow.ActiveSMB.Stale || smbRow.ActiveSMB.IdleMinutes < 8 {
		t.Fatalf("expected stale SMB session, got %+v", smbRow.ActiveSMB)
	}
	if okRow := byMAC["3c:52:82:11:90:ab"]; okRow.ActiveSMB == nil || okRow.ActiveSMB.Stale {
		t.Fatalf("expected healthy active SMB session, got %+v", okRow.ActiveSMB)
	}
	if httpRow := byMAC["54:ee:75:aa:12:04"]; httpRow.ActiveHTTP == nil || !httpRow.ActiveHTTP.Stale || httpRow.ActiveHTTP.ID != 7 {
		t.Fatalf("expected stale HTTP transfer, got %+v", httpRow.ActiveHTTP)
	}
	if floodRow := byMAC["de:ad:be:ef:00:99"]; !floodRow.DHCPFlood || floodRow.DHCPFloodCount != 12 {
		t.Fatalf("expected DHCP flood with 12 events, got flood=%v count=%d", floodRow.DHCPFlood, floodRow.DHCPFloodCount)
	}
	if quietRow := byMAC["aa:bb:cc:dd:ee:ff"]; quietRow.DHCPFlood {
		t.Fatalf("quiet DHCP client flagged as flood")
	}

	alerts := nowSessionAlerts(rows)
	if len(alerts) != 2 || !strings.Contains(alerts[0].Title, "Зависшие сессии: 2") || !strings.Contains(alerts[1].Title, "DHCP-флуд: 1") {
		t.Fatalf("unexpected alerts: %+v", alerts)
	}

	// Marking flooder as ignored hides it from the "now" view.
	for i := range rows {
		if rows[i].MAC == "de:ad:be:ef:00:99" {
			rows[i].DHCPIgnored = true
		}
	}
	view := &nowView{Show: "now"}
	fillNowMachines(view, rows, now)
	for _, m := range view.Machines {
		if m.MAC == "de:ad:be:ef:00:99" {
			t.Fatalf("ignored MAC must be hidden from now view")
		}
	}
}

func TestCopiedSMBSessionIsNeverStaleOrLiveAfterCopy(t *testing.T) {
	// Reproduces the production bug: WinPE finished copying install.wim and
	// rebooted, but Samba kept the dead TCP/SMB session open (Ended == nil) for
	// 38 minutes without I/O. The machine must stay "copied" (green), must not
	// be flagged as "SMB зависла", must not raise alerts, and must leave "now".
	t0 := time.Date(2026, 11, 26, 16, 37, 19, 0, time.UTC)
	started := t0.Add(1*time.Minute + 21*time.Second)
	copyingAt := t0.Add(2*time.Minute + 22*time.Second)
	copiedAt := t0.Add(6*time.Minute + 23*time.Second)
	now := copiedAt.Add(38 * time.Minute)
	mac := "ac:91:a1:50:ee:55"
	ip := "10.206.42.68"
	const copyBytes int64 = 7 << 30

	httpEvents := []boot.Event{
		{At: t0.Add(-6 * time.Second), Kind: "choice", Path: "/boot/choice?id=4", RemoteAddr: ip + ":5001", MAC: mac, Status: 200},
		{At: t0, Kind: "asset", Path: "/assets/windows-driver-wims/abc/boot.wim", RemoteAddr: ip + ":5002", MAC: mac, Status: 200},
	}
	sess := agent.SMBSession{
		ID:       "2001",
		IP:       ip,
		PID:      "61234",
		Profile:  "1",
		Started:  started,
		LastSeen: now, // watcher keeps updating LastSeen every 10s while smbd holds dead TCP
		LastIOAt: &copiedAt,
		Setup:    true,
		WIMOpen:  false,
		Copying:  &copyingAt,
		Copied:   &copiedAt,
		CopyRead: copyBytes,
		Ended:    nil,
	}
	if eval := evaluateActiveSMB(sess, now); eval.Stale {
		t.Fatalf("copied session must never be evaluated as stale: %+v", eval)
	}

	smb := agent.SMBSessions{
		Since:    t0.Add(-time.Hour),
		Sessions: []agent.SMBSession{sess},
	}
	install := installEvents(smb, httpEvents)
	rows := machineRowsWith(httpEvents, nil, install, map[string]string{"install_4": "Windows 26H2 x64 test clean"}, now)
	attachActiveSMBSessions(rows, smb, httpEvents, now)

	if len(rows) != 1 {
		t.Fatalf("expected 1 machine row, got %d", len(rows))
	}
	row := rows[0]
	if row.Stage != "copied" {
		t.Fatalf("stage = %q, want copied", row.Stage)
	}
	if row.ActiveSMB != nil {
		t.Fatalf("completed copy session must not stay attached as ActiveSMB: %+v", row.ActiveSMB)
	}
	if !row.LastSeen.Equal(copiedAt) {
		t.Fatalf("LastSeen = %v, want copiedAt %v (must not be kept live by idle smbd)", row.LastSeen, copiedAt)
	}

	trail := machineTrail(row, false)
	if trail[6].State != "done" || trail[6].Sub != "скопировано, перезагрузка" || trail[6].Bytes != copyBytes {
		t.Fatalf("unexpected install trail step: %+v", trail[6])
	}

	if alerts := nowSessionAlerts(rows); len(alerts) != 0 {
		t.Fatalf("expected no stale session alerts after copy, got %+v", alerts)
	}

	nowViewPage := &nowView{Show: "now"}
	fillNowMachines(nowViewPage, rows, now)
	if nowViewPage.Booting != 0 || nowViewPage.Failed != 0 || nowViewPage.Day != 1 || len(nowViewPage.Machines) != 0 {
		t.Fatalf("unexpected nowView: booting=%d failed=%d day=%d machines=%d", nowViewPage.Booting, nowViewPage.Failed, nowViewPage.Day, len(nowViewPage.Machines))
	}

	failedView := &nowView{Show: "failed"}
	fillNowMachines(failedView, rows, now)
	if len(failedView.Machines) != 0 {
		t.Fatalf("copied machine must not appear in failed filter: %+v", failedView.Machines)
	}
}

func TestStaleSMBStillDetectedBeforeCopy(t *testing.T) {
	now := time.Date(2026, 11, 26, 17, 30, 0, 0, time.UTC)
	started := now.Add(-35 * time.Minute)
	lastIO := now.Add(-31 * time.Minute)

	// WinPE stuck on edition/disk screen before copying for >= 30m must still be stale.
	winpeIdle := agent.SMBSession{
		ID:       "3001",
		IP:       "10.206.42.70",
		PID:      "70001",
		Profile:  "1",
		Started:  started,
		LastSeen: now,
		LastIOAt: &lastIO,
		Setup:    true,
	}
	eval := evaluateActiveSMB(winpeIdle, now)
	if !eval.Stale || eval.Copying {
		t.Fatalf("expected pre-copy WinPE session idle 31m to be stale, got %+v", eval)
	}
}

func TestSessionResetAndDHCPIgnoreHandlersAndRBAC(t *testing.T) {
	baseServer, baseStore := newInstallServer(t)
	store := &dhcpIgnoreTestStore{newInstallStore: baseStore, machines: map[string]MachineInfo{}}
	smbAPI := &fakeSMBDisconnectAPI{}
	httpAPI := &fakeTransferCancelAPI{}
	driversSync := &recordingBootDrivers{}
	baseServer.Store = store
	baseServer.SMBDisconnect = smbAPI
	baseServer.TransferCancel = httpAPI
	baseServer.BootDrivers = driversSync

	// Observer cannot disconnect SMB, cancel HTTP, or ignore DHCP.
	store.user.Role = "observer"
	for _, tc := range []struct {
		path string
		vals url.Values
	}{
		{"/sessions/smb/disconnect", url.Values{"session_id": {"1001"}, "ip": {"10.30.0.42"}}},
		{"/sessions/http/cancel", url.Values{"transfer_id": {"7"}}},
		{"/events/machines/de:ad:be:ef:00:99/dhcp-ignore", url.Values{"ignored": {"1"}}},
	} {
		if res := menuPost(t, baseServer, tc.path, tc.vals); res.Code != http.StatusForbidden {
			t.Fatalf("observer POST %s = %d, want 403", tc.path, res.Code)
		}
	}

	// Operator can disconnect SMB and cancel HTTP, but cannot toggle DHCP ignore.
	store.user.Role = "operator"
	if res := menuPost(t, baseServer, "/sessions/smb/disconnect", url.Values{"session_id": {"1001"}, "ip": {"10.30.0.42"}, "pid": {"48192"}, "return_to": {"/"}}); res.Code != http.StatusSeeOther {
		t.Fatalf("operator SMB disconnect = %d %s", res.Code, res.Body.String())
	}
	if len(smbAPI.calls) != 1 || smbAPI.calls[0].pid != "48192" {
		t.Fatalf("unexpected SMB disconnect calls: %+v", smbAPI.calls)
	}
	if res := menuPost(t, baseServer, "/sessions/http/cancel", url.Values{"transfer_id": {"7"}, "mac": {"54:ee:75:aa:12:04"}, "return_to": {"https://evil.example/x"}}); res.Code != http.StatusSeeOther || !strings.HasPrefix(res.Header().Get("Location"), "/?message=") {
		t.Fatalf("operator HTTP cancel = %d loc=%q", res.Code, res.Header().Get("Location"))
	}
	if len(httpAPI.ids) != 1 || httpAPI.ids[0] != 7 {
		t.Fatalf("unexpected HTTP cancel calls: %+v", httpAPI.ids)
	}
	if res := menuPost(t, baseServer, "/events/machines/de:ad:be:ef:00:99/dhcp-ignore", url.Values{"ignored": {"1"}}); res.Code != http.StatusForbidden {
		t.Fatalf("operator DHCP ignore = %d, want 403", res.Code)
	}

	// Admin can ignore and unignore a MAC in DHCP and syncs dnsmasq rules.
	store.user.Role = "admin"
	res := menuPost(t, baseServer, "/events/machines/de:ad:be:ef:00:99/dhcp-ignore", url.Values{"ignored": {"1"}, "return_to": {"/"}})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("admin DHCP ignore = %d %s", res.Code, res.Body.String())
	}
	if !store.machines["de:ad:be:ef:00:99"].DHCPIgnored {
		t.Fatal("machine not marked dhcp_ignored in store")
	}
	if len(driversSync.snapshots) != 1 || len(driversSync.snapshots[0]) != 1 || driversSync.snapshots[0][0].Driver != "ignore" {
		t.Fatalf("expected ignore rule synced to agent, got %+v", driversSync.snapshots)
	}

	// Network driver form must reject "ignore".
	if res := menuPost(t, baseServer, "/events/machines/de:ad:be:ef:00:99/network-driver", url.Values{"network_driver": {"ignore"}}); res.Code != http.StatusBadRequest {
		t.Fatalf("network-driver accepted ignore: %d", res.Code)
	}

	// Unignore removes the rule.
	res = menuPost(t, baseServer, "/events/machines/de:ad:be:ef:00:99/dhcp-ignore", url.Values{"ignored": {"0"}, "return_to": {"/machines"}})
	if res.Code != http.StatusSeeOther || !strings.HasPrefix(res.Header().Get("Location"), "/machines?message=") {
		t.Fatalf("admin DHCP unignore = %d loc=%q", res.Code, res.Header().Get("Location"))
	}
	if store.machines["de:ad:be:ef:00:99"].DHCPIgnored || len(driversSync.snapshots) != 2 || len(driversSync.snapshots[1]) != 0 {
		t.Fatalf("expected ignore rule cleared, got %+v", driversSync.snapshots)
	}
}

func TestNowAndMachinePagesRenderVariant2MenusAndBanners(t *testing.T) {
	now := time.Now()
	staleSMB := &activeSMBSession{ID: "1001", IP: "10.30.0.42", PID: "48192", Profile: "17", CopyRead: 1 << 30, WIMSize: 4 << 30, Copying: true, Stale: true, IdleMinutes: 14}
	staleHTTP := &activeHTTPTransfer{ID: 7, Path: "/images/ubuntu-desktop.iso", RemoteAddr: "10.30.0.51:5000", Bytes: 1 << 30, Size: 5 << 30, Stale: true, IdleMinutes: 3}

	// Render Now page for admin, operator, observer.
	makeNowData := func(role string) pageData {
		m1 := machineRow{MAC: "a4:bb:6d:14:cb:f7", Name: "Dell", IP: "10.30.0.42", Stage: "copy", LastSeen: now, ActiveSMB: staleSMB}
		m2 := machineRow{MAC: "de:ad:be:ef:00:99", IP: "10.30.0.88", Stage: "pxe", LastSeen: now, DHCPFlood: true, DHCPFloodCount: 25}
		return pageData{
			Info: requestContext{User: User{ID: 1, Username: "u", Role: role}, CSRF: "csrf-token", Path: "/"},
			Now: &nowView{
				Show: "now",
				Machines: []nowMachine{
					{machineRow: m1, Trail: machineTrail(m1, true), When: "сейчас", Live: true, Bad: true},
					{machineRow: m2, Trail: machineTrail(m2, true), When: "сейчас", Live: true, Bad: true},
				},
			},
		}
	}

	adminHTML := renderTemplateForTest(t, "now", makeNowData("admin"))
	for _, want := range []string{"trail-with-menu", "now-menu-0", "now-menu-1", "Сбросить SMB-сессию…", "Игнорировать в DHCP…", "SMB зависла", "DHCP-флуд", "now-smb-disconnect-dialog", "now-dhcp-ignore-dialog"} {
		if !strings.Contains(adminHTML, want) {
			t.Fatalf("admin now page missing %q", want)
		}
	}

	opHTML := renderTemplateForTest(t, "now", makeNowData("operator"))
	if !strings.Contains(opHTML, "now-menu-0") || strings.Contains(opHTML, "now-menu-1") || strings.Contains(opHTML, "Игнорировать в DHCP…") {
		t.Fatalf("operator now page menu visibility mismatch")
	}

	obsHTML := renderTemplateForTest(t, "now", makeNowData("observer"))
	if strings.Contains(obsHTML, "trail-with-menu") || strings.Contains(obsHTML, "now-menu-0") || strings.Contains(obsHTML, "now-smb-disconnect-dialog") {
		t.Fatalf("observer now page must not render action menus or dialogs")
	}

	// Machine page shows active/stale SMB, HTTP, and DHCP ignored banners.
	machineHTML := renderTemplateForTest(t, "machine", pageData{
		Info:      requestContext{User: User{ID: 1, Username: "admin", Role: "admin"}, CSRF: "csrf-token", Path: "/events/machines/a4:bb:6d:14:cb:f7"},
		MACFilter: "a4:bb:6d:14:cb:f7",
		Machine: &machineCard{
			MachineInfo: MachineInfo{MAC: "a4:bb:6d:14:cb:f7", Name: "Dell", DHCPIgnored: true},
			Known:       true,
			ActiveSMB:   staleSMB,
			ActiveHTTP:  staleHTTP,
		},
	})
	for _, want := range []string{"Зависшая SMB-сессия", "Зависшая HTTP-передача", "Игнорируется в DHCP", "machine-smb-disconnect-dialog", "machine-http-cancel-dialog", "Разрешить в DHCP"} {
		if !strings.Contains(machineHTML, want) {
			t.Fatalf("machine page missing %q", want)
		}
	}

	// Machines list shows DHCP ignored badge and Allow button for admin.
	machinesHTML := renderTemplateForTest(t, "machines", pageData{
		Info: requestContext{User: User{ID: 1, Username: "admin", Role: "admin"}, CSRF: "csrf-token", Path: "/machines"},
		MachineList: &machineList{
			Available: true,
			Total:     1,
			CanEdit:   true,
			CanDelete: true,
			CSRF:      "csrf-token",
			Rows: []machineListRow{
				{MachineInfo: MachineInfo{MAC: "de:ad:be:ef:00:99", DHCPIgnored: true}},
			},
		},
	})
	if !strings.Contains(machinesHTML, "Игнорируется в DHCP") || !strings.Contains(machinesHTML, "Разрешить DHCP") {
		t.Fatalf("machines list missing ignored badge or allow button")
	}
}

func renderTemplateForTest(t *testing.T, name string, data pageData) string {
	t.Helper()
	var buf strings.Builder
	if err := pagesByLanguage[langRU].ExecuteTemplate(&buf, name, data); err != nil {
		t.Fatalf("render %s: %v", name, err)
	}
	return buf.String()
}
