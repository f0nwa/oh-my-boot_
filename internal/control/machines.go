package control

import (
	"context"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"pxe-platform/internal/agent"
	"pxe-platform/internal/boot"
)

// machineRow sums up what one PXE client (by MAC) did most recently.
// Stage is the furthest step of its latest boot: pxe, loader,
// loader_failed, ipxe, menu or files.
type machineRow struct {
	MAC, IP, Firmware, Stage, Choice string
	LastSeen                         time.Time
	Boots                            int
	CopyRead                         int64 // install image read by Setup in this boot
	// Problem is the boot file this boot failed to get, as "missing:boot.wim"
	// or "cut:boot.wim"; the stage is then files_failed.
	Problem string
	// Download is a boot file being sent to the machine right now.
	Download *boot.Transfer
}

// fileProblem tells how a boot file request ended badly: missing (an HTTP
// error) or cut (fewer bytes sent than the file holds). Events logged before
// transfers were recorded carry no status and count as fine.
func fileProblem(e boot.Event) string {
	switch {
	case e.Status >= 400:
		return "missing"
	case e.Status == 200 && e.Size > 0 && e.Bytes < e.Size:
		return "cut"
	}
	return ""
}

// fileName is the last part of a request path.
func fileName(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

// problemText says in words how a boot file failed; problemFile names it.
func problemText(problem string) string {
	if strings.HasPrefix(problem, "missing:") {
		return "файл не найден"
	}
	return "передача прервалась"
}

func problemFile(problem string) string {
	_, name, _ := strings.Cut(problem, ":")
	return name
}

const macMatchWindow = 30 * time.Minute

func eventIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

// resolveDHCPMACs fills the MAC of TFTP lines, which name only an IP, from
// the nearest HTTP request or DHCP answer with that IP within half an hour.
func resolveDHCPMACs(dhcp []agent.DHCPEvent, httpEvents []boot.Event) []agent.DHCPEvent {
	type seen struct {
		at  time.Time
		mac string
	}
	byIP := map[string][]seen{}
	for _, e := range httpEvents {
		if e.MAC != "" {
			ip := eventIP(e.RemoteAddr)
			byIP[ip] = append(byIP[ip], seen{e.At, e.MAC})
		}
	}
	for _, e := range dhcp {
		if e.MAC != "" && e.IP != "" {
			byIP[e.IP] = append(byIP[e.IP], seen{e.At, e.MAC})
		}
	}
	out := make([]agent.DHCPEvent, len(dhcp))
	for i, e := range dhcp {
		out[i] = e
		if e.MAC != "" || e.IP == "" {
			continue
		}
		best := macMatchWindow + 1
		for _, s := range byIP[e.IP] {
			d := s.at.Sub(e.At)
			if d < 0 {
				d = -d
			}
			if d <= macMatchWindow && d < best {
				best, out[i].MAC = d, s.mac
			}
		}
		if out[i].MAC == "" && strings.HasPrefix(e.Kind, "tftp-") {
			out[i].MAC = soleRecentPXEClient(dhcp, e.At)
		}
	}
	return out
}

// tftpAnswerWindow is how soon after a PXE answer a client asks for its
// loader over TFTP.
const tftpAnswerWindow = 15 * time.Second

// soleRecentPXEClient names the MAC that got a DHCP or PXE answer shortly
// before a TFTP transfer, when exactly one machine did. Proxy-DHCP answers
// carry no IP, so this is the only link between such a machine and the TFTP
// lines, which name only an IP; with two candidates it gives up.
func soleRecentPXEClient(dhcp []agent.DHCPEvent, at time.Time) string {
	mac := ""
	for _, e := range dhcp {
		if e.MAC == "" || strings.HasPrefix(e.Kind, "tftp-") || e.At.After(at) || at.Sub(e.At) > tftpAnswerWindow {
			continue
		}
		if mac != "" && mac != e.MAC {
			return ""
		}
		mac = e.MAC
	}
	return mac
}

var choicePath = regexp.MustCompile(`^/assets/(windows-driver-configs|windows-answers|windows-profiles)/([0-9]+)/`)

// choiceKey names the installation a boot file belongs to; more specific
// installations rank higher.
func choiceKey(e boot.Event) (string, int) {
	if m := choicePath.FindStringSubmatch(e.Path); m != nil {
		switch m[1] {
		case "windows-driver-configs":
			return "windriver_" + m[2], 3
		case "windows-answers":
			return "winanswer_" + m[2], 2
		default:
			return "winiso_" + m[2], 1
		}
	}
	if e.Kind == "iso" || strings.Contains(e.Path, "/ubuntu") {
		return "ubuntu", 1
	}
	return "", 0
}

func firmwareOf(file string) string {
	switch {
	case strings.HasSuffix(file, ".efi"):
		return "UEFI"
	case strings.HasSuffix(file, ".kpxe") || strings.HasSuffix(file, ".pxe"):
		return "BIOS"
	}
	return ""
}

// machineRows groups DHCP, TFTP and HTTP events by MAC. names maps menu keys
// (winiso_N, …, ubuntu) to the titles people know them by.
func machineRows(httpEvents []boot.Event, dhcp []agent.DHCPEvent, names map[string]string) []machineRow {
	return machineRowsWith(httpEvents, dhcp, installLog{}, names, time.Now())
}

// machineRowsWith also follows WinPE on the install share; now decides when
// a silent WinPE counts as stuck.
func machineRowsWith(httpEvents []boot.Event, dhcp []agent.DHCPEvent, install installLog, names map[string]string, now time.Time) []machineRow {
	type entry struct {
		at        time.Time
		stage, ip string
		firmware  string
		key       string
		rank      int
		install   int // share session state; install moments never start a new boot
		read      int64
		started   time.Time // share session start of an install moment
		file      string    // boot file name of an HTTP request
		problem   string    // missing or cut, from fileProblem
		bootWIM   bool      // WinPE was fetched
	}
	byMAC := map[string][]entry{}
	for _, e := range resolveDHCPMACs(dhcp, httpEvents) {
		if e.MAC == "" {
			continue
		}
		en := entry{at: e.At, ip: e.IP, stage: "pxe"}
		switch e.Kind {
		case "tftp-sent":
			en.stage, en.firmware = "loader", firmwareOf(e.Detail)
		case "tftp-failed":
			en.stage, en.firmware = "loader_failed", firmwareOf(e.Detail)
		}
		byMAC[e.MAC] = append(byMAC[e.MAC], en)
	}
	for _, e := range httpEvents {
		if e.MAC == "" {
			continue
		}
		en := entry{at: e.At, ip: eventIP(e.RemoteAddr), stage: "files"}
		if e.Kind == "menu" {
			en.stage = "menu"
		}
		en.key, en.rank = choiceKey(e)
		en.problem = fileProblem(e)
		en.bootWIM = isBootWIM(e) && en.problem == ""
		if e.Kind != "menu" {
			en.file = fileName(e.Path)
		}
		byMAC[e.MAC] = append(byMAC[e.MAC], en)
	}
	for _, e := range install.Events {
		byMAC[e.MAC] = append(byMAC[e.MAC], entry{at: e.At, ip: e.IP, stage: "install", install: e.State, read: e.Read, started: e.Started})
	}
	var rows []machineRow
	for mac, entries := range byMAC {
		sort.Slice(entries, func(i, j int) bool { return entries[i].at.Before(entries[j].at) })
		row := machineRow{MAC: mac}
		var boot bootProgress
		var last, bootWIMAt, bootStart time.Time
		bestRank := 0
		problems := map[string]string{} // boot file -> its last problem in this boot
		for _, en := range entries {
			if en.install > 0 {
				// Setup may run for an hour after the last boot request; it
				// belongs to that boot. A share session that began before
				// this boot belongs to an earlier one, even if Samba still
				// lists it after the machine rebooted.
				if en.started.Before(bootStart) {
					continue
				}
				if en.install >= boot.install {
					row.CopyRead = en.read
				}
				boot.addInstall(en.install)
				if en.at.After(row.LastSeen) {
					row.LastSeen = en.at
				}
				continue
			}
			if last.IsZero() || en.at.Sub(last) > bootSessionGap || boot.restartedBy(en.stage) {
				// A new boot attempt: progress and choice start over.
				boot, row.Choice, bestRank, bootWIMAt, row.CopyRead = bootProgress{}, "", 0, time.Time{}, 0
				problems = map[string]string{}
				bootStart = en.at
			}
			last = en.at
			if en.at.After(row.LastSeen) {
				row.LastSeen = en.at
			}
			if en.bootWIM {
				bootWIMAt = en.at
			}
			if en.file != "" {
				// A file fetched again after a failure is fine again.
				problems[en.file] = en.problem
			}
			if en.ip != "" {
				row.IP = en.ip
			}
			if en.firmware != "" {
				row.Firmware = en.firmware
			}
			boot.add(en.stage)
			if en.stage == "menu" {
				row.Boots++
				row.Choice, bestRank = "", 0
			}
			if en.rank > bestRank {
				row.Choice, bestRank = en.key, en.rank
			}
		}
		row.Stage = boot.stage()
		if row.Stage == "files" {
			var failed []string
			for file, problem := range problems {
				if problem != "" {
					failed = append(failed, file)
				}
			}
			sort.Strings(failed)
			if len(failed) > 0 {
				row.Stage, row.Problem = "files_failed", problems[failed[0]]+":"+failed[0]
			}
		}
		// WinPE that never reached the share while the Agent was watching.
		if row.Stage == "files" && install.Known && !bootWIMAt.IsZero() && bootWIMAt.After(install.Since) && now.Sub(bootWIMAt) > stuckAfter {
			row.Stage = "stuck"
		}
		if title, ok := names[row.Choice]; ok {
			row.Choice = title
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].LastSeen.After(rows[j].LastSeen) })
	return rows
}

// attachDownloads gives each machine the boot file being sent to its IP right
// now, if any.
func attachDownloads(rows []machineRow, transfers []boot.Transfer, now time.Time) {
	for i := range rows {
		for j := range transfers {
			if rows[i].IP != "" && eventIP(transfers[j].RemoteAddr) == rows[i].IP {
				rows[i].Download = &transfers[j]
				// A download in progress means the machine is active now.
				rows[i].LastSeen = now
			}
		}
	}
}

// menuNames maps menu keys to object titles for the machine summary.
func (s *Server) menuNames(ctx context.Context) map[string]string {
	names := map[string]string{"ubuntu": "Ubuntu Desktop"}
	profiles, answers, configs, err := s.menuSources(ctx)
	if err != nil {
		return names
	}
	for _, p := range profiles {
		names[keyFor("winiso", p.ID)] = p.Title
	}
	for _, a := range answers {
		names[keyFor("winanswer", a.ID)] = a.Title
	}
	for _, c := range configs {
		names[keyFor("windriver", c.ID)] = c.Title
	}
	return names
}

// bootProgress is how far one boot attempt got. dnsmasq logs a second
// DHCP/PXE answer when iPXE itself asks for an address after the loader, and
// a UEFI shim aborts its first transfer on purpose before fetching the file
// again, so the furthest step counts, not the last line.
type bootProgress struct {
	loaderSent, loaderFailed, ipxe, menu, files bool
	install                                     int // furthest share session state
}

// restartedBy tells whether a step can only belong to a new boot of the
// machine: the firmware fetching a loader again after iPXE already ran, or
// iPXE fetching the menu again after the boot files. A quick reboot then
// starts a new row state instead of piling onto the last one.
func (b bootProgress) restartedBy(stage string) bool {
	switch stage {
	case "loader", "loader_failed":
		return b.ipxe || b.menu || b.files || b.install > 0
	case "menu":
		return b.files || b.install > 0
	}
	return false
}

func (b *bootProgress) addInstall(state int) {
	b.install = max(b.install, state)
}

func (b *bootProgress) add(stage string) {
	switch stage {
	case "pxe":
		if b.loaderSent {
			b.ipxe = true
		}
	case "loader":
		b.loaderSent = true
	case "loader_failed":
		b.loaderFailed = true
	case "menu":
		b.menu = true
	case "files":
		b.files = true
	}
}

// stage names the furthest step: copied, copy, winpe, winpe_left, files,
// menu, ipxe, loader, loader_failed or pxe.
func (b bootProgress) stage() string {
	if name, ok := installStageNames[b.install]; ok {
		return name
	}
	switch {
	case b.files:
		return "files"
	case b.menu:
		return "menu"
	case b.ipxe:
		return "ipxe"
	case b.loaderSent:
		return "loader"
	case b.loaderFailed:
		return "loader_failed"
	}
	return "pxe"
}

func keyFor(prefix string, id int64) string {
	return prefix + "_" + strconv.FormatInt(id, 10)
}

func dhcpKindLabel(kind string) string {
	switch kind {
	case "PXE":
		return "Ответ PXE (proxy-DHCP)"
	case "DHCPDISCOVER":
		return "DHCP: запрос адреса"
	case "DHCPOFFER":
		return "DHCP: предложен адрес"
	case "DHCPREQUEST":
		return "DHCP: подтверждение адреса"
	case "DHCPACK":
		return "DHCP: адрес выдан"
	case "DHCPNAK":
		return "DHCP: отказ"
	case "tftp-sent":
		return "Отправлен загрузчик по TFTP"
	case "tftp-failed":
		return "Ошибка отправки по TFTP"
	}
	return kind
}

func machineStageLabel(stage string) string {
	switch stage {
	case "pxe":
		return "DHCP / PXE"
	case "loader":
		return "Загрузчик получен"
	case "loader_failed":
		return "Ошибка загрузчика"
	case "ipxe":
		return "iPXE запущен"
	case "menu":
		return "Меню"
	case "files":
		return "Загрузка файлов"
	case "files_failed":
		return "Файл установки не получен"
	case "stuck":
		return "Застрял в WinPE"
	case "winpe":
		return "WinPE подключился к установщику"
	case "winpe_left":
		return "Установщик отключился"
	case "copy":
		return "Копирует Windows"
	case "copied":
		return "Файлы скопированы, перезагрузка"
	case "copy_broken":
		return "Связь прервалась во время копирования"
	}
	return stage
}
