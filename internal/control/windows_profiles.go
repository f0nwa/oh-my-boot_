package control

import (
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"pxe-platform/internal/agent"
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

func (s *Server) windowsProfilePrepare(w http.ResponseWriter, r *http.Request) {
	s.windowsMu.Lock()
	defer s.windowsMu.Unlock()
	if s.Images == nil || s.WindowsISO == nil {
		http.Error(w, "Подготовка Windows ISO недоступна", http.StatusServiceUnavailable)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Неверный профиль", http.StatusBadRequest)
		return
	}
	profile, err := s.Store.WindowsProfile(r.Context(), id)
	if err != nil {
		http.Error(w, "Профиль не найден", http.StatusNotFound)
		return
	}
	catalog, err := s.Images.Catalog(r.Context())
	if err != nil {
		http.Error(w, "Каталог ISO недоступен", http.StatusServiceUnavailable)
		return
	}
	current, err := SelectWindowsISO(catalog, profile.StorageID, profile.Path, profile.Title, profile.Folder)
	if err != nil || current.StorageRoot != profile.StorageRoot || current.Size != profile.Size || !current.Modified.Equal(profile.Modified) {
		_ = s.Store.SetWindowsProfileResult(r.Context(), id, "unavailable", "", "Исходный ISO изменился или недоступен")
		http.Error(w, "Исходный ISO изменился или недоступен; обновите каталог", http.StatusConflict)
		return
	}
	if err := s.Store.SetWindowsProfileResult(r.Context(), id, "preparing", "", ""); err != nil {
		http.Error(w, "Не удалось начать подготовку", http.StatusServiceUnavailable)
		return
	}
	result, err := s.WindowsISO.PrepareWindowsISO(r.Context(), agent.WindowsISORequest{ID: id, StorageRoot: current.StorageRoot, RelativePath: current.Path, Size: current.Size, Modified: current.Modified})
	if err != nil {
		_ = s.Store.SetWindowsProfileResult(r.Context(), id, "error", "", "Не удалось подготовить ISO")
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "windows-profile.prepare", "error", fmt.Sprintf("id=%d", id))
		http.Error(w, "Не удалось подготовить ISO", http.StatusServiceUnavailable)
		return
	}
	if err := s.Store.SetWindowsProfileResult(r.Context(), id, "ready", result.InstallFile, ""); err != nil {
		http.Error(w, "Не удалось сохранить результат подготовки", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "windows-profile.prepare", "ok", fmt.Sprintf("id=%d", id))
	http.Redirect(w, r, "/images?message=ISO+подготовлен", http.StatusSeeOther)
}

// SelectWindowsISO resolves a form selection against the current Images catalog.
// The browser supplies only a storage ID and relative path, never a host path.
func SelectWindowsISO(catalog images.Catalog, storageID int64, imagePath, title, folder string) (WindowsProfileSource, error) {
	if storageID <= 0 || !safeMenuText(title, 80) || !isASCII(title) || validateMenuFolder(folder) != nil || !isASCII(folder) {
		return WindowsProfileSource{}, fmt.Errorf("invalid Windows profile selection")
	}
	if imagePath == "" || path.IsAbs(imagePath) || path.Clean(imagePath) != imagePath || strings.ContainsAny(imagePath, "\\\x00") || !strings.EqualFold(path.Ext(imagePath), ".iso") {
		return WindowsProfileSource{}, fmt.Errorf("invalid ISO path")
	}
	if strings.Contains(strings.ToLower(path.Base(imagePath)), "arm64") {
		return WindowsProfileSource{}, fmt.Errorf("ARM64 ISO is not supported by the x64 profile")
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
	if storage == nil || storage.Kind != "smb" || storage.State != "ready" || !filepath.IsAbs(storage.Root) {
		return WindowsProfileSource{}, fmt.Errorf("ISO storage is unavailable")
	}
	for _, image := range catalog.Images {
		if image.StorageID == storageID && image.Path == imagePath && image.Present && image.Size > 0 {
			return WindowsProfileSource{StorageID: storageID, StorageRoot: storage.Root, Path: image.Path, Size: image.Size, Modified: image.Modified, Title: title, Folder: folder}, nil
		}
	}
	return WindowsProfileSource{}, fmt.Errorf("ISO is not present in the catalog")
}
