package control

import (
	"context"
	"fmt"
	"net/http"
	"omb/internal/agent"
	"path"
	"strconv"
	"strings"
)

// Names only provide a hint. The mounted image is validated by Agent before
// any source is considered ready; an arbitrary ISO is never booted directly.
func isoKindHint(name string) (kind, problem string) {
	name = strings.ToLower(path.Base(name))
	switch {
	case strings.Contains(name, "arm64"):
		return "ARM64", "ARM64 пока не поддерживается"
	case strings.HasPrefix(name, "ubuntu"):
		return "Ubuntu", "Проверьте ISO для Ubuntu в библиотеке образов"
	case strings.Contains(name, "strelec"):
		return "WinPE Strelec", ""
	case strings.HasPrefix(name, "winpe"):
		return "WinPE", "Для этого образа WinPE пока нет сценария загрузки"
	default:
		return "ISO", ""
	}
}

func (c bootProfileChoice) Selectable() bool {
	return c.State == "ready" || isISOBootKind(c.Kind) && (c.State == "pending" || c.State == "error") || c.Kind == BootProfileUbuntuISO && (c.State == "verified" || c.State == "error")
}

func (s *Server) downloadSourceChoices(ctx context.Context, shared []BootProfile, windows []WindowsProfile, discover bool) ([]bootProfileChoice, error) {
	choices := s.bootProfileChoices(ctx, shared, windows)
	existing := map[string]bool{}
	for i := range choices {
		choice := &choices[i]
		choice.Value = strconv.FormatInt(choice.ID, 10)
		choice.DefaultTitle = choice.Title
		if choice.Kind == BootProfileUbuntuISO {
			existing[fmt.Sprintf("%d:%s", choice.UbuntuStorageID, choice.UbuntuPath)] = true
		}
		for _, w := range windows {
			if w.ID == choice.WindowsProfileID && isISOBootKind(choice.Kind) {
				existing[fmt.Sprintf("%d:%s", w.StorageID, w.Path)] = true
				if _, problem := isoKindHint(w.Path); problem != "" {
					choice.State, choice.Error = "unsupported", problem
				}
			}
		}
	}
	if !discover || s.Images == nil {
		return choices, nil
	}
	catalog, err := s.Images.Catalog(ctx)
	if err != nil {
		return nil, fmt.Errorf("Каталог ISO недоступен")
	}
	for i := range choices {
		if choices[i].Kind != BootProfileUbuntuISO {
			continue
		}
		for _, storage := range catalog.Storages {
			if storage.ID == choices[i].UbuntuStorageID {
				choices[i].StorageName = storage.Name
				break
			}
		}
	}
	for _, image := range catalog.Images {
		if existing[fmt.Sprintf("%d:%s", image.StorageID, image.Path)] {
			continue
		}
		kind, title := isoSourceKind(image.Path)
		choice := bootProfileChoice{BootProfile: BootProfile{Kind: kind, Title: path.Base(image.Path)}, DefaultTitle: title, Value: fmt.Sprintf("iso:%d:%s", image.StorageID, image.Path), Path: image.Path, State: "pending"}
		for _, storage := range catalog.Storages {
			if storage.ID == image.StorageID {
				choice.StorageName = storage.Name
				if storage.State != "ready" || !(storage.Kind == "smb" || storage.Kind == "local" && localISOStorage(storage.Root)) {
					choice.State, choice.Error = "unavailable", "Хранилище недоступно"
				}
			}
		}
		if !image.Present {
			choice.State, choice.Error = "unavailable", "Исходный ISO недоступен"
		}
		if _, problem := isoKindHint(image.Path); problem != "" {
			choice.State, choice.Error = "unsupported", problem
		}
		choices = append(choices, choice)
	}
	return choices, nil
}

// resolveDownloadSource has no side effects: invalid form options must not
// create internal source records or start mounting images.
func (s *Server) resolveDownloadSource(r *http.Request, sources InstallationMenuSources, profiles []WindowsProfile) (BootProfile, WindowsProfile, *WindowsProfileSource, error) {
	raw := r.FormValue("boot_profile_id")
	if strings.HasPrefix(raw, "iso:") {
		parts := strings.SplitN(raw, ":", 3)
		if len(parts) != 3 || s.Images == nil {
			return BootProfile{}, WindowsProfile{}, nil, fmt.Errorf("Выберите исходный ISO")
		}
		sid, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return BootProfile{}, WindowsProfile{}, nil, fmt.Errorf("Выберите исходный ISO")
		}
		if _, problem := isoKindHint(parts[2]); problem != "" {
			return BootProfile{}, WindowsProfile{}, nil, fmt.Errorf("%s", problem)
		}
		catalog, err := s.Images.Catalog(r.Context())
		if err != nil {
			return BootProfile{}, WindowsProfile{}, nil, fmt.Errorf("Каталог ISO недоступен")
		}
		kind, title := isoSourceKind(parts[2])
		selected, err := SelectWindowsISO(catalog, sid, parts[2], title, "")
		if err != nil {
			return BootProfile{}, WindowsProfile{}, nil, fmt.Errorf("Исходный ISO недоступен")
		}
		for _, p := range profiles {
			if p.StorageID == sid && p.Path == parts[2] {
				for _, shared := range sources.BootProfiles {
					if shared.WindowsProfileID == p.ID && isISOBootKind(shared.Kind) {
						return shared, p, nil, nil
					}
				}
				return BootProfile{}, WindowsProfile{}, nil, fmt.Errorf("Источник загрузки недоступен")
			}
		}
		return BootProfile{Kind: kind, Title: title}, WindowsProfile{}, &selected, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return BootProfile{}, WindowsProfile{}, nil, fmt.Errorf("Выберите источник загрузки")
	}
	for _, choice := range s.bootProfileChoices(r.Context(), sources.BootProfiles, profiles) {
		if choice.ID == id {
			if !choice.Selectable() && r.PathValue("id") == "" {
				return BootProfile{}, WindowsProfile{}, nil, fmt.Errorf("Источник загрузки недоступен: %s", choice.Error)
			}
			var windows WindowsProfile
			for _, p := range profiles {
				if p.ID == choice.WindowsProfileID && isISOBootKind(choice.Kind) {
					windows = p
				}
			}
			if isISOBootKind(choice.Kind) {
				if _, problem := isoKindHint(windows.Path); problem != "" {
					return BootProfile{}, WindowsProfile{}, nil, fmt.Errorf("%s", problem)
				}
			}
			return choice.BootProfile, windows, nil, nil
		}
	}
	return BootProfile{}, WindowsProfile{}, nil, fmt.Errorf("Выберите источник загрузки")
}

func isoBootMode(name string) string {
	if strings.Contains(strings.ToLower(path.Base(name)), "strelec") {
		return agent.ISOStrelecMode
	}
	return ""
}
func isoSourceKind(name string) (string, string) {
	if isoBootMode(name) == agent.ISOStrelecMode {
		return BootProfileWinPEISO, "Strelec x64"
	}
	return BootProfileWindowsISO, "Windows x64"
}
