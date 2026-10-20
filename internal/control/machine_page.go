package control

import (
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"pxe-platform/internal/agent"
	"pxe-platform/internal/boot"
)

var macAddress = regexp.MustCompile(`^[0-9a-f]{2}(:[0-9a-f]{2}){5}$`)

// bootSessionGap separates two boots of the same machine: a pause this long
// between its requests starts a new attempt.
const bootSessionGap = 10 * time.Minute

// machineStep is one line of a machine's boot timeline.
type machineStep struct {
	At     time.Time
	Stage  string // DHCP, TFTP, iPXE, Меню, Образ
	Text   string
	Raw    string
	Failed bool
}

// bootSession is one boot attempt of a machine: its steps in time order and
// how far it got.
type bootSession struct {
	Start, End time.Time
	Steps      []machineStep
	Choice     string
	Outcome    string // loader_failed, dhcp_only, loader, menu, files
}

// OutcomeLabel names where the attempt stopped, in words.
func (b bootSession) OutcomeLabel() string {
	switch b.Outcome {
	case "loader_failed":
		return "Загрузчик не получен"
	case "dhcp_only":
		return "Дальше DHCP не видно"
	case "loader":
		return "Загрузчик получен, меню не запрошено"
	case "menu":
		return "Остановилась в меню"
	}
	return "Получила файлы установки"
}

// OK reports whether the attempt reached the installation files.
func (b bootSession) OK() bool { return b.Outcome == "files" }

// Failed reports whether the attempt hit an error rather than just stopping.
func (b bootSession) Failed() bool { return b.Outcome == "loader_failed" }

// machineSessions builds the boot attempts of one MAC from the HTTP and
// DHCP/TFTP journals, newest first. names maps menu keys to titles.
func machineSessions(mac string, httpEvents []boot.Event, dhcp []agent.DHCPEvent, names map[string]string) []bootSession {
	var steps []machineStep
	type choice struct {
		at   time.Time
		key  string
		rank int
	}
	var choices []choice
	for _, e := range resolveDHCPMACs(dhcp, httpEvents) {
		if e.MAC != mac {
			continue
		}
		step := machineStep{At: e.At, Stage: "DHCP", Text: dhcpKindLabel(e.Kind), Raw: strings.TrimSpace(e.IP + " " + e.Detail)}
		switch e.Kind {
		case "tftp-sent":
			step.Stage, step.Text, step.Raw = "TFTP", "Загрузчик отдан", e.Detail
		case "tftp-failed":
			step.Stage, step.Text, step.Raw, step.Failed = "TFTP", "Загрузчик не отдан", e.Detail, true
		}
		steps = append(steps, step)
	}
	for _, e := range httpEvents {
		if e.MAC != mac {
			continue
		}
		step := machineStep{At: e.At, Stage: "iPXE", Text: "Файл загрузки", Raw: e.Path}
		key, rank := choiceKey(e)
		switch {
		case e.Kind == "menu":
			step.Stage, step.Text = "Меню", "Показано меню"
		case rank > 0:
			title := names[key]
			if title == "" {
				title = key
			}
			step.Stage, step.Text = "Образ", title
			choices = append(choices, choice{e.At, title, rank})
		}
		steps = append(steps, step)
	}
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].At.Before(steps[j].At) })
	var sessions []bootSession
	for _, step := range steps {
		if len(sessions) == 0 || step.At.Sub(sessions[len(sessions)-1].End) > bootSessionGap {
			sessions = append(sessions, bootSession{Start: step.At})
		}
		current := &sessions[len(sessions)-1]
		current.Steps = append(current.Steps, step)
		current.End = step.At
	}
	for i := range sessions {
		session := &sessions[i]
		session.Outcome = "dhcp_only"
		best := 0
		for _, step := range session.Steps {
			switch {
			case step.Failed:
				session.Outcome = "loader_failed"
			case step.Stage == "TFTP" && session.Outcome == "dhcp_only":
				session.Outcome = "loader"
			case step.Stage == "Меню" && session.Outcome != "loader_failed":
				session.Outcome = "menu"
			case step.Stage == "Образ" && session.Outcome != "loader_failed":
				session.Outcome = "files"
			}
		}
		for _, c := range choices {
			if !c.at.Before(session.Start) && !c.at.After(session.End) && c.rank > best {
				session.Choice, best = c.key, c.rank
			}
		}
	}
	for i, j := 0, len(sessions)-1; i < j; i, j = i+1, j-1 {
		sessions[i], sessions[j] = sessions[j], sessions[i]
	}
	return sessions
}

// machinePage shows one machine: where its boots stopped and every step of
// the selected attempt.
func (s *Server) machinePage(w http.ResponseWriter, r *http.Request) {
	mac := strings.ToLower(r.PathValue("mac"))
	if !macAddress.MatchString(mac) {
		http.Error(w, "Неверный MAC-адрес", http.StatusBadRequest)
		return
	}
	data := pageData{Info: requestInfo(r), MACFilter: mac}
	var httpEvents []boot.Event
	if s.Boot == nil {
		data.EventError = "Служба Boot недоступна"
	} else if v, err := s.Boot.Events(r.Context(), 1000); err != nil {
		data.EventError = "Журнал временно недоступен"
	} else {
		httpEvents = v
	}
	var dhcp []agent.DHCPEvent
	if s.Agent == nil {
		data.DHCPError = "Журнал DHCP недоступен"
	} else if v, err := s.Agent.DHCPEvents(r.Context()); err != nil {
		data.DHCPError = "Журнал DHCP недоступен"
	} else {
		dhcp = v
	}
	names := s.menuNames(r.Context())
	for _, row := range machineRows(httpEvents, dhcp, names) {
		if row.MAC == mac {
			data.Machines = []machineRow{row}
		}
	}
	data.Sessions = machineSessions(mac, httpEvents, dhcp, names)
	if n, err := strconv.Atoi(r.URL.Query().Get("boot")); err == nil && n >= 0 && n < len(data.Sessions) {
		data.SessionIndex = n
	}
	render(w, "machine", data)
}
