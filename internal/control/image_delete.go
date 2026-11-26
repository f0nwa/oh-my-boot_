package control

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"omb/internal/images"
)

// safeImagesReturnURL validates a return_to value targeting /images and keeps
// only safe list-view query parameters (show, q, sort, dir), stripping any prior message.
func safeImagesReturnURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return "/images"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Path != "/images" {
		return "/images"
	}
	q := u.Query()
	clean := url.Values{}
	for _, key := range []string{"tab", "show", "q", "sort", "dir"} {
		if v := strings.TrimSpace(q.Get(key)); v != "" {
			clean.Set(key, v)
		}
	}
	if len(clean) == 0 {
		return "/images"
	}
	return "/images?" + clean.Encode()
}

type imageBatchItem struct {
	storageID int64
	path      string
}

func parseImageBatchItems(values []string) ([]imageBatchItem, error) {
	items := make([]imageBatchItem, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, raw := range values {
		idStr, path, ok := strings.Cut(raw, ":")
		if !ok || path == "" {
			return nil, fmt.Errorf("invalid item")
		}
		storageID, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
		if err != nil || storageID <= 0 {
			return nil, fmt.Errorf("invalid storage_id")
		}
		key := imageAuthorKey(storageID, path)
		if seen[key] {
			continue
		}
		seen[key] = true
		items = append(items, imageBatchItem{storageID: storageID, path: path})
	}
	return items, nil
}

type imageDeleteContext struct {
	storagesByID     map[int64]images.Storage
	imagesByKey      map[string]images.Image
	maintLookup      maintenanceLookup
	profilesByKey    map[string]WindowsProfile
	bootWIMsByKey    map[string]WindowsBootImage
	installWIMsByKey map[string]WindowsInstallImage
	downloads        map[string][]imageDownload
	uploadEnabled    map[int64]bool
}

func (s *Server) prepareImageDeleteContext(r *http.Request) (*imageDeleteContext, int, string) {
	if s.Images == nil {
		return nil, http.StatusServiceUnavailable, "Сервис образов недоступен"
	}
	catalog, err := s.Images.Catalog(r.Context())
	if err != nil {
		return nil, http.StatusServiceUnavailable, "Сервис образов недоступен"
	}
	profiles, err := s.Store.WindowsProfiles(r.Context())
	if err != nil {
		return nil, http.StatusServiceUnavailable, "Не удалось проверить профили ISO"
	}
	sources, err := s.libraryMenuReferences(r.Context())
	if err != nil {
		return nil, http.StatusServiceUnavailable, "Не удалось проверить загрузки"
	}
	dctx := &imageDeleteContext{
		storagesByID:     make(map[int64]images.Storage, len(catalog.Storages)),
		imagesByKey:      make(map[string]images.Image, len(catalog.Images)),
		maintLookup:      s.loadMaintenanceLookup(r.Context()),
		profilesByKey:    make(map[string]WindowsProfile, len(profiles)),
		bootWIMsByKey:    make(map[string]WindowsBootImage),
		installWIMsByKey: make(map[string]WindowsInstallImage),
		downloads:        imageDownloadsBySource(profiles, sources),
		uploadEnabled:    make(map[int64]bool),
	}
	for _, st := range catalog.Storages {
		dctx.storagesByID[st.ID] = st
	}
	for _, img := range catalog.Images {
		dctx.imagesByKey[imageAuthorKey(img.StorageID, img.Path)] = img
	}
	for _, p := range profiles {
		dctx.profilesByKey[imageAuthorKey(p.StorageID, p.Path)] = p
	}
	if wimStore, ok := s.windowsWIMStore(); ok {
		bootImages, err := wimStore.ListWindowsBootImages(r.Context())
		if err != nil {
			return nil, http.StatusServiceUnavailable, "Не удалось проверить образы boot.wim"
		}
		installImages, err := wimStore.ListWindowsInstallImages(r.Context())
		if err != nil {
			return nil, http.StatusServiceUnavailable, "Не удалось проверить образы install.wim"
		}
		for _, img := range bootImages {
			if img.SourceKind == WIMSourceKindFile && img.StorageID > 0 && img.Path != "" {
				dctx.bootWIMsByKey[imageAuthorKey(img.StorageID, img.Path)] = img
			}
		}
		for _, img := range installImages {
			if img.StorageID > 0 && img.Path != "" {
				dctx.installWIMsByKey[imageAuthorKey(img.StorageID, img.Path)] = img
			}
		}
		for k, list := range imageDownloadsByWIMSource(bootImages, installImages, sources) {
			dctx.downloads[k] = append(dctx.downloads[k], list...)
		}
	}
	return dctx, 0, ""
}

func (s *Server) deleteSingleImageItem(r *http.Request, dctx *imageDeleteContext, storageID int64, imagePath string) (storageName string, imagePresent bool, status int, errMsg string) {
	if _, _, locked := dctx.maintLookup.ImageLock(storageID, imagePath); locked {
		return "", false, http.StatusConflict, "Хранилище или образ в режиме обслуживания"
	}
	storage, storageFound := dctx.storagesByID[storageID]
	if storageFound && storage.State != "ready" {
		return "", false, http.StatusConflict, "Хранилище недоступно"
	}
	key := imageAuthorKey(storageID, imagePath)
	image, imageFound := dctx.imagesByKey[key]
	if !storageFound || !imageFound || (storage.Kind != "local" && storage.Kind != "mounted" && storage.Kind != "smb" && storage.Kind != "nfs") {
		return "", false, http.StatusNotFound, "ISO не найден"
	}
	storageName = storage.Name
	imagePresent = image.Present
	if storage.Kind == "nfs" && !storage.Uploads && imagePresent {
		return storageName, imagePresent, http.StatusForbidden, "Хранилище доступно только для чтения"
	}
	if uses := dctx.downloads[key]; len(uses) > 0 {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "iso.delete", "rejected", fmt.Sprintf("id=%d used by installation %d", storageID, uses[0].ID))
		return storageName, imagePresent, http.StatusConflict, fmt.Sprintf("Объект используется загрузкой № %d", uses[0].ID)
	}
	if profile, hasProfile := dctx.profilesByKey[key]; hasProfile && profile.ID > 0 {
		if s.Cleanup == nil {
			return storageName, imagePresent, http.StatusServiceUnavailable, "Очистка файлов недоступна"
		}
		active, err := s.activeMenuContains(r, installationComponent{profileID: profile.ID})
		if err != nil {
			return storageName, imagePresent, http.StatusServiceUnavailable, "Не удалось проверить меню"
		}
		if active {
			_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "iso.delete", "rejected", fmt.Sprintf("id=%d published", storageID))
			return storageName, imagePresent, http.StatusConflict, "Сначала снимите пункт с публикации меню"
		}
		configs, err := s.Store.DriverConfigs(r.Context())
		if err != nil {
			return storageName, imagePresent, http.StatusServiceUnavailable, "Не удалось проверить конфигурации"
		}
		for _, config := range configs {
			if config.ProfileID == profile.ID {
				_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "iso.delete", "rejected", fmt.Sprintf("id=%d used by driver config %d", storageID, config.ID))
				return storageName, imagePresent, http.StatusConflict, fmt.Sprintf("Сначала удалите конфигурацию драйверов № %d", config.ID)
			}
		}
		if err := s.Cleanup.RemoveWindowsProfile(r.Context(), profile.ID); err != nil {
			_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "iso.delete", "error", fmt.Sprintf("%d %s cleanup", storageID, imagePath))
			return storageName, imagePresent, http.StatusServiceUnavailable, "Не удалось удалить подготовленные файлы"
		}
		if err := s.Store.DeleteWindowsProfile(r.Context(), profile.ID); err != nil {
			_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "iso.delete", "error", fmt.Sprintf("%d %s database", storageID, imagePath))
			return storageName, imagePresent, http.StatusServiceUnavailable, "Не удалось удалить запись профиля"
		}
		delete(dctx.profilesByKey, key)
	}
	if bootImg, hasBoot := dctx.bootWIMsByKey[key]; hasBoot && bootImg.ID > 0 {
		reason, msg, err := s.checkWIMImageReferences(r.Context(), bootImg.ID, 0)
		if err != nil {
			return storageName, imagePresent, http.StatusServiceUnavailable, "Не удалось проверить использование boot.wim"
		}
		if reason != "" {
			_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "iso.delete", "rejected", fmt.Sprintf("id=%d boot_wim=%d %s", storageID, bootImg.ID, reason))
			return storageName, imagePresent, http.StatusConflict, msg
		}
		wimAPI := s.windowsWIMAPI()
		wimStore, ok := s.windowsWIMStore()
		if wimAPI == nil || !ok {
			return storageName, imagePresent, http.StatusServiceUnavailable, "Очистка файлов boot.wim недоступна"
		}
		if err := wimAPI.RemoveBootImage(r.Context(), bootImg.ID); err != nil {
			_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "iso.delete", "error", fmt.Sprintf("%d %s boot cleanup", storageID, imagePath))
			return storageName, imagePresent, http.StatusServiceUnavailable, "Не удалось удалить подготовленные файлы boot.wim"
		}
		if err := wimStore.DeleteWindowsBootImage(r.Context(), bootImg.ID); err != nil {
			_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "iso.delete", "error", fmt.Sprintf("%d %s boot database", storageID, imagePath))
			return storageName, imagePresent, http.StatusServiceUnavailable, "Не удалось удалить запись boot.wim"
		}
		delete(dctx.bootWIMsByKey, key)
	}
	if instImg, hasInst := dctx.installWIMsByKey[key]; hasInst && instImg.ID > 0 {
		reason, msg, err := s.checkWIMImageReferences(r.Context(), 0, instImg.ID)
		if err != nil {
			return storageName, imagePresent, http.StatusServiceUnavailable, "Не удалось проверить использование install.wim"
		}
		if reason != "" {
			_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "iso.delete", "rejected", fmt.Sprintf("id=%d install_wim=%d %s", storageID, instImg.ID, reason))
			return storageName, imagePresent, http.StatusConflict, msg
		}
		wimAPI := s.windowsWIMAPI()
		wimStore, ok := s.windowsWIMStore()
		if wimAPI == nil || !ok {
			return storageName, imagePresent, http.StatusServiceUnavailable, "Очистка файлов install.wim недоступна"
		}
		if err := wimAPI.RemoveInstallImage(r.Context(), instImg.ID); err != nil {
			_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "iso.delete", "error", fmt.Sprintf("%d %s install cleanup", storageID, imagePath))
			return storageName, imagePresent, http.StatusServiceUnavailable, "Не удалось удалить подготовленные файлы install.wim"
		}
		if err := wimStore.DeleteWindowsInstallImage(r.Context(), instImg.ID); err != nil {
			_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "iso.delete", "error", fmt.Sprintf("%d %s install database", storageID, imagePath))
			return storageName, imagePresent, http.StatusServiceUnavailable, "Не удалось удалить запись install.wim"
		}
		delete(dctx.installWIMsByKey, key)
	}
	if imagePresent && !dctx.uploadEnabled[storageID] {
		if storage.Kind == "smb" {
			if s.SMB == nil {
				return storageName, imagePresent, http.StatusServiceUnavailable, "Служба хранилищ недоступна"
			}
			if err := s.SMB.EnableUpload(r.Context(), storage.Key, storage.Server, storage.Share, storage.Subpath); err != nil {
				_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "iso.delete", "error", fmt.Sprintf("%d %s mount", storageID, imagePath))
				return storageName, imagePresent, http.StatusServiceUnavailable, "Не удалось подключить хранилище для записи. Проверьте права SMB."
			}
			dctx.uploadEnabled[storageID] = true
		}
		if storage.Kind == "nfs" {
			if s.NFS == nil {
				return storageName, imagePresent, http.StatusServiceUnavailable, "Служба хранилищ недоступна"
			}
			if err := s.NFS.EnableNFSUpload(r.Context(), storage.Key, storage.Server, storage.ExportPath, storage.NFSVersion); err != nil {
				_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "iso.delete", "error", fmt.Sprintf("%d %s mount", storageID, imagePath))
				return storageName, imagePresent, http.StatusServiceUnavailable, "Не удалось подключить хранилище для записи. Проверьте права NFS."
			}
			dctx.uploadEnabled[storageID] = true
		}
	}
	if err := s.Images.DeleteImage(r.Context(), storageID, imagePath); err != nil {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "iso.delete", "error", fmt.Sprintf("%d %s storage", storageID, imagePath))
		return storageName, imagePresent, http.StatusServiceUnavailable, "Не удалось удалить ISO. Проверьте доступность хранилища и права на запись."
	}
	delete(dctx.imagesByKey, key)
	if store, ok := s.Store.(interface {
		DeleteImageName(context.Context, int64, string) error
	}); ok {
		_ = store.DeleteImageName(r.Context(), storageID, imagePath)
	}
	if store, ok := s.Store.(interface {
		DeleteImageInspection(context.Context, int64, string) error
	}); ok {
		_ = store.DeleteImageInspection(r.Context(), storageID, imagePath)
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "iso.delete", "ok", fmt.Sprintf("%d %s", storageID, imagePath))
	return storageName, imagePresent, 0, ""
}

func (s *Server) imageDelete(w http.ResponseWriter, r *http.Request) {
	s.menuMu.Lock()
	defer s.menuMu.Unlock()
	s.windowsMu.Lock()
	defer s.windowsMu.Unlock()
	s.smbMu.Lock()
	defer s.smbMu.Unlock()
	s.nfsMu.Lock()
	defer s.nfsMu.Unlock()
	if s.Images == nil {
		http.Error(w, "Сервис образов недоступен", http.StatusServiceUnavailable)
		return
	}
	storageID, err := strconv.ParseInt(r.FormValue("storage_id"), 10, 64)
	if err != nil || storageID <= 0 {
		http.Error(w, "Неверное хранилище", http.StatusBadRequest)
		return
	}
	imagePath := r.FormValue("path")
	dctx, status, errMsg := s.prepareImageDeleteContext(r)
	if status != 0 {
		http.Error(w, errMsg, status)
		return
	}
	storageName, imagePresent, status, errMsg := s.deleteSingleImageItem(r, dctx, storageID, imagePath)
	if status != 0 {
		http.Error(w, errMsg, status)
		return
	}
	target := safeImagesReturnURL(r.PostFormValue("return_to"))
	redirectWithNotice(w, r, target, imageDeletedMessage(imagePath, storageName, imagePresent))
}

func (s *Server) imageDeleteBatch(w http.ResponseWriter, r *http.Request) {
	s.menuMu.Lock()
	defer s.menuMu.Unlock()
	s.windowsMu.Lock()
	defer s.windowsMu.Unlock()
	s.smbMu.Lock()
	defer s.smbMu.Unlock()
	s.nfsMu.Lock()
	defer s.nfsMu.Unlock()
	if s.Images == nil {
		http.Error(w, "Сервис образов недоступен", http.StatusServiceUnavailable)
		return
	}
	_ = r.ParseForm()
	items, err := parseImageBatchItems(r.PostForm["item"])
	if err != nil {
		http.Error(w, "Неверный список образов", http.StatusBadRequest)
		return
	}
	if len(items) == 0 {
		http.Error(w, "Не выбраны образы для удаления", http.StatusBadRequest)
		return
	}
	dctx, status, errMsg := s.prepareImageDeleteContext(r)
	if status != 0 {
		http.Error(w, errMsg, status)
		return
	}
	var deletedTotal, deletedPresent, failedCount int
	var lastPath, lastStorageName string
	var lastPresent bool
	var firstFailStatus int
	var firstFailMsg string
	for _, item := range items {
		stName, present, st, msg := s.deleteSingleImageItem(r, dctx, item.storageID, item.path)
		if st != 0 {
			failedCount++
			if firstFailStatus == 0 {
				firstFailStatus = st
				firstFailMsg = msg
			}
			continue
		}
		deletedTotal++
		if present {
			deletedPresent++
		}
		lastPath = item.path
		lastStorageName = stName
		lastPresent = present
	}
	if deletedTotal == 0 {
		http.Error(w, firstFailMsg, firstFailStatus)
		return
	}
	target := safeImagesReturnURL(r.PostFormValue("return_to"))
	var notice string
	if failedCount > 0 {
		notice = fmt.Sprintf("Удалено образов: %d; не удалось удалить: %d (%s)", deletedTotal, failedCount, firstFailMsg)
	} else if deletedTotal == 1 {
		notice = imageDeletedMessage(lastPath, lastStorageName, lastPresent)
	} else if deletedPresent == 0 {
		notice = fmt.Sprintf("Удалено записей из библиотеки: %d", deletedTotal)
	} else {
		notice = fmt.Sprintf("Удалено образов ISO: %d", deletedTotal)
	}
	redirectWithNotice(w, r, target, notice)
}

// imageDeletedMessage names the deleted ISO and the storage it was removed from.
func imageDeletedMessage(imagePath, storageName string, present bool) string {
	if !present {
		return fmt.Sprintf("Запись образа «%s» удалена из библиотеки", imagePath)
	}
	if storageName == "" {
		return fmt.Sprintf("ISO «%s» удалён", imagePath)
	}
	return fmt.Sprintf("ISO «%s» удалён из хранилища «%s»", imagePath, storageName)
}

//go:embed image_delete.js
var imageDeleteJS []byte

func imageDeleteScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(imageDeleteJS)
}
