package control

import (
	"context"
	"fmt"
	"strings"
	"time"

	"omb/internal/agent"
	"omb/internal/boot"
)

// After boot.wim WinPE starts, connects to the install share and runs
// Windows Setup from it. The share is the last place the server sees the
// machine: Setup reboots once the files are copied and reports nothing more.

// stuckAfter is how long WinPE may take from boot.wim to the share before the
// machine counts as stuck.
const stuckAfter = 5 * time.Minute

// Install states of one share session, furthest last.
const (
	installLeft   = 1 // disconnected before copying
	installWinPE  = 2 // connected, Setup not copying yet (edition and disk screens)
	installBroken = 3 // disconnected while copying
	installCopy   = 4 // reading the install image
	installCopied = 5 // install image released after copying: Setup reboots
)

var installStageNames = map[int]string{installLeft: "winpe_left", installWinPE: "winpe", installBroken: "copy_broken", installCopy: "copy", installCopied: "copied"}

// installEvent is one moment of a share session, tied to a machine.
type installEvent struct {
	At      time.Time
	MAC, IP string
	Session string
	Profile string
	Kind    string    // connected, copy, copied, ended, seen
	State   int       // the session's furthest state
	Read    int64     // bytes of the install image read so far
	Started time.Time // when the share session began
}

// installLog is what the Agent saw on the install share. Known is false when
// the Agent did not answer, so a missing session proves nothing.
type installLog struct {
	Known  bool
	Since  time.Time
	Events []installEvent
}

// sessionState is how far a share session got.
func sessionState(s agent.SMBSession) int {
	switch {
	case s.Copied != nil:
		return installCopied
	case s.Copying != nil && s.Ended != nil:
		return installBroken
	case s.Copying != nil:
		return installCopy
	case s.Ended != nil:
		return installLeft
	}
	return installWinPE
}

// installEvents ties share sessions to machines. WinPE connects from the IP
// that fetched boot.wim minutes earlier, so the nearest earlier HTTP request
// from that IP names the MAC.
func installEvents(v agent.SMBSessions, httpEvents []boot.Event) installLog {
	out := installLog{Known: true, Since: v.Since}
	for _, s := range v.Sessions {
		mac := ""
		var best time.Duration = macMatchWindow + 1
		for _, e := range httpEvents {
			if e.MAC == "" || eventIP(e.RemoteAddr) != s.IP || e.At.After(s.Started) {
				continue
			}
			if d := s.Started.Sub(e.At); d <= macMatchWindow && d < best {
				best, mac = d, e.MAC
			}
		}
		if mac == "" {
			continue
		}
		state := sessionState(s)
		add := func(at time.Time, kind string) {
			out.Events = append(out.Events, installEvent{At: at, MAC: mac, IP: s.IP, Session: s.ID, Profile: s.Profile, Kind: kind, State: state, Read: s.CopyRead, Started: s.Started})
		}
		add(s.Started, "connected")
		if s.Copying != nil {
			add(*s.Copying, "copy")
		}
		if s.Copied != nil {
			add(*s.Copied, "copied")
		}
		if s.Ended != nil {
			add(*s.Ended, "ended")
		} else if s.Copied == nil {
			add(s.LastSeen, "seen")
		}
	}
	return out
}

// loadInstallLog asks the Agent for share sessions; an error leaves the log
// unknown.
func (s *Server) loadInstallLog(ctx context.Context, httpEvents []boot.Event) installLog {
	if s.Agent == nil {
		return installLog{}
	}
	v, err := s.Agent.SMBSessions(ctx)
	if err != nil {
		return installLog{}
	}
	return installEvents(v, httpEvents)
}

// isBootWIM reports a request for WinPE itself.
func isBootWIM(e boot.Event) bool {
	return strings.HasSuffix(strings.ToLower(e.Path), "/boot.wim")
}

// installStep turns a share moment into a timeline line.
func installStep(e installEvent) machineStep {
	step := machineStep{At: e.At, Stage: "Установка", Raw: e.IP}
	switch e.Kind {
	case "connected":
		step.Text = "WinPE подключился к установщику"
		if e.Profile != "" {
			step.Raw = fmt.Sprintf("%s · \\\\ombisos\\%s", e.IP, e.Profile)
		}
	case "copy":
		step.Text, step.Raw = "Копирует Windows", "install.wim"
	case "copied":
		step.Text, step.Raw = "Файлы скопированы, перезагрузка", "install.wim"
	case "ended":
		switch e.State {
		case installLeft:
			step.Text, step.Failed = "Установщик отключился до копирования", true
		case installBroken:
			step.Text, step.Failed = "Связь прервалась во время копирования", true
		default:
			step.Text = "Сеанс закрыт, машина перезагружается"
		}
		step.Raw = ""
	}
	if (e.Kind == "copied" || e.Kind == "ended") && e.Read > 0 {
		step.Bytes = e.Read
	}
	return step
}
