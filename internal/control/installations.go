package control

import (
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// installationRow is one installation: a profile, a profile with an answer
// file, or a driver configuration, with its place relative to the menu.
type installationRow struct {
	Key, Title, Image, Drivers, Answer string
	AnswerWipesDisk                    *bool
	// Status is in_menu, pending_add, pending_remove, not_in_menu,
	// needs_build, building, error or unavailable.
	Status   string
	ConfigID int64
	Error    string
}

func driverSummary(config DriverConfig) string {
	var names []string
	for _, inf := range config.PackageINFs {
		names = append(names, strings.TrimSuffix(strings.TrimSuffix(path.Base(inf.INFPath), ".inf"), ".INF"))
	}
	return strings.Join(names, ", ")
}

func installationRows(profiles []WindowsProfile, answers []WindowsAnswerVariant, configs []DriverConfig, draft, active MenuDraft) []installationRow {
	profileByID := map[int64]WindowsProfile{}
	for _, p := range profiles {
		profileByID[p.ID] = p
	}
	answerByID := map[int64]WindowsAnswerVariant{}
	for _, a := range answers {
		answerByID[a.ID] = a
	}
	rows := map[string]installationRow{}
	var keys []string
	for _, p := range profiles {
		key := fmt.Sprintf("winiso_%d", p.ID)
		keys = append(keys, key)
		rows[key] = installationRow{Key: key, Title: p.Title, Image: p.Title}
	}
	for _, a := range answers {
		key := fmt.Sprintf("winanswer_%d", a.ID)
		keys = append(keys, key)
		rows[key] = installationRow{Key: key, Title: a.Title, Image: profileByID[a.ProfileID].Title, Answer: a.Title, AnswerWipesDisk: a.WipesDisk}
	}
	for _, c := range configs {
		key := fmt.Sprintf("windriver_%d", c.ID)
		keys = append(keys, key)
		row := installationRow{Key: key, Title: c.Title, Image: profileByID[c.ProfileID].Title, Drivers: driverSummary(c), ConfigID: c.ID, Error: c.Error}
		if a, ok := answerByID[c.AnswerID]; ok && c.AnswerID > 0 {
			row.Answer, row.AnswerWipesDisk = a.Title, a.WipesDisk
		}
		switch c.State {
		case "pending":
			row.Status = "needs_build"
		case "preparing":
			row.Status = "building"
		case "error":
			row.Status = "error"
		}
		rows[key] = row
	}
	inDraft, inActive := map[string]MenuDraftItem{}, map[string]bool{}
	for _, item := range draft.Items {
		inDraft[item.Key] = item
	}
	for _, item := range active.Items {
		inActive[item.Key] = true
	}
	var ordered []string
	seen := map[string]bool{}
	for _, list := range [][]MenuDraftItem{draft.Items, active.Items} {
		for _, item := range list {
			if _, ok := rows[item.Key]; !ok {
				// The object was deleted from the library but the menu still names it.
				rows[item.Key] = installationRow{Key: item.Key, Title: item.Title, Status: "unavailable", Error: "Объект удалён из библиотеки"}
			}
			if !seen[item.Key] {
				ordered, seen[item.Key] = append(ordered, item.Key), true
			}
		}
	}
	for _, key := range keys {
		if !seen[key] {
			ordered, seen[key] = append(ordered, key), true
		}
	}
	var out []installationRow
	for _, key := range ordered {
		row := rows[key]
		if item, ok := inDraft[key]; ok && item.Title != "" {
			row.Title = item.Title
		}
		if row.Status == "" && row.Error == "" {
			if _, _, err := menuProfileForKey(key, "", profiles, answers, configs); err != nil {
				row.Status, row.Error = "unavailable", err.Error()
			} else {
				_, draftHas := inDraft[key]
				switch {
				case draftHas && inActive[key]:
					row.Status = "in_menu"
				case draftHas:
					row.Status = "pending_add"
				case inActive[key]:
					row.Status = "pending_remove"
				default:
					row.Status = "not_in_menu"
				}
			}
		}
		out = append(out, row)
	}
	return out
}

func (s *Server) installationsPage(w http.ResponseWriter, r *http.Request) {
	data := pageData{Info: requestInfo(r), Message: r.URL.Query().Get("message"), Error: pageError(r)}
	profiles, answers, configs, err := s.menuSources(r.Context())
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
	for _, row := range installationRows(profiles, answers, configs, draft, active) {
		haystack := strings.ToLower(row.Title + " " + row.Key + " " + row.Image + " " + row.Answer + " " + row.Drivers)
		if data.Query == "" || strings.Contains(haystack, strings.ToLower(data.Query)) {
			data.Installations = append(data.Installations, row)
		}
	}
	data.Changes = diffMenuDraft(active, draft)
	render(w, "installations", data)
}

// installationAdd puts an installation at the end of the menu draft.
func (s *Server) installationAdd(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !menuItemKey.MatchString(key) {
		http.Error(w, "Неверная установка", http.StatusBadRequest)
		return
	}
	profiles, answers, configs, err := s.menuSources(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	_, folder, err := menuProfileForKey(key, "", profiles, answers, configs)
	if err != nil {
		http.Error(w, "Установка не готова: "+err.Error(), http.StatusConflict)
		return
	}
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	draft, active, err := s.currentDraft(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать черновик меню", http.StatusServiceUnavailable)
		return
	}
	if draftItemIndex(&draft, key) < 0 {
		draft.Items = insertDraftItem(draft.Items, active.Items, MenuDraftItem{Key: key, Folder: folder})
		if err := s.Store.SaveMenuDraft(r.Context(), draft); err != nil {
			http.Error(w, "Не удалось сохранить черновик меню", http.StatusServiceUnavailable)
			return
		}
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "menu.draft.add", "ok", key)
	if strings.HasPrefix(r.URL.Path, "/menu/") {
		http.Redirect(w, r, "/menu?message=Пункт+возвращён+в+меню", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/?message=Установка+добавлена+в+меню", http.StatusSeeOther)
}

// insertDraftItem adds item to the draft. An item still in the published menu
// gets back its published title, folder and place, so re-adding undoes a
// removal instead of creating new changes.
func insertDraftItem(items, published []MenuDraftItem, item MenuDraftItem) []MenuDraftItem {
	at := -1
	for i, old := range published {
		if old.Key != item.Key {
			continue
		}
		item = old
		for _, later := range published[i+1:] {
			for j, current := range items {
				if current.Key == later.Key && (at < 0 || j < at) {
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
func menuUsage(active MenuDraft, answers []WindowsAnswerVariant, configs []DriverConfig) (map[int64]int, map[int64]int) {
	profileUse, answerUse := map[int64]int{}, map[int64]int{}
	answerProfile := map[int64]int64{}
	for _, a := range answers {
		answerProfile[a.ID] = a.ProfileID
	}
	for _, item := range active.Items {
		prefix, raw, _ := strings.Cut(item.Key, "_")
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			continue
		}
		switch prefix {
		case "winiso":
			profileUse[id]++
		case "winanswer":
			answerUse[id]++
			if p, ok := answerProfile[id]; ok {
				profileUse[p]++
			}
		case "windriver":
			for _, c := range configs {
				if c.ID == id {
					profileUse[c.ProfileID]++
					if c.AnswerID > 0 {
						answerUse[c.AnswerID]++
					}
				}
			}
		}
	}
	return profileUse, answerUse
}
