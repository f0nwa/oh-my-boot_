package control

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"omb/internal/agent"
	"omb/internal/boot"
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

// searchMatchSHA matches a search query against a SHA-256 hash.
func searchMatchSHA(query string, sha string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	sha = strings.ToLower(strings.TrimSpace(sha))
	if q == "" || sha == "" || len(q) < 3 {
		return false
	}
	if strings.Contains(sha, q) {
		return true
	}
	short := strings.ToLower(shortSHA(sha))
	if strings.Contains(short, q) || strings.Contains(strings.ReplaceAll(short, "…", "..."), q) {
		return true
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
	authors := s.resourceAuthors(r.Context())
	profiles, answers, configs, err := s.menuSources(r.Context())
	if err == nil {
		if draft, active, err := s.currentDraft(r.Context()); err == nil {
			inDraft := map[string]bool{}
			for _, item := range draft.Items {
				inDraft[menuDraftItemKey(item)] = true
			}
			sources, _ := s.installationMenuSources(r.Context())
			for _, row := range s.installationRows(r.Context(), profiles, answers, configs, draft, active, sources) {
				if searchMatch(query, row.Title, row.Key, row.Image, row.Answer, row.Drivers, row.Author, row.AnswerAuthor, row.ImageAuthor) ||
					searchMatchSHA(query, row.AnswerSHA) || searchMatchSHA(query, row.DriverSHA) {
					href := "/installations?q=" + row.Key
					if inDraft[row.Key] {
						href = "/menu?item=" + row.Key
					}
					sub := row.Key
					if row.Author != "" {
						sub += " · " + row.Author
					}
					add(searchResult{Kind: "Загрузка", Title: row.Title, Sub: sub, Href: href})
				}
			}
		}
		for _, profile := range profiles {
			author := authors.Images[imageAuthorKey(profile.StorageID, profile.Path)]
			if searchMatch(query, profile.Title, profile.Path, author) {
				sub := profile.Path
				if author != "" {
					sub += " · " + author
				}
				add(searchResult{Kind: "Образ", Title: profile.Title, Sub: sub, Href: "/images"})
			}
		}
		for _, answer := range answers {
			author := authors.Answers[answer.ID]
			if searchMatch(query, answer.Title, author) || searchMatchSHA(query, answer.SHA256) {
				sub := shortSHA(answer.SHA256)
				if author != "" {
					sub += " · " + author
				}
				add(searchResult{Kind: "Скрипт установки", Title: answer.Title, Sub: sub, Href: "/answers?q=" + answer.Title})
			}
		}
		for _, cfg := range configs {
			author := cfg.Author
			if author == "" {
				author = authors.DriverConfigs[cfg.ID]
			}
			if searchMatch(query, cfg.Title, author) || searchMatchSHA(query, cfg.Build.WIMSHA256) {
				title := cfg.Title
				if title == "" {
					title = fmt.Sprintf("%s %d", translate(normalizeLanguage(info.User.Language), "Конфигурация №"), cfg.ID)
				}
				sub := fmt.Sprintf("№ %d", cfg.ID)
				if author != "" {
					sub += " · " + author
				}
				if cfg.Build.WIMSHA256 != "" {
					sub += " · " + shortSHA(cfg.Build.WIMSHA256)
				}
				add(searchResult{Kind: "Конфигурация WinPE", Title: title, Sub: sub, Href: fmt.Sprintf("/drivers#driver-config-%d", cfg.ID)})
			}
		}
	}
	if s.Store != nil {
		if autoinstalls, err := s.Store.UbuntuAutoinstalls(r.Context()); err == nil {
			for _, file := range autoinstalls {
				author := authors.UbuntuAnswers[file.ID]
				if searchMatch(query, file.Title, author) || searchMatchSHA(query, file.SHA256) {
					sub := shortSHA(file.SHA256)
					if author != "" {
						sub += " · " + author
					}
					add(searchResult{Kind: "Скрипт установки", Title: file.Title, Sub: sub, Href: "/answers?q=" + file.Title})
				}
			}
		}
		if packages, err := s.Store.DriverPackages(r.Context()); err == nil {
			for _, pkg := range packages {
				author := authors.Drivers[pkg.SHA256]
				if searchMatch(query, author) || searchMatchSHA(query, pkg.SHA256) {
					title := fmt.Sprintf("Пакет %s", shortSHA(pkg.SHA256))
					sub := author
					if sub == "" {
						sub = pkg.SHA256[:min(12, len(pkg.SHA256))]
					}
					add(searchResult{Kind: "Пакет драйверов", Title: title, Sub: sub, Href: "/drivers"})
				}
			}
		}
		if versions, err := s.Store.MenuVersions(r.Context()); err == nil {
			for _, ver := range versions {
				if searchMatch(query, ver.Author) || searchMatchSHA(query, ver.SHA256) {
					title := fmt.Sprintf("Версия меню #%d", ver.ID)
					if ver.Active {
						title += " (активна)"
					}
					add(searchResult{Kind: "Версия меню", Title: title, Sub: ver.Author + " · " + shortSHA(ver.SHA256), Href: "/menu"})
				}
			}
		}
		if users, err := s.Store.Users(r.Context()); err == nil {
			for _, u := range users {
				if searchMatch(query, u.Username) {
					add(searchResult{Kind: "Пользователь", Title: u.Username, Sub: u.Role, Href: "/users"})
				}
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
	if cat, err := s.wikiCatalog(); err == nil {
		for _, hit := range cat.Search(query, 5) {
			add(searchResult{
				Kind:  "Wiki",
				Title: hit.Article.Title,
				Sub:   hit.Article.SectionTitle,
				Href:  "/wiki/" + hit.Article.Slug,
			})
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
