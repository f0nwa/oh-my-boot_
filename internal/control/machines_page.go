package control

import (
	"context"
	_ "embed"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"omb/internal/agent"
	"omb/internal/boot"
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
	// CanEdit and CanDelete follow the viewer's role.
	CanEdit, CanDelete bool
	// Duplicate is a MAC that «Добавить MAC» found already in the list.
	Duplicate string
	// Rules are the MAC prefix rules with how many machines fit each;
	// RuleFilter is the rule the table is narrowed to.
	Rules      []MacRule
	RuleFilter *MacRule
	HasRules   bool // the store keeps rules
}

// Filtered reports whether the table shows only part of the list.
func (l machineList) Filtered() bool { return l.Query != "" || l.RuleFilter != nil }

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
	for _, field := range []string{r.Name, r.MAC, r.Label, r.Maker(), r.Model(), r.NICVendor(), r.RuleTitle(), r.Serial, r.UUID, r.LastIP, r.Note} {
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

// buildMachineList sorts and filters the book for the page. ruleFilter
// narrows it to machines whose MAC starts with that rule's prefix.
func buildMachineList(book []MachineInfo, query, sortBy string, rules macRules, ruleFilter string) machineList {
	list := machineList{Available: true, Query: strings.TrimSpace(query), Sort: sortBy, Total: len(book)}
	if _, ok := machineSorts[list.Sort]; !ok {
		list.Sort = "last"
	}
	attachRules(book, rules)
	list.Rules = append([]MacRule(nil), rules...)
	for i := range list.Rules {
		for _, info := range book {
			if strings.HasPrefix(info.MAC, list.Rules[i].Prefix+":") {
				list.Rules[i].Machines++
			}
		}
	}
	if prefix, ok := parseMACPrefix(ruleFilter); ok {
		list.RuleFilter = macRules(list.Rules).find(prefix)
	}
	for _, info := range book {
		row := machineListRow{MachineInfo: info, Label: machineLabel(&info, info.MAC), HasModel: info.Model() != ""}
		if info.Name != "" {
			list.Named++
		}
		if !row.HasModel {
			list.NoModel++
		}
		inRule := list.RuleFilter == nil || strings.HasPrefix(info.MAC, list.RuleFilter.Prefix+":")
		if inRule && row.matches(list.Query) {
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
	if l.RuleFilter != nil {
		v.Set("rule", l.RuleFilter.Prefix)
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
	render(w, "machines", s.machinesData(r))
}

// machinesData collects the table for the page and the import preview.
func (s *Server) machinesData(r *http.Request) pageData {
	data := pageData{Info: requestInfo(r), Message: r.URL.Query().Get("message"), Error: pageError(r), Form: pageForm(r)}
	q := r.URL.Query()
	machines, ok := s.refreshedBook(r.Context())
	if ok {
		list := buildMachineList(machines, q.Get("q"), q.Get("sort"), s.macRules(r.Context()), q.Get("rule"))
		_, list.HasRules = s.Store.(MacRuleBook)
		data.MachineList = &list
	} else {
		data.MachineList = &machineList{Query: q.Get("q")}
	}
	role := data.Info.User.Role
	data.MachineList.CanEdit = ok && Allowed(role, "images")
	data.MachineList.CanDelete = ok && Allowed(role, "delete")
	// A failed «Добавить MAC» for a MAC already in the list links to it.
	if data.Error != "" && data.Form != nil {
		if mac, valid := parseMAC(data.Form.Get("mac")); valid {
			for _, m := range machines {
				if m.MAC == mac {
					data.MachineList.Duplicate = mac
				}
			}
		}
	}
	return data
}

// parseMAC reads a MAC typed in any common form — bc:24:11:aa:bb:01,
// BC-24-11-AA-BB-01, bc2411aabb01, bc24.11aa.bb01 — as aa:bb:cc:dd:ee:ff.
// All-zero, broadcast and multicast addresses are not network cards.
func parseMAC(v string) (string, bool) {
	var digits strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(v)) {
		switch {
		case r >= '0' && r <= '9' || r >= 'a' && r <= 'f':
			digits.WriteRune(r)
		case r == ':' || r == '-' || r == '.' || r == ' ':
		default:
			return "", false
		}
	}
	d := digits.String()
	if len(d) != 12 || d == "000000000000" {
		return "", false
	}
	if first := strings.IndexByte("0123456789abcdef", d[1]); first&1 == 1 {
		return "", false // multicast and broadcast
	}
	parts := make([]string, 6)
	for i := range parts {
		parts[i] = d[2*i : 2*i+2]
	}
	return strings.Join(parts, ":"), true
}

// machinesReturn is the table address to go back to after a form: the same
// search and order, the message, and the machine's row.
func machinesReturn(r *http.Request, message, mac string) string {
	v := url.Values{}
	if q := strings.TrimSpace(r.PostFormValue("q")); q != "" && len(q) <= 200 {
		v.Set("q", q)
	}
	if sortBy := r.PostFormValue("sort"); sortBy != "last" {
		if _, ok := machineSorts[sortBy]; ok {
			v.Set("sort", sortBy)
		}
	}
	v.Set("message", message)
	out := "/machines?" + v.Encode()
	if mac != "" {
		out += "#m-" + mac
	}
	return out
}

// machineAdd adds a machine by MAC before it ever boots.
func (s *Server) machineAdd(w http.ResponseWriter, r *http.Request) {
	book, ok := s.Store.(MachineBook)
	if !ok {
		http.Error(w, "Список машин недоступен", http.StatusServiceUnavailable)
		return
	}
	mac, valid := parseMAC(r.PostFormValue("mac"))
	if !valid {
		http.Error(w, "Неверный MAC-адрес: нужно 12 шестнадцатеричных цифр, например bc:24:11:aa:bb:01", http.StatusBadRequest)
		return
	}
	edit, valid := parseMachineEdit(r)
	if !valid {
		http.Error(w, "Слишком длинный текст или недопустимые символы", http.StatusBadRequest)
		return
	}
	added, err := book.AddMachine(r.Context(), mac, edit)
	if err != nil {
		http.Error(w, "Не удалось сохранить машину", http.StatusServiceUnavailable)
		return
	}
	if !added {
		http.Error(w, "Такой MAC уже есть в списке", http.StatusConflict)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "machine.add", "ok", mac)
	http.Redirect(w, r, machinesReturn(r, "Машина добавлена", mac), http.StatusSeeOther)
}

// machineTableEdit saves a row edited in the table and returns to it.
func (s *Server) machineTableEdit(w http.ResponseWriter, r *http.Request) {
	mac := strings.ToLower(r.PathValue("mac"))
	book, ok := s.Store.(MachineBook)
	if !macAddress.MatchString(mac) || !ok {
		http.Error(w, "Неверный MAC-адрес", http.StatusBadRequest)
		return
	}
	edit, valid := parseMachineEdit(r)
	if !valid {
		http.Error(w, "Слишком длинный текст или недопустимые символы", http.StatusBadRequest)
		return
	}
	if err := book.EditMachine(r.Context(), mac, edit); err != nil {
		http.Error(w, "Не удалось сохранить машину", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "machine.edit", "ok", mac)
	http.Redirect(w, r, machinesReturn(r, "Машина сохранена", mac), http.StatusSeeOther)
}

// machineTableDelete removes a machine from the list and returns to the table.
func (s *Server) machineTableDelete(w http.ResponseWriter, r *http.Request) {
	mac := strings.ToLower(r.PathValue("mac"))
	book, ok := s.Store.(MachineBook)
	if !macAddress.MatchString(mac) || !ok {
		http.Error(w, "Неверный MAC-адрес", http.StatusBadRequest)
		return
	}
	if err := book.DeleteMachine(r.Context(), mac); err != nil {
		http.Error(w, "Не удалось удалить машину", http.StatusServiceUnavailable)
		return
	}
	s.bookCache.forget(mac)
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "machine.delete", "ok", mac)
	http.Redirect(w, r, machinesReturn(r, "Машина удалена из списка", ""), http.StatusSeeOther)
}

//go:embed machines.js
var machinesJS []byte

func machinesScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(machinesJS)
}
