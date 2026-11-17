package control

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"time"

	"omb/internal/agent"
	"omb/internal/boot"
	"omb/internal/nfsstorage"
)

// trailStep is one of the boot steps shown for a machine on the
// «Сейчас» page. State is done, current, failed or none.
type trailStep struct {
	Label, Sub, State string
	Bytes             int64  // shown after Sub
	Total             int64  // with Bytes: "Bytes of Total"
	File              string // a boot file name, shown before Sub
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
	Services     []nowService
	Machines     []nowMachine
	Booting, Day int
	Failed       int
	// Show is the machine filter: now (booting right now), day (the last
	// 24 hours) or failed (failed in the last 24 hours).
	Show string
	// More counts machines the filter matched beyond the page's limit; Last
	// is the latest machine seen, for the empty "now" view.
	More          int
	Last          *nowMachine
	Alerts        []nowAlert
	Audit         []AuditEvent
	ActiveVersion int64
}

// nowMachineLimit is how many machines the page lists; the rest are in the
// Journal.
const nowMachineLimit = 50

// liveWindow is how long after its last request a machine still counts as
// booting right now.
const liveWindow = 3 * time.Minute

var trailLabels = []string{"DHCP", "TFTP", "Загрузчик", "Меню", "Выбор", "Файлы", "Установка"}

// machineTrail keeps explicit choice telemetry separate from file transfer.
// A missing choice stays unknown even when the boot files have been served.
func machineTrail(row machineRow, live bool) []trailStep {
	done := 1 // the machine is in the log, so DHCP answered
	failed := -1
	switch row.Stage {
	case "loader":
		done = 2
	case "loader_failed":
		failed = 1
	case "loader_rejected":
		done, failed = 2, 2
	case "ipxe":
		done = 3
	case "menu":
		done = 4
	case "choice":
		done = 5
	case "files_failed":
		done, failed = 5, 5
	case "files", "winpe", "copy":
		done = 6
	case "stuck", "winpe_left", "copy_broken":
		done, failed = 6, 6
	case "copied":
		done = 7
	}
	// A boot file still on its way: the choice is made but not yet loaded.
	if row.Download != nil && row.Stage == "files" {
		done = 5
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
	// The address and the firmware may come from an earlier boot; show them
	// only on steps this boot reached.
	if row.IP != "" && steps[0].State != "none" {
		steps[0].Sub = row.IP
	}
	if row.Firmware != "" && steps[1].State != "none" {
		steps[1].Sub = row.Firmware
	}
	if row.Stage == "loader_rejected" {
		steps[2].Sub = "отклонён прошивкой"
	}
	if row.Stage == "menu" && live {
		steps[3].Sub = "ждёт выбора"
	}
	steps[4].Sub = row.Choice
	// Later file or Setup progress never invents a missing choice event.
	if row.Choice == "" && row.Stage != "menu" {
		steps[4].State = "none"
	}
	switch {
	case row.Stage == "files_failed":
		steps[5].File, steps[5].Sub = problemFile(row.Problem), problemText(row.Problem)
	case row.Download != nil:
		steps[5].File, steps[5].Sub = fileName(row.Download.Path), ""
		steps[5].Bytes, steps[5].Total = row.Download.Bytes, row.Download.Size
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
	data := s.storageLibraryData(r)
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
		} else if storage.Kind == "nfs" {
			value = nfsstorage.Source(storage.Server, storage.ExportPath)
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
		if v, err := s.dhcpEvents(r.Context()); err == nil {
			dhcp = v
		} else {
			data.DHCPError = "Журнал DHCP недоступен"
		}
	}
	now := time.Now()
	install := s.loadInstallLog(r.Context(), httpEvents)
	rows := machineRowsWith(httpEvents, dhcp, install, s.menuNames(r.Context()), now)
	attachDownloads(rows, s.bootTransfers(r.Context()), now)
	describeMachines(rows, s.machineBook(r.Context(), rows, httpEvents), s.macRules(r.Context()))
	view.Show = r.URL.Query().Get("show")
	if view.Show != "day" && view.Show != "failed" {
		view.Show = "now"
	}
	fillNowMachines(view, rows, now)
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

// bootTransfers asks Boot which files it is sending now; without Boot there
// are none.
func (s *Server) bootTransfers(ctx context.Context) []boot.Transfer {
	if s.Boot == nil {
		return nil
	}
	transfers, err := s.Boot.Transfers(ctx)
	if err != nil {
		return nil
	}
	return transfers
}

// fillNowMachines counts the machines of the last 24 hours and lists those
// view.Show asks for: booting now, all of the day, or failed.
func fillNowMachines(view *nowView, rows []machineRow, now time.Time) {
	for _, row := range rows {
		live := now.Sub(row.LastSeen) <= liveWindow
		day := now.Sub(row.LastSeen) <= 24*time.Hour
		machine := nowMachine{machineRow: row, Trail: machineTrail(row, live), When: ago(now, row.LastSeen), Live: live, Bad: row.Stage == "loader_failed" || row.Stage == "loader_rejected" || row.Stage == "files_failed" || row.Stage == "stuck" || row.Stage == "copy_broken"}
		if view.Last == nil {
			last := machine
			view.Last = &last
		}
		if !day {
			// Older machines live in the Journal, not here.
			continue
		}
		view.Day++
		if live {
			view.Booting++
		}
		if machine.Bad {
			view.Failed++
		}
		switch {
		case view.Show == "now" && !live, view.Show == "failed" && !machine.Bad:
			continue
		case len(view.Machines) >= nowMachineLimit:
			view.More++
		default:
			view.Machines = append(view.Machines, machine)
		}
	}
}
