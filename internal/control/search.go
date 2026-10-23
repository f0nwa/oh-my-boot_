package control

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"strings"

	"pxe-platform/internal/agent"
	"pxe-platform/internal/boot"
)

// searchResult is one hit of the ⌘K search.
type searchResult struct {
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Sub   string `json:"sub"`
	Href  string `json:"href"`
}

const maxSearchResults = 20

// searchKey folds text for matching: lower case, and MAC addresses match
// with or without separators.
func searchKey(text string) string {
	return strings.NewReplacer(":", "", "-", "", " ", "").Replace(strings.ToLower(text))
}

func searchMatch(query string, fields ...string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	compact := searchKey(q)
	for _, field := range fields {
		if field == "" {
			continue
		}
		if strings.Contains(strings.ToLower(field), q) || (compact != "" && strings.Contains(searchKey(field), compact)) {
			return true
		}
	}
	return false
}

// searchAll finds machines, installations, images, answer files and pages
// whose names, keys, files or MAC addresses contain the query.
func (s *Server) searchAll(r *http.Request, query string) []searchResult {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	info := requestInfo(r)
	var results []searchResult
	add := func(result searchResult) {
		if len(results) < maxSearchResults {
			results = append(results, result)
		}
	}
	var httpEvents []boot.Event
	if s.Boot != nil {
		if v, err := s.Boot.Events(r.Context(), 1000); err == nil {
			httpEvents = v
		}
	}
	var dhcp []agent.DHCPEvent
	if s.Agent != nil {
		if v, err := s.dhcpEvents(r.Context()); err == nil {
			dhcp = v
		}
	}
	names := s.menuNames(r.Context())
	rows := machineRows(httpEvents, dhcp, names)
	book := s.machineBook(r.Context(), rows, httpEvents)
	rules := s.macRules(r.Context())
	seen := map[string]bool{}
	for _, row := range rows {
		seen[row.MAC] = true
		info, ok := book[row.MAC]
		if !ok {
			info = MachineInfo{MAC: row.MAC, Rule: rules.match(row.MAC)}
		}
		if searchMatch(query, row.MAC, row.IP, info.Name, info.Maker(), info.Model(), info.Serial, info.Note, info.NICVendor(), info.RuleTitle()) {
			add(machineSearchResult(row.MAC, row.IP, row.Choice, info))
		}
	}
	// Machines only the book still remembers.
	for _, info := range book {
		if !seen[info.MAC] && searchMatch(query, info.MAC, info.LastIP, info.Name, info.Maker(), info.Model(), info.Serial, info.Note, info.NICVendor(), info.RuleTitle()) {
			add(machineSearchResult(info.MAC, info.LastIP, "", info))
		}
	}
	profiles, answers, configs, err := s.menuSources(r.Context())
	if err == nil {
		if draft, active, err := s.currentDraft(r.Context()); err == nil {
			inDraft := map[string]bool{}
			for _, item := range draft.Items {
				inDraft[item.Key] = true
			}
			for _, row := range installationRows(profiles, answers, configs, draft, active) {
				if searchMatch(query, row.Title, row.Key, row.Image, row.Answer, row.Drivers) {
					href := "/installations?q=" + row.Key
					if inDraft[row.Key] {
						href = "/menu?item=" + row.Key
					}
					add(searchResult{Kind: "Установка", Title: row.Title, Sub: row.Key, Href: href})
				}
			}
		}
		for _, profile := range profiles {
			if searchMatch(query, profile.Title, profile.Path) {
				add(searchResult{Kind: "Образ", Title: profile.Title, Sub: profile.Path, Href: "/images"})
			}
		}
		for _, answer := range answers {
			if searchMatch(query, answer.Title) {
				add(searchResult{Kind: "Файл ответов", Title: answer.Title, Sub: keyFor("winanswer", answer.ID), Href: "/answers"})
			}
		}
	}
	for _, group := range navigation(info.User.Role, "") {
		for _, item := range group.Items {
			if searchMatch(query, item.Label, translate(langEN, item.Label)) {
				add(searchResult{Kind: "Раздел", Title: item.Label, Sub: group.Title, Href: item.Path})
			}
		}
	}
	return results
}

// searchPage answers the ⌘K box with JSON and, without JavaScript, shows the
// same results as a page.
func (s *Server) searchPage(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	results := s.searchAll(r, query)
	lang := normalizeLanguage(requestInfo(r).User.Language)
	for i := range results {
		results[i].Kind = translate(lang, results[i].Kind)
		results[i].Title = translate(lang, results[i].Title)
		results[i].Sub = translate(lang, results[i].Sub)
	}
	if r.URL.Query().Get("format") == "json" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if results == nil {
			results = []searchResult{}
		}
		_ = json.NewEncoder(w).Encode(results)
		return
	}
	render(w, "search", pageData{Info: requestInfo(r), Query: query, SearchResults: results})
}

//go:embed search.js
var searchJS []byte

func searchScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(searchJS)
}

// machineSearchResult names a machine by its own name when it has one.
func machineSearchResult(mac, ip, choice string, info MachineInfo) searchResult {
	title, parts := mac, []string{}
	if info.Name != "" {
		title = info.Name
		parts = append(parts, mac)
	}
	if label := machineLabel(&info, mac); label != "" {
		parts = append(parts, label)
	}
	if ip != "" {
		parts = append(parts, ip)
	}
	if choice != "" {
		parts = append(parts, choice)
	}
	return searchResult{Kind: "Машина", Title: title, Sub: strings.Join(parts, " · "), Href: "/events/machines/" + mac}
}
