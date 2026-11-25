package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"omb/internal/agent"
	"omb/internal/autoinstall"
	"omb/internal/boot"
)

type menuDraftRow struct {
	Item              MenuDraftItem
	Title             string
	Folder            string // the folder the item shows in
	Kind              string
	Available         bool
	Error             string
	Maintenance       bool
	MaintenanceReason string
}

func (row menuDraftRow) Key() string { return menuDraftItemKey(row.Item) }

var menuScreenLabel = regexp.MustCompile(`^(menu_root|folder_[0-9]+)$`)

func (s *Server) menuPage(w http.ResponseWriter, r *http.Request) {
	data := pageData{Info: requestInfo(r), Message: r.URL.Query().Get("message"), Error: pageError(r)}
	versions, err := s.Store.MenuVersions(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать версии меню", 500)
		return
	}
	data.VersionsCount = len(versions)
	for _, version := range versions {
		if version.Active {
			data.ActiveVersion = version.ID
			break
		}
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
	draft, active, err := s.currentDraftFromVersions(r.Context(), versions)
	if err != nil {
		http.Error(w, "Не удалось прочитать черновик меню", 500)
		return
	}
	data.Draft = draft
	data.DraftRows = menuDraftRows(draft, profiles, answers, configs, sources)
	data.MenuFolders = collectMenuFolders(sources.Installations, data.DraftRows)
	installs := installationRows(profiles, answers, configs, draft, active, sources)
	s.applyMenuStorageStatus(r.Context(), data.DraftRows, installs)
	data.Changes = diffMenuDraft(active, draft)
	// The draft bar on this page follows the same read as the page itself.
	data.Info.Draft = summarizeChanges(data.Changes)
	data.Info.Draft.Active = data.ActiveVersion
	key, screen := r.URL.Query().Get("item"), r.URL.Query().Get("screen")
	if !menuItemKey.MatchString(key) && key != "diagnostics" && key != "local" && key != "autoboot" {
		key = ""
	}
	if !menuScreenLabel.MatchString(screen) {
		screen = ""
	}
	data.Preview = buildMenuPreview(draft, data.Changes, data.DraftRows, installs, profiles, answers, configs, key, screen, sources)
	render(w, "menu", data)
}

func (s *Server) applyMenuStorageStatus(ctx context.Context, rows []menuDraftRow, installs []installationRow) {
	byID := map[int64]installationRow{}
	needsCatalog := false
	for _, inst := range installs {
		if inst.ID > 0 {
			byID[inst.ID] = inst
			if (isISOBootKind(inst.Kind) || inst.Kind == BootProfileUbuntuISO) && inst.StorageID > 0 {
				needsCatalog = true
			}
		}
	}
	maintLookup := s.loadMaintenanceLookup(ctx)
	if len(maintLookup.locks) > 0 {
		for i := range rows {
			if rows[i].Item.InstallationID <= 0 {
				continue
			}
			inst, ok := byID[rows[i].Item.InstallationID]
			if !ok || inst.StorageID <= 0 {
				continue
			}
			if lock, _, locked := maintLookup.ImageLock(inst.StorageID, inst.SourcePath); locked {
				rows[i].Maintenance = true
				rows[i].MaintenanceReason = lock.Reason
			}
		}
	}
	if s.Images == nil || !needsCatalog {
		return
	}
	catalog, err := s.Images.Catalog(ctx)
	if err != nil {
		return
	}
	storageStates := map[int64]string{}
	for _, storage := range catalog.Storages {
		storageStates[storage.ID] = storage.State
	}
	for i := range rows {
		if !rows[i].Available || rows[i].Item.InstallationID <= 0 {
			continue
		}
		inst, ok := byID[rows[i].Item.InstallationID]
		if !ok || !(isISOBootKind(inst.Kind) || inst.Kind == BootProfileUbuntuISO) || inst.StorageID <= 0 {
			continue
		}
		switch state, found := storageStates[inst.StorageID]; {
		case !found:
			rows[i].Available, rows[i].Error = false, "Хранилище ISO не найдено"
		case state == "unavailable":
			rows[i].Available, rows[i].Error = false, "Хранилище ISO недоступно"
		case state == "scanning":
			rows[i].Available, rows[i].Error = false, "Проверяется хранилище ISO"
		case state == "unknown":
			rows[i].Available, rows[i].Error = false, "Хранилище ISO не проверено"
		}
	}
}

func (s *Server) menuVersions(w http.ResponseWriter, r *http.Request) {
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
	data := pageData{Info: requestInfo(r)}
	for _, version := range versions {
		row := menuVersionRow{MenuVersion: version, Restorable: menuVersionRestorable(version, sources, profiles, answers, configs)}
		if legacyUbuntuMenuWithoutSource(version, sources) {
			row.UnrestorableReason = "Источник Ubuntu не подтверждён"
		}
		data.Versions = append(data.Versions, row)
		if version.Active {
			data.ActiveVersion = version.ID
		}
	}
	render(w, "menu_versions_table", data)
}

func menuDraftRows(draft MenuDraft, profiles []WindowsProfile, answers []WindowsAnswerVariant, configs []DriverConfig, sources InstallationMenuSources) []menuDraftRow {
	var rows []menuDraftRow
	for _, item := range draft.Items {
		row := menuDraftRow{Item: item, Title: item.Title, Kind: "Образ", Available: true, Folder: item.Folder}
		entry, folder, err := resolveDraftItem(item, "", sources, profiles, answers, configs)
		if item.InstallationID > 0 {
			switch {
			case entry.kind == BootProfileUbuntuBuiltin || entry.kind == BootProfileUbuntuISO:
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
	items, err := s.Store.Installations(ctx)
	if err != nil {
		return InstallationMenuSources{}, err
	}
	profiles, err := s.Store.BootProfiles(ctx)
	if err != nil {
		return InstallationMenuSources{}, err
	}
	files, err := s.Store.UbuntuAutoinstalls(ctx)
	if err != nil {
		return InstallationMenuSources{}, err
	}
	return InstallationMenuSources{Installations: items, BootProfiles: profiles, Autoinstalls: files}, nil
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

func legacyUbuntuMenuWithoutSource(version MenuVersion, sources InstallationMenuSources) bool {
	if version.ID == 0 || (!strings.Contains(version.Script, "/images/ubuntu-desktop.iso") && !strings.Contains(version.Script, "/assets/ubuntu/vmlinuz") && !strings.Contains(version.Script, "/assets/ubuntu/initrd") && !strings.Contains(version.Script, "/assets/ubuntu/bootx64.efi")) {
		return false
	}
	for _, profile := range sources.BootProfiles {
		if version.LegacyUbuntuSourceID > 0 && profile.Kind == BootProfileUbuntuBuiltin && profile.UbuntuSourceID == version.LegacyUbuntuSourceID && profile.UbuntuState == "ready" && ubuntuSourceDigest.MatchString(profile.UbuntuSHA256) {
			return false
		}
	}
	return true
}

func menuVersionRestorable(version MenuVersion, sources InstallationMenuSources, profiles []WindowsProfile, answers []WindowsAnswerVariant, configs []DriverConfig) bool {
	if version.Items == nil {
		return false
	}
	if legacyUbuntuMenuWithoutSource(version, sources) {
		return false
	}
	_, err := BuildMenuFromDraft(draftFromVersion(version), previewBaseURL, profiles, answers, configs, sources)
	return err == nil
}

// verifyMenuVersion resolves structural references before checking the snapshot's
// immutable components and the prepared files behind them. All preflight work
// finishes before the active Boot script or saved menu/draft can change.
func (s *Server) verifyMenuVersion(ctx context.Context, version MenuVersion) error {
	if version.Items == nil {
		return fmt.Errorf("menu version has no structural items")
	}
	sources, err := s.installationMenuSources(ctx)
	if err != nil {
		return err
	}
	if legacyUbuntuMenuWithoutSource(version, sources) {
		return fmt.Errorf("legacy Ubuntu menu source is not verified")
	}
	if err := s.verifyLegacyUbuntuAlias(ctx, version, sources); err != nil {
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
	checkedSources := map[string]bool{}
	for _, item := range draft.Items {
		entry, _, err := resolveDraftItem(item, "", sources, profiles, answers, configs)
		if err != nil {
			return err
		}
		if entry.kind == BootProfileUbuntuBuiltin && !ubuntuChecked {
			if err := s.checkUbuntuReadiness(ctx); err != nil {
				return err
			}
			ubuntuChecked = true
		}
		if entry.kind == BootProfileUbuntuISO {
			key := fmt.Sprintf("%d/%s", entry.ubuntuSourceID, entry.ubuntuSourceSHA256)
			if !checkedSources[key] {
				if _, err := s.ubuntuSourceCatalogFor(ctx, entry.ubuntuSourceID, entry.ubuntuSourceSHA256); err != nil {
					return err
				}
				checkedSources[key] = true
			}
		}
		entries = append(entries, entry)
	}
	if err := s.verifyMenuWindowsProfiles(ctx, version.Script, entries...); err != nil {
		return err
	}
	if err := verifyUbuntuSourceReferences(version.Script, entries, version.LegacyUbuntuSourceID); err != nil {
		return err
	}
	if err := verifyMenuAutoinstallReferences(version.Script, entries); err != nil {
		return err
	}
	return s.stageMenuAutoinstalls(ctx, entries)
}

func verifyUbuntuSourceReferences(script string, entries []menuProfile, legacySourceID int64) error {
	for _, entry := range entries {
		if entry.kind != BootProfileUbuntuISO {
			continue
		}
		label := ":" + entry.id + "\n"
		endLabel := ":" + entry.id + "_failed\n"
		if strings.Count(script, label) != 1 || strings.Count(script, endLabel) != 1 {
			return fmt.Errorf("Ubuntu source snapshot labels are invalid")
		}
		start := strings.Index(script, label) + len(label)
		end := strings.Index(script[start:], endLabel)
		if end < 0 {
			return fmt.Errorf("Ubuntu source snapshot labels are invalid")
		}
		block := script[start : start+end]
		pathPart := fmt.Sprintf("%d/%s", entry.ubuntuSourceID, entry.ubuntuSourceSHA256)
		kernelPath := "/assets/ubuntu/" + pathPart + "/vmlinuz"
		isoPath := "/images/ubuntu/" + pathPart + ".iso"
		assetsPath := "/assets/ubuntu/" + pathPart
		if legacySourceID > 0 && legacySourceID == entry.ubuntuSourceID && strings.Contains(script, "/images/ubuntu-desktop.iso") {
			assetsPath = "/assets/ubuntu"
			kernelPath = assetsPath + "/vmlinuz"
			isoPath = "/images/ubuntu-desktop.iso"
		}
		var base string
		kernels := 0
		var initrds, shims []string
		for _, line := range strings.Split(block, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			if fields[0] == "initrd" {
				initrds = append(initrds, line)
				continue
			}
			if fields[0] == "shim" {
				shims = append(shims, line)
				continue
			}
			if fields[0] != "kernel" {
				continue
			}
			kernels++
			if len(fields) < 3 {
				return fmt.Errorf("Ubuntu source snapshot kernel is invalid")
			}
			u, err := url.Parse(fields[1])
			if err != nil || u.Path != kernelPath || u.RawQuery != "" || u.Fragment != "" {
				return fmt.Errorf("Ubuntu source snapshot kernel is invalid")
			}
			base, err = validatedBootURL(u.Scheme + "://" + u.Host)
			if err != nil || fields[1] != base+kernelPath {
				return fmt.Errorf("Ubuntu source snapshot kernel is invalid")
			}
			isoCount := 0
			for _, arg := range fields[2:] {
				if strings.HasPrefix(arg, "iso-url=") {
					isoCount++
					if arg != "iso-url="+base+isoPath {
						return fmt.Errorf("Ubuntu source snapshot ISO is invalid")
					}
				}
			}
			if isoCount != 1 {
				return fmt.Errorf("Ubuntu source snapshot ISO is invalid")
			}
		}
		validInitrd := len(initrds) == 1 && (initrds[0] == "initrd "+base+assetsPath+"/initrd || goto "+entry.id+"_failed" || initrds[0] == "initrd "+base+assetsPath+"/initrd")
		if kernels != 1 || !validInitrd || len(shims) != 1 || shims[0] != "shim "+base+assetsPath+"/bootx64.efi || goto "+entry.id+"_failed" {
			return fmt.Errorf("Ubuntu source snapshot assets are invalid")
		}
	}
	return nil
}

// Match saved kernel arguments to the current immutable library references.
// As with Windows component hashes, a changed digest must block restoration
// rather than stage a different seed while publishing the old snapshot URL.
func verifyMenuAutoinstallReferences(script string, entries []menuProfile) error {
	expected := map[string]bool{}
	for _, entry := range entries {
		if entry.autoinstallID > 0 {
			expected[ubuntuAutoinstallSeedPath(entry.id, entry.autoinstallSHA256)] = true
		}
	}
	for line := range strings.SplitSeq(script, "\n") {
		args := strings.Fields(line)
		if len(args) == 0 || args[0] != "kernel" {
			continue
		}
		for _, arg := range args[1:] {
			seed, ok := strings.CutPrefix(arg, "ds=nocloud-net;s=")
			if !ok {
				continue
			}
			u, err := url.Parse(seed)
			if err != nil {
				return fmt.Errorf("Ubuntu Autoinstall snapshot references are invalid")
			}
			base, err := validatedBootURL(u.Scheme + "://" + u.Host)
			if err != nil || seed != base+u.Path || !expected[u.Path] {
				return fmt.Errorf("Ubuntu Autoinstall snapshot references are invalid or changed")
			}
			delete(expected, u.Path)
		}
	}
	if len(expected) != 0 {
		return fmt.Errorf("Ubuntu Autoinstall snapshot references are missing")
	}
	return nil
}

// stageMenuAutoinstalls fetches bodies only for selected automated entries.
// Repeating this on restoration reuses (or repairs a missing) immutable pair;
// a corrupt existing pair is rejected by Boot rather than overwritten.
func (s *Server) stageMenuAutoinstalls(ctx context.Context, entries []menuProfile) error {
	for _, entry := range entries {
		if entry.autoinstallID == 0 {
			continue
		}
		if s.Boot == nil {
			return fmt.Errorf("Boot unavailable for Ubuntu Autoinstall")
		}
		file, err := s.Store.UbuntuAutoinstall(ctx, entry.autoinstallID)
		if err != nil {
			return fmt.Errorf("Ubuntu Autoinstall file %d is unavailable", entry.autoinstallID)
		}
		rendered, err := autoinstall.Render(file.Values)
		if err != nil || file.ID != entry.autoinstallID || file.State != "validated" || file.SHA256 != entry.autoinstallSHA256 || len(file.UserData) == 0 || len(file.UserData) > boot.MaxAutoinstallUserDataSize || !bytes.Equal(rendered, file.UserData) || fmt.Sprintf("%x", sha256.Sum256(file.UserData)) != entry.autoinstallSHA256 {
			return fmt.Errorf("Ubuntu Autoinstall file %d is invalid or changed", entry.autoinstallID)
		}
		if err := s.validateUbuntuSourceFor(ctx, file.Values, entry.ubuntuSourceID, entry.ubuntuSourceSHA256); err != nil {
			return err
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(entry.id, "install_"), 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("invalid Ubuntu Autoinstall installation identity")
		}
		meta := fmt.Sprintf("instance-id: omb-installation-%d-%s\n", id, file.SHA256)
		expected := boot.AssetInfo{UserDataSHA256: file.SHA256, MetaDataSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(meta)))}
		staged, err := s.Boot.StageAutoinstall(ctx, id, file.SHA256, file.UserData)
		if err != nil || staged != expected {
			return fmt.Errorf("Ubuntu Autoinstall assets for %s could not be staged", entry.id)
		}
		checked, err := s.Boot.CheckAutoinstall(ctx, id, file.SHA256)
		if err != nil || checked != expected {
			return fmt.Errorf("Ubuntu Autoinstall assets for %s could not be verified", entry.id)
		}
	}
	return nil
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
	published := MenuVersion{Title: productMenuTitle, Label: draft.Label, Message: draft.Message, Script: string(script), NoDiagnostics: draft.NoDiagnostics, LocalTimeout: draft.LocalTimeout, NoLocalTimer: draft.NoLocalTimer, DefaultInstallationID: draft.DefaultInstallationID, Look: menuLook, Items: make([]MenuVersionItem, 0, len(draft.Items))}
	for position, item := range draft.Items {
		entry, folder, _ := resolveDraftItem(item, "", sources, profiles, answers, configs)
		if item.Title != "" {
			entry.title = item.Title
		}
		if item.Folder != defaultMenuFolder {
			folder = item.Folder
		}
		published.Items = append(published.Items, MenuVersionItem{InstallationID: item.InstallationID, Position: position, Title: entry.title, Folder: folder, Components: item.Components})
	}
	if s.publishMenu(w, r, published, "menu.publish") {
		// Keep title inheritance in the draft while the version freezes its text.
		for i := range draft.Items {
			draft.Items[i].Title = published.Items[i].Title
			draft.Items[i].Folder = published.Items[i].Folder
		}
		if err := s.Store.SaveMenuDraft(r.Context(), draft); err != nil {
			_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "menu.draft.save", "error", err.Error())
		}
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
	if !s.publishMenuLocked(w, r, m, action) {
		return false
	}
	http.Redirect(w, r, "/menu?message=Меню+опубликовано", http.StatusSeeOther)
	return true
}

// publishMenuLocked publishes without a redirect so compound actions can finish
// under the same menu lock. The caller must hold menuMu.
func (s *Server) publishMenuLocked(w http.ResponseWriter, r *http.Request, m MenuVersion, action string) bool {
	if err := s.verifyMenuVersion(r.Context(), m); err != nil {
		http.Error(w, "Загрузка недоступна: "+err.Error(), http.StatusConflict)
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
	return true
}

func (s *Server) verifyMenuWindowsProfiles(ctx context.Context, script string, entries ...menuProfile) error {
	selected := menuWindowsProfileIDs(script)
	answerRefs := menuWindowsAnswerRefs(script)
	driverRefs := menuDriverConfigRefs(script)
	answerItems := map[[2]int64]bool{}
	driverItems := map[int64]bool{}
	for _, entry := range entries {
		if entry.windowsISOID > 0 {
			selected[entry.windowsISOID] = true
		}
		if entry.answerID > 0 {
			answerItems[[2]int64{entry.answerID, entry.windowsISOID}] = true
		}
		if entry.driverConfigID > 0 {
			driverItems[entry.driverConfigID] = true
		}
	}
	answerRefMap := make(map[[2]int64]menuWindowsAnswerRef, len(answerRefs))
	for _, ref := range answerRefs {
		answerRefMap[[2]int64{ref.ID, ref.ProfileID}] = ref
		selected[ref.ProfileID] = true
	}
	if len(answerItems) != len(answerRefMap) {
		return fmt.Errorf("ссылки на сценарии повреждены")
	}
	for pair := range answerItems {
		if _, ok := answerRefMap[pair]; !ok {
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
				if !ok || answer.State != "ready" || answer.SHA256 != ref.AnswerSHA256 || answer.Size <= 0 || s.WindowsAnswers == nil {
					return fmt.Errorf("сценарий конфигурации драйверов %d недоступен", config.ID)
				}
				if err := s.WindowsAnswers.CheckWindowsAnswer(ctx, agent.WindowsAnswerCheck{ID: answer.ID, SHA256: answer.SHA256}); err != nil {
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
		answersByID := make(map[int64]WindowsAnswerVariant, len(answers))
		for _, answer := range answers {
			answersByID[answer.ID] = answer
		}
		checkedAnswers := map[int64]bool{}
		for _, ref := range answerRefs {
			answer, ok := answersByID[ref.ID]
			if !ok {
				return fmt.Errorf("сценарий отсутствует")
			}
			if answer.State != "ready" || answer.SHA256 != ref.SHA256 || answer.Size <= 0 {
				return fmt.Errorf("сценарий %d изменился или не подготовлен", answer.ID)
			}
			if !checkedAnswers[answer.ID] {
				if err := s.WindowsAnswers.CheckWindowsAnswer(ctx, agent.WindowsAnswerCheck{ID: answer.ID, SHA256: answer.SHA256}); err != nil {
					return fmt.Errorf("файлы сценария %d недоступны", answer.ID)
				}
				checkedAnswers[answer.ID] = true
			}
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
		if !profileBootable(p) {
			return fmt.Errorf("профиль %d не подготовлен", p.ID)
		}
		if problem := windowsProfileSourceProblem(catalog, p); problem != nil {
			return problem
		}
		checked, err := s.WindowsISO.CheckWindowsISO(ctx, agent.WindowsISORequest{ID: p.ID, BootMode: p.BootMode, StorageRoot: p.StorageRoot, RelativePath: p.Path, Size: p.Size, Modified: p.Modified})
		if err != nil {
			return fmt.Errorf("файлы профиля %d недоступны", p.ID)
		}
		if !p.WIMInfo.Known && checked.WIMInfo.Known {
			_ = s.Store.SetWindowsProfileWIMInfo(ctx, p.ID, checked.WIMInfo)
		}
		delete(selected, p.ID)
	}
	if len(selected) != 0 {
		return fmt.Errorf("профиль отсутствует")
	}
	return nil
}
