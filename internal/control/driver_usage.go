package control

import (
	"context"
	"path"
)

type driverUse struct {
	ID                              int64
	Title                           string
	Exists, Saved, Draft, Published bool
}

type driverUsage struct {
	Available    bool
	Error        string
	Configs      map[int64][]driverUse
	Packages     map[string][]int64
	Profiles     map[int64]string
	ProfilePaths map[int64]string
}

// loadDriverUsage includes frozen menu bindings: editing a saved download does
// not change the driver configuration in the currently published menu.
func (s *Server) loadDriverUsage(ctx context.Context, profiles []WindowsProfile, configs []DriverConfig) driverUsage {
	items, err := s.Store.Installations(ctx)
	if err != nil {
		return driverUsage{Error: "Не удалось проверить использование"}
	}
	draft, active, err := s.currentDraft(ctx)
	if err != nil {
		return driverUsage{Error: "Не удалось проверить использование"}
	}
	return buildDriverUsage(profiles, configs, items, draft, active)
}

func buildDriverUsage(profiles []WindowsProfile, configs []DriverConfig, items []Installation, draft, active MenuDraft) driverUsage {
	result := driverUsage{Available: true, Configs: map[int64][]driverUse{}, Packages: map[string][]int64{}, Profiles: map[int64]string{}, ProfilePaths: map[int64]string{}}
	for _, p := range profiles {
		result.Profiles[p.ID] = p.Title
		if p.Path != "" {
			result.Profiles[p.ID] = path.Base(p.Path)
			result.ProfilePaths[p.ID] = p.Path
		}
	}
	for _, c := range configs {
		seen := map[string]bool{}
		for _, inf := range c.PackageINFs {
			if !seen[inf.PackageSHA256] {
				result.Packages[inf.PackageSHA256] = append(result.Packages[inf.PackageSHA256], c.ID)
				seen[inf.PackageSHA256] = true
			}
		}
	}
	byID := make(map[int64]Installation, len(items))
	type refKey struct{ configID, installationID int64 }
	positions := map[refKey]int{}
	add := func(configID int64, use driverUse) {
		if configID <= 0 {
			return
		}
		key := refKey{configID, use.ID}
		if i, ok := positions[key]; ok {
			old := &result.Configs[configID][i]
			old.Saved = old.Saved || use.Saved
			old.Draft = old.Draft || use.Draft
			old.Published = old.Published || use.Published
			return
		}
		positions[key] = len(result.Configs[configID])
		result.Configs[configID] = append(result.Configs[configID], use)
	}
	for _, item := range items {
		byID[item.ID] = item
		add(item.DriverConfigID, driverUse{ID: item.ID, Title: item.Title, Exists: true, Saved: true})
	}
	for _, menu := range []struct {
		draft     MenuDraft
		published bool
	}{{draft, false}, {active, true}} {
		for _, placement := range menu.draft.Items {
			item, exists := byID[placement.InstallationID]
			configID := item.DriverConfigID
			if placement.Components != nil {
				configID = placement.Components.DriverConfigID
			}
			title := item.Title
			if title == "" {
				title = placement.Title
			}
			add(configID, driverUse{ID: placement.InstallationID, Title: title, Exists: exists, Draft: !menu.published, Published: menu.published})
		}
	}
	return result
}

func (c DriverConfig) StateLabel() string {
	switch c.State {
	case "ready":
		return "Готова"
	case "pending":
		return "Нужна подготовка"
	case "preparing":
		return "Подготавливается"
	case "error":
		return "Ошибка подготовки"
	default:
		return "Недоступно"
	}
}
