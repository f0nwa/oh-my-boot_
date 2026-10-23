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
	Bytes  int64 // bytes sent or read, shown after the text
	Size   int64 // with Bytes: the whole file
	// problem marks a boot file this step failed to deliver.
	problem string

	bootWIM bool
}

// bootSession is one boot attempt of a machine: its steps in time order and
// how far it got.
type bootSession struct {
	Start, End time.Time
	Steps      []machineStep
	Choice     string
	Outcome    string // loader_failed, dhcp_only, loader, menu, files, stuck, winpe, winpe_left, copy, copy_broken, copied
	CopyRead   int64  // install image read by Setup
	Problem    string // with files_failed: "missing:boot.wim" or "cut:boot.wim"
	// Download is the boot file being sent now; only the latest boot has it.
	Download  *boot.Transfer
	bootWIMAt time.Time
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
	case "stuck":
		return "Застрял в WinPE: нет подключения к установщику"
	case "winpe":
		return "WinPE подключился к установщику"
	case "winpe_left":
		return "Установщик отключился до копирования"
	case "copy":
		return "Копирует Windows"
	case "copied":
		return "Файлы скопированы, перезагрузка"
	case "copy_broken":
		return "Связь прервалась во время копирования"
	case "files_failed":
		return "Файл установки не получен"
	}
	return "Получила файлы установки"
}

// ProblemText says how a boot file failed; ProblemFile names it.
func (b bootSession) ProblemText() string { return problemText(b.Problem) }

func (b bootSession) ProblemFile() string { return problemFile(b.Problem) }

// OK reports whether the attempt reached the installation files.
func (b bootSession) OK() bool {
	switch b.Outcome {
	case "files", "winpe", "copy", "copied":
		return true
	}
	return false
}

// Failed reports whether the attempt hit an error rather than just stopping.
func (b bootSession) Failed() bool {
	return b.Outcome == "loader_failed" || b.Outcome == "files_failed" || b.Outcome == "stuck" || b.Outcome == "winpe_left" || b.Outcome == "copy_broken"
}

// Installing reports whether Setup took over, after which the machine no
// longer reports itself.
func (b bootSession) Installing() bool {
	return b.Outcome == "winpe" || b.Outcome == "copy" || b.Outcome == "copied"
}

// machineSessions builds the boot attempts of one MAC from the HTTP and
// DHCP/TFTP journals, newest first. names maps menu keys to titles.
func machineSessions(mac string, httpEvents []boot.Event, dhcp []agent.DHCPEvent, names map[string]string) []bootSession {
	return machineSessionsWith(mac, httpEvents, dhcp, installLog{}, names, time.Now())
}

// machineSessionsWith also places the machine's install-share moments in the
// boot they followed.
func machineSessionsWith(mac string, httpEvents []boot.Event, dhcp []agent.DHCPEvent, install installLog, names map[string]string, now time.Time) []bootSession {
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
		problem := fileProblem(e)
		step := machineStep{At: e.At, Stage: "iPXE", Text: "Файл загрузки", Raw: e.Path, bootWIM: isBootWIM(e) && problem == "", Size: e.Size, Bytes: e.Bytes}
		switch problem {
		case "missing":
			step.Text, step.Failed, step.problem = "Файл не найден", true, "missing:"+fileName(e.Path)
		case "cut":
			step.Text, step.Failed, step.problem = "Передача прервалась", true, "cut:"+fileName(e.Path)
		}
		key, rank := choiceKey(e)
		switch {
		case e.Kind == "menu":
			step.Stage, step.Text = "Меню", "Показано меню"
		case rank > 0:
			title := names[key]
			if title == "" {
				title = key
			}
			step.Stage = "Выбор"
			if problem == "" {
				step.Text = title
			}
			choices = append(choices, choice{e.At, title, rank})
		}
		steps = append(steps, step)
	}
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].At.Before(steps[j].At) })
	var sessions []bootSession
	var progress bootProgress
	for _, step := range steps {
		stage := stepStage(step)
		if len(sessions) == 0 || step.At.Sub(sessions[len(sessions)-1].End) > bootSessionGap || progress.restartedBy(stage) {
			sessions = append(sessions, bootSession{Start: step.At})
			progress = bootProgress{}
		}
		progress.add(stage)
		current := &sessions[len(sessions)-1]
		current.Steps = append(current.Steps, step)
		current.End = step.At
		if step.bootWIM {
			current.bootWIMAt = step.At
		}
	}
	// Setup can run long after the last boot request, so share moments join
	// the latest boot that started before them instead of opening a new one.
	installState := make([]int, len(sessions))
	for _, e := range install.Events {
		if e.MAC != mac || e.Kind == "seen" {
			continue
		}
		// The boot the share session began in, not the one running when
		// Samba last listed it.
		for i := len(sessions) - 1; i >= 0; i-- {
			if !sessions[i].Start.After(e.Started) {
				sessions[i].Steps = append(sessions[i].Steps, installStep(e))
				installState[i] = max(installState[i], e.State)
				sessions[i].CopyRead = max(sessions[i].CopyRead, e.Read)
				break
			}
		}
	}
	for i := range sessions {
		sort.SliceStable(sessions[i].Steps, func(a, b int) bool { return sessions[i].Steps[a].At.Before(sessions[i].Steps[b].At) })
	}
	for i := range sessions {
		session := &sessions[i]
		var progress bootProgress
		problems := map[string]string{}
		for i := range session.Steps {
			step := &session.Steps[i]
			if step.Stage == "Выбор" || step.Stage == "iPXE" && step.Raw != "" {
				// The last request for a file decides: a retry that worked
				// clears the earlier failure.
				problems[fileName(step.Raw)] = step.problem
			}
			switch {
			case step.Stage == "Установка":
				// counted from the share sessions below
			case step.problem != "":
				progress.add("files")
			case step.Failed:
				progress.add("loader_failed")
			case step.Stage == "TFTP":
				progress.add("loader")
			case step.Stage == "DHCP" && progress.loaderSent:
				// iPXE asks for an address itself after the loader.
				step.Stage, step.Text = "iPXE", "iPXE запросил адрес"
				progress.add("pxe")
			case step.Stage == "Меню":
				progress.add("menu")
			case step.Stage == "Выбор":
				progress.add("files")
			}
		}
		// A failed transfer followed by a good one is the UEFI shim checking
		// the file size; it is not an error.
		if progress.loaderSent {
			for i := range session.Steps {
				if session.Steps[i].Failed && session.Steps[i].Stage == "TFTP" {
					session.Steps[i].Failed, session.Steps[i].Text = false, "Передача прервана клиентом и повторена"
				}
			}
		}
		var failed []string
		for _, problem := range problems {
			if problem != "" {
				failed = append(failed, problem)
			}
		}
		sort.Strings(failed)
		progress.addInstall(installState[i])
		session.Outcome = map[string]string{"files": "files", "menu": "menu", "ipxe": "loader", "loader": "loader", "loader_failed": "loader_failed", "pxe": "dhcp_only", "winpe": "winpe", "winpe_left": "winpe_left", "copy_broken": "copy_broken", "copy": "copy", "copied": "copied"}[progress.stage()]
		if session.Outcome == "files" && len(failed) > 0 {
			session.Outcome, session.Problem = "files_failed", failed[0]
		}
		// Only the latest boot can still be waiting for WinPE.
		if session.Outcome == "files" && install.Known && i == len(sessions)-1 && !session.bootWIMAt.IsZero() && session.bootWIMAt.After(install.Since) && now.Sub(session.bootWIMAt) > stuckAfter {
			session.Outcome = "stuck"
		}
		best := 0
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

// stepStage classifies a timeline step the way machineRows classifies log
// entries, to tell where a new boot begins.
func stepStage(step machineStep) string {
	switch {
	case step.Stage == "TFTP" && step.Failed:
		return "loader_failed"
	case step.Stage == "TFTP":
		return "loader"
	case step.Stage == "Меню":
		return "menu"
	case step.Stage == "Выбор":
		return "files"
	case step.Stage == "DHCP":
		return "pxe"
	}
	return ""
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
	} else if v, err := s.dhcpEvents(r.Context()); err != nil {
		data.DHCPError = "Журнал DHCP недоступен"
	} else {
		dhcp = v
	}
	names := s.menuNames(r.Context())
	now := time.Now()
	install := s.loadInstallLog(r.Context(), httpEvents)
	for _, row := range machineRowsWith(httpEvents, dhcp, install, names, now) {
		if row.MAC == mac {
			data.Machines = []machineRow{row}
		}
	}
	data.Sessions = machineSessionsWith(mac, httpEvents, dhcp, install, names, now)
	if len(data.Machines) == 1 && len(data.Sessions) > 0 {
		attachDownloads(data.Machines, s.bootTransfers(r.Context()), now)
		data.Sessions[0].Download = data.Machines[0].Download
	}
	if n, err := strconv.Atoi(r.URL.Query().Get("boot")); err == nil && n >= 0 && n < len(data.Sessions) {
		data.SessionIndex = n
	}
	render(w, "machine", data)
}
