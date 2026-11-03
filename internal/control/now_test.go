package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"omb/internal/images"
)

func trailStates(steps []trailStep) string {
	var out []string
	for _, step := range steps {
		out = append(out, step.State)
	}
	return strings.Join(out, ",")
}

func TestMachineTrailMarksDoneCurrentAndFailedSteps(t *testing.T) {
	cases := []struct {
		row  machineRow
		live bool
		want string
	}{
		{machineRow{Stage: "pxe"}, true, "done,current,none,none,none,none,none"},
		{machineRow{Stage: "loader_failed", Firmware: "BIOS"}, true, "done,failed,none,none,none,none,none"},
		{machineRow{Stage: "menu"}, true, "done,done,done,done,current,none,none"},
		{machineRow{Stage: "menu"}, false, "done,done,done,done,none,none,none"},
		{machineRow{Stage: "files", Choice: "Win11"}, false, "done,done,done,done,done,done,none"},
		{machineRow{Stage: "files", Choice: "Win11"}, true, "done,done,done,done,done,done,current"},
		{machineRow{Stage: "copy", Choice: "Win11"}, false, "done,done,done,done,done,done,current"},
		{machineRow{Stage: "copied", Choice: "Win11"}, false, "done,done,done,done,done,done,done"},
		{machineRow{Stage: "stuck", Choice: "Win11"}, false, "done,done,done,done,done,done,failed"},
		{machineRow{Stage: "files"}, true, "done,done,done,done,none,done,current"},
	}
	for _, c := range cases {
		if got := trailStates(machineTrail(c.row, c.live)); got != c.want {
			t.Errorf("%+v live=%v: %s, want %s", c.row, c.live, got, c.want)
		}
	}
	steps := machineTrail(machineRow{Stage: "menu", Firmware: "UEFI", Choice: "Win11"}, true)
	if steps[1].Sub != "UEFI" || steps[3].Sub != "ждёт выбора" || steps[4].Sub != "Win11" {
		t.Fatalf("subs=%+v", steps)
	}
	if early := machineTrail(machineRow{Stage: "pxe", Firmware: "UEFI"}, false); early[1].Sub != "" {
		t.Fatalf("firmware shown on a step not reached: %+v", early)
	}
}

func TestNowAlertsNameWhatNeedsAPerson(t *testing.T) {
	data := pageData{
		Storages:        []images.Storage{{Name: "NAS", State: "unavailable", Error: "host is down"}, {Name: "Local", State: "ready"}},
		WindowsProfiles: []WindowsProfile{{WindowsProfileSource: WindowsProfileSource{Title: "Windows 10"}, State: "unavailable"}, {WindowsProfileSource: WindowsProfileSource{Title: "Windows 11"}, State: "ready"}},
		DriverConfigs:   []DriverConfig{{Title: "VirtIO", State: "preparing"}, {Title: "RST", State: "error", Error: "no space"}, {Title: "Ready", State: "ready"}},
	}
	alerts := nowAlerts(data)
	var titles []string
	for _, a := range alerts {
		titles = append(titles, a.State+":"+a.Title)
	}
	want := "bad:Хранилище «NAS» недоступно,bad:Windows 10: образ недоступен,accent:VirtIO: собирается WinPE,bad:RST: ошибка сборки WinPE"
	if strings.Join(titles, ",") != want {
		t.Fatalf("alerts=%v", titles)
	}
	if got := translate(langEN, alerts[0].Title); got != "Storage “NAS” is unavailable" {
		t.Fatalf("english alert=%q", got)
	}
}

func TestAgoSaysHowLongAgo(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.Local)
	for d, want := range map[time.Duration]string{10 * time.Second: "сейчас", 12 * time.Minute: "12 мин назад", 5 * time.Hour: "5 ч назад", 50 * time.Hour: "19.10 10:00"} {
		if got := ago(now, now.Add(-d)); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
	if translate(langEN, "12 мин назад") != "12 min ago" {
		t.Fatal("ago is not translated")
	}
}

func TestHomeIsNowAndInstallationsMoved(t *testing.T) {
	s := libraryServer()
	s.Store = authOnlyStore{user: User{ID: 2, Role: "operator", Enabled: true}}
	body := libraryGet(t, s, "/")
	for _, want := range []string{`id="live"`, "<h1>Сейчас</h1>", `class="service-strip"`, "DSM", `src="/assets/now.js"`, `href="/" class="active"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("home lacks %q: %s", want, body)
		}
	}
	if body := libraryGet(t, s, "/installations"); !strings.Contains(body, "<h1>Загрузки</h1>") || !strings.Contains(body, `href="/installations" class="active"`) {
		t.Fatalf("installations page: %s", body)
	}
	req := httptest.NewRequest(http.MethodGet, "/no-such-page", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("unknown path status=%d", res.Code)
	}
	script := httptest.NewRecorder()
	s.Handler().ServeHTTP(script, httptest.NewRequest(http.MethodGet, "/assets/now.js", nil))
	if script.Code != http.StatusOK || !strings.Contains(script.Body.String(), "getElementById('live')") {
		t.Fatalf("now.js status=%d", script.Code)
	}
}

func TestNowPageHidesPublishForObservers(t *testing.T) {
	data := samplePageData(langRU)
	data.Info.Path = "/"
	data.Now = &nowView{Machines: []nowMachine{{machineRow: machineRow{MAC: "bc:24:11:04:2a:ed", Stage: "loader_failed"}, Trail: machineTrail(machineRow{Stage: "loader_failed"}, true), Bad: true, When: "сейчас"}}}
	if body := renderPage("now", data); !strings.Contains(body, `action="/menu/publish"`) || !strings.Contains(body, `class="trail trail-failed"`) || !strings.Contains(body, "Загрузчик не отдан по TFTP.") {
		t.Fatalf("admin now page: %s", body)
	}
	data.Info.User.Role = "observer"
	if body := renderPage("now", data); strings.Contains(body, `action="/menu/publish"`) {
		t.Fatal("observer sees publish")
	}
	data.Lang = langEN
	data.Info.User.Role = "admin"
	if found := cyrillic.FindAllString(renderPage("now", data), 5); len(found) > 0 {
		t.Fatalf("English now page has Russian text: %v", found)
	}
}

func TestNowPageFiltersMachines(t *testing.T) {
	now := time.Now()
	live := machineRow{MAC: "aa:aa:aa:aa:aa:01", Stage: "menu", LastSeen: now}
	failed := machineRow{MAC: "aa:aa:aa:aa:aa:02", Stage: "files_failed", Problem: "missing:boot.wim", LastSeen: now.Add(-2 * time.Hour)}
	old := machineRow{MAC: "aa:aa:aa:aa:aa:03", Stage: "files", LastSeen: now.Add(-48 * time.Hour)}
	render := func(show string, rows ...machineRow) string {
		view := &nowView{Show: show}
		fillNowMachines(view, rows, now)
		data := samplePageData(langRU)
		data.Info.Path = "/"
		data.Now = view
		return renderPage("now", data)
	}
	view := &nowView{Show: "day"}
	fillNowMachines(view, []machineRow{live, failed, old}, now)
	if view.Booting != 1 || view.Day != 2 || view.Failed != 1 || len(view.Machines) != 2 || view.Last == nil || view.Last.MAC != live.MAC {
		t.Fatalf("view=%+v", view)
	}
	if body := render("now", live, failed, old); !strings.Contains(body, live.MAC) || strings.Contains(body, failed.MAC) || strings.Contains(body, old.MAC) {
		t.Fatalf("now: %s", body)
	}
	if body := render("failed", live, failed, old); strings.Contains(body, live.MAC+"</a></div>") || !strings.Contains(body, failed.MAC) {
		t.Fatalf("failed: %s", body)
	}
	if body := render("now", failed, old); !strings.Contains(body, "Сейчас никто не грузится.") || !strings.Contains(body, "Последняя загрузка:") || !strings.Contains(body, failed.MAC) {
		t.Fatalf("empty now: %s", body)
	}
}

func TestMachineTrailSeparatesChoiceFromFileTransfer(t *testing.T) {
	steps := machineTrail(machineRow{Stage: "choice", Choice: "Ubuntu Rescue"}, true)
	if len(steps) != 7 || steps[4].Label != "Выбор" || steps[4].State != "done" || steps[4].Sub != "Ubuntu Rescue" || steps[5].Label != "Файлы" || steps[5].State != "current" {
		t.Fatalf("choice trail=%+v", steps)
	}
	steps = machineTrail(machineRow{Stage: "files"}, false)
	if len(steps) != 7 || steps[4].State != "none" || steps[4].Sub != "" || steps[5].State != "done" {
		t.Fatalf("file-only trail=%+v", steps)
	}
}
