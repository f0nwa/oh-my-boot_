package control

import (
	"context"
	"net"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"omb/internal/agent"
	"omb/internal/boot"
)

// machineRow sums up what one PXE client (by MAC) did most recently.
// Stage is the furthest step of its latest boot: pxe, loader,
// loader_failed, loader_rejected, ipxe, menu, choice or files.
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
	// Name and Label come from the book of machines: a person's name for
	// the machine, and its maker and model (or network card maker).
	Name, Label string
}

// describeMachines puts names and labels from the book on the rows; a
// machine the book does not know is still labelled by its prefix rule.
func describeMachines(rows []machineRow, book map[string]MachineInfo, rules macRules) {
	for i := range rows {
		info, ok := book[rows[i].MAC]
		if !ok {
			info = MachineInfo{MAC: rows[i].MAC}
		}
		if info.Rule == nil {
			info.Rule = rules.match(info.MAC)
		}
		rows[i].Name = info.Name
		rows[i].Label = machineLabel(&info, rows[i].MAC)
	}
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

// choiceKey reads only the canonical choice events recorded by Boot. Shared
// profile files cannot identify an installation, even for older events.
func choiceKey(e boot.Event) string {
	if e.Kind != "choice" {
		return ""
	}
	raw, ok := strings.CutPrefix(e.Path, "/boot/choice?id=")
	if !ok {
		return ""
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != raw {
		return ""
	}
	return "install_" + raw
}

// shimRejectAfter is how long after the firmware got a UEFI shim the shim
// may take to ask for iPXE. A shim that runs fetches ipxe.efi within a
// second; when nothing follows, the firmware refused to start it, almost
// always Secure Boot without the Microsoft UEFI CA in its db.
const shimRejectAfter = 30 * time.Second

// loaderStage tells a UEFI shim from the loader it starts: a shim first, the
// iPXE it fetches over TFTP second.
func loaderStage(file string) string {
	if strings.Contains(strings.ToLower(path.Base(file)), "shim") {
		return "loader_shim"
	}
	return "loader"
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
// (install_N) to the titles people know them by.
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
			en.stage, en.firmware = loaderStage(e.Detail), firmwareOf(e.Detail)
		case "tftp-failed":
			en.stage, en.firmware = "loader_failed", firmwareOf(e.Detail)
		}
		byMAC[e.MAC] = append(byMAC[e.MAC], en)
	}
	for _, e := range httpEvents {
		// A hello report is not a boot step; the book of machines reads it.
		if e.MAC == "" || e.Kind == "hello" {
			continue
		}
		en := entry{at: e.At, ip: eventIP(e.RemoteAddr), stage: "files"}
		if e.Kind == "menu" {
			en.stage = "menu"
		} else if e.Kind == "choice" {
			en.key = choiceKey(e)
			if en.key == "" {
				continue
			}
			en.stage = "choice"
		}
		en.problem = fileProblem(e)
		en.bootWIM = isBootWIM(e) && en.problem == ""
		if e.Kind == "asset" || e.Kind == "iso" {
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
		var last, bootWIMAt, bootStart, shimAt time.Time
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
				boot, row.Choice, bootWIMAt, row.CopyRead, shimAt = bootProgress{}, "", time.Time{}, 0, time.Time{}
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
			if en.stage == "loader_shim" {
				shimAt = en.at
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
				row.Choice = ""
			}
			if en.key != "" {
				row.Choice = en.key
			}
		}
		row.Stage = boot.stage()
		if boot.shimOnly() && now.Sub(shimAt) > shimRejectAfter {
			row.Stage = "loader_rejected"
		}
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
	names := map[string]string{}
	sources, err := s.installationMenuSources(ctx)
	if err != nil {
		return names
	}
	for _, item := range sources.Installations {
		names[keyFor("install", item.ID)] = item.Title
	}
	// The published title is the name clients actually saw when choosing.
	versions, err := s.Store.MenuVersions(ctx)
	if err == nil {
		for _, version := range versions {
			if !version.Active {
				continue
			}
			for _, item := range version.Items {
				if item.InstallationID > 0 && item.Title != "" {
					names[keyFor("install", item.InstallationID)] = item.Title
				}
			}
			break
		}
	}
	return names
}

// bootProgress is how far one boot attempt got. dnsmasq logs a second
// DHCP/PXE answer when iPXE itself asks for an address after the loader, and
// a UEFI shim aborts its first transfer on purpose before fetching the file
// again, so the furthest step counts, not the last line.
type bootProgress struct {
	loaderSent, loaderFailed, ipxe, menu, choice, files bool
	// shim is a UEFI shim sent; chain is the loader it fetched after it.
	// Without chain the shim never ran, and a DHCP answer after it is the
	// firmware trying again, not iPXE.
	shim, chain bool
	install     int // furthest share session state
}

// restartedBy tells whether a step can only belong to a new boot of the
// machine: the firmware fetching a loader again after iPXE already ran, or
// iPXE fetching the menu again after the boot files. A quick reboot then
// starts a new row state instead of piling onto the last one.
func (b bootProgress) restartedBy(stage string) bool {
	switch stage {
	case "loader", "loader_failed":
		return b.ipxe || b.menu || b.choice || b.files || b.install > 0
	case "loader_shim":
		return b.chain || b.ipxe || b.menu || b.choice || b.files || b.install > 0
	case "menu":
		return b.choice || b.files || b.install > 0
	}
	return false
}

func (b *bootProgress) addInstall(state int) {
	b.install = max(b.install, state)
}

func (b *bootProgress) add(stage string) {
	switch stage {
	case "pxe":
		if b.loaderSent && (!b.shim || b.chain) {
			b.ipxe = true
		}
	case "loader":
		b.loaderSent, b.chain = true, true
	case "loader_shim":
		b.loaderSent, b.shim = true, true
	case "loader_failed":
		b.loaderFailed = true
	case "menu":
		b.menu = true
	case "choice":
		b.choice = true
	case "files":
		b.files = true
	}
}

// shimOnly reports a boot that got a UEFI shim and nothing after it.
func (b bootProgress) shimOnly() bool {
	return b.shim && !b.chain && !b.ipxe && !b.menu && !b.choice && !b.files && b.install == 0
}

// stage names the furthest step: copied, copy, winpe, winpe_left, files,
// choice, menu, ipxe, loader, loader_failed or pxe.
func (b bootProgress) stage() string {
	if name, ok := installStageNames[b.install]; ok {
		return name
	}
	switch {
	case b.files:
		return "files"
	case b.choice:
		return "choice"
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
	case "loader_rejected":
		return "Прошивка отклонила загрузчик"
	case "ipxe":
		return "iPXE запущен"
	case "menu":
		return "Меню"
	case "choice":
		return "Установка выбрана"
	case "files":
		return "Загрузка файлов"
	case "files_failed":
		return "Файл установки не получен"
	case "unknown":
		return "Нет в журнале"
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
