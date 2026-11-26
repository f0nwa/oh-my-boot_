package control

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

type ubuntuAutoinstallDeleter interface {
	DeleteUbuntuAutoinstall(context.Context, int64) error
}

// safeSectionReturnURL validates return_to for a fixed section path, keeping
// only allowlisted query parameters and dropping any stale message param.
func safeSectionReturnURL(raw, basePath string, allowedParams ...string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, basePath) {
		return basePath
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.Path != basePath {
		return basePath
	}
	allow := make(map[string]bool, len(allowedParams))
	for _, k := range allowedParams {
		allow[k] = true
	}
	clean := url.Values{}
	for key, vals := range parsed.Query() {
		if !allow[key] {
			continue
		}
		for _, v := range vals {
			if strings.TrimSpace(v) != "" {
				clean.Add(key, v)
			}
		}
	}
	if encoded := clean.Encode(); encoded != "" {
		return basePath + "?" + encoded
	}
	return basePath
}

func (s *Server) deleteSingleUbuntuAutoinstall(r *http.Request, id int64, source InstallationMenuSources) (int, string) {
	deleter, ok := s.Store.(ubuntuAutoinstallDeleter)
	if !ok {
		return http.StatusServiceUnavailable, "Удаление скриптов Ubuntu недоступно"
	}
	active, err := s.activeMenuContains(r, installationComponent{autoinstallID: id})
	if err != nil {
		return http.StatusServiceUnavailable, "Не удалось проверить меню"
	}
	if active {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "ubuntu-autoinstall.delete", "rejected", fmt.Sprintf("id=%d published", id))
		return http.StatusConflict, "Сначала снимите пункт с публикации меню"
	}
	for _, item := range source.Installations {
		if (installationComponent{autoinstallID: id}).matches(item, source.BootProfiles) {
			_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "ubuntu-autoinstall.delete", "rejected", fmt.Sprintf("id=%d used by installation %d", id, item.ID))
			return http.StatusConflict, fmt.Sprintf("Объект используется загрузкой № %d", item.ID)
		}
	}
	err = deleter.DeleteUbuntuAutoinstall(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return http.StatusNotFound, "Скрипт не найден"
	}
	if err != nil {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "ubuntu-autoinstall.delete", "error", fmt.Sprintf("id=%d database", id))
		return http.StatusConflict, "Скрипт используется"
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "ubuntu-autoinstall.delete", "ok", fmt.Sprintf("id=%d", id))
	return 0, ""
}

func (s *Server) ubuntuAutoinstallDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := deletionID(w, r)
	if !ok {
		return
	}
	s.menuMu.Lock()
	defer s.menuMu.Unlock()
	source, err := s.installationMenuSources(r.Context())
	if err != nil {
		http.Error(w, "Не удалось проверить загрузки", http.StatusServiceUnavailable)
		return
	}
	if status, msg := s.deleteSingleUbuntuAutoinstall(r, id, source); status != 0 {
		http.Error(w, msg, status)
		return
	}
	target := safeSectionReturnURL(r.PostFormValue("return_to"), "/answers", "q", "sort", "dir")
	redirectWithNotice(w, r, target, "Скрипт Ubuntu удалён")
}

func (s *Server) answersDeleteBatch(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Некорректный запрос", http.StatusBadRequest)
		return
	}
	rawItems := r.PostForm["item"]
	if len(rawItems) == 0 {
		http.Error(w, "Не выбрано ни одного скрипта", http.StatusBadRequest)
		return
	}
	type answerItem struct {
		kind string
		id   int64
	}
	seen := make(map[string]bool, len(rawItems))
	items := make([]answerItem, 0, len(rawItems))
	for _, raw := range rawItems {
		kind, idStr, ok := strings.Cut(strings.TrimSpace(raw), ":")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if !ok || (kind != "windows" && kind != "ubuntu") || err != nil || id <= 0 {
			http.Error(w, "Некорректный идентификатор скрипта", http.StatusBadRequest)
			return
		}
		key := kind + ":" + strconv.FormatInt(id, 10)
		if seen[key] {
			continue
		}
		seen[key] = true
		items = append(items, answerItem{kind: kind, id: id})
	}

	s.menuMu.Lock()
	defer s.menuMu.Unlock()
	s.windowsMu.Lock()
	defer s.windowsMu.Unlock()

	source, err := s.installationMenuSources(r.Context())
	if err != nil {
		http.Error(w, "Не удалось проверить загрузки", http.StatusServiceUnavailable)
		return
	}
	configs, err := s.Store.DriverConfigs(r.Context())
	if err != nil {
		http.Error(w, "Не удалось проверить зависимости", http.StatusServiceUnavailable)
		return
	}

	var deleted, failed int
	var firstErr string
	for _, item := range items {
		if item.kind == "ubuntu" {
			status, msg := s.deleteSingleUbuntuAutoinstall(r, item.id, source)
			if status != 0 {
				failed++
				if firstErr == "" {
					firstErr = msg
				}
			} else {
				deleted++
			}
			continue
		}
		// Windows XML answer deletion
		if s.Cleanup == nil {
			failed++
			if firstErr == "" {
				firstErr = "Очистка файлов недоступна"
			}
			continue
		}
		answer, err := s.Store.WindowsAnswer(r.Context(), item.id)
		if err != nil {
			failed++
			if firstErr == "" {
				firstErr = "Скрипт не найден"
			}
			continue
		}
		active, err := s.activeMenuContains(r, installationComponent{answerID: item.id})
		if err != nil || active {
			failed++
			if firstErr == "" {
				firstErr = "Сначала снимите пункт с публикации меню"
			}
			continue
		}
		inUse := false
		for _, inst := range source.Installations {
			if (installationComponent{answerID: item.id}).matches(inst, source.BootProfiles) {
				inUse = true
				break
			}
		}
		if !inUse {
			for _, cfg := range configs {
				if cfg.AnswerID == item.id {
					inUse = true
					break
				}
			}
		}
		if inUse {
			failed++
			if firstErr == "" {
				firstErr = "Скрипт используется загрузками"
			}
			continue
		}
		if err := s.Store.DeleteWindowsAnswer(r.Context(), item.id); err != nil {
			failed++
			if firstErr == "" {
				firstErr = "Не удалось удалить скрипт"
			}
			continue
		}
		_ = s.Cleanup.RemoveWindowsAnswer(r.Context(), item.id)
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "windows-answer.delete", "ok", fmt.Sprintf("id=%d title=%s", item.id, answer.Title))
		deleted++
	}

	target := safeSectionReturnURL(r.PostFormValue("return_to"), "/answers", "q", "sort", "dir")
	if deleted == 0 && failed > 0 {
		http.Error(w, firstErr, http.StatusConflict)
		return
	}
	msg := fmt.Sprintf("Удалено скриптов: %d", deleted)
	if failed > 0 {
		msg = fmt.Sprintf("Удалено скриптов: %d (пропущено: %d — %s)", deleted, failed, firstErr)
	}
	redirectWithNotice(w, r, target, msg)
}

func (s *Server) deleteBootWIMBatch(w http.ResponseWriter, r *http.Request) {
	s.deleteWIMBatch(w, r, true)
}

func (s *Server) deleteInstallWIMBatch(w http.ResponseWriter, r *http.Request) {
	s.deleteWIMBatch(w, r, false)
}

func (s *Server) deleteWIMBatch(w http.ResponseWriter, r *http.Request, isBoot bool) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Некорректный запрос", http.StatusBadRequest)
		return
	}
	rawItems := r.PostForm["item"]
	if len(rawItems) == 0 {
		http.Error(w, "Не выбрано ни одного образа", http.StatusBadRequest)
		return
	}

	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	s.menuMu.Lock()
	defer s.menuMu.Unlock()
	s.windowsMu.Lock()
	defer s.windowsMu.Unlock()
	s.smbMu.Lock()
	defer s.smbMu.Unlock()
	s.nfsMu.Lock()
	defer s.nfsMu.Unlock()

	store, ok := s.windowsWIMStore()
	api := s.windowsWIMAPI()
	if !ok || api == nil {
		http.Error(w, "Удаление WIM-образов недоступно", http.StatusServiceUnavailable)
		return
	}
	deleteFile := r.PostFormValue("delete_file") == "1"
	maint := s.loadMaintenanceLookup(r.Context())
	var imgDelCtx *imageDeleteContext
	var deleted, failed int
	var firstErr string
	seen := make(map[string]bool, len(rawItems))

	for _, raw := range rawItems {
		raw = strings.TrimSpace(raw)
		if raw == "" || seen[raw] {
			continue
		}
		seen[raw] = true

		if strings.HasPrefix(raw, "file:") {
			rest := strings.TrimPrefix(raw, "file:")
			sidStr, imgPath, ok := strings.Cut(rest, ":")
			sid, err := strconv.ParseInt(sidStr, 10, 64)
			if !ok || err != nil || sid <= 0 || imgPath == "" {
				failed++
				if firstErr == "" {
					firstErr = "Некорректный путь образа"
				}
				continue
			}
			if imgDelCtx == nil {
				var status int
				var msg string
				imgDelCtx, status, msg = s.prepareImageDeleteContext(r)
				if status != 0 {
					http.Error(w, msg, status)
					return
				}
			}
			_, _, status, msg := s.deleteSingleImageItem(r, imgDelCtx, sid, imgPath)
			if status != 0 {
				failed++
				if firstErr == "" {
					firstErr = msg
				}
			} else {
				deleted++
			}
			continue
		}

		if !strings.HasPrefix(raw, "id:") {
			failed++
			if firstErr == "" {
				firstErr = "Некорректный идентификатор образа"
			}
			continue
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(raw, "id:"), 10, 64)
		if err != nil || id <= 0 {
			failed++
			if firstErr == "" {
				firstErr = "Некорректный идентификатор образа"
			}
			continue
		}

		if isBoot {
			rec, err := store.GetWindowsBootImage(r.Context(), id)
			if err != nil {
				failed++
				if firstErr == "" {
					firstErr = "Образ boot.wim не найден"
				}
				continue
			}
			if rec.SourceKind == WIMSourceKindISO {
				failed++
				if firstErr == "" {
					firstErr = "Этот boot.wim принадлежит образу ISO; удалите исходный ISO"
				}
				continue
			}
			if rec.StorageID > 0 && rec.Path != "" {
				if _, _, locked := maint.ImageLock(rec.StorageID, rec.Path); locked {
					failed++
					if firstErr == "" {
						firstErr = "Хранилище или образ в режиме обслуживания"
					}
					continue
				}
			}
			active, err := s.activeMenuContains(r, installationComponent{bootImageID: id})
			if err != nil || active {
				failed++
				if firstErr == "" {
					firstErr = "Сначала снимите пункт с публикации меню"
				}
				continue
			}
			src, err := s.installationMenuSources(r.Context())
			if err != nil {
				failed++
				if firstErr == "" {
					firstErr = "Не удалось проверить загрузки"
				}
				continue
			}
			used := false
			for _, inst := range src.Installations {
				if inst.BootImageID == id {
					used = true
					break
				}
			}
			if used {
				failed++
				if firstErr == "" {
					firstErr = "Образ используется загрузками"
				}
				continue
			}
			doDelFile := deleteFile && rec.StorageID > 0 && rec.Path != ""
			if doDelFile {
				if status, msg := s.prepareWIMSourceFileDelete(r, "boot-wim.delete", id, rec.StorageID, rec.Path); status != 0 {
					failed++
					if firstErr == "" {
						firstErr = msg
					}
					continue
				}
			}
			if err := api.RemoveBootImage(r.Context(), id); err != nil {
				failed++
				if firstErr == "" {
					firstErr = "Не удалось удалить подготовленные файлы boot.wim"
				}
				continue
			}
			if doDelFile {
				if err := s.Images.DeleteImage(r.Context(), rec.StorageID, rec.Path); err != nil {
					failed++
					if firstErr == "" {
						firstErr = "Не удалось удалить файл образа"
					}
					continue
				}
			}
			if err := store.DeleteWindowsBootImage(r.Context(), id); err != nil {
				failed++
				if firstErr == "" {
					firstErr = "Образ используется загрузками"
				}
				continue
			}
			_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "boot-wim.delete", "ok", fmt.Sprintf("id=%d delete_file=%t", id, doDelFile))
			deleted++
		} else {
			rec, err := store.GetWindowsInstallImage(r.Context(), id)
			if err != nil {
				failed++
				if firstErr == "" {
					firstErr = "Образ install.wim не найден"
				}
				continue
			}
			if rec.StorageID > 0 && rec.Path != "" {
				if _, _, locked := maint.ImageLock(rec.StorageID, rec.Path); locked {
					failed++
					if firstErr == "" {
						firstErr = "Хранилище или образ в режиме обслуживания"
					}
					continue
				}
			}
			active, err := s.activeMenuContains(r, installationComponent{installImageID: id})
			if err != nil || active {
				failed++
				if firstErr == "" {
					firstErr = "Сначала снимите пункт с публикации меню"
				}
				continue
			}
			src, err := s.installationMenuSources(r.Context())
			if err != nil {
				failed++
				if firstErr == "" {
					firstErr = "Не удалось проверить загрузки"
				}
				continue
			}
			used := false
			for _, inst := range src.Installations {
				if inst.InstallImageID == id {
					used = true
					break
				}
			}
			if used {
				failed++
				if firstErr == "" {
					firstErr = "Образ используется загрузками"
				}
				continue
			}
			doDelFile := deleteFile && rec.StorageID > 0 && rec.Path != ""
			if doDelFile {
				if status, msg := s.prepareWIMSourceFileDelete(r, "install-wim.delete", id, rec.StorageID, rec.Path); status != 0 {
					failed++
					if firstErr == "" {
						firstErr = msg
					}
					continue
				}
			}
			if err := api.RemoveInstallImage(r.Context(), id); err != nil {
				failed++
				if firstErr == "" {
					firstErr = "Не удалось удалить подготовленные файлы install.wim"
				}
				continue
			}
			if doDelFile {
				if err := s.Images.DeleteImage(r.Context(), rec.StorageID, rec.Path); err != nil {
					failed++
					if firstErr == "" {
						firstErr = "Не удалось удалить файл образа"
					}
					continue
				}
			}
			if err := store.DeleteWindowsInstallImage(r.Context(), id); err != nil {
				failed++
				if firstErr == "" {
					firstErr = "Образ используется загрузками"
				}
				continue
			}
			_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "install-wim.delete", "ok", fmt.Sprintf("id=%d delete_file=%t", id, doDelFile))
			deleted++
		}
	}

	target := safeImagesReturnURL(r.PostFormValue("return_to"))
	if deleted == 0 && failed > 0 {
		http.Error(w, firstErr, http.StatusConflict)
		return
	}
	msg := fmt.Sprintf("Удалено образов: %d", deleted)
	if failed > 0 {
		msg = fmt.Sprintf("Удалено образов: %d (пропущено: %d — %s)", deleted, failed, firstErr)
	}
	redirectWithNotice(w, r, target, msg)
}

func parsePositiveIDList(rawItems []string) ([]int64, error) {
	seen := make(map[int64]bool, len(rawItems))
	out := make([]int64, 0, len(rawItems))
	for _, raw := range rawItems {
		id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("invalid id %q", raw)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

func (s *Server) installationsMenuBatch(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Некорректный запрос", http.StatusBadRequest)
		return
	}
	action := strings.TrimSpace(r.PostFormValue("action"))
	if action != "add" && action != "remove" {
		http.Error(w, "Неизвестное действие", http.StatusBadRequest)
		return
	}
	ids, err := parsePositiveIDList(r.PostForm["item"])
	if err != nil || len(ids) == 0 {
		http.Error(w, "Не выбрано ни одной загрузки", http.StatusBadRequest)
		return
	}

	s.draftMu.Lock()
	defer s.draftMu.Unlock()

	sources, err := s.installationMenuSources(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать загрузки", http.StatusServiceUnavailable)
		return
	}
	profiles, answers, configs, err := s.menuSources(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать источники меню", http.StatusServiceUnavailable)
		return
	}
	draft, active, err := s.currentDraft(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать черновик меню", http.StatusServiceUnavailable)
		return
	}

	byID := make(map[int64]Installation, len(sources.Installations))
	for _, inst := range sources.Installations {
		byID[inst.ID] = inst
	}

	changed := 0
	skipped := 0
	var firstSkip string
	for _, id := range ids {
		if action == "add" {
			inst, ok := byID[id]
			if !ok {
				skipped++
				if firstSkip == "" {
					firstSkip = "Загрузка не найдена"
				}
				continue
			}
			if _, _, err := menuProfileForInstallation(id, "", sources, profiles, answers, configs); err != nil {
				skipped++
				if firstSkip == "" {
					firstSkip = "Загрузка не готова"
				}
				continue
			}
			if s.installationMaintenanceLocked(r.Context(), inst, sources, profiles) {
				skipped++
				if firstSkip == "" {
					firstSkip = "Хранилище или образ в режиме обслуживания"
				}
				continue
			}
			if draft.InstallationIndex(id) < 0 {
				draft.Items = insertDraftItem(draft.Items, active.Items, MenuDraftItem{
					InstallationID: id,
					Title:          inst.Title,
					Folder:         inst.Folder,
					Components:     installationComponents(inst),
					InheritsTitle:  true,
				})
				changed++
			}
		} else {
			if idx := draft.InstallationIndex(id); idx >= 0 {
				draft.Items = append(draft.Items[:idx], draft.Items[idx+1:]...)
				changed++
			}
		}
	}

	if changed > 0 {
		if err := s.Store.SaveMenuDraft(r.Context(), draft); err != nil {
			http.Error(w, "Не удалось сохранить черновик меню", http.StatusServiceUnavailable)
			return
		}
		auditAction := "menu.draft.add"
		if action == "remove" {
			auditAction = "menu.draft.remove"
		}
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, auditAction, "ok", fmt.Sprintf("batch=%d", changed))
	}

	target := safeSectionReturnURL(r.PostFormValue("return_to"), "/installations", "show", "q", "sort", "dir")
	if changed == 0 && skipped > 0 {
		http.Error(w, firstSkip, http.StatusConflict)
		return
	}
	var msg string
	if action == "add" {
		msg = fmt.Sprintf("Добавлено в черновик меню: %d", changed)
	} else {
		msg = fmt.Sprintf("Убрано из черновика меню: %d", changed)
	}
	if skipped > 0 {
		msg = fmt.Sprintf("%s (пропущено: %d — %s)", msg, skipped, firstSkip)
	}
	redirectWithNotice(w, r, target, msg)
}

func (s *Server) installationsDeleteBatch(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Некорректный запрос", http.StatusBadRequest)
		return
	}
	ids, err := parsePositiveIDList(r.PostForm["item"])
	if err != nil || len(ids) == 0 {
		http.Error(w, "Не выбрано ни одной загрузки", http.StatusBadRequest)
		return
	}

	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	s.menuMu.Lock()
	defer s.menuMu.Unlock()

	var deleted, skipped int
	var firstSkip string
	for _, id := range ids {
		active, err := s.activeMenuContains(r, installationComponent{installationID: id})
		if err != nil {
			http.Error(w, "Не удалось проверить меню", http.StatusServiceUnavailable)
			return
		}
		if active {
			skipped++
			if firstSkip == "" {
				firstSkip = "Сначала уберите загрузку из меню и опубликуйте изменения"
			}
			continue
		}
		err = s.Store.DeleteInstallation(r.Context(), id)
		if errors.Is(err, pgx.ErrNoRows) {
			skipped++
			if firstSkip == "" {
				firstSkip = "Загрузка не найдена"
			}
			continue
		}
		if errors.Is(err, errInstallationPublished) {
			skipped++
			if firstSkip == "" {
				firstSkip = "Сначала уберите загрузку из меню и опубликуйте изменения"
			}
			continue
		}
		if err != nil {
			skipped++
			if firstSkip == "" {
				firstSkip = "Не удалось удалить загрузку"
			}
			continue
		}
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "installation.delete", "ok", fmt.Sprintf("id=%d", id))
		deleted++
	}

	target := safeSectionReturnURL(r.PostFormValue("return_to"), "/installations", "show", "q", "sort", "dir")
	if deleted == 0 && skipped > 0 {
		http.Error(w, firstSkip, http.StatusConflict)
		return
	}
	msg := fmt.Sprintf("Удалено загрузок: %d", deleted)
	if skipped > 0 {
		msg = fmt.Sprintf("Удалено загрузок: %d (пропущено: %d — %s)", deleted, skipped, firstSkip)
	}
	redirectWithNotice(w, r, target, msg)
}

func (s *Server) driverPackagesDeleteBatch(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Некорректный запрос", http.StatusBadRequest)
		return
	}
	rawItems := r.PostForm["item"]
	if len(rawItems) == 0 {
		http.Error(w, "Не выбрано ни одного пакета", http.StatusBadRequest)
		return
	}
	seen := make(map[string]bool, len(rawItems))
	hashes := make([]string, 0, len(rawItems))
	for _, raw := range rawItems {
		h := strings.TrimSpace(raw)
		if !validAnswerHash(h) {
			http.Error(w, "Неверный пакет драйверов", http.StatusBadRequest)
			return
		}
		if seen[h] {
			continue
		}
		seen[h] = true
		hashes = append(hashes, h)
	}

	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	s.windowsMu.Lock()
	defer s.windowsMu.Unlock()

	if s.Cleanup == nil {
		http.Error(w, "Очистка файлов недоступна", http.StatusServiceUnavailable)
		return
	}
	packages, err := s.Store.DriverPackages(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать каталог драйверов", http.StatusServiceUnavailable)
		return
	}
	pkgExists := make(map[string]bool, len(packages))
	for _, pkg := range packages {
		pkgExists[pkg.SHA256] = true
	}
	configs, err := s.Store.DriverConfigs(r.Context())
	if err != nil {
		http.Error(w, "Не удалось проверить использование", http.StatusServiceUnavailable)
		return
	}
	usedPkg := map[string]bool{}
	for _, cfg := range configs {
		for _, inf := range cfg.PackageINFs {
			usedPkg[inf.PackageSHA256] = true
		}
	}

	var deleted, failed int
	var firstErr string
	for _, hash := range hashes {
		if !pkgExists[hash] {
			failed++
			if firstErr == "" {
				firstErr = "Пакет драйверов не найден"
			}
			continue
		}
		if usedPkg[hash] {
			failed++
			if firstErr == "" {
				firstErr = "Пакет используется конфигурацией драйверов"
			}
			continue
		}
		if err := s.Cleanup.RemoveDriverPackage(r.Context(), hash); err != nil {
			failed++
			if firstErr == "" {
				firstErr = "Не удалось удалить файлы пакета драйверов"
			}
			continue
		}
		if err := s.Store.DeleteDriverPackage(r.Context(), hash); err != nil {
			failed++
			if firstErr == "" {
				firstErr = "Не удалось удалить запись пакета"
			}
			continue
		}
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "driver-package.delete", "ok", "sha256="+hash)
		deleted++
	}
	if deleted > 0 {
		s.InvalidateResourceAuthorsCache()
	}

	target := safeSectionReturnURL(r.PostFormValue("return_to"), "/drivers", "q", "sort", "dir")
	if deleted == 0 && failed > 0 {
		http.Error(w, firstErr, http.StatusConflict)
		return
	}
	msg := fmt.Sprintf("Удалено пакетов драйверов: %d", deleted)
	if failed > 0 {
		msg = fmt.Sprintf("Удалено пакетов драйверов: %d (пропущено: %d — %s)", deleted, failed, firstErr)
	}
	redirectWithNotice(w, r, target, msg)
}

func (s *Server) driverConfigsDeleteBatch(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Некорректный запрос", http.StatusBadRequest)
		return
	}
	ids, err := parsePositiveIDList(r.PostForm["item"])
	if err != nil || len(ids) == 0 {
		http.Error(w, "Не выбрано ни одной конфигурации", http.StatusBadRequest)
		return
	}

	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	s.menuMu.Lock()
	defer s.menuMu.Unlock()
	s.windowsMu.Lock()
	defer s.windowsMu.Unlock()

	if s.Cleanup == nil {
		http.Error(w, "Очистка файлов недоступна", http.StatusServiceUnavailable)
		return
	}
	usage := s.loadDriverUsage(r.Context(), nil, nil)
	if !usage.Available {
		http.Error(w, usage.Error, http.StatusServiceUnavailable)
		return
	}
	source, err := s.installationMenuSources(r.Context())
	if err != nil {
		http.Error(w, "Не удалось проверить загрузки", http.StatusServiceUnavailable)
		return
	}

	var deleted, failed int
	var firstErr string
	for _, id := range ids {
		config, err := s.Store.DriverConfig(r.Context(), id)
		if err != nil {
			failed++
			if firstErr == "" {
				firstErr = "Конфигурация не найдена"
			}
			continue
		}
		active, err := s.activeMenuContains(r, installationComponent{driverConfigID: id})
		if err != nil || active {
			failed++
			if firstErr == "" {
				firstErr = "Сначала снимите пункт с публикации меню"
			}
			continue
		}
		usedByInst := false
		for _, item := range source.Installations {
			if (installationComponent{driverConfigID: id}).matches(item, source.BootProfiles) {
				usedByInst = true
				break
			}
		}
		if usedByInst || len(usage.Configs[id]) > 0 {
			failed++
			if firstErr == "" {
				firstErr = "Конфигурация используется в черновике или опубликованном меню"
			}
			continue
		}
		configs, err := s.Store.DriverConfigs(r.Context())
		if err != nil {
			failed++
			if firstErr == "" {
				firstErr = "Не удалось проверить общие файлы"
			}
			continue
		}
		wimKey := config.Build.WIMKey
		if wimKey == "" {
			wimKey, err = s.Cleanup.DriverConfigWIMKey(r.Context(), id)
			if err != nil {
				failed++
				if firstErr == "" {
					firstErr = "Не удалось проверить подготовленные файлы"
				}
				continue
			}
		}
		removeWIM := wimKey != ""
		for _, other := range configs {
			if other.ID != id && wimKey != "" && (other.Build.WIMKey == wimKey || (other.Build.WIMKey == "" && reflect.DeepEqual(other.PackageINFs, config.PackageINFs))) {
				removeWIM = false
				break
			}
		}
		if err := s.Cleanup.RemoveDriverConfig(r.Context(), id, wimKey, removeWIM); err != nil {
			failed++
			if firstErr == "" {
				firstErr = "Не удалось удалить подготовленные файлы"
			}
			continue
		}
		if err := s.Store.DeleteDriverConfig(r.Context(), id); err != nil {
			failed++
			if firstErr == "" {
				firstErr = "Не удалось удалить запись"
			}
			continue
		}
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "driver-config.delete", "ok", fmt.Sprintf("id=%d", id))
		deleted++
	}

	target := safeSectionReturnURL(r.PostFormValue("return_to"), "/drivers", "q", "sort", "dir")
	if deleted == 0 && failed > 0 {
		http.Error(w, firstErr, http.StatusConflict)
		return
	}
	msg := fmt.Sprintf("Удалено конфигураций WinPE: %d", deleted)
	if failed > 0 {
		msg = fmt.Sprintf("Удалено конфигураций WinPE: %d (пропущено: %d — %s)", deleted, failed, firstErr)
	}
	redirectWithNotice(w, r, target, msg)
}

func (s *Server) machinesDriverBatch(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Некорректный запрос", http.StatusBadRequest)
		return
	}
	book, ok := s.Store.(MachineBook)
	if !ok {
		http.Error(w, "Список машин недоступен", http.StatusServiceUnavailable)
		return
	}
	driver := strings.TrimSpace(r.PostFormValue("network_driver"))
	if driver != "" && driver != "ipxe" && driver != "snp" {
		http.Error(w, "Неверный сетевой драйвер", http.StatusBadRequest)
		return
	}
	rawItems := r.PostForm["item"]
	if len(rawItems) == 0 {
		http.Error(w, "Не выбрано ни одной машины", http.StatusBadRequest)
		return
	}
	seen := make(map[string]bool, len(rawItems))
	macs := make([]string, 0, len(rawItems))
	for _, raw := range rawItems {
		mac := strings.ToLower(strings.TrimSpace(raw))
		if !macAddress.MatchString(mac) {
			http.Error(w, "Неверный MAC-адрес", http.StatusBadRequest)
			return
		}
		if seen[mac] {
			continue
		}
		seen[mac] = true
		macs = append(macs, mac)
	}

	updated := 0
	saved, err := s.mutateBootDrivers(r.Context(), func() error {
		for _, mac := range macs {
			if err := book.SetMachineNetworkDriver(r.Context(), mac, driver); err != nil {
				return err
			}
			updated++
		}
		return nil
	})
	result, message := driverMutationResult(saved, err, fmt.Sprintf("Сетевой драйвер обновлён для машин: %d", updated))
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "machine.network_driver", result, fmt.Sprintf("batch=%d driver=%s", updated, driver))
	if !saved {
		http.Error(w, "Не удалось обновить сетевой драйвер машин", http.StatusServiceUnavailable)
		return
	}
	for _, mac := range macs {
		s.bookCache.forget(mac)
	}
	target := safeSectionReturnURL(r.PostFormValue("return_to"), "/machines", "q", "sort", "rule")
	redirectWithNotice(w, r, target, message)
}

func (s *Server) machinesDeleteBatch(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Некорректный запрос", http.StatusBadRequest)
		return
	}
	book, ok := s.Store.(MachineBook)
	if !ok {
		http.Error(w, "Список машин недоступен", http.StatusServiceUnavailable)
		return
	}
	rawItems := r.PostForm["item"]
	if len(rawItems) == 0 {
		http.Error(w, "Не выбрано ни одной машины", http.StatusBadRequest)
		return
	}
	seen := make(map[string]bool, len(rawItems))
	macs := make([]string, 0, len(rawItems))
	for _, raw := range rawItems {
		mac := strings.ToLower(strings.TrimSpace(raw))
		if !macAddress.MatchString(mac) {
			http.Error(w, "Неверный MAC-адрес", http.StatusBadRequest)
			return
		}
		if seen[mac] {
			continue
		}
		seen[mac] = true
		macs = append(macs, mac)
	}

	deleted := 0
	saved, err := s.mutateBootDrivers(r.Context(), func() error {
		for _, mac := range macs {
			if err := book.DeleteMachine(r.Context(), mac); err != nil {
				return err
			}
			deleted++
		}
		return nil
	})
	result, message := driverMutationResult(saved, err, fmt.Sprintf("Удалено машин: %d", deleted))
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "machine.delete", result, fmt.Sprintf("batch=%d", deleted))
	if !saved {
		http.Error(w, "Не удалось удалить машины", http.StatusServiceUnavailable)
		return
	}
	for _, mac := range macs {
		s.bookCache.forget(mac)
	}
	target := safeSectionReturnURL(r.PostFormValue("return_to"), "/machines", "q", "sort", "rule")
	redirectWithNotice(w, r, target, message)
}

func (s *Server) macRulesDeleteBatch(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Некорректный запрос", http.StatusBadRequest)
		return
	}
	book, ok := s.Store.(MacRuleBook)
	if !ok {
		http.Error(w, "Правила по префиксу MAC недоступны", http.StatusServiceUnavailable)
		return
	}
	rawItems := r.PostForm["item"]
	if len(rawItems) == 0 {
		http.Error(w, "Не выбрано ни одного правила", http.StatusBadRequest)
		return
	}
	seen := make(map[string]bool, len(rawItems))
	prefixes := make([]string, 0, len(rawItems))
	for _, raw := range rawItems {
		p, valid := parseMACPrefix(raw)
		if !valid || p != strings.ToLower(strings.TrimSpace(raw)) {
			http.Error(w, "Неверный префикс", http.StatusBadRequest)
			return
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		prefixes = append(prefixes, p)
	}

	deleted := 0
	saved, err := s.mutateBootDrivers(r.Context(), func() error {
		for _, p := range prefixes {
			if err := book.DeleteMacRule(r.Context(), p); err != nil {
				return err
			}
			deleted++
		}
		return nil
	})
	result, message := driverMutationResult(saved, err, fmt.Sprintf("Удалено правил MAC: %d", deleted))
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "mac_rule.delete", result, fmt.Sprintf("batch=%d", deleted))
	if !saved {
		http.Error(w, "Не удалось удалить правила", http.StatusServiceUnavailable)
		return
	}
	target := safeSectionReturnURL(r.PostFormValue("return_to"), "/machines", "q", "sort", "rule")
	redirectWithNotice(w, r, target, message)
}
