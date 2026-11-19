package control

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

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
	if _, _, locked := s.loadMaintenanceLookup(r.Context()).ImageLock(storageID, imagePath); locked {
		http.Error(w, "Хранилище или образ в режиме обслуживания", http.StatusConflict)
		return
	}
	catalog, err := s.Images.Catalog(r.Context())
	if err != nil {
		http.Error(w, "Сервис образов недоступен", http.StatusServiceUnavailable)
		return
	}
	var storageFound, imageFound, imagePresent bool
	var storageKind, storageKey, storageName, server, share, subpath, exportPath, version string
	var storageUploads bool
	for _, storage := range catalog.Storages {
		if storage.ID == storageID {
			storageFound = true
			storageKind, storageKey, storageName = storage.Kind, storage.Key, storage.Name
			server, share, subpath = storage.Server, storage.Share, storage.Subpath
			exportPath, version = storage.ExportPath, storage.NFSVersion
			storageUploads = storage.Uploads
			if storage.State != "ready" {
				http.Error(w, "Хранилище недоступно", http.StatusConflict)
				return
			}
			break
		}
	}
	for _, image := range catalog.Images {
		if image.StorageID == storageID && image.Path == imagePath {
			imageFound = true
			imagePresent = image.Present
			break
		}
	}
	if !storageFound || !imageFound || (storageKind != "local" && storageKind != "mounted" && storageKind != "smb" && storageKind != "nfs") {
		http.Error(w, "ISO не найден", http.StatusNotFound)
		return
	}
	if storageKind == "nfs" && !storageUploads && imagePresent {
		http.Error(w, "Хранилище доступно только для чтения", http.StatusForbidden)
		return
	}
	profiles, err := s.Store.WindowsProfiles(r.Context())
	if err != nil {
		http.Error(w, "Не удалось проверить профили ISO", http.StatusServiceUnavailable)
		return
	}
	sources, err := s.libraryMenuReferences(r.Context())
	if err != nil {
		http.Error(w, "Не удалось проверить загрузки", http.StatusServiceUnavailable)
		return
	}
	if uses := imageDownloadsBySource(profiles, sources)[imageAuthorKey(storageID, imagePath)]; len(uses) > 0 {
		s.rejectDeletion(w, r, "iso.delete", storageID, fmt.Sprintf("used by installation %d", uses[0].ID), fmt.Sprintf("Объект используется загрузкой № %d", uses[0].ID))
		return
	}
	var profileID int64
	for _, profile := range profiles {
		if profile.StorageID == storageID && profile.Path == imagePath {
			profileID = profile.ID
			break
		}
	}
	if profileID > 0 {
		if s.Cleanup == nil {
			http.Error(w, "Очистка файлов недоступна", http.StatusServiceUnavailable)
			return
		}
		active, err := s.activeMenuContains(r, installationComponent{profileID: profileID})
		if err != nil {
			http.Error(w, "Не удалось проверить меню", http.StatusServiceUnavailable)
			return
		}
		if active {
			s.rejectDeletion(w, r, "iso.delete", storageID, "published", "Сначала снимите пункт с публикации меню")
			return
		}
		configs, err := s.Store.DriverConfigs(r.Context())
		if err != nil {
			http.Error(w, "Не удалось проверить конфигурации", http.StatusServiceUnavailable)
			return
		}
		for _, config := range configs {
			if config.ProfileID == profileID {
				s.rejectDeletion(w, r, "iso.delete", storageID, fmt.Sprintf("used by driver config %d", config.ID), fmt.Sprintf("Сначала удалите конфигурацию драйверов № %d", config.ID))
				return
			}
		}
		if err := s.Cleanup.RemoveWindowsProfile(r.Context(), profileID); err != nil {
			_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "iso.delete", "error", fmt.Sprintf("%d %s cleanup", storageID, imagePath))
			http.Error(w, "Не удалось удалить подготовленные файлы", http.StatusServiceUnavailable)
			return
		}
		if err := s.Store.DeleteWindowsProfile(r.Context(), profileID); err != nil {
			_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "iso.delete", "error", fmt.Sprintf("%d %s database", storageID, imagePath))
			http.Error(w, "Не удалось удалить запись профиля", http.StatusServiceUnavailable)
			return
		}
	}
	if imagePresent {
		if storageKind == "smb" {
			if s.SMB == nil {
				http.Error(w, "Служба хранилищ недоступна", http.StatusServiceUnavailable)
				return
			}
			if err := s.SMB.EnableUpload(r.Context(), storageKey, server, share, subpath); err != nil {
				_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "iso.delete", "error", fmt.Sprintf("%d %s mount", storageID, imagePath))
				http.Error(w, "Не удалось подключить хранилище для записи. Проверьте права SMB.", http.StatusServiceUnavailable)
				return
			}
		}
		if storageKind == "nfs" {
			if s.NFS == nil {
				http.Error(w, "Служба хранилищ недоступна", http.StatusServiceUnavailable)
				return
			}
			if err := s.NFS.EnableNFSUpload(r.Context(), storageKey, server, exportPath, version); err != nil {
				_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "iso.delete", "error", fmt.Sprintf("%d %s mount", storageID, imagePath))
				http.Error(w, "Не удалось подключить хранилище для записи. Проверьте права NFS.", http.StatusServiceUnavailable)
				return
			}
		}
	}
	if err := s.Images.DeleteImage(r.Context(), storageID, imagePath); err != nil {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "iso.delete", "error", fmt.Sprintf("%d %s storage", storageID, imagePath))
		http.Error(w, "Не удалось удалить ISO. Проверьте доступность хранилища и права на запись.", http.StatusServiceUnavailable)
		return
	}
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
	http.Redirect(w, r, "/images?message="+url.QueryEscape(imageDeletedMessage(imagePath, storageName, imagePresent)), http.StatusSeeOther)
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
