package control

import (
	_ "embed"
	"fmt"
	"net/http"
	"time"

	"pxe-platform/internal/agent"
	"pxe-platform/internal/boot"
)

// trailStep is one of the seven boot steps shown for a machine on the
// «Сейчас» page. State is done, current, failed or none.
type trailStep struct {
	Label, Sub, State string
	Bytes             int64 // shown after Sub
}

type nowMachine struct {
	machineRow
	Trail []trailStep
	When  string
	Live  bool
	Bad   bool
}

type nowService struct {
	Name, Value, State string // State: ok, accent, bad
}

type nowAlert struct {
	State, Title, Detail, Link string
}

// nowView is everything the «Сейчас» page shows.
type nowView struct {
	Services      []nowService
	Machines      []nowMachine
	Booting, Day  int
	Failed        int
	Alerts        []nowAlert
	Audit         []AuditEvent
	ActiveVersion int64
}

// liveWindow is how long after its last request a machine still counts as
// booting right now.
const liveWindow = 3 * time.Minute

var trailLabels = []string{"DHCP", "TFTP", "iPXE", "Меню", "Выбор", "Образ", "Установка"}

// machineTrail turns a machine summary into the seven boot steps. The step
// after the last completed one is current while the machine is still active.
func machineTrail(row machineRow, live bool) []trailStep {
	done := 1 // the machine is in the log, so DHCP answered
	failed := -1
	switch row.Stage {
	case "loader":
		done = 2
	case "loader_failed":
		failed = 1
	case "ipxe":
		done = 3
	case "menu":
		done = 4
	case "files", "winpe", "copy":
		done = 6
	case "stuck", "winpe_left", "copy_broken":
		done, failed = 6, 6
	case "copied":
		done = 7
	}
	if row.Stage == "files" && row.Choice == "" {
		done = 4
	}
	// While Setup holds the share, the machine is busy even if the last
	// boot request was long ago.
	installing := row.Stage == "winpe" || row.Stage == "copy"
	steps := make([]trailStep, len(trailLabels))
	for i, label := range trailLabels {
		step := trailStep{Label: label, State: "none"}
		switch {
		case i == failed:
			step.State = "failed"
		case i < done:
			step.State = "done"
		case i == done && (live || installing) && failed < 0:
			step.State = "current"
		}
		steps[i] = step
	}
	// The firmware may come from an earlier boot; show it only on a step this
	// boot reached.
	if row.Firmware != "" && steps[1].State != "none" {
		steps[1].Sub = row.Firmware
	}
	if row.Choice != "" {
		steps[4].Sub = row.Choice
	}
	if row.Stage == "menu" && live {
		steps[3].Sub = "ждёт выбора"
	}
	steps[6].Sub = map[string]string{"winpe": "WinPE подключился", "copy": "копирует Windows", "copied": "скопировано, перезагрузка", "stuck": "застрял в WinPE", "winpe_left": "отключился", "copy_broken": "связь прервалась"}[row.Stage]
	if row.Stage == "copy" || row.Stage == "copied" || row.Stage == "copy_broken" {
		steps[6].Bytes = row.CopyRead
	}
	return steps
}

// ago says how long ago t was, in short words.
func ago(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "сейчас"
	case d < time.Hour:
		return fmt.Sprintf("%d мин назад", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d ч назад", int(d.Hours()))
	}
	return t.Local().Format("02.01 15:04")
}

// nowAlerts lists what needs a person: storages that are down, images and
// installations that cannot boot, builds in progress or failed.
func nowAlerts(data pageData) []nowAlert {
	var alerts []nowAlert
	for _, storage := range data.Storages {
		if storage.State == "unavailable" {
			alerts = append(alerts, nowAlert{State: "bad", Title: fmt.Sprintf("Хранилище «%s» недоступно", storage.Name), Detail: storage.Error, Link: "/storages"})
		}
	}
	for _, profile := range data.WindowsProfiles {
		if profile.State == "error" || profile.State == "unavailable" {
			alerts = append(alerts, nowAlert{State: "bad", Title: fmt.Sprintf("%s: образ недоступен", profile.Title), Detail: profile.Error, Link: "/images"})
		}
	}
	for _, config := range data.DriverConfigs {
		switch config.State {
		case "error":
			alerts = append(alerts, nowAlert{State: "bad", Title: fmt.Sprintf("%s: ошибка сборки WinPE", config.Title), Detail: config.Error, Link: "/installations"})
		case "preparing":
			alerts = append(alerts, nowAlert{State: "accent", Title: fmt.Sprintf("%s: собирается WinPE", config.Title), Link: "/installations"})
		case "pending":
			alerts = append(alerts, nowAlert{State: "accent", Title: fmt.Sprintf("%s: нужна сборка WinPE", config.Title), Link: "/installations"})
		}
	}
	if data.Recovery != nil && data.Recovery.LastError != "" {
		alerts = append(alerts, nowAlert{State: "bad", Title: "Автовосстановление хранилищ", Detail: data.Recovery.LastError, Link: "/storages"})
	}
	return alerts
}

// nowPage is the home page: what is happening on the PXE network right now.
func (s *Server) nowPage(w http.ResponseWriter, r *http.Request) {
	data := s.libraryData(r)
	data.Message = r.URL.Query().Get("message")
	s.loadServiceStatus(r.Context(), &data)
	if configs, err := s.Store.DriverConfigs(r.Context()); err == nil {
		data.DriverConfigs = configs
	}
	view := &nowView{ActiveVersion: data.Info.Draft.Active}
	bootState, agentState := "ok", "ok"
	if !data.BootOK {
		bootState = "bad"
	}
	if !data.AgentOK {
		agentState = "bad"
	}
	view.Services = append(view.Services, nowService{Name: "Boot", Value: data.BootStatus, State: bootState}, nowService{Name: "Network Agent", Value: data.AgentStatus, State: agentState})
	for _, storage := range data.Storages {
		state := "ok"
		switch storage.State {
		case "unavailable":
			state = "bad"
		case "ready":
		default:
			state = "accent"
		}
		value := storage.Root
		if storage.Kind == "smb" {
			value = "//" + storage.Server + "/" + storage.Share
		}
		view.Services = append(view.Services, nowService{Name: storage.Name, Value: value, State: state})
	}
	if _, active, err := s.currentDraft(r.Context()); err == nil {
		menu := nowService{Name: "Меню у клиентов", State: "ok"}
		if view.ActiveVersion > 0 {
			menu.Value = fmt.Sprintf("v%d · пунктов: %d", view.ActiveVersion, len(active.Items))
		} else {
			menu.Value, menu.State = "ещё не публиковалось", "accent"
		}
		view.Services = append(view.Services, menu)
	}
	var httpEvents []boot.Event
	if s.Boot != nil {
		if v, err := s.Boot.Events(r.Context(), 1000); err == nil {
			httpEvents = v
		} else {
			data.EventError = "Журнал временно недоступен"
		}
	}
	var dhcp []agent.DHCPEvent
	if s.Agent != nil {
		if v, err := s.Agent.DHCPEvents(r.Context()); err == nil {
			dhcp = v
		} else {
			data.DHCPError = "Журнал DHCP недоступен"
		}
	}
	now := time.Now()
	install := s.loadInstallLog(r.Context(), httpEvents)
	for _, row := range machineRowsWith(httpEvents, dhcp, install, s.menuNames(r.Context()), now) {
		if now.Sub(row.LastSeen) > 24*time.Hour && len(view.Machines) >= 5 {
			continue
		}
		live := now.Sub(row.LastSeen) <= liveWindow
		machine := nowMachine{machineRow: row, Trail: machineTrail(row, live), When: ago(now, row.LastSeen), Live: live, Bad: row.Stage == "loader_failed" || row.Stage == "stuck" || row.Stage == "copy_broken"}
		if live {
			view.Booting++
		}
		if machine.Bad {
			view.Failed++
		}
		if now.Sub(row.LastSeen) <= 24*time.Hour {
			view.Day++
		}
		if len(view.Machines) < 8 {
			view.Machines = append(view.Machines, machine)
		}
	}
	view.Alerts = nowAlerts(data)
	if Allowed(data.Info.User.Role, "users") {
		if events, err := s.Store.AuditList(r.Context()); err == nil {
			view.Audit = events[:min(5, len(events))]
		}
	}
	if draft, active, err := s.currentDraft(r.Context()); err == nil {
		data.Changes = diffMenuDraft(active, draft)
	}
	data.Now = view
	render(w, "now", data)
}

//go:embed now.js
var nowJS []byte

func nowScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(nowJS)
}
