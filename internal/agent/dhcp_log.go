package agent

import (
	"bufio"
	"container/heap"
	"encoding/json"
	"io"
	"net/http"
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
	dhcpLine = regexp.MustCompile(`^(DHCP[A-Z]+|PXE)\([^)]*\) (?:([0-9.]+) )?([0-9a-fA-F]{2}(?::[0-9a-fA-F]{2}){5})(?: (.*))?$`)
	tftpLine = regexp.MustCompile(`^(sent|failed sending) (\S+) to ([0-9.]+)$`)
	tftpRoot = "/srv/omb/tftp/"
)

const maxDHCPLog = 1000

type dnsmasqEvent struct {
	DHCPEvent
	sequence uint64
}

// The root is the least useful event: oldest timestamp, then last input order.
type dnsmasqHeap []dnsmasqEvent

func (h dnsmasqHeap) Len() int { return len(h) }
func (h dnsmasqHeap) Less(i, j int) bool {
	if h[i].At.Equal(h[j].At) {
		return h[i].sequence > h[j].sequence
	}
	return h[i].At.Before(h[j].At)
}
func (h dnsmasqHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *dnsmasqHeap) Push(v any)   { *h = append(*h, v.(dnsmasqEvent)) }
func (h *dnsmasqHeap) Pop() any {
	old := *h
	n := len(old) - 1
	v := old[n]
	old[n] = dnsmasqEvent{}
	*h = old[:n]
	return v
}

type dnsmasqBuffer struct {
	events dnsmasqHeap
	cursor string
	next   uint64
}

func (b *dnsmasqBuffer) read(r io.Reader) error {
	if b.events == nil {
		b.events = make(dnsmasqHeap, 0, maxDHCPLog)
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if strings.HasPrefix(string(line), "-- cursor: ") {
			b.cursor = strings.TrimPrefix(string(line), "-- cursor: ")
			continue
		}
		var entry struct {
			Cursor     string `json:"__CURSOR"`
			Time       string `json:"__REALTIME_TIMESTAMP"`
			Identifier string `json:"SYSLOG_IDENTIFIER"`
			Message    string `json:"MESSAGE"`
		}
		if json.Unmarshal(line, &entry) != nil {
			continue
		}
		// Advance past unrecognised messages too, so they do not trigger rescans.
		if entry.Cursor != "" {
			b.cursor = entry.Cursor
		}
		micros, err := strconv.ParseInt(entry.Time, 10, 64)
		if err != nil {
			continue
		}
		event := DHCPEvent{At: time.UnixMicro(micros).UTC()}
		switch entry.Identifier {
		case "dnsmasq-dhcp":
			m := dhcpLine.FindStringSubmatch(entry.Message)
			if m == nil {
				continue
			}
			event.Kind, event.IP, event.MAC, event.Detail = m[1], m[2], strings.ToLower(m[3]), m[4]
		case "dnsmasq-tftp":
			m := tftpLine.FindStringSubmatch(entry.Message)
			if m == nil {
				continue
			}
			event.Kind = "tftp-sent"
			if m[1] != "sent" {
				event.Kind = "tftp-failed"
			}
			event.IP, event.Detail = m[3], strings.TrimPrefix(m[2], tftpRoot)
		default:
			continue
		}
		candidate := dnsmasqEvent{DHCPEvent: event, sequence: b.next}
		b.next++
		if len(b.events) < maxDHCPLog {
			heap.Push(&b.events, candidate)
		} else if candidate.At.After(b.events[0].At) {
			b.events[0] = candidate
			heap.Fix(&b.events, 0)
		}
	}
	return scanner.Err()
}
func (b *dnsmasqBuffer) ordered() []DHCPEvent {
	ordered := append(dnsmasqHeap(nil), b.events...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].At.Equal(ordered[j].At) {
			return ordered[i].sequence < ordered[j].sequence
		}
		return ordered[i].At.After(ordered[j].At)
	})
	events := make([]DHCPEvent, len(ordered))
	for i := range ordered {
		events[i] = ordered[i].DHCPEvent
	}
	return events
}

// ParseDnsmasqJournal parses journalctl JSON with bounded working memory and
// returns the newest events, preserving input order for equal timestamps.
// Only dedicated DHCP/TFTP identifiers are used to avoid syslog duplicates.
func ParseDnsmasqJournal(r io.Reader) []DHCPEvent {
	var b dnsmasqBuffer
	_ = b.read(r)
	return b.ordered()
}

// HandlerWithDHCPLog adds GET /dhcp-events to the Agent API.
func HandlerWithDHCPLog(inner http.Handler, log *DHCPLog) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", inner)
	mux.HandleFunc("GET /dhcp-events", func(w http.ResponseWriter, r *http.Request) {
		if log == nil {
			http.Error(w, "dhcp log unavailable", http.StatusServiceUnavailable)
			return
		}
		events, err := log.snapshot(r.Context())
		if err != nil {
			http.Error(w, "dhcp log unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(events)
	})
	return mux
}
