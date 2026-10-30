package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"pxe-platform/internal/agent"
)

type menuDraftRow struct {
	Item      MenuDraftItem
	Title     string
	Folder    string // the folder the item shows in
	Kind      string
	Available bool
	Error     string
}

func (row menuDraftRow) Key() string { return menuDraftItemKey(row.Item) }

func menuItemKind(key string) string {
	switch {
	case strings.HasPrefix(key, "winanswer_"):
		return "Образ и файл ответов"
	case strings.HasPrefix(key, "windriver_"):
		return "Образ с драйверами"
	default:
		return "Образ"
	}
}

var menuScreenLabel = regexp.MustCompile(`^(menu_root|folder_[0-9]+)$`)

func (s *Server) menuPage(w http.ResponseWriter, r *http.Request) {
	data := pageData{Info: requestInfo(r), Message: r.URL.Query().Get("message"), Error: pageError(r)}
	versions, err := s.Store.MenuVersions(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать версии меню", 500)
		return
	}
	profiles, answers, configs, err := s.menuSources(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	sources, err := s.installationMenuSources(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 503)
		return
	}
	for _, version := range versions {
		data.Versions = append(data.Versions, menuVersionRow{MenuVersion: version, Restorable: menuVersionRestorable(version, sources, profiles, answers, configs)})
		if version.Active {
			data.ActiveVersion = version.ID
		}
	}
	draft, active, err := s.currentDraft(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать черновик меню", 500)
		return
	}
	data.Draft = draft
	data.DraftRows = menuDraftRows(draft, profiles, answers, configs, sources)
	data.Changes = diffMenuDraft(active, draft)
	// The draft bar on this page follows the same read as the page itself.
	data.Info.Draft = summarizeChanges(data.Changes)
	data.Info.Draft.Active = data.ActiveVersion
	key, screen := r.URL.Query().Get("item"), r.URL.Query().Get("screen")
	if !menuItemKey.MatchString(key) && key != "ubuntu" && key != "diagnostics" && key != "local" {
		key = ""
	}
	if !menuScreenLabel.MatchString(screen) {
		screen = ""
	}
	data.Preview = buildMenuPreview(draft, data.Changes, data.DraftRows, installationRows(profiles, answers, configs, draft, active, sources), profiles, answers, configs, key, screen, sources)
	render(w, "menu", data)
}

func menuDraftRows(draft MenuDraft, profiles []WindowsProfile, answers []WindowsAnswerVariant, configs []DriverConfig, sources ...InstallationMenuSources) []menuDraftRow {
	var rows []menuDraftRow
	for _, item := range draft.Items {
		row := menuDraftRow{Item: item, Title: item.Title, Kind: menuItemKind(item.Key), Available: true, Folder: item.Folder}
		entry, folder, err := resolveDraftItem(item, "", sources, profiles, answers, configs)
		if item.InstallationID > 0 {
			switch {
			case entry.kind == BootProfileUbuntuBuiltin:
				row.Kind = "Ubuntu"
			case entry.driverConfigID > 0:
				row.Kind = "Образ с драйверами"
			case entry.answerID > 0:
				row.Kind = "Образ и файл ответов"
			}
		}
		if item.Folder == defaultMenuFolder {
			// The item follows its object's folder.
			row.Folder = folder
		}
		if err != nil {
			row.Available, row.Error = false, err.Error()
		} else if row.Title == "" {
			row.Title = entry.title
		}
		if row.Title == "" {
			row.Title = menuDraftItemKey(item)
		}
		rows = append(rows, row)
	}
	return rows
}

// installationMenuSources reads shared identities for rendering and restoration.
func (s *Server) installationMenuSources(ctx context.Context) (InstallationMenuSources, error) {
	store, ok := s.Store.(interface {
		Installations(context.Context) ([]Installation, error)
		BootProfiles(context.Context) ([]BootProfile, error)
	})
	if !ok {
		// Temporary legacy caller bridge.
		return InstallationMenuSources{}, nil
	}
	items, err := store.Installations(ctx)
	if err != nil {
		return InstallationMenuSources{}, err
	}
	profiles, err := store.BootProfiles(ctx)
	if err != nil {
		return InstallationMenuSources{}, err
	}
	return InstallationMenuSources{Installations: items, BootProfiles: profiles}, nil
}

// menuSources reads the objects menu items can point to.
func (s *Server) menuSources(ctx context.Context) ([]WindowsProfile, []WindowsAnswerVariant, []DriverConfig, error) {
	profiles, err := s.Store.WindowsProfiles(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("Не удалось прочитать профили Windows ISO")
	}
	answers, err := s.Store.WindowsAnswers(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("Не удалось прочитать сценарии Windows")
	}
	configs, err := s.Store.DriverConfigs(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("Не удалось прочитать конфигурации драйверов")
	}
	return profiles, answers, configs, nil
}

func menuSnapshotRestorable(script string, profiles []WindowsProfile, answers []WindowsAnswerVariant, configs []DriverConfig) bool {
	profileIDs, answerIDs, configIDs := map[int64]bool{}, map[int64]bool{}, map[int64]bool{}
	for _, item := range profiles {
		profileIDs[item.ID] = true
	}
	for _, item := range answers {
		answerIDs[item.ID] = true
	}
	for _, item := range configs {
		configIDs[item.ID] = true
	}
	for _, line := range strings.Split(script, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "item" {
			continue
		}
		for _, choice := range []struct {
			prefix  string
			present map[int64]bool
		}{
			{"winiso_", profileIDs}, {"winanswer_", answerIDs}, {"windriver_", configIDs},
		} {
			if !strings.HasPrefix(fields[1], choice.prefix) {
				continue
			}
			id, err := strconv.ParseInt(strings.TrimPrefix(fields[1], choice.prefix), 10, 64)
			if err != nil || !choice.present[id] {
				return false
			}
		}
	}
	return true
}

func menuVersionRestorable(version MenuVersion, sources InstallationMenuSources, profiles []WindowsProfile, answers []WindowsAnswerVariant, configs []DriverConfig) bool {
	if version.Items == nil {
		return menuSnapshotRestorable(version.Script, profiles, answers, configs)
	}
	_, err := BuildMenuFromDraft(draftFromVersion(version), previewBaseURL, profiles, answers, configs, sources)
	return err == nil
}

// verifyMenuVersion resolves structural references before checking the snapshot's
// immutable Windows component hashes and the prepared files behind them.
func (s *Server) verifyMenuVersion(ctx context.Context, version MenuVersion) error {
	if version.Items == nil {
		return s.verifyMenuWindowsProfiles(ctx, version.Script)
	}
	sources, err := s.installationMenuSources(ctx)
	if err != nil {
		return err
	}
	profiles, answers, configs, err := s.menuSources(ctx)
	if err != nil {
		return err
	}
	draft := draftFromVersion(version)
	if _, err := BuildMenuFromDraft(draft, previewBaseURL, profiles, answers, configs, sources); err != nil {
		return err
	}
	entries := make([]menuProfile, 0, len(draft.Items))
	ubuntuChecked := false
	for _, item := range draft.Items {
		entry, _, err := resolveDraftItem(item, "", []InstallationMenuSources{sources}, profiles, answers, configs)
		if err != nil {
			return err
		}
		if entry.kind == BootProfileUbuntuBuiltin && !ubuntuChecked {
			if err := s.checkUbuntuReadiness(ctx); err != nil {
				return err
			}
			ubuntuChecked = true
		}
		entries = append(entries, entry)
	}
	return s.verifyMenuWindowsProfiles(ctx, version.Script, entries...)
}

func (s *Server) menuPublish(w http.ResponseWriter, r *http.Request) {
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	profiles, answers, configs, err := s.menuSources(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 503)
		return
	}
	draft, _, err := s.currentDraft(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать черновик меню", 503)
		return
	}
	sources, err := s.installationMenuSources(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 503)
		return
	}
	script, err := BuildMenuFromDraft(draft, s.BootBaseURL, profiles, answers, configs, sources)
	if err != nil {
		http.Error(w, "Меню не собрано: "+err.Error(), 400)
		return
	}
	published := MenuVersion{Title: productMenuTitle, Label: draft.Label, Message: draft.Message, Script: string(script), NoDiagnostics: draft.NoDiagnostics, LocalTimeout: draft.LocalTimeout, NoLocalTimer: draft.NoLocalTimer, Look: menuLook, Items: make([]MenuVersionItem, 0, len(draft.Items))}
	for position, item := range draft.Items {
		if item.InstallationID == 0 {
			// Development bridge until form/list conversion.
			published.Items = nil
			break
		}
		entry, folder, _ := resolveDraftItem(item, "", []InstallationMenuSources{sources}, profiles, answers, configs)
		if item.Title != "" {
			entry.title = item.Title
		}
		if item.Folder != defaultMenuFolder {
			folder = item.Folder
		}
		published.Items = append(published.Items, MenuVersionItem{InstallationID: item.InstallationID, Position: position, Title: entry.title, Folder: folder})
	}
	if s.publishMenu(w, r, published, "menu.publish") {
		// Store the resolved published placements, so
		// object titles and folders it relied on no longer look like changes.
		s.saveDraftAfterPublish(r, published)
	}
}

func (s *Server) menuRollback(w http.ResponseWriter, r *http.Request) {
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Неверная версия", 400)
		return
	}
	m, err := s.Store.MenuByID(r.Context(), id)
	if err != nil {
		http.Error(w, "Версия не найдена", 404)
		return
	}
	if len(m.Script) == 0 || len(m.Script) > 256<<10 {
		http.Error(w, "Недопустимый снимок меню", 400)
		return
	}
	if s.publishMenu(w, r, m, "menu.rollback") {
		s.saveDraftAfterPublish(r, m)
	}
}

// menuDiscard throws away the unpublished draft: the draft becomes the
// active version again. Machines are not affected. The form must confirm.
func (s *Server) menuDiscard(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("confirm") != "yes" {
		http.Error(w, "Подтвердите отмену изменений", http.StatusBadRequest)
		return
	}
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	_, active, err := s.currentDraft(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать черновик меню", http.StatusServiceUnavailable)
		return
	}
	if err := s.Store.SaveMenuDraft(r.Context(), active); err != nil {
		http.Error(w, "Не удалось сохранить черновик меню", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "menu.draft.discard", "ok", "")
	http.Redirect(w, r, "/menu?message="+strings.ReplaceAll("Изменения черновика отменены", " ", "+"), http.StatusSeeOther)
}

func (s *Server) saveDraftAfterPublish(r *http.Request, m MenuVersion) {
	if err := s.Store.SaveMenuDraft(r.Context(), draftFromVersion(m)); err != nil {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "menu.draft.save", "error", err.Error())
	}
}

// publishMenu sends m to Boot and records it; it reports whether it succeeded.
func (s *Server) publishMenu(w http.ResponseWriter, r *http.Request, m MenuVersion, action string) bool {
	s.menuMu.Lock()
	defer s.menuMu.Unlock()
	if err := s.verifyMenuVersion(r.Context(), m); err != nil {
		http.Error(w, "Установка недоступна: "+err.Error(), http.StatusConflict)
		return false
	}
	info := requestInfo(r)
	if s.Boot == nil {
		http.Error(w, "Boot недоступен", 503)
		return false
	}
	previous, err := s.Boot.CurrentMenu(r.Context())
	if err != nil {
		http.Error(w, "Не удалось получить текущее меню", 503)
		return false
	}
	sum := sha256.Sum256([]byte(m.Script))
	m.SHA256 = hex.EncodeToString(sum[:])
	hash, err := s.Boot.Publish(r.Context(), []byte(m.Script))
	if err != nil {
		state, checkErr := s.Boot.Status(r.Context())
		if checkErr != nil || state.SHA256 != m.SHA256 {
			_ = s.Store.Audit(r.Context(), info.User.ID, action, "error", "boot publish")
			http.Error(w, "Публикация не подтверждена", 503)
			return false
		}
		hash = state.SHA256
	}
	if hash != m.SHA256 {
		http.Error(w, "Контрольная сумма Boot не совпала", 502)
		return false
	}
	if err := s.Store.SaveActiveMenu(r.Context(), info.User.ID, m); err != nil {
		_, rollbackErr := s.Boot.Publish(r.Context(), previous)
		_ = s.Store.Audit(r.Context(), info.User.ID, action, "error", "database write")
		if rollbackErr != nil {
			http.Error(w, "Не удалось сохранить версию и вернуть старое меню: "+fmt.Sprint(rollbackErr), 500)
			return false
		}
		http.Error(w, "Не удалось сохранить версию; старое меню восстановлено", 500)
		return false
	}
	_ = s.Store.Audit(r.Context(), info.User.ID, action, "ok", "sha256="+m.SHA256)
	http.Redirect(w, r, "/menu?message=Меню+опубликовано", 303)
	return true
}

func (s *Server) verifyMenuWindowsProfiles(ctx context.Context, script string, entries ...menuProfile) error {
	selected := menuWindowsProfileIDs(script)
	answerRefs := menuWindowsAnswerRefs(script)
	driverRefs := menuDriverConfigRefs(script)
	answerItems := map[int64]bool{}
	driverItems := map[int64]bool{}
	for _, entry := range entries {
		if entry.windowsISOID > 0 {
			selected[entry.windowsISOID] = true
		}
		if entry.answerID > 0 {
			answerItems[entry.answerID] = true
		}
		if entry.driverConfigID > 0 {
			driverItems[entry.driverConfigID] = true
		}
	}
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(line, "item windriver_") {
			fields := strings.Fields(line)
			if len(fields) < 3 {
				return fmt.Errorf("пункт конфигурации драйверов повреждён")
			}
			id, err := strconv.ParseInt(strings.TrimPrefix(fields[1], "windriver_"), 10, 64)
			if err != nil || id <= 0 || driverItems[id] {
				return fmt.Errorf("пункт конфигурации драйверов повреждён")
			}
			driverItems[id] = true
		}
		if !strings.HasPrefix(line, "item winanswer_") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return fmt.Errorf("пункт сценария повреждён")
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(fields[1], "winanswer_"), 10, 64)
		if err != nil || id <= 0 || answerItems[id] {
			return fmt.Errorf("пункт сценария повреждён")
		}
		answerItems[id] = true
	}
	if len(answerItems) != len(answerRefs) {
		return fmt.Errorf("ссылки на сценарии повреждены")
	}
	for id := range answerItems {
		if _, ok := answerRefs[id]; !ok {
			return fmt.Errorf("ссылки на сценарии повреждены")
		}
	}
	if len(driverItems) != len(driverRefs) {
		return fmt.Errorf("ссылки на конфигурации драйверов повреждены")
	}
	for id := range driverItems {
		if _, ok := driverRefs[id]; !ok {
			return fmt.Errorf("ссылки на конфигурации драйверов повреждены")
		}
	}
	for _, ref := range answerRefs {
		selected[ref.ProfileID] = true
	}
	for _, ref := range driverRefs {
		selected[ref.ProfileID] = true
	}
	if len(selected) == 0 {
		return nil
	}
	if s.Images == nil || s.WindowsISO == nil {
		return fmt.Errorf("службы образов недоступны")
	}
	profiles, err := s.Store.WindowsProfiles(ctx)
	if err != nil {
		return err
	}
	if len(driverRefs) > 0 {
		if s.DriverConfigs == nil || s.DriverPackages == nil {
			return fmt.Errorf("служба драйверов недоступна")
		}
		configs, err := s.Store.DriverConfigs(ctx)
		if err != nil {
			return err
		}
		answers, err := s.Store.WindowsAnswers(ctx)
		if err != nil {
			return err
		}
		answersByID := make(map[int64]WindowsAnswerVariant, len(answers))
		for _, answer := range answers {
			answersByID[answer.ID] = answer
		}
		for _, config := range configs {
			ref, ok := driverRefs[config.ID]
			if !ok {
				continue
			}
			if config.State != "ready" || config.ProfileID != ref.ProfileID || config.AnswerID != ref.AnswerID || config.Build.WIMKey != ref.WIMKey || config.Build.WIMSHA256 != ref.WIMSHA256 || config.Build.ScriptSHA256 != ref.ScriptSHA256 || len(config.PackageINFs) == 0 {
				return fmt.Errorf("конфигурация драйверов %d изменилась или не подготовлена", config.ID)
			}
			if config.AnswerID > 0 {
				answer, ok := answersByID[config.AnswerID]
				if !ok || answer.State != "ready" || answer.ProfileID != config.ProfileID || answer.SHA256 != ref.AnswerSHA256 || answer.Size <= 0 || s.WindowsAnswers == nil {
					return fmt.Errorf("сценарий конфигурации драйверов %d недоступен", config.ID)
				}
				if err := s.WindowsAnswers.CheckWindowsAnswer(ctx, agent.WindowsAnswerCheck{ID: answer.ID, ProfileID: answer.ProfileID, SHA256: answer.SHA256}); err != nil {
					return fmt.Errorf("файлы сценария %d недоступны", answer.ID)
				}
			}
			for _, inf := range config.PackageINFs {
				if err := s.DriverPackages.CheckDriverPackage(ctx, inf.PackageSHA256); err != nil {
					return fmt.Errorf("пакет драйверов %s недоступен", inf.PackageSHA256)
				}
			}
			if err := s.DriverConfigs.CheckDriverConfig(ctx, agent.DriverBuildCheck{Request: agent.DriverBuildRequest{ConfigID: config.ID, ProfileID: config.ProfileID, AnswerID: config.AnswerID, PackageINFs: config.PackageINFs}, Result: config.Build}); err != nil {
				return fmt.Errorf("файлы конфигурации драйверов %d недоступны", config.ID)
			}
			delete(driverRefs, config.ID)
		}
		if len(driverRefs) != 0 {
			return fmt.Errorf("конфигурация драйверов отсутствует")
		}
	}
	if len(answerRefs) > 0 {
		if s.WindowsAnswers == nil {
			return fmt.Errorf("служба сценариев недоступна")
		}
		answers, err := s.Store.WindowsAnswers(ctx)
		if err != nil {
			return err
		}
		for _, answer := range answers {
			ref, ok := answerRefs[answer.ID]
			if !ok {
				continue
			}
			if answer.State != "ready" || answer.ProfileID != ref.ProfileID || answer.SHA256 != ref.SHA256 || answer.Size <= 0 {
				return fmt.Errorf("сценарий %d изменился или не подготовлен", answer.ID)
			}
			if err := s.WindowsAnswers.CheckWindowsAnswer(ctx, agent.WindowsAnswerCheck{ID: answer.ID, ProfileID: answer.ProfileID, SHA256: answer.SHA256}); err != nil {
				return fmt.Errorf("файлы сценария %d недоступны", answer.ID)
			}
			delete(answerRefs, answer.ID)
		}
		if len(answerRefs) != 0 {
			return fmt.Errorf("сценарий отсутствует")
		}
	}
	catalog, err := s.Images.Catalog(ctx)
	if err != nil {
		return err
	}
	for _, p := range profiles {
		if !selected[p.ID] {
			continue
		}
		if p.State != "ready" || (p.InstallFile != "install.wim" && p.InstallFile != "install.esd") {
			return fmt.Errorf("профиль %d не подготовлен", p.ID)
		}
		if problem := windowsProfileSourceProblem(catalog, p); problem != nil {
			return problem
		}
		if err := s.WindowsISO.CheckWindowsISO(ctx, agent.WindowsISORequest{ID: p.ID, StorageRoot: p.StorageRoot, RelativePath: p.Path, Size: p.Size, Modified: p.Modified}); err != nil {
			return fmt.Errorf("файлы профиля %d недоступны", p.ID)
		}
		delete(selected, p.ID)
	}
	if len(selected) != 0 {
		return fmt.Errorf("профиль отсутствует")
	}
	return nil
}
