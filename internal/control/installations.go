package control

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"pxe-platform/internal/boot"
	"strconv"
	"strings"
	"time"
)

// installationRow shows an installation, its shared source and its menu placement.
type installationRow struct {
	Key, Title, Image, Drivers, Answer string
	ID                                 int64
	Kind                               string
	AnswerWipesDisk                    *bool
	// Status is in_menu, pending_add, pending_remove, not_in_menu,
	// needs_build, building, error or unavailable.
	Status   string
	ConfigID int64
	Error    string
	// Folder is where the item shows in the menu; empty is the main screen.
	Folder string
	// Used counts explicit choices of this installation; LastUsed is the latest.
	Used     int
	LastUsed time.Time
}

// UsedLabel says how often and how recently machines chose this installation.
func (r installationRow) UsedLabel() string {
	if r.Used == 0 {
		return "не выбирали"
	}
	return fmt.Sprintf("выборов: %d · %s", r.Used, ago(time.Now(), r.LastUsed))
}

// Group puts the status in one of the filters on the installations page.
func (r installationRow) Group() string {
	switch r.Status {
	case "in_menu":
		return "menu"
	case "pending_add", "pending_remove":
		return "draft"
	case "not_in_menu":
		return "off"
	}
	return "attention"
}

type installationFilter struct {
	Label, Value, Count string
}

// installationFilters are the filters above the installations table.
func installationFilters() []installationFilter {
	return []installationFilter{
		{"Все", "", "all"}, {"В меню", "menu", "menu"}, {"В черновике", "draft", "draft"},
		{"Требуют внимания", "attention", "attention"}, {"Не в меню", "off", "off"},
	}
}

// installationUsage counts explicit menu choices by installation ID. Each
// choice counts once; file requests and server probes do not count.
func installationUsage(events []boot.Event, skip map[string]bool) map[string]installationUse {
	use := map[string]installationUse{}
	for _, e := range events {
		key := choiceKey(e)
		if key == "" || skip[eventIP(e.RemoteAddr)] {
			continue
		}
		u := use[key]
		u.Count++
		if e.At.After(u.Last) {
			u.Last = e.At
		}
		use[key] = u
	}
	return use
}

type installationUse struct {
	Count int
	Last  time.Time
}

func driverSummary(config DriverConfig) string {
	var names []string
	for _, inf := range config.PackageINFs {
		names = append(names, strings.TrimSuffix(strings.TrimSuffix(path.Base(inf.INFPath), ".inf"), ".INF"))
	}
	return strings.Join(names, ", ")
}

// installationRows shows persisted choices; library components never invent rows.
func installationRows(profiles []WindowsProfile, answers []WindowsAnswerVariant, configs []DriverConfig, draft, active MenuDraft, sources InstallationMenuSources) []installationRow {
	source := sources
	rows := map[string]installationRow{}
	keys := []string{}
	for _, installation := range source.Installations {
		key := fmt.Sprintf("install_%d", installation.ID)
		row := installationRow{ID: installation.ID, Key: key, Title: installation.Title, Folder: installation.Folder}
		for _, profile := range source.BootProfiles {
			if profile.ID == installation.BootProfileID {
				row.Kind = profile.Kind
				row.Image = profile.Title
				for _, windows := range profiles {
					if profile.Kind == BootProfileWindowsISO && windows.ID == profile.WindowsProfileID {
						row.Image = windows.Title
					}
				}
				if profile.Kind == BootProfileUbuntuBuiltin {
					row.Image = "Системный источник Ubuntu"
				}
			}
		}
		answerID := installation.AnswerID
		for _, config := range configs {
			if config.ID == installation.DriverConfigID {
				row.ConfigID, row.Drivers, row.Error = config.ID, driverSummary(config), config.Error
				answerID = config.AnswerID
				switch config.State {
				case "pending":
					row.Status = "needs_build"
				case "preparing":
					row.Status = "building"
				case "error":
					row.Status = "error"
				}
			}
		}
		for _, answer := range answers {
			if answer.ID == answerID {
				row.Answer, row.AnswerWipesDisk = answer.Title, answer.WipesDisk
			}
		}
		if row.Status == "" {
			if _, _, err := menuProfileForInstallation(installation.ID, "", source, profiles, answers, configs); err != nil {
				row.Status, row.Error = "unavailable", err.Error()
			}
		}
		rows[key] = row
		keys = append(keys, key)
	}
	inDraft, inActive := map[string]MenuDraftItem{}, map[string]MenuDraftItem{}
	for _, item := range draft.Items {
		inDraft[menuDraftItemKey(item)] = item
	}
	for _, item := range active.Items {
		inActive[menuDraftItemKey(item)] = item
	}
	ordered := []string{}
	seen := map[string]bool{}
	for _, items := range [][]MenuDraftItem{draft.Items, active.Items} {
		for _, item := range items {
			key := menuDraftItemKey(item)
			if _, ok := rows[key]; !ok {
				rows[key] = installationRow{ID: item.InstallationID, Key: key, Title: item.Title, Status: "unavailable", Error: "Объект удалён из библиотеки"}
			}
			if !seen[key] {
				ordered = append(ordered, key)
				seen[key] = true
			}
		}
	}
	for _, key := range keys {
		if !seen[key] {
			ordered = append(ordered, key)
			seen[key] = true
		}
	}
	out := make([]installationRow, 0, len(ordered))
	for _, key := range ordered {
		row := rows[key]
		item, draftHas := inDraft[key]
		published, activeHas := inActive[key]
		if !draftHas && activeHas {
			item = published
		}
		if draftHas || activeHas {
			if item.Title != "" {
				row.Title = item.Title
			}
			if item.Folder != defaultMenuFolder {
				row.Folder = item.Folder
			}
		}
		if row.Status == "" {
			switch {
			case draftHas && activeHas:
				row.Status = "in_menu"
			case draftHas:
				row.Status = "pending_add"
			case activeHas:
				row.Status = "pending_remove"
			default:
				row.Status = "not_in_menu"
			}
		}
		out = append(out, row)
	}
	return out
}

// installationRows attaches live Ubuntu readiness to its persisted choices.
func (s *Server) installationRows(ctx context.Context, profiles []WindowsProfile, answers []WindowsAnswerVariant, configs []DriverConfig, draft, active MenuDraft, sources InstallationMenuSources) []installationRow {
	rows := installationRows(profiles, answers, configs, draft, active, sources)
	checked := false
	var problem error
	for i := range rows {
		if rows[i].Kind != BootProfileUbuntuBuiltin {
			continue
		}
		if !checked {
			problem = s.checkUbuntuReadiness(ctx)
			checked = true
		}
		if problem != nil {
			rows[i].Status, rows[i].Error = "unavailable", problem.Error()
		}
	}
	return rows
}

func (s *Server) installationsPage(w http.ResponseWriter, r *http.Request) {
	data := pageData{Info: requestInfo(r), Message: r.URL.Query().Get("message"), Error: pageError(r)}
	profiles, answers, configs, err := s.menuSources(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sources, err := s.installationMenuSources(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	draft, active, err := s.currentDraft(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать черновик меню", http.StatusInternalServerError)
		return
	}
	data.Query = strings.TrimSpace(r.URL.Query().Get("q"))
	data.Tab = r.URL.Query().Get("show")
	var use map[string]installationUse
	if s.Boot != nil {
		if events, err := s.Boot.Events(r.Context(), 1000); err == nil {
			skip := map[string]bool{"127.0.0.1": true, "::1": true}
			if s.Agent != nil {
				if c, err := s.Agent.Config(r.Context()); err == nil {
					skip[c.ServerIP] = true
				}
			}
			use = installationUsage(events, skip)
		}
	}
	data.InstallCounts = map[string]int{}
	for _, row := range s.installationRows(r.Context(), profiles, answers, configs, draft, active, sources) {
		haystack := strings.ToLower(row.Title + " " + row.Key + " " + row.Image + " " + row.Answer + " " + row.Drivers)
		if data.Query != "" && !strings.Contains(haystack, strings.ToLower(data.Query)) {
			continue
		}
		row.Used, row.LastUsed = use[row.Key].Count, use[row.Key].Last
		data.InstallCounts["all"]++
		data.InstallCounts[row.Group()]++
		if data.Tab == "" || data.Tab == row.Group() {
			data.Installations = append(data.Installations, row)
		}
	}
	data.Changes = diffMenuDraft(active, draft)
	render(w, "installations", data)
}

// installationAdd puts an installation at the end of the menu draft.
func (s *Server) installationAdd(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("key")
	if id, err := strconv.ParseInt(strings.TrimPrefix(raw, "install_"), 10, 64); err == nil && id > 0 {
		s.installationAddByID(w, r, id)
		return
	}
	http.Error(w, "Неверная установка", http.StatusBadRequest)
}

func (s *Server) installationAddByID(w http.ResponseWriter, r *http.Request, id int64) {
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	sources, err := s.installationMenuSources(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	profiles, answers, configs, err := s.menuSources(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	_, _, err = menuProfileForInstallation(id, "", sources, profiles, answers, configs)
	if err != nil {
		http.Error(w, "Установка не готова: "+err.Error(), http.StatusConflict)
		return
	}
	var installation Installation
	for _, item := range sources.Installations {
		if item.ID == id {
			installation = item
			break
		}
	}
	draft, active, err := s.currentDraft(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать черновик меню", http.StatusServiceUnavailable)
		return
	}
	if draft.InstallationIndex(id) < 0 {
		draft.Items = insertDraftItem(draft.Items, active.Items, MenuDraftItem{InstallationID: id, Title: installation.Title, Folder: installation.Folder})
		if err := s.Store.SaveMenuDraft(r.Context(), draft); err != nil {
			http.Error(w, "Не удалось сохранить черновик меню", http.StatusServiceUnavailable)
			return
		}
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "menu.draft.add", "ok", fmt.Sprintf("install_%d", id))
	target := "/installations?message=Установка+добавлена+в+меню"
	if strings.HasPrefix(r.URL.Path, "/menu/") {
		target = "/menu?message=Пункт+возвращён+в+меню"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// insertDraftItem adds item to the draft. An item still in the published menu
// gets back its published title, folder and place, so re-adding undoes a
// removal instead of creating new changes.
func insertDraftItem(items, published []MenuDraftItem, item MenuDraftItem) []MenuDraftItem {
	at := -1
	for i, old := range published {
		if menuDraftItemKey(old) != menuDraftItemKey(item) {
			continue
		}
		item = old
		for _, later := range published[i+1:] {
			for j, current := range items {
				if menuDraftItemKey(current) == menuDraftItemKey(later) && (at < 0 || j < at) {
					at = j
				}
			}
		}
		if at < 0 {
			at = len(items)
		}
		break
	}
	if at < 0 {
		return append(items, item)
	}
	out := append([]MenuDraftItem(nil), items[:at]...)
	out = append(out, item)
	return append(out, items[at:]...)
}

// menuUsage counts the published menu items that rely on each profile and
// each answer file.
func menuUsage(active MenuDraft, configs []DriverConfig, source InstallationMenuSources) (map[int64]int, map[int64]int) {
	profileUse, answerUse := map[int64]int{}, map[int64]int{}
	for _, item := range active.Items {
		for _, installation := range source.Installations {
			if installation.ID != item.InstallationID {
				continue
			}
			for _, profile := range source.BootProfiles {
				if profile.ID == installation.BootProfileID && profile.Kind == BootProfileWindowsISO {
					profileUse[profile.WindowsProfileID]++
				}
			}
			answerID := installation.AnswerID
			for _, config := range configs {
				if config.ID == installation.DriverConfigID {
					answerID = config.AnswerID
				}
			}
			if answerID > 0 {
				answerUse[answerID]++
			}
		}
	}
	return profileUse, answerUse
}
