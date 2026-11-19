package control

import (
	"context"
	"net/http"
	"sort"
	"strconv"
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
	data := pageData{
		Info:      requestInfo(r),
		Tab:       r.URL.Query().Get("tab"),
		MACFilter: strings.ToLower(strings.TrimSpace(r.URL.Query().Get("mac"))),
		SystemSev: strings.TrimSpace(r.URL.Query().Get("sev")),
		SystemCat: strings.TrimSpace(r.URL.Query().Get("cat")),
		Query:     strings.TrimSpace(r.URL.Query().Get("q")),
	}
	if data.Tab != "requests" && data.Tab != "dhcp" && data.Tab != "system" {
		data.Tab = "machines"
	}
	if data.SystemSev != "error" && data.SystemSev != "warning" && data.SystemSev != "info" {
		data.SystemSev = "all"
	}
	if data.SystemCat != "storage" && data.SystemCat != "service" && data.SystemCat != "network" && data.SystemCat != "task" {
		data.SystemCat = "all"
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
	data.SystemErrorCount = s.recentSystemErrorCount(r.Context())
	render(w, "events", data)
}

func (s *Server) eventsContent(w http.ResponseWriter, r *http.Request) {
	r, cancel := eventsContext(r)
	defer cancel()
	data := s.eventsTableData(r)
	if r.URL.Query().Get("partial") == "1" {
		if data.Tab == "requests" {
			if data.EventError != "" {
				http.Error(w, data.EventError, http.StatusServiceUnavailable)
				return
			}
			render(w, "event_requests_partial", data)
			return
		}
		if data.Tab == "dhcp" {
			if data.DHCPError != "" {
				http.Error(w, data.DHCPError, http.StatusServiceUnavailable)
				return
			}
			render(w, "event_dhcp_partial", data)
			return
		}
		if data.Tab == "system" {
			if data.SystemEventError != "" {
				http.Error(w, data.SystemEventError, http.StatusServiceUnavailable)
				return
			}
			render(w, "event_system_partial", data)
			return
		}
	}
	render(w, "events-table", data)
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

	if data.Tab == "system" {
		store, ok := s.Store.(SystemEventStore)
		if !ok {
			data.SystemEventError = "Журнал системных событий недоступен"
			return data
		}
		filter := SystemEventFilter{
			Severity: data.SystemSev,
			Category: data.SystemCat,
			Query:    data.Query,
		}
		if bt := r.URL.Query().Get("before_time"); bt != "" {
			if t, err := time.Parse(time.RFC3339Nano, bt); err == nil {
				filter.BeforeTime = &t
			} else if t, err := time.Parse(time.RFC3339, bt); err == nil {
				filter.BeforeTime = &t
			}
		}
		if bid := r.URL.Query().Get("before_id"); bid != "" {
			if id, err := strconv.ParseInt(bid, 10, 64); err == nil && id > 0 {
				filter.BeforeID = id
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		items, err := store.SystemEventsPaged(ctx, filter, 50)
		if err != nil {
			data.SystemEventError = "Журнал системных событий временно недоступен"
			return data
		}
		data.SystemEvents = items
		if len(items) == 50 {
			data.EventsHasMore = true
			last := items[len(items)-1]
			data.EventsNextTime = last.OccurredAt.Format(time.RFC3339Nano)
			data.EventsNextID = last.ID
		}
		return data
	}

	if data.Tab == "requests" {
		var beforeTime *time.Time
		if bt := r.URL.Query().Get("before_time"); bt != "" {
			if t, err := time.Parse(time.RFC3339Nano, bt); err == nil {
				beforeTime = &t
			} else if t, err := time.Parse(time.RFC3339, bt); err == nil {
				beforeTime = &t
			}
		}
		var beforeID int64
		if bid := r.URL.Query().Get("before_id"); bid != "" {
			if id, err := strconv.ParseInt(bid, 10, 64); err == nil && id > 0 {
				beforeID = id
			}
		}

		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if s.Boot == nil {
			data.EventError = "Служба Boot недоступна"
		} else if v, err := s.Boot.EventsPaged(ctx, 50, beforeTime, beforeID, data.MACFilter); err != nil {
			data.EventError = "Журнал временно недоступен"
		} else {
			for _, e := range v {
				if data.MACFilter == "" || e.MAC == data.MACFilter {
					data.Events = append(data.Events, e)
				}
			}
			if len(v) == 50 {
				data.EventsHasMore = true
				lastEvent := v[len(v)-1]
				data.EventsNextTime = lastEvent.At.Format(time.RFC3339Nano)
				data.EventsNextID = lastEvent.ID
			} else {
				data.EventsHasMore = false
			}
		}
		return data
	}

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
	case "dhcp":
		offset := 0
		if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && v >= 0 {
			offset = v
		}
		var filtered []agent.DHCPEvent
		for _, e := range dhcp {
			if data.MACFilter == "" || e.MAC == data.MACFilter {
				filtered = append(filtered, e)
			}
		}
		total := len(filtered)
		const limit = 50
		if offset < total {
			end := offset + limit
			if end > total {
				end = total
			}
			data.DHCPEvents = filtered[offset:end]
		} else {
			data.DHCPEvents = nil
		}
		if offset+limit < total {
			data.EventsHasMore = true
			data.EventsNextOffset = offset + limit
		} else {
			data.EventsHasMore = false
		}
	}
	return data
}
