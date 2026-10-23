package control

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"pxe-platform/internal/agent"
	"pxe-platform/internal/boot"
)

// machineList is the «Система → Машины» page: every machine in the book,
// including those whose requests have long left the logs.
type machineList struct {
	Available bool
	Rows      []machineListRow
	Query     string
	Sort      string
	Total     int // machines in the book
	Named     int // with a name given by a person
	NoModel   int // never reported a model and none was written in
}

type machineListRow struct {
	MachineInfo
	Label    string // maker and model, else the network card's maker
	HasModel bool   // Label is a model rather than the network card's maker
}

// NoteShort is the note cut to fit a table cell.
func (r machineListRow) NoteShort() string {
	const max = 40
	runes := []rune(strings.Join(strings.Fields(r.Note), " "))
	if len(runes) <= max {
		return string(runes)
	}
	return strings.TrimSpace(string(runes[:max])) + "…"
}

// machineSorts are the columns the table can be sorted by. Dates go newest
// first, text and addresses from the start of the alphabet.
var machineSorts = map[string]func(a, b machineListRow) int{
	"last":  func(a, b machineListRow) int { return b.LastSeen.Compare(a.LastSeen) },
	"first": func(a, b machineListRow) int { return b.FirstSeen.Compare(a.FirstSeen) },
	"mac":   func(a, b machineListRow) int { return strings.Compare(a.MAC, b.MAC) },
	"name":  func(a, b machineListRow) int { return compareFilled(a.Name, b.Name) },
	"model": func(a, b machineListRow) int {
		if a.HasModel != b.HasModel {
			if a.HasModel {
				return -1
			}
			return 1
		}
		return compareFilled(a.Label, b.Label)
	},
	"serial":   func(a, b machineListRow) int { return compareFilled(a.Serial, b.Serial) },
	"ip":       func(a, b machineListRow) int { return compareIP(a.LastIP, b.LastIP) },
	"firmware": func(a, b machineListRow) int { return compareFilled(a.Firmware, b.Firmware) },
}

// compareFilled orders text case-insensitively, empty values last.
func compareFilled(a, b string) int {
	switch {
	case a == "" && b == "":
		return 0
	case a == "":
		return 1
	case b == "":
		return -1
	}
	return strings.Compare(strings.ToLower(a), strings.ToLower(b))
}

// compareIP orders IPv4 addresses by number, empty ones last.
func compareIP(a, b string) int {
	ia, ib := net.ParseIP(a).To4(), net.ParseIP(b).To4()
	switch {
	case ia == nil && ib == nil:
		return compareFilled(a, b)
	case ia == nil:
		return 1
	case ib == nil:
		return -1
	}
	return strings.Compare(string(ia), string(ib))
}

// macDigits keeps the hex digits of a MAC typed in any form.
func macDigits(v string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(v) {
		if r >= '0' && r <= '9' || r >= 'a' && r <= 'f' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// matches reports whether a machine fits a search: any part of its name,
// MAC (with or without separators), maker, model, network card maker,
// serial number, UUID, address or note.
func (r machineListRow) matches(query string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return true
	}
	for _, field := range []string{r.Name, r.MAC, r.Label, r.Maker(), r.Model(), r.NICVendor(), r.Serial, r.UUID, r.LastIP, r.Note} {
		if strings.Contains(strings.ToLower(field), q) {
			return true
		}
	}
	// "001b21" or "00-1B-21": compare hex digits only, when the query looks
	// like a piece of a MAC.
	if digits := macDigits(q); len(digits) >= 4 && len(digits)*3 >= len(q)*2 {
		return strings.Contains(macDigits(r.MAC), digits)
	}
	return false
}

// buildMachineList sorts and filters the book for the page.
func buildMachineList(book []MachineInfo, query, sortBy string) machineList {
	list := machineList{Available: true, Query: strings.TrimSpace(query), Sort: sortBy, Total: len(book)}
	if _, ok := machineSorts[list.Sort]; !ok {
		list.Sort = "last"
	}
	for _, info := range book {
		row := machineListRow{MachineInfo: info, Label: machineLabel(&info, info.MAC), HasModel: info.Model() != ""}
		if info.Name != "" {
			list.Named++
		}
		if !row.HasModel {
			list.NoModel++
		}
		if row.matches(list.Query) {
			list.Rows = append(list.Rows, row)
		}
	}
	by := machineSorts[list.Sort]
	sort.SliceStable(list.Rows, func(i, j int) bool {
		if c := by(list.Rows[i], list.Rows[j]); c != 0 {
			return c < 0
		}
		return list.Rows[i].MAC < list.Rows[j].MAC
	})
	return list
}

// SortLink is the address of the page sorted by a column, keeping the search.
func (l machineList) SortLink(column string) string {
	v := url.Values{}
	if l.Query != "" {
		v.Set("q", l.Query)
	}
	if column != "last" {
		v.Set("sort", column)
	}
	if len(v) == 0 {
		return "/machines"
	}
	return "/machines?" + v.Encode()
}

// SortLinkAll is the page without the search, keeping the order.
func (l machineList) SortLinkAll() string {
	return machineList{Sort: l.Sort}.SortLink(l.Sort)
}

// machineColumn is one sortable header of the table.
type machineColumn struct {
	Label, Link  string
	Active, Desc bool
}

func machineCol(l machineList, column, label string) machineColumn {
	return machineColumn{Label: label, Link: l.SortLink(column), Active: l.Sort == column, Desc: column == "last" || column == "first"}
}

// refreshedBook writes what the logs show into the book and reads it back,
// so a machine that booted a minute ago is already in the list.
func (s *Server) refreshedBook(ctx context.Context) ([]MachineInfo, bool) {
	book, ok := s.Store.(MachineBook)
	if !ok {
		return nil, false
	}
	var httpEvents []boot.Event
	if s.Boot != nil {
		if v, err := s.Boot.Events(ctx, 1000); err == nil {
			httpEvents = v
		}
	}
	var dhcp []agent.DHCPEvent
	if s.Agent != nil {
		if v, err := s.dhcpEvents(ctx); err == nil {
			dhcp = v
		}
	}
	if len(httpEvents) > 0 || len(dhcp) > 0 {
		rows := machineRowsWith(httpEvents, dhcp, installLog{}, nil, time.Now())
		s.machineBook(ctx, rows, httpEvents)
	}
	machines, err := book.Machines(ctx)
	if err != nil {
		return nil, false
	}
	return machines, true
}

// machinesPage lists every machine in the book with search and sorting.
func (s *Server) machinesPage(w http.ResponseWriter, r *http.Request) {
	data := pageData{Info: requestInfo(r)}
	q := r.URL.Query()
	if machines, ok := s.refreshedBook(r.Context()); ok {
		list := buildMachineList(machines, q.Get("q"), q.Get("sort"))
		data.MachineList = &list
	} else {
		data.MachineList = &machineList{Query: q.Get("q")}
	}
	render(w, "machines", data)
}
