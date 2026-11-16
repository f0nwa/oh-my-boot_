package control

import (
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
	catalog, err := s.Images.Catalog(r.Context())
	if err != nil {
		http.Error(w, "Сервис образов недоступен", http.StatusServiceUnavailable)
		return
	}
	var storageFound, imageFound bool
	var storageKind, storageKey, server, share, subpath string
	for _, storage := range catalog.Storages {
		if storage.ID == storageID {
			storageFound = true
			storageKind, storageKey = storage.Kind, storage.Key
			server, share, subpath = storage.Server, storage.Share, storage.Subpath
			if storage.State != "ready" {
				http.Error(w, "Хранилище недоступно", http.StatusConflict)
				return
			}
			break
		}
	}
	for _, image := range catalog.Images {
		if image.StorageID == storageID && image.Path == imagePath && image.Present {
			imageFound = true
			break
		}
	}
	if !storageFound || !imageFound || (storageKind != "local" && storageKind != "mounted" && storageKind != "smb") {
		http.Error(w, "ISO не найден", http.StatusNotFound)
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
	if err := s.Images.DeleteImage(r.Context(), storageID, imagePath); err != nil {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "iso.delete", "error", fmt.Sprintf("%d %s storage", storageID, imagePath))
		http.Error(w, "Не удалось удалить ISO. Проверьте доступность хранилища и права на запись.", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "iso.delete", "ok", fmt.Sprintf("%d %s", storageID, imagePath))
	http.Redirect(w, r, "/images?message="+url.QueryEscape("ISO удалён"), http.StatusSeeOther)
}

//go:embed image_delete.js
var imageDeleteJS []byte

func imageDeleteScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(imageDeleteJS)
}
