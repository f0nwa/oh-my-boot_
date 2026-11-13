package control

import "context"

type answerFileDownload struct {
	ID    int64
	Title string
}
type answerFileUses struct {
	Downloads []answerFileDownload
	Published int
}
type answerFileUsage struct {
	DownloadsKnown, MenuKnown bool
	Windows, Ubuntu           map[int64]answerFileUses
}

// buildAnswerFileUsage separates saved downloads from frozen published bindings.
// Windows XML used by a driver configuration belongs to that configuration.
func buildAnswerFileUsage(items []Installation, configs []DriverConfig, active MenuDraft) answerFileUsage {
	result := answerFileUsage{DownloadsKnown: true, MenuKnown: true, Windows: map[int64]answerFileUses{}, Ubuntu: map[int64]answerFileUses{}}
	byID := make(map[int64]Installation, len(items))
	drivers := make(map[int64]int64, len(configs))
	for _, c := range configs {
		drivers[c.ID] = c.AnswerID
	}
	add := func(item Installation, published bool) {
		answerID := item.AnswerID
		if item.DriverConfigID > 0 {
			answerID = drivers[item.DriverConfigID]
		}
		for _, ref := range []struct {
			id     int64
			target map[int64]answerFileUses
		}{{answerID, result.Windows}, {item.AutoinstallID, result.Ubuntu}} {
			if ref.id <= 0 {
				continue
			}
			use := ref.target[ref.id]
			if published {
				use.Published++
			} else {
				use.Downloads = append(use.Downloads, answerFileDownload{ID: item.ID, Title: item.Title})
			}
			ref.target[ref.id] = use
		}
	}
	for _, item := range items {
		byID[item.ID] = item
		add(item, false)
	}
	for _, placement := range active.Items {
		item := placement.Components.apply(byID[placement.InstallationID])
		add(item, true)
	}
	return result
}

func (s *Server) loadAnswerFileUsage(ctx context.Context) answerFileUsage {
	items, itemsErr := s.Store.Installations(ctx)
	configs, configsErr := s.Store.DriverConfigs(ctx)
	versions, menuErr := s.Store.MenuVersions(ctx)
	var active MenuDraft
	if menuErr == nil {
		for _, v := range versions {
			if v.Active {
				active = draftFromVersion(v)
				break
			}
		}
	}
	result := buildAnswerFileUsage(items, configs, active)
	result.DownloadsKnown = itemsErr == nil && configsErr == nil
	result.MenuKnown = result.DownloadsKnown && menuErr == nil
	return result
}
