package control

import (
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"pxe-platform/internal/images"
)

type WindowsProfileSource struct {
	StorageID   int64
	StorageRoot string
	Path        string
	Size        int64
	Modified    time.Time
	Title       string
	Folder      string
}

type WindowsProfile struct {
	ID int64
	WindowsProfileSource
	InstallFile string
	State       string
	Error       string
}

func (s *Server) windowsProfileCreate(w http.ResponseWriter, r *http.Request) {
	if s.Images == nil {
		http.Error(w, "Сервис образов недоступен", http.StatusServiceUnavailable)
		return
	}
	storageID, err := strconv.ParseInt(r.FormValue("storage_id"), 10, 64)
	if err != nil {
		http.Error(w, "Неверное хранилище", http.StatusBadRequest)
		return
	}
	catalog, err := s.Images.Catalog(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать каталог ISO", http.StatusServiceUnavailable)
		return
	}
	source, err := SelectWindowsISO(catalog, storageID, r.FormValue("path"), r.FormValue("title"), r.FormValue("folder"))
	if err != nil {
		http.Error(w, "Нельзя выбрать ISO: "+err.Error(), http.StatusBadRequest)
		return
	}
	profile, err := s.Store.CreateWindowsProfile(r.Context(), source)
	if err != nil {
		http.Error(w, "Не удалось создать профиль ISO", http.StatusConflict)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "windows-profile.create", "ok", fmt.Sprintf("id=%d storage=%d", profile.ID, source.StorageID))
	http.Redirect(w, r, "/images?message=Профиль+создан", http.StatusSeeOther)
}

// SelectWindowsISO resolves a form selection against the current Images catalog.
// The browser supplies only a storage ID and relative path, never a host path.
func SelectWindowsISO(catalog images.Catalog, storageID int64, imagePath, title, folder string) (WindowsProfileSource, error) {
	if storageID <= 0 || !safeMenuText(title, 80) || validateMenuFolder(folder) != nil {
		return WindowsProfileSource{}, fmt.Errorf("invalid Windows profile selection")
	}
	if imagePath == "" || path.IsAbs(imagePath) || path.Clean(imagePath) != imagePath || strings.ContainsAny(imagePath, "\\\x00") || !strings.EqualFold(path.Ext(imagePath), ".iso") {
		return WindowsProfileSource{}, fmt.Errorf("invalid ISO path")
	}
	for _, component := range strings.Split(imagePath, "/") {
		if component == "" || component == "." || component == ".." {
			return WindowsProfileSource{}, fmt.Errorf("invalid ISO path")
		}
	}
	var storage *images.Storage
	for i := range catalog.Storages {
		if catalog.Storages[i].ID == storageID {
			storage = &catalog.Storages[i]
			break
		}
	}
	if storage == nil || storage.State != "ready" || !filepath.IsAbs(storage.Root) {
		return WindowsProfileSource{}, fmt.Errorf("ISO storage is unavailable")
	}
	for _, image := range catalog.Images {
		if image.StorageID == storageID && image.Path == imagePath && image.Present && image.Size > 0 {
			return WindowsProfileSource{StorageID: storageID, StorageRoot: storage.Root, Path: image.Path, Size: image.Size, Modified: image.Modified, Title: title, Folder: folder}, nil
		}
	}
	return WindowsProfileSource{}, fmt.Errorf("ISO is not present in the catalog")
}
