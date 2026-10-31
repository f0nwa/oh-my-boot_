package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DHCPEvent is one line of the PXE dnsmasq log: a DHCP or proxy-DHCP answer,
// or a TFTP transfer of a boot loader.
type DHCPEvent struct {
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"`
	MAC    string    `json:"mac,omitempty"`
	IP     string    `json:"ip,omitempty"`
	Detail string    `json:"detail,omitempty"`
}

var (
	dhcpLine   = regexp.MustCompile(`^(DHCP[A-Z]+|PXE)\([^)]*\) (?:([0-9.]+) )?([0-9a-fA-F]{2}(?::[0-9a-fA-F]{2}){5})(?: (.*))?$`)
	tftpLine   = regexp.MustCompile(`^(sent|failed sending) (\S+) to ([0-9.]+)$`)
	tftpRoot   = "/srv/omb/tftp/"
	maxDHCPLog = 1000
)

// ParseDnsmasqJournal reads `journalctl -o json` output of the dnsmasq unit
// and returns its DHCP and TFTP events, newest first. dnsmasq logs every
// line twice (to stderr and syslog); only the dnsmasq-dhcp and dnsmasq-tftp
// copies are used.
func ParseDnsmasqJournal(r io.Reader) []DHCPEvent {
	var events []DHCPEvent
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		var entry struct {
			Time       string `json:"__REALTIME_TIMESTAMP"`
			Identifier string `json:"SYSLOG_IDENTIFIER"`
			Message    string `json:"MESSAGE"`
		}
		if json.Unmarshal(scanner.Bytes(), &entry) != nil {
			continue
		}
		micros, err := strconv.ParseInt(entry.Time, 10, 64)
		if err != nil {
			continue
		}
		at := time.UnixMicro(micros).UTC()
		switch entry.Identifier {
		case "dnsmasq-dhcp":
			m := dhcpLine.FindStringSubmatch(entry.Message)
			if m == nil {
				continue
			}
			events = append(events, DHCPEvent{At: at, Kind: m[1], IP: m[2], MAC: strings.ToLower(m[3]), Detail: m[4]})
		case "dnsmasq-tftp":
			m := tftpLine.FindStringSubmatch(entry.Message)
			if m == nil {
				continue
			}
			kind := "tftp-sent"
			if m[1] != "sent" {
				kind = "tftp-failed"
			}
			events = append(events, DHCPEvent{At: at, Kind: kind, IP: m[3], Detail: strings.TrimPrefix(m[2], tftpRoot)})
		}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].At.After(events[j].At) })
	if len(events) > maxDHCPLog {
		events = events[:maxDHCPLog]
	}
	return events
}

// DHCPLog reads the dnsmasq journal on request.
type DHCPLog struct {
	Read func(context.Context) ([]byte, error)
}

// NewDHCPLog reads the last seven days of the omb-dnsmasq unit from journald.
func NewDHCPLog() *DHCPLog {
	return &DHCPLog{Read: func(ctx context.Context) ([]byte, error) {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, "journalctl", "-u", "omb-dnsmasq", "--since", "-7d", "-o", "json", "--no-pager", "-q").Output()
	}}
}

// HandlerWithDHCPLog adds GET /dhcp-events to the Agent API.
func HandlerWithDHCPLog(inner http.Handler, log *DHCPLog) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", inner)
	mux.HandleFunc("GET /dhcp-events", func(w http.ResponseWriter, r *http.Request) {
		if log == nil || log.Read == nil {
			http.Error(w, "dhcp log unavailable", http.StatusServiceUnavailable)
			return
		}
		data, err := log.Read(r.Context())
		if err != nil {
			http.Error(w, "dhcp log unavailable", http.StatusServiceUnavailable)
			return
		}
		events := ParseDnsmasqJournal(strings.NewReader(string(data)))
		if events == nil {
			events = []DHCPEvent{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(events)
	})
	return mux
}
