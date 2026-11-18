package control

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"net/url"
	"omb/internal/boot"
	"omb/internal/images"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed download_search.js
var downloadSearchJS []byte

func downloadSearchScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(downloadSearchJS)
}

// installationRow shows an installation, its shared source and its menu placement.
type installationRow struct {
	Key, Title, Image, Drivers, Answer string
	Author, AnswerAuthor, ImageAuthor  string
	AnswerSHA, DriverSHA               string
	AnswerID                           int64
	ID                                 int64
	Kind                               string
	SourcePath, StorageName            string
	StorageID                          int64
	StorageWarning                     string
	StorageUnavailable                 bool
	CanDelete                          bool
	InMenu                             bool
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
	case "pending_add", "pending_remove", "pending_change":
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
					if isISOBootKind(profile.Kind) && windows.ID == profile.WindowsProfileID {
						row.Image = windows.Title
						row.SourcePath, row.StorageID = windows.Path, windows.StorageID
						switch windows.State {
						case "pending":
							row.Status = "needs_prepare"
						case "preparing":
							row.Status = "preparing"
						case "error":
							row.Status, row.Error = "error", windows.Error
						case "unavailable":
							row.Status, row.Error = "unavailable", windows.Error
						}
					}
				}
				if profile.Kind == BootProfileUbuntuISO {
					row.Image = path.Base(profile.UbuntuPath)
					row.SourcePath, row.StorageID = profile.UbuntuPath, profile.UbuntuStorageID
					switch profile.UbuntuState {
					case "unverified", "checking", "verified":
						row.Status = "needs_prepare"
					case "preparing":
						row.Status = "preparing"
					case "error":
						row.Status, row.Error = "error", "Не удалось подготовить Ubuntu ISO. Проверьте образ, хранилище и службы Agent и Boot."
					}
				}
				if profile.Kind == BootProfileUbuntuBuiltin {
					row.Image = profile.Title
					if profile.UbuntuPath != "" {
						row.Image = path.Base(profile.UbuntuPath)
						row.SourcePath, row.StorageID = profile.UbuntuPath, profile.UbuntuStorageID
					}
				}
			}
		}
		answerID := installation.AnswerID
		for _, config := range configs {
			if config.ID == installation.DriverConfigID {
				row.ConfigID, row.Drivers = config.ID, driverSummary(config)
				row.DriverSHA = config.Build.ScriptSHA256
				if row.DriverSHA == "" {
					row.DriverSHA = config.Build.WIMSHA256
				}
				if row.Status != "" {
					continue
				}
				row.Error = config.Error
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
				row.Answer, row.AnswerWipesDisk, row.AnswerID = answer.Title, answer.WipesDisk, answer.ID
				row.AnswerSHA = answer.SHA256
			}
		}
		for _, file := range source.Autoinstalls {
			if file.ID == installation.AutoinstallID {
				wipes := file.DiskMode == "default" || file.DiskMode == "serial"
				row.Answer, row.AnswerWipesDisk = file.Title, &wipes
				row.AnswerSHA = file.SHA256
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
		row.CanDelete = !activeHas && !draftHas
		row.InMenu = activeHas || draftHas
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
				if item.Title != published.Title || item.Folder != published.Folder || !sameInstallationComponents(item.Components, published.Components) {
					row.Status = "pending_change"
				}
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

// installationRows attaches current source availability to persisted choices.
func (s *Server) installationRows(ctx context.Context, profiles []WindowsProfile, answers []WindowsAnswerVariant, configs []DriverConfig, draft, active MenuDraft, sources InstallationMenuSources) []installationRow {
	rows := installationRows(profiles, answers, configs, draft, active, sources)
	storageNames := map[int64]string{}
	storageStates := map[int64]string{}
	storageRoots := []images.Storage{}
	catalogAvailable := false
	needsCatalog := len(profiles) > 0
	for _, row := range rows {
		if row.Kind == BootProfileUbuntuBuiltin || row.Kind == BootProfileUbuntuISO {
			needsCatalog = true
			break
		}
	}
	if s.Images != nil && needsCatalog {
		// A catalog outage must not hide saved paths or prevent editing.
		if catalog, err := s.Images.Catalog(ctx); err == nil {
			catalogAvailable = true
			storageRoots = catalog.Storages
			for _, storage := range catalog.Storages {
				storageNames[storage.ID] = storage.Name
				storageStates[storage.ID] = storage.State
			}
		}
	}
	ubuntuISOPath := ""
	if s.Boot != nil {
		ubuntuISOPath, _ = s.Boot.UbuntuISOPath(ctx)
	}
	checked := false
	names, _ := s.imageNames(ctx)
	var problem error
	for i := range rows {
		rows[i].StorageName = storageNames[rows[i].StorageID]
		if (isISOBootKind(rows[i].Kind) || rows[i].Kind == BootProfileUbuntuISO) && rows[i].StorageID > 0 {
			switch state, found := storageStates[rows[i].StorageID]; {
			case !catalogAvailable:
				rows[i].StorageWarning = "Не удалось проверить хранилище ISO"
			case !found:
				rows[i].StorageWarning = "Хранилище ISO не найдено"
			case state == "unavailable":
				rows[i].StorageWarning, rows[i].StorageUnavailable = "Хранилище ISO недоступно", true
			case state == "scanning":
				rows[i].StorageWarning = "Проверяется хранилище ISO"
			}
		}
		if rows[i].Kind != BootProfileUbuntuBuiltin {
			if rows[i].StorageID > 0 && rows[i].SourcePath != "" {
				rows[i].Image = path.Base(rows[i].SourcePath)
				if name := names[imageAuthorKey(rows[i].StorageID, rows[i].SourcePath)]; name != "" {
					rows[i].Image = name
				}
			}
			continue
		}
		if ubuntuISOPath != "" {
			rows[i].SourcePath = ubuntuISOPath
			for _, storage := range storageRoots {
				if relative, ok := storageRelativePath(storage.Root, ubuntuISOPath); ok {
					rows[i].StorageID = storage.ID
					rows[i].StorageName = storage.Name
					rows[i].SourcePath = relative
					break
				}
			}
		}
		if rows[i].StorageID > 0 && rows[i].SourcePath != "" {
			rows[i].Image = path.Base(rows[i].SourcePath)
			if name := names[imageAuthorKey(rows[i].StorageID, rows[i].SourcePath)]; name != "" {
				rows[i].Image = name
			}
		}
		if !checked {
			problem = s.checkUbuntuReadinessCached(ctx)
			checked = true
		}
		if problem != nil {
			rows[i].Status, rows[i].Error = "unavailable", problem.Error()
		}
	}
	authors := s.resourceAuthors(ctx)
	profileByID := make(map[int64]WindowsProfile, len(profiles))
	for _, profile := range profiles {
		profileByID[profile.ID] = profile
	}
	bootByID := make(map[int64]int64, len(sources.BootProfiles))
	for _, profile := range sources.BootProfiles {
		bootByID[profile.ID] = profile.WindowsProfileID
	}
	installationByID := make(map[int64]Installation, len(sources.Installations))
	for _, installation := range sources.Installations {
		installationByID[installation.ID] = installation
	}
	for i := range rows {
		rows[i].Author = authors.Installations[rows[i].ID]
		if rows[i].AnswerID > 0 {
			rows[i].AnswerAuthor = authors.Answers[rows[i].AnswerID]
		}
		if inst, ok := installationByID[rows[i].ID]; ok {
			if inst.AutoinstallID > 0 && rows[i].AnswerAuthor == "" {
				rows[i].AnswerAuthor = authors.UbuntuAnswers[inst.AutoinstallID]
			}
			if profile, ok := profileByID[bootByID[inst.BootProfileID]]; ok {
				rows[i].ImageAuthor = authors.Images[imageAuthorKey(profile.StorageID, profile.Path)]
			}
		}
	}
	return rows
}

func storageRelativePath(root, source string) (string, bool) {
	if !filepath.IsAbs(root) || !filepath.IsAbs(source) {
		return "", false
	}
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(source))
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(relative), true
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
	data.Sort = r.URL.Query().Get("sort")
	data.Dir = r.URL.Query().Get("dir")
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
		haystack := strings.ToLower(row.Title + " " + row.Key + " " + row.Image + " " + row.Answer + " " + row.Drivers + " " + row.SourcePath + " " + row.StorageName + " " + row.Author + " " + row.AnswerAuthor + " " + row.ImageAuthor + " " + row.AnswerSHA + " " + row.DriverSHA)
		if data.Query != "" && !strings.Contains(haystack, strings.ToLower(data.Query)) && !searchMatchSHA(data.Query, row.AnswerSHA) && !searchMatchSHA(data.Query, row.DriverSHA) {
			continue
		}
		row.Used, row.LastUsed = use[row.Key].Count, use[row.Key].Last
		data.InstallCounts["all"]++
		data.InstallCounts[row.Group()]++
		if row.StorageWarning != "" && row.Group() != "attention" {
			data.InstallCounts["attention"]++
		}
		if data.Tab == "" || data.Tab == row.Group() || (data.Tab == "attention" && row.StorageWarning != "") {
			data.Installations = append(data.Installations, row)
		}
	}
	if data.Sort != "" {
		sortInstallations(data.Installations, data.Sort, data.Dir)
	}
	data.Changes = diffMenuDraft(active, draft)
	render(w, "installations", data)
}

func sortInstallations(rows []installationRow, sortBy, dir string) {
	desc := dir == "desc"
	switch sortBy {
	case "title":
		sort.SliceStable(rows, func(i, j int) bool {
			if c := compareStrings(rows[i].Title, rows[j].Title, desc); c != 0 {
				return c < 0
			}
			return rows[i].ID < rows[j].ID
		})
	case "folder":
		sort.SliceStable(rows, func(i, j int) bool {
			if c := compareStrings(rows[i].Folder, rows[j].Folder, desc); c != 0 {
				return c < 0
			}
			return compareStrings(rows[i].Title, rows[j].Title, false) < 0
		})
	case "status":
		sort.SliceStable(rows, func(i, j int) bool {
			if c := compareStrings(rows[i].Status, rows[j].Status, desc); c != 0 {
				return c < 0
			}
			return compareStrings(rows[i].Title, rows[j].Title, false) < 0
		})
	case "used":
		sort.SliceStable(rows, func(i, j int) bool {
			if c := compareInts(rows[i].Used, rows[j].Used, desc); c != 0 {
				return c < 0
			}
			return compareStrings(rows[i].Title, rows[j].Title, false) < 0
		})
	}
}

// installationAdd puts an installation at the end of the menu draft.
func (s *Server) installationAdd(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("key")
	if id, err := strconv.ParseInt(strings.TrimPrefix(raw, "install_"), 10, 64); err == nil && id > 0 {
		s.installationAddByID(w, r, id)
		return
	}
	http.Error(w, "Неверная загрузка", http.StatusBadRequest)
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
		http.Error(w, "Загрузка не готова: "+err.Error(), http.StatusConflict)
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
		draft.Items = insertDraftItem(draft.Items, active.Items, MenuDraftItem{InstallationID: id, Title: installation.Title, Folder: installation.Folder, Components: installationComponents(installation), InheritsTitle: true})
		if err := s.Store.SaveMenuDraft(r.Context(), draft); err != nil {
			http.Error(w, "Не удалось сохранить черновик меню", http.StatusServiceUnavailable)
			return
		}
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "menu.draft.add", "ok", fmt.Sprintf("install_%d", id))
	target := "/installations?message=Добавлено+в+черновик+меню"
	if edit := fmt.Sprintf("/installations/%d/edit", id); r.FormValue("return") == edit {
		target = edit + "?message=" + url.QueryEscape("Добавлено в черновик меню")
	} else if strings.HasPrefix(r.URL.Path, "/menu/") {
		msg := "Пункт возвращён в меню"
		if installation.Title != "" {
			msg = fmt.Sprintf("Пункт «%s» возвращён в меню", installation.Title)
		}
		target = "/menu?message=" + url.QueryEscape(msg)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// insertDraftItem restores the published position and menu-only placement
// overrides, while retaining edits to the saved entry and its components.
func insertDraftItem(items, published []MenuDraftItem, item MenuDraftItem) []MenuDraftItem {
	at := -1
	for i, old := range published {
		if menuDraftItemKey(old) != menuDraftItemKey(item) {
			continue
		}
		// A menu-only rename/folder override survives undoing removal unless
		// the saved entry itself was edited since that menu was published.
		if old.Components == nil {
			item.Title, item.Folder, item.Components, item.InheritsTitle = old.Title, old.Folder, nil, false
		} else {
			if old.Title != old.Components.Title {
				item.Title, item.InheritsTitle = old.Title, false
			} else if item.Components == nil || item.Components.Title == old.Components.Title {
				item.Title = old.Title
			}
			if item.Components == nil || item.Components.Folder == old.Components.Folder {
				item.Folder = old.Folder
			}
		}
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
