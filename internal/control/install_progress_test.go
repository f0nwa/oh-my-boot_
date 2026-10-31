package control

import (
	"strings"
	"testing"
	"time"

	"omb/internal/agent"
	"omb/internal/boot"
)

func installFixture() (time.Time, []boot.Event) {
	t0 := time.Date(2026, 10, 23, 7, 0, 0, 0, time.UTC)
	mac := "bc:24:11:52:02:39"
	return t0, []boot.Event{
		{At: t0.Add(10 * time.Second), Kind: "choice", Path: "/boot/choice?id=2", MAC: mac, RemoteAddr: "10.30.0.19:5000"},
		{At: t0, Kind: "menu", MAC: mac, RemoteAddr: "10.30.0.19:5000", Path: "/menu.ipxe"},
		{At: t0.Add(20 * time.Second), MAC: mac, RemoteAddr: "10.30.0.19:5001", Path: "/assets/windows-profiles/2/wimboot"},
		{At: t0.Add(25 * time.Second), MAC: mac, RemoteAddr: "10.30.0.19:5002", Path: "/assets/windows-profiles/2/boot.wim"},
	}
}

func tp(t time.Time) *time.Time { return &t }

func TestInstallProgressFollowsTheShareSession(t *testing.T) {
	t0, httpEvents := installFixture()
	mac := "bc:24:11:52:02:39"
	session := agent.SMBSession{ID: "1002", IP: "10.30.0.19", Profile: "2", Started: t0.Add(2 * time.Minute), LastSeen: t0.Add(40 * time.Minute), Setup: true}
	// An unrelated client on the share is ignored: no boot request came from it.
	other := agent.SMBSession{ID: "7", IP: "10.30.0.50", Started: t0, LastSeen: t0}
	cases := []struct {
		name  string
		edit  func(*agent.SMBSession)
		stage string
		trail string
	}{
		{"connected", func(*agent.SMBSession) {}, "winpe", "done,done,done,done,done,done,current"},
		// Setup holds install.wim open on the edition and disk screens; that
		// is not copying yet.
		{"choosing", func(s *agent.SMBSession) {
			s.WIMOpened, s.WIMOpen, s.CopyRead = tp(t0.Add(3*time.Minute)), true, 20<<20
		}, "winpe", "done,done,done,done,done,done,current"},
		{"copying", func(s *agent.SMBSession) {
			s.WIMOpened, s.WIMOpen, s.Copying, s.CopyRead = tp(t0.Add(3*time.Minute)), true, tp(t0.Add(30*time.Minute)), 2<<30
		}, "copy", "done,done,done,done,done,done,current"},
		{"copied", func(s *agent.SMBSession) {
			s.WIMOpened, s.Copying, s.Copied, s.Ended, s.CopyRead = tp(t0.Add(3*time.Minute)), tp(t0.Add(30*time.Minute)), tp(t0.Add(38*time.Minute)), tp(t0.Add(40*time.Minute)), 5<<30
		}, "copied", "done,done,done,done,done,done,done"},
		{"cut short", func(s *agent.SMBSession) {
			s.Copying, s.Ended, s.CopyRead = tp(t0.Add(30*time.Minute)), tp(t0.Add(35*time.Minute)), 2<<30
		}, "copy_broken", "done,done,done,done,done,done,failed"},
		{"left", func(s *agent.SMBSession) { s.Ended = tp(t0.Add(5 * time.Minute)) }, "winpe_left", "done,done,done,done,done,done,failed"},
	}
	for _, c := range cases {
		s := session
		c.edit(&s)
		install := installEvents(agent.SMBSessions{Since: t0.Add(-time.Hour), Sessions: []agent.SMBSession{s, other}}, httpEvents)
		rows := machineRowsWith(httpEvents, nil, install, map[string]string{"install_2": "Win11"}, t0.Add(41*time.Minute))
		if len(rows) != 1 || rows[0].Stage != c.stage || rows[0].Choice != "Win11" {
			t.Fatalf("%s: rows=%+v", c.name, rows)
		}
		// Setup keeps the machine on the page while it holds the share.
		if c.name == "connected" && !rows[0].LastSeen.Equal(s.LastSeen) {
			t.Fatalf("%s: last seen %s", c.name, rows[0].LastSeen)
		}
		if c.name == "copying" && (rows[0].CopyRead != 2<<30 || machineTrail(rows[0], false)[6].Bytes != 2<<30) {
			t.Fatalf("copying: read %d", rows[0].CopyRead)
		}
		if got := trailStates(machineTrail(rows[0], false)); got != c.trail {
			t.Fatalf("%s: trail=%s", c.name, got)
		}
		sessions := machineSessionsWith(mac, httpEvents, nil, install, nil, t0.Add(41*time.Minute))
		if len(sessions) != 1 || sessions[0].Outcome != c.stage {
			t.Fatalf("%s: one boot expected, got %+v", c.name, sessions)
		}
		var texts []string
		for _, step := range sessions[0].Steps {
			if step.Stage == "Установка" {
				texts = append(texts, step.Text)
			}
		}
		if len(texts) == 0 || texts[0] != "WinPE подключился к установщику" {
			t.Fatalf("%s: install steps %v", c.name, texts)
		}
		if c.name == "copied" && strings.Join(texts, "|") != "WinPE подключился к установщику|Копирует Windows|Файлы скопированы, перезагрузка|Сеанс закрыт, машина перезагружается" {
			t.Fatalf("copied steps %v", texts)
		}
	}
}

func TestInstallProgressMarksSilentWinPEAsStuck(t *testing.T) {
	t0, httpEvents := installFixture()
	watched := installLog{Known: true, Since: t0.Add(-time.Hour)}
	if rows := machineRowsWith(httpEvents, nil, watched, nil, t0.Add(3*time.Minute)); rows[0].Stage != "files" {
		t.Fatalf("too early: %s", rows[0].Stage)
	}
	rows := machineRowsWith(httpEvents, nil, watched, nil, t0.Add(6*time.Minute))
	if rows[0].Stage != "stuck" {
		t.Fatalf("stage=%s", rows[0].Stage)
	}
	if s := machineSessionsWith(rows[0].MAC, httpEvents, nil, watched, nil, t0.Add(6*time.Minute)); s[0].Outcome != "stuck" || !s[0].Failed() {
		t.Fatalf("session=%+v", s[0])
	}
	// Without the Agent, or for boots before it started watching, silence
	// proves nothing.
	for _, install := range []installLog{{}, {Known: true, Since: t0.Add(time.Minute)}} {
		if rows := machineRowsWith(httpEvents, nil, install, nil, t0.Add(time.Hour)); rows[0].Stage != "files" {
			t.Fatalf("%+v: stage=%s", install, rows[0].Stage)
		}
	}
}
