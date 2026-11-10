package control

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"omb/internal/agent"
	"omb/internal/boot"
)

// Fragment requests do not need the expensive draft bar used by full HTML pages.
func needsDraftSummary(r *http.Request) bool {
	p := r.URL.Path
	if strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/assets/") ||
		r.URL.Query().Get("format") == "json" || strings.Contains(r.Header.Get("Accept"), "application/json") {
		return false
	}
	switch r.Pattern {
	case "GET /images/storages/{id}/folders", "GET /ubuntu-autoinstall/{id}/download":
		return false
	}
	switch p {
	case "/events/content", "/events/services", "/events/journal", "/machines.csv":
		return false
	}
	return true
}

func eventsRequest(r *http.Request) pageData {
	data := pageData{Info: requestInfo(r), Tab: r.URL.Query().Get("tab"), MACFilter: strings.ToLower(strings.TrimSpace(r.URL.Query().Get("mac")))}
	if data.Tab != "requests" && data.Tab != "dhcp" {
		data.Tab = "machines"
	}
	return data
}

func eventsContext(r *http.Request) (*http.Request, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	return r.WithContext(ctx), cancel
}

func (s *Server) eventsPage(w http.ResponseWriter, r *http.Request) {
	data := eventsRequest(r)
	data.EventsAsync = r.URL.Query().Get("full") != "1"
	if !data.EventsAsync {
		r, cancel := eventsContext(r)
		defer cancel()
		data = s.eventsTableData(r)
		s.loadServiceStatus(r.Context(), &data)
		s.loadEventsJournal(r, &data)
	}
	render(w, "events", data)
}

func (s *Server) eventsContent(w http.ResponseWriter, r *http.Request) {
	r, cancel := eventsContext(r)
	defer cancel()
	render(w, "events-table", s.eventsTableData(r))
}

func (s *Server) eventsServices(w http.ResponseWriter, r *http.Request) {
	r, cancel := eventsContext(r)
	defer cancel()
	data := eventsRequest(r)
	s.loadServiceStatus(r.Context(), &data)
	render(w, "events-services", data)
}

func (s *Server) loadEventsJournal(r *http.Request, data *pageData) {
	if !Allowed(data.Info.User.Role, "delete") {
		return
	}
	if s.Boot != nil {
		if state, err := s.Boot.Journal(r.Context()); err == nil {
			data.Journal = &journalView{JournalState: state, Choices: journalChoices()}
			return
		}
	}
	data.Error = "Журнал временно недоступен"
}

func (s *Server) eventsJournal(w http.ResponseWriter, r *http.Request) {
	r, cancel := eventsContext(r)
	defer cancel()
	data := eventsRequest(r)
	s.loadEventsJournal(r, &data)
	render(w, "events-heading", data)
}

func (s *Server) eventsTableData(r *http.Request) pageData {
	data := eventsRequest(r)
	var httpEvents []boot.Event
	var rawDHCP []agent.DHCPEvent
	var sources sync.WaitGroup
	sources.Add(1)
	go func() {
		defer sources.Done()
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if s.Boot == nil {
			data.EventError = "Служба Boot недоступна"
		} else if v, err := s.Boot.Events(ctx, 1000); err != nil {
			data.EventError = "Журнал временно недоступен"
		} else {
			httpEvents = v
		}
	}()
	if data.Tab != "requests" {
		sources.Add(1)
		go func() {
			defer sources.Done()
			if s.Agent == nil {
				data.DHCPError = "Журнал DHCP недоступен"
			} else if v, err := s.dhcpEvents(r.Context()); err != nil {
				data.DHCPError = "Журнал DHCP недоступен"
			} else {
				rawDHCP = v
			}
		}()
	}
	sources.Wait()
	dhcp := resolveDHCPMACs(rawDHCP, httpEvents)

	switch data.Tab {
	case "machines":
		rows := machineRowsWith(httpEvents, dhcp, s.loadInstallLog(r.Context(), httpEvents), s.menuNames(r.Context()), time.Now())
		book := s.machineBook(r.Context(), rows, httpEvents)
		// The book also knows machines whose requests have left the logs.
		inRows := map[string]bool{}
		for _, row := range rows {
			inRows[row.MAC] = true
		}
		for _, info := range book {
			if !inRows[info.MAC] {
				rows = append(rows, machineRow{MAC: info.MAC, IP: info.LastIP, Firmware: info.Firmware, LastSeen: info.LastSeen, Stage: "unknown"})
			}
		}
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].LastSeen.After(rows[j].LastSeen) })
		describeMachines(rows, book, s.macRules(r.Context()))
		data.Machines = rows
	case "requests":
		for _, e := range httpEvents {
			if data.MACFilter == "" || e.MAC == data.MACFilter {
				data.Events = append(data.Events, e)
			}
		}
		if len(data.Events) > 300 {
			data.Events = data.Events[:300]
		}
	case "dhcp":
		for _, e := range dhcp {
			if data.MACFilter == "" || e.MAC == data.MACFilter {
				data.DHCPEvents = append(data.DHCPEvents, e)
			}
		}
		if len(data.DHCPEvents) > 300 {
			data.DHCPEvents = data.DHCPEvents[:300]
		}
	}
	return data
}
