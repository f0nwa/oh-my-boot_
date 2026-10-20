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
// Stage is pxe, loader, loader_failed, menu or files.
type machineRow struct {
	MAC, IP, Firmware, Stage, Choice string
	LastSeen                         time.Time
	Boots                            int
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
	type entry struct {
		at        time.Time
		stage, ip string
		firmware  string
		key       string
		rank      int
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
		byMAC[e.MAC] = append(byMAC[e.MAC], en)
	}
	var rows []machineRow
	for mac, entries := range byMAC {
		sort.Slice(entries, func(i, j int) bool { return entries[i].at.Before(entries[j].at) })
		row := machineRow{MAC: mac}
		bestRank := 0
		for _, en := range entries {
			row.LastSeen, row.Stage = en.at, en.stage
			if en.ip != "" {
				row.IP = en.ip
			}
			if en.firmware != "" {
				row.Firmware = en.firmware
			}
			if en.stage == "menu" {
				row.Boots++
				row.Choice, bestRank = "", 0
			}
			if en.rank > bestRank {
				row.Choice, bestRank = en.key, en.rank
			}
		}
		if title, ok := names[row.Choice]; ok {
			row.Choice = title
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].LastSeen.After(rows[j].LastSeen) })
	return rows
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
	case "menu":
		return "Меню"
	case "files":
		return "Загрузка файлов"
	}
	return stage
}
