package control

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"omb/internal/agent"
	"omb/internal/images"

	"github.com/jackc/pgx/v5"
)

// WindowsWIMAPI manages standalone Windows boot.wim and install.wim/esd assets in omb-agent.
type WindowsWIMAPI interface {
	PrepareBootImage(context.Context, agent.WindowsBootImageRequest) (agent.WindowsBootImageResult, error)
	RemoveBootImage(context.Context, int64) error
	PrepareInstallImage(context.Context, agent.WindowsInstallImageRequest) (agent.WindowsInstallImageResult, error)
	RemoveInstallImage(context.Context, int64) error
}

type windowsWIMStore interface {
	ListWindowsBootImages(context.Context) ([]WindowsBootImage, error)
	GetWindowsBootImage(context.Context, int64) (WindowsBootImage, error)
	UpsertFileBootImage(context.Context, WindowsBootImageSource) (WindowsBootImage, error)
	SetBootImageReady(context.Context, int64, string, bool, agent.WindowsWIMInfo) error
	SetBootImageError(context.Context, int64, string, string) error
	SetBootImageTitle(context.Context, int64, string) error
	DeleteWindowsBootImage(context.Context, int64) error
	ListWindowsInstallImages(context.Context) ([]WindowsInstallImage, error)
	GetWindowsInstallImage(context.Context, int64) (WindowsInstallImage, error)
	UpsertWindowsInstallImage(context.Context, WindowsInstallImageSource) (WindowsInstallImage, error)
	SetInstallImageReady(context.Context, int64, string, agent.WindowsWIMInfo) error
	SetInstallImageError(context.Context, int64, string, string) error
	SetInstallImageTitle(context.Context, int64, string) error
	DeleteWindowsInstallImage(context.Context, int64) error
}

func (s *Server) windowsWIMStore() (windowsWIMStore, bool) {
	st, ok := s.Store.(windowsWIMStore)
	return st, ok
}

func (s *Server) windowsWIMAPI() WindowsWIMAPI {
	if !isNilProvider(s.WindowsWIM) {
		return s.WindowsWIM
	}
	if s.Agent != nil {
		return s.Agent
	}
	return nil
}

type profileBootWIMInspector interface {
	InspectProfileBootWIM(context.Context, int64) (agent.WindowsWIMInfo, error)
}

type isoBootWIMInfoStore interface {
	SetISOBootImageWIMInfo(context.Context, int64, agent.WindowsWIMInfo) error
}

func (s *Server) profileBootWIMInspector() profileBootWIMInspector {
	if insp, ok := s.WindowsWIM.(profileBootWIMInspector); ok && !isNilProvider(insp) {
		return insp
	}
	if insp, ok := s.WindowsISO.(profileBootWIMInspector); ok && !isNilProvider(insp) {
		return insp
	}
	if s.Agent != nil {
		return s.Agent
	}
	return nil
}

func (s *Server) saveISOBootWIMInfo(ctx context.Context, profileID int64, info agent.WindowsWIMInfo) {
	if profileID <= 0 || !info.Known {
		return
	}
	if st, ok := s.Store.(isoBootWIMInfoStore); ok {
		_ = st.SetISOBootImageWIMInfo(ctx, profileID, info)
	}
}

// SyncISOBootWIMInfo backfills WIM metadata for ISO-backed boot.wim entries
// whose wim_info is still unknown by inspecting each profile's cached boot.wim
// via omb-agent.
func (s *Server) SyncISOBootWIMInfo(ctx context.Context) error {
	wimStore, ok := s.windowsWIMStore()
	if !ok {
		return nil
	}
	setter, ok := s.Store.(isoBootWIMInfoStore)
	if !ok {
		return nil
	}
	inspector := s.profileBootWIMInspector()
	if inspector == nil {
		return nil
	}
	list, err := wimStore.ListWindowsBootImages(ctx)
	if err != nil {
		return err
	}
	var firstErr error
	for _, b := range list {
		if b.SourceKind != WIMSourceKindISO || b.State != "ready" || b.WIMInfo.Known || b.WindowsProfileID <= 0 {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		info, err := inspector.InspectProfileBootWIM(ctx, b.WindowsProfileID)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !info.Known {
			continue
		}
		if err := setter.SetISOBootImageWIMInfo(ctx, b.WindowsProfileID, info); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func enrichISOBootImageWIMInfo(boot WindowsBootImage, profiles []WindowsProfile) WindowsBootImage {
	if boot.SourceKind != WIMSourceKindISO || boot.WIMInfo.Known || boot.WindowsProfileID <= 0 {
		return boot
	}
	for _, p := range profiles {
		if p.ID == boot.WindowsProfileID && p.WIMInfo.Known {
			boot.WIMInfo = p.WIMInfo
			break
		}
	}
	return boot
}

func findCatalogImage(catalog images.Catalog, storageID int64, relPath string) (images.Storage, images.Image, bool) {
	var st images.Storage
	var stFound bool
	for _, candidate := range catalog.Storages {
		if candidate.ID == storageID {
			st = candidate
			stFound = true
			break
		}
	}
	if !stFound || st.State != "ready" || st.Root == "" {
		return images.Storage{}, images.Image{}, false
	}
	for _, img := range catalog.Images {
		if img.StorageID == storageID && img.Path == relPath && img.Present {
			return st, img, true
		}
	}
	return images.Storage{}, images.Image{}, false
}

func validWIMTitle(raw string) (string, bool) {
	title := strings.TrimSpace(raw)
	if title == "" || !utf8.ValidString(title) || utf8.RuneCountInString(title) > 120 || strings.IndexFunc(title, unicode.IsControl) >= 0 {
		return "", false
	}
	return title, true
}

func safeWIMReturnURL(raw, defaultTab string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if defaultTab != "" {
			return "/images?tab=" + defaultTab
		}
		return "/images"
	}
	return safeImagesReturnURL(raw)
}

// checkWIMImageReferences reports whether a standalone boot.wim or install.wim
// is referenced by any installation, menu draft, or published menu version.
func (s *Server) checkWIMImageReferences(ctx context.Context, bootImageID, installImageID int64) (reason string, message string, err error) {
	sources, err := s.libraryMenuReferences(ctx)
	if err != nil {
		return "", "", err
	}
	comp := installationComponent{bootImageID: bootImageID, installImageID: installImageID}
	instByID := make(map[int64]Installation, len(sources.Installations))
	for _, item := range sources.Installations {
		instByID[item.ID] = item
		if comp.matches(item, sources.BootProfiles) {
			return fmt.Sprintf("used by installation %d", item.ID), fmt.Sprintf("Объект используется загрузкой № %d", item.ID), nil
		}
	}

	draft, hasDraft, err := s.Store.MenuDraft(ctx)
	if err != nil {
		return "", "", err
	}
	if hasDraft {
		for _, item := range draft.Items {
			if item.Components != nil {
				if (bootImageID > 0 && item.Components.BootImageID == bootImageID) ||
					(installImageID > 0 && item.Components.InstallImageID == installImageID) {
					return "used by menu draft", "Объект используется в черновике меню", nil
				}
			}
			if inst, ok := instByID[item.InstallationID]; ok && comp.matches(item.Components.apply(inst), sources.BootProfiles) {
				return "used by menu draft", "Объект используется в черновике меню", nil
			}
		}
	}

	versions, err := s.Store.MenuVersions(ctx)
	if err != nil {
		return "", "", err
	}
	for _, ver := range versions {
		for _, placement := range ver.Items {
			matched := false
			if placement.Components != nil {
				if (bootImageID > 0 && placement.Components.BootImageID == bootImageID) ||
					(installImageID > 0 && placement.Components.InstallImageID == installImageID) {
					matched = true
				}
			}
			if !matched {
				if inst, ok := instByID[placement.InstallationID]; ok && comp.matches(placement.Components.apply(inst), sources.BootProfiles) {
					matched = true
				}
			}
			if matched {
				if ver.Active {
					return "published", "Сначала снимите пункт с публикации меню", nil
				}
				return fmt.Sprintf("used by menu version %d", ver.ID), fmt.Sprintf("Объект используется в опубликованной версии меню № %d", ver.ID), nil
			}
		}
	}
	return "", "", nil
}

func (s *Server) inspectBootWIM(w http.ResponseWriter, r *http.Request) {
	store, ok := s.windowsWIMStore()
	api := s.windowsWIMAPI()
	if !ok || api == nil || s.Images == nil {
		http.Error(w, "Проверка boot.wim недоступна", http.StatusServiceUnavailable)
		return
	}
	storageID, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("storage_id")), 10, 64)
	relPath := strings.TrimSpace(r.FormValue("path"))
	if err != nil || storageID <= 0 || relPath == "" {
		http.Error(w, "Выберите исходный файл boot.wim", http.StatusBadRequest)
		return
	}
	if !strings.EqualFold(path.Ext(relPath), ".wim") {
		http.Error(w, "Для boot.wim поддерживаются только файлы .wim", http.StatusBadRequest)
		return
	}
	if _, _, locked := s.loadMaintenanceLookup(r.Context()).ImageLock(storageID, relPath); locked {
		http.Error(w, "Хранилище или образ в режиме обслуживания", http.StatusConflict)
		return
	}
	catalog, err := s.Images.Catalog(r.Context())
	if err != nil {
		http.Error(w, "Каталог образов недоступен", http.StatusServiceUnavailable)
		return
	}
	st, img, found := findCatalogImage(catalog, storageID, relPath)
	if !found {
		http.Error(w, "Файл boot.wim не найден в хранилище", http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		title = path.Base(img.Path)
	}
	rec, err := store.UpsertFileBootImage(r.Context(), WindowsBootImageSource{
		StorageID:   storageID,
		Path:        img.Path,
		StorageRoot: st.Root,
		Size:        img.Size,
		Modified:    img.Modified,
		Title:       title,
	})
	if err != nil {
		http.Error(w, "Не удалось сохранить запись boot.wim", http.StatusServiceUnavailable)
		return
	}

	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(11 * time.Minute))
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Minute)
	defer cancel()

	res, prepErr := api.PrepareBootImage(ctx, agent.WindowsBootImageRequest{
		ID:           rec.ID,
		StorageRoot:  st.Root,
		RelativePath: img.Path,
		Size:         img.Size,
		Modified:     img.Modified,
	})
	persistCtx, persistCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer persistCancel()
	if prepErr != nil {
		_ = store.SetBootImageError(persistCtx, rec.ID, "error", prepErr.Error())
		_ = s.Store.Audit(persistCtx, requestInfo(r).User.ID, "boot-wim.inspect", "error", fmt.Sprintf("id=%d %v", rec.ID, prepErr))
		msg := "Не удалось проверить boot.wim. Убедитесь, что файл является загрузочным образом WinPE."
		if errors.Is(prepErr, agent.ErrInvalidWIMType) || strings.Contains(prepErr.Error(), agent.ErrInvalidWIMType.Error()) {
			msg = "Файл не является загрузочным образом boot.wim (отсутствует загрузочный индекс WinPE)"
		}
		http.Error(w, msg, http.StatusUnprocessableEntity)
		return
	}
	if err := store.SetBootImageReady(persistCtx, rec.ID, res.SHA256, res.HasSetupExe, res.WIMInfo); err != nil {
		http.Error(w, "Не удалось сохранить результат проверки boot.wim", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(persistCtx, requestInfo(r).User.ID, "boot-wim.inspect", "ok", fmt.Sprintf("id=%d storage=%d path=%s", rec.ID, storageID, img.Path))
	redirectWithNotice(w, r, safeWIMReturnURL(r.FormValue("return_to"), "boot"), "Образ boot.wim проверен и готов к использованию")
}

func (s *Server) inspectInstallWIM(w http.ResponseWriter, r *http.Request) {
	store, ok := s.windowsWIMStore()
	api := s.windowsWIMAPI()
	if !ok || api == nil || s.Images == nil {
		http.Error(w, "Проверка install.wim недоступна", http.StatusServiceUnavailable)
		return
	}
	storageID, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("storage_id")), 10, 64)
	relPath := strings.TrimSpace(r.FormValue("path"))
	if err != nil || storageID <= 0 || relPath == "" {
		http.Error(w, "Выберите исходный файл install.wim или install.esd", http.StatusBadRequest)
		return
	}
	ext := strings.ToLower(path.Ext(relPath))
	if ext != ".wim" && ext != ".esd" {
		http.Error(w, "Для install.wim поддерживаются только файлы .wim и .esd", http.StatusBadRequest)
		return
	}
	if _, _, locked := s.loadMaintenanceLookup(r.Context()).ImageLock(storageID, relPath); locked {
		http.Error(w, "Хранилище или образ в режиме обслуживания", http.StatusConflict)
		return
	}
	catalog, err := s.Images.Catalog(r.Context())
	if err != nil {
		http.Error(w, "Каталог образов недоступен", http.StatusServiceUnavailable)
		return
	}
	st, img, found := findCatalogImage(catalog, storageID, relPath)
	if !found {
		http.Error(w, "Файл install.wim не найден в хранилище", http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		title = path.Base(img.Path)
	}
	rec, err := store.UpsertWindowsInstallImage(r.Context(), WindowsInstallImageSource{
		StorageID:   storageID,
		Path:        img.Path,
		StorageRoot: st.Root,
		Size:        img.Size,
		Modified:    img.Modified,
		Title:       title,
	})
	if err != nil {
		http.Error(w, "Не удалось сохранить запись install.wim", http.StatusServiceUnavailable)
		return
	}

	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(11 * time.Minute))
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Minute)
	defer cancel()

	res, prepErr := api.PrepareInstallImage(ctx, agent.WindowsInstallImageRequest{
		ID:           rec.ID,
		StorageRoot:  st.Root,
		RelativePath: img.Path,
		Size:         img.Size,
		Modified:     img.Modified,
	})
	persistCtx, persistCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer persistCancel()
	if prepErr != nil {
		_ = store.SetInstallImageError(persistCtx, rec.ID, "error", prepErr.Error())
		_ = s.Store.Audit(persistCtx, requestInfo(r).User.ID, "install-wim.inspect", "error", fmt.Sprintf("id=%d %v", rec.ID, prepErr))
		msg := "Не удалось проверить install.wim. Убедитесь, что файл является установочным образом Windows."
		if errors.Is(prepErr, agent.ErrInvalidWIMType) || strings.Contains(prepErr.Error(), agent.ErrInvalidWIMType.Error()) {
			msg = "Файл является загрузочным образом WinPE (boot.wim), а не установочным образом install.wim"
		}
		http.Error(w, msg, http.StatusUnprocessableEntity)
		return
	}
	if err := store.SetInstallImageReady(persistCtx, rec.ID, res.InstallFile, res.WIMInfo); err != nil {
		http.Error(w, "Не удалось сохранить результат проверки install.wim", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(persistCtx, requestInfo(r).User.ID, "install-wim.inspect", "ok", fmt.Sprintf("id=%d storage=%d path=%s", rec.ID, storageID, img.Path))
	redirectWithNotice(w, r, safeWIMReturnURL(r.FormValue("return_to"), "install"), "Образ install.wim проверен и готов к использованию")
}

func (s *Server) renameBootWIM(w http.ResponseWriter, r *http.Request) {
	store, ok := s.windowsWIMStore()
	if !ok {
		http.Error(w, "Переименование boot.wim недоступно", http.StatusServiceUnavailable)
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("id")), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Неверный номер boot.wim", http.StatusBadRequest)
		return
	}
	rawTitle := r.FormValue("title")
	if strings.TrimSpace(rawTitle) == "" {
		rawTitle = r.FormValue("name")
	}
	title, valid := validWIMTitle(rawTitle)
	if !valid {
		http.Error(w, "Укажите название образа: до 120 символов без управляющих знаков", http.StatusBadRequest)
		return
	}
	rec, err := store.GetWindowsBootImage(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "Образ boot.wim не найден", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "Не удалось прочитать образ boot.wim", http.StatusServiceUnavailable)
		return
	}
	if rec.SourceKind == WIMSourceKindISO {
		http.Error(w, "Название boot.wim из ISO задаётся через исходный ISO", http.StatusBadRequest)
		return
	}
	if err := store.SetBootImageTitle(r.Context(), id, title); err != nil {
		http.Error(w, "Не удалось сохранить название boot.wim", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "boot-wim.rename", "ok", fmt.Sprintf("id=%d title=%s", id, title))
	redirectWithNotice(w, r, safeWIMReturnURL(r.FormValue("return_to"), "boot"), "Название boot.wim обновлено")
}

func (s *Server) renameInstallWIM(w http.ResponseWriter, r *http.Request) {
	store, ok := s.windowsWIMStore()
	if !ok {
		http.Error(w, "Переименование install.wim недоступно", http.StatusServiceUnavailable)
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("id")), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Неверный номер install.wim", http.StatusBadRequest)
		return
	}
	rawTitle := r.FormValue("title")
	if strings.TrimSpace(rawTitle) == "" {
		rawTitle = r.FormValue("name")
	}
	title, valid := validWIMTitle(rawTitle)
	if !valid {
		http.Error(w, "Укажите название образа: до 120 символов без управляющих знаков", http.StatusBadRequest)
		return
	}
	if _, err := store.GetWindowsInstallImage(r.Context(), id); errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "Образ install.wim не найден", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "Не удалось прочитать образ install.wim", http.StatusServiceUnavailable)
		return
	}
	if err := store.SetInstallImageTitle(r.Context(), id, title); err != nil {
		http.Error(w, "Не удалось сохранить название install.wim", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "install-wim.rename", "ok", fmt.Sprintf("id=%d title=%s", id, title))
	redirectWithNotice(w, r, safeWIMReturnURL(r.FormValue("return_to"), "install"), "Название install.wim обновлено")
}

func (s *Server) prepareWIMSourceFileDelete(r *http.Request, action string, id, storageID int64, imagePath string) (int, string) {
	if s.Images == nil {
		return http.StatusServiceUnavailable, "Сервис образов недоступен"
	}
	catalog, err := s.Images.Catalog(r.Context())
	if err != nil {
		return http.StatusServiceUnavailable, "Сервис образов недоступен"
	}
	var storage images.Storage
	storageFound := false
	for _, st := range catalog.Storages {
		if st.ID == storageID {
			storage = st
			storageFound = true
			break
		}
	}
	if storageFound && storage.State != "ready" {
		return http.StatusConflict, "Хранилище недоступно"
	}
	var image images.Image
	imageFound := false
	for _, img := range catalog.Images {
		if img.StorageID == storageID && img.Path == imagePath {
			image = img
			imageFound = true
			break
		}
	}
	if !storageFound || !imageFound || (storage.Kind != "local" && storage.Kind != "mounted" && storage.Kind != "smb" && storage.Kind != "nfs") {
		return http.StatusNotFound, "Файл образа не найден"
	}
	if storage.Kind == "nfs" && !storage.Uploads && image.Present {
		return http.StatusForbidden, "Хранилище доступно только для чтения"
	}
	if image.Present {
		if storage.Kind == "smb" {
			if s.SMB == nil {
				return http.StatusServiceUnavailable, "Служба хранилищ недоступна"
			}
			if err := s.SMB.EnableUpload(r.Context(), storage.Key, storage.Server, storage.Share, storage.Subpath); err != nil {
				_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, action, "error", fmt.Sprintf("id=%d %d %s mount", id, storageID, imagePath))
				return http.StatusServiceUnavailable, "Не удалось подключить хранилище для записи. Проверьте права SMB."
			}
		}
		if storage.Kind == "nfs" {
			if s.NFS == nil {
				return http.StatusServiceUnavailable, "Служба хранилищ недоступна"
			}
			if err := s.NFS.EnableNFSUpload(r.Context(), storage.Key, storage.Server, storage.ExportPath, storage.NFSVersion); err != nil {
				_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, action, "error", fmt.Sprintf("id=%d %d %s mount", id, storageID, imagePath))
				return http.StatusServiceUnavailable, "Не удалось подключить хранилище для записи. Проверьте права NFS."
			}
		}
	}
	return 0, ""
}

func (s *Server) deleteBootWIM(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "Удаление boot.wim недоступно", http.StatusServiceUnavailable)
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("id")), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Неверный номер boot.wim", http.StatusBadRequest)
		return
	}
	rec, err := store.GetWindowsBootImage(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "Образ boot.wim не найден", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "Не удалось прочитать образ boot.wim", http.StatusServiceUnavailable)
		return
	}
	if rec.SourceKind == WIMSourceKindISO {
		s.rejectDeletion(w, r, "boot-wim.delete", id, "iso-backed", "Этот boot.wim принадлежит образу ISO; удалите исходный ISO")
		return
	}
	if rec.StorageID > 0 && rec.Path != "" {
		if _, _, locked := s.loadMaintenanceLookup(r.Context()).ImageLock(rec.StorageID, rec.Path); locked {
			s.rejectDeletion(w, r, "boot-wim.delete", id, "maintenance", "Хранилище или образ в режиме обслуживания")
			return
		}
	}
	reason, message, err := s.checkWIMImageReferences(r.Context(), id, 0)
	if err != nil {
		http.Error(w, "Не удалось проверить использование boot.wim", http.StatusServiceUnavailable)
		return
	}
	if reason != "" {
		s.rejectDeletion(w, r, "boot-wim.delete", id, reason, message)
		return
	}
	deleteFile := r.FormValue("delete_file") == "1" && rec.StorageID > 0 && rec.Path != ""
	if deleteFile {
		if status, errMsg := s.prepareWIMSourceFileDelete(r, "boot-wim.delete", id, rec.StorageID, rec.Path); status != 0 {
			http.Error(w, errMsg, status)
			return
		}
	}
	if err := api.RemoveBootImage(r.Context(), id); err != nil {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "boot-wim.delete", "error", fmt.Sprintf("id=%d cleanup", id))
		http.Error(w, "Не удалось удалить подготовленные файлы boot.wim", http.StatusServiceUnavailable)
		return
	}
	if deleteFile {
		if err := s.Images.DeleteImage(r.Context(), rec.StorageID, rec.Path); err != nil {
			_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "boot-wim.delete", "error", fmt.Sprintf("id=%d %d %s storage", id, rec.StorageID, rec.Path))
			http.Error(w, "Не удалось удалить файл образа. Проверьте доступность хранилища и права на запись.", http.StatusServiceUnavailable)
			return
		}
	}
	if err := store.DeleteWindowsBootImage(r.Context(), id); err != nil {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "boot-wim.delete", "error", fmt.Sprintf("id=%d database", id))
		http.Error(w, "Не удалось удалить запись boot.wim", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "boot-wim.delete", "ok", fmt.Sprintf("id=%d", id))
	msg := fmt.Sprintf("Образ boot.wim «%s» удалён", rec.Title)
	redirectWithNotice(w, r, safeWIMReturnURL(r.FormValue("return_to"), "boot"), msg)
}

func (s *Server) deleteInstallWIM(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "Удаление install.wim недоступно", http.StatusServiceUnavailable)
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("id")), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Неверный номер install.wim", http.StatusBadRequest)
		return
	}
	rec, err := store.GetWindowsInstallImage(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "Образ install.wim не найден", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "Не удалось прочитать образ install.wim", http.StatusServiceUnavailable)
		return
	}
	if rec.StorageID > 0 && rec.Path != "" {
		if _, _, locked := s.loadMaintenanceLookup(r.Context()).ImageLock(rec.StorageID, rec.Path); locked {
			s.rejectDeletion(w, r, "install-wim.delete", id, "maintenance", "Хранилище или образ в режиме обслуживания")
			return
		}
	}
	reason, message, err := s.checkWIMImageReferences(r.Context(), 0, id)
	if err != nil {
		http.Error(w, "Не удалось проверить использование install.wim", http.StatusServiceUnavailable)
		return
	}
	if reason != "" {
		s.rejectDeletion(w, r, "install-wim.delete", id, reason, message)
		return
	}
	deleteFile := r.FormValue("delete_file") == "1" && rec.StorageID > 0 && rec.Path != ""
	if deleteFile {
		if status, errMsg := s.prepareWIMSourceFileDelete(r, "install-wim.delete", id, rec.StorageID, rec.Path); status != 0 {
			http.Error(w, errMsg, status)
			return
		}
	}
	if err := api.RemoveInstallImage(r.Context(), id); err != nil {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "install-wim.delete", "error", fmt.Sprintf("id=%d cleanup", id))
		http.Error(w, "Не удалось удалить подготовленные файлы install.wim", http.StatusServiceUnavailable)
		return
	}
	if deleteFile {
		if err := s.Images.DeleteImage(r.Context(), rec.StorageID, rec.Path); err != nil {
			_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "install-wim.delete", "error", fmt.Sprintf("id=%d %d %s storage", id, rec.StorageID, rec.Path))
			http.Error(w, "Не удалось удалить файл образа. Проверьте доступность хранилища и права на запись.", http.StatusServiceUnavailable)
			return
		}
	}
	if err := store.DeleteWindowsInstallImage(r.Context(), id); err != nil {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "install-wim.delete", "error", fmt.Sprintf("id=%d database", id))
		http.Error(w, "Не удалось удалить запись install.wim", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "install-wim.delete", "ok", fmt.Sprintf("id=%d", id))
	msg := fmt.Sprintf("Образ install.wim «%s» удалён", rec.Title)
	redirectWithNotice(w, r, safeWIMReturnURL(r.FormValue("return_to"), "install"), msg)
}
