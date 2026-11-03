package control

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"

	"omb/internal/agent"
	"omb/internal/images"
)

type WindowsProfileSource struct {
	BootMode    string
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
	if storage == nil || (storage.Kind != "smb" && !(storage.Kind == "local" && localISOStorage(storage.Root))) || storage.State != "ready" || !filepath.IsAbs(storage.Root) {
		return WindowsProfileSource{}, fmt.Errorf("ISO storage is unavailable")
	}
	for _, image := range catalog.Images {
		if image.StorageID == storageID && image.Path == imagePath && image.Present && image.Size > 0 {
			return WindowsProfileSource{BootMode: isoBootMode(image.Path), StorageID: storageID, StorageRoot: storage.Root, Path: image.Path, Size: image.Size, Modified: image.Modified, Title: title, Folder: folder}, nil
		}
	}
	return WindowsProfileSource{}, fmt.Errorf("ISO is not present in the catalog")
}

// windowsProfileSourceProblem explains why a prepared profile's source ISO
// cannot be used now: its storage is missing or unavailable, the ISO is gone,
// or the ISO differs from the prepared one. It returns nil when it matches.
func windowsProfileSourceProblem(catalog images.Catalog, profile WindowsProfile) error {
	var storage *images.Storage
	for i := range catalog.Storages {
		if catalog.Storages[i].ID == profile.StorageID {
			storage = &catalog.Storages[i]
			break
		}
	}
	if storage == nil {
		return fmt.Errorf("хранилище профиля %d не найдено", profile.ID)
	}
	switch storage.State {
	case "ready":
	case "scanning":
		return fmt.Errorf("хранилище «%s» сканируется, повторите позже", storage.Name)
	default:
		reason := strings.TrimPrefix(storage.Error, "Хранилище недоступно: ")
		if reason == "" {
			return fmt.Errorf("хранилище «%s» недоступно", storage.Name)
		}
		return fmt.Errorf("хранилище «%s» недоступно: %s", storage.Name, reason)
	}
	current, err := SelectWindowsISO(catalog, profile.StorageID, profile.Path, profile.Title, profile.Folder)
	if err != nil {
		return fmt.Errorf("исходный ISO профиля %d не найден в хранилище «%s»", profile.ID, storage.Name)
	}
	if current.BootMode != profile.BootMode || current.StorageRoot != profile.StorageRoot || current.Size != profile.Size || !current.Modified.Equal(profile.Modified) {
		return fmt.Errorf("исходный ISO профиля %d изменился", profile.ID)
	}
	return nil
}

// localISOStorage reports whether a local storage lies where the Agent may
// mount profile ISOs from.
func localISOStorage(root string) bool {
	rel, err := filepath.Rel(agent.LocalISORoot, filepath.Clean(root))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../") && filepath.IsAbs(root)
}

func capitalizeFirst(text string) string {
	for i, r := range text {
		return strings.ToUpper(string(r)) + text[i+len(string(r)):]
	}
	return text
}
