package control

import (
	"strings"
	"testing"
	"time"

	"pxe-platform/internal/boot"
)

func bootFilesFixture(wim boot.Event) []boot.Event {
	t0 := time.Date(2026, 10, 23, 7, 0, 0, 0, time.UTC)
	mac := "bc:24:11:52:02:39"
	wim.MAC, wim.RemoteAddr, wim.Path = mac, "10.30.0.19:5002", "/assets/windows-profiles/2/boot.wim"
	if wim.At.IsZero() {
		wim.At = t0.Add(40 * time.Second)
	}
	return []boot.Event{
		{At: t0, Kind: "menu", MAC: mac, RemoteAddr: "10.30.0.19:5000", Path: "/boot/menu.ipxe"},
		{At: t0.Add(20 * time.Second), Kind: "asset", MAC: mac, RemoteAddr: "10.30.0.19:5001", Path: "/assets/windows-profiles/2/wimboot", Status: 200, Bytes: 76064, Size: 76064},
		wim,
	}
}

func TestChoiceStepShowsMissingAndCutBootFiles(t *testing.T) {
	names := map[string]string{"winiso_2": "Win11"}
	cases := []struct {
		name  string
		wim   boot.Event
		stage string
		sub   string
		trail string
	}{
		{"served", boot.Event{Kind: "asset", Status: 200, Bytes: 500, Size: 500}, "files", "Win11", "done,done,done,done,done,none"},
		{"old event without status", boot.Event{Kind: "asset"}, "files", "Win11", "done,done,done,done,done,none"},
		{"missing", boot.Event{Kind: "asset", Status: 404}, "files_failed", "boot.wim · файл не найден", "done,done,done,done,failed,none"},
		{"cut", boot.Event{Kind: "asset", Status: 200, Bytes: 312, Size: 540}, "files_failed", "boot.wim · передача прервалась", "done,done,done,done,failed,none"},
	}
	for _, c := range cases {
		rows := machineRows(bootFilesFixture(c.wim), nil, names)
		if len(rows) != 1 || rows[0].Stage != c.stage {
			t.Fatalf("%s: rows=%+v", c.name, rows)
		}
		steps := machineTrail(rows[0], false)
		sub := steps[4].Sub
		if steps[4].File != "" {
			sub = steps[4].File + " · " + steps[4].Sub
		}
		if sub != c.sub || trailStates(steps) != c.trail {
			t.Fatalf("%s: sub=%q trail=%s", c.name, sub, trailStates(steps))
		}
		sessions := machineSessions(rows[0].MAC, bootFilesFixture(c.wim), nil, names)
		if len(sessions) != 1 || (c.stage == "files_failed") != (sessions[0].Outcome == "files_failed") || (c.stage == "files_failed" && !sessions[0].Failed()) {
			t.Fatalf("%s: sessions=%+v", c.name, sessions)
		}
	}
	// A retry that gets the whole file clears the failure.
	events := bootFilesFixture(boot.Event{Kind: "asset", Status: 200, Bytes: 312, Size: 540})
	retry := events[2]
	retry.At, retry.Bytes = retry.At.Add(time.Minute), 540
	if rows := machineRows(append(events, retry), nil, names); rows[0].Stage != "files" {
		t.Fatalf("retry: %+v", rows[0])
	}
}

func TestChoiceStepShowsADownloadInProgress(t *testing.T) {
	now := time.Now()
	events := bootFilesFixture(boot.Event{Kind: "asset", Status: 200, Bytes: 500, Size: 500})[:2]
	rows := machineRows(events, nil, map[string]string{"winiso_2": "Win11"})
	attachDownloads(rows, []boot.Transfer{{Path: "/assets/windows-profiles/2/boot.wim", RemoteAddr: "10.30.0.19:5002", Size: 540 << 20, Bytes: 312 << 20}, {Path: "/x", RemoteAddr: "10.30.0.50:1"}}, now)
	if rows[0].Download == nil || !rows[0].LastSeen.Equal(now) {
		t.Fatalf("row=%+v", rows[0])
	}
	steps := machineTrail(rows[0], true)
	if steps[4].State != "current" || steps[4].File != "boot.wim" || steps[4].Bytes != 312<<20 || steps[4].Total != 540<<20 {
		t.Fatalf("steps=%+v", steps)
	}
	body := renderPage("now", pageData{Info: requestContext{}, Now: &nowView{Machines: []nowMachine{{machineRow: rows[0], Trail: steps, Live: true}}}})
	if !strings.Contains(body, "boot.wim · 312,0 МБ из 540,0 МБ") {
		t.Fatalf("now page: %s", body)
	}
}
