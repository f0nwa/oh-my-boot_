package control

import (
	"context"
	"fmt"
	"net/http"
	"omb/internal/agent"
	"omb/internal/images"
	"path"
	"strconv"
	"strings"
)

func applyCatalogStorageStatesToChoices(choices []bootProfileChoice, windows []WindowsProfile, catalog images.Catalog) {
	storagesByID := map[int64]images.Storage{}
	for _, storage := range catalog.Storages {
		storagesByID[storage.ID] = storage
	}
	windowsByID := map[int64]WindowsProfile{}
	for _, w := range windows {
		windowsByID[w.ID] = w
	}
	for i := range choices {
		var storageID int64
		switch choices[i].Kind {
		case BootProfileUbuntuISO, BootProfileUbuntuBuiltin:
			storageID = choices[i].UbuntuStorageID
		case BootProfileWindowsISO, BootProfileWinPEISO:
			if w, ok := windowsByID[choices[i].WindowsProfileID]; ok {
				storageID = w.StorageID
			}
		}
		if storageID <= 0 {
			continue
		}
		storage, ok := storagesByID[storageID]
		if !ok {
			continue
		}
		if choices[i].StorageName == "" {
			choices[i].StorageName = storage.Name
		}
		switch storage.State {
		case "unavailable":
			choices[i].State, choices[i].Error, choices[i].InspectStorageID = "unavailable", "Хранилище недоступно", 0
		case "scanning":
			choices[i].State, choices[i].Error, choices[i].InspectStorageID = "unavailable", "Проверяется хранилище", 0
		case "unknown":
			choices[i].State, choices[i].Error, choices[i].InspectStorageID = "unavailable", "Хранилище не проверено", 0
		}
	}
}

func applyMaintenanceLocksToChoices(choices []bootProfileChoice, windows []WindowsProfile, lookup maintenanceLookup) {
	if len(lookup.locks) == 0 {
		return
	}
	windowsByID := map[int64]WindowsProfile{}
	for _, w := range windows {
		windowsByID[w.ID] = w
	}
	for i := range choices {
		var storageID int64
		var imgPath string
		switch choices[i].Kind {
		case BootProfileUbuntuISO, BootProfileUbuntuBuiltin:
			storageID, imgPath = choices[i].UbuntuStorageID, choices[i].UbuntuPath
		case BootProfileWindowsISO, BootProfileWinPEISO:
			if w, ok := windowsByID[choices[i].WindowsProfileID]; ok {
				storageID, imgPath = w.StorageID, w.Path
			}
		}
		if storageID <= 0 {
			continue
		}
		if _, inherited, locked := lookup.ImageLock(storageID, imgPath); locked {
			msg := "Образ в режиме обслуживания"
			if inherited {
				msg = "Хранилище в режиме обслуживания"
			}
			choices[i].State, choices[i].Error, choices[i].InspectStorageID = "unavailable", msg, 0
		}
	}
}

func (c bootProfileChoice) Selectable() bool {
	return c.State == "ready" || isISOBootKind(c.Kind) && (c.State == "pending" || c.State == "error") || c.Kind == BootProfileUbuntuISO && (c.State == "verified" || c.State == "error")
}

func (s *Server) downloadSourceChoices(ctx context.Context, shared []BootProfile, windows []WindowsProfile, discover bool) ([]bootProfileChoice, error) {
	names, err := s.imageNames(ctx)
	if err != nil {
		return nil, fmt.Errorf("Не удалось прочитать названия образов")
	}
	records, err := s.imageInspections(ctx)
	if err != nil {
		return nil, fmt.Errorf("Не удалось прочитать результаты проверки ISO")
	}
	choices := s.bootProfileChoices(ctx, shared, windows)
	existing := map[string]bool{}
	for i := range choices {
		choice := &choices[i]
		choice.Value = strconv.FormatInt(choice.ID, 10)
		choice.DefaultTitle = choice.Title
		if choice.Path != "" && choice.UbuntuStorageID > 0 {
			choice.Title = path.Base(choice.Path)
			if name := names[imageAuthorKey(choice.UbuntuStorageID, choice.Path)]; name != "" {
				choice.Title = name
			}
		}
		if (choice.Kind == BootProfileUbuntuISO || choice.Kind == BootProfileUbuntuBuiltin) && choice.UbuntuStorageID > 0 && choice.UbuntuPath != "" {
			existing[fmt.Sprintf("%d:%s", choice.UbuntuStorageID, choice.UbuntuPath)] = true
		}
		for _, w := range windows {
			if w.ID == choice.WindowsProfileID && isISOBootKind(choice.Kind) {
				existing[fmt.Sprintf("%d:%s", w.StorageID, w.Path)] = true
				choice.Title = path.Base(w.Path)
				if name := names[imageAuthorKey(w.StorageID, w.Path)]; name != "" {
					choice.Title = name
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
	records = matchingImageInspections(catalog, records)
	applyCatalogStorageStatesToChoices(choices, windows, catalog)
	maintLookup := s.loadMaintenanceLookup(ctx)
	applyMaintenanceLocksToChoices(choices, windows, maintLookup)
	for _, image := range catalog.Images {
		if existing[fmt.Sprintf("%d:%s", image.StorageID, image.Path)] {
			continue
		}
		choice := bootProfileChoice{BootProfile: BootProfile{Title: path.Base(image.Path)}, Value: fmt.Sprintf("iso:%d:%s", image.StorageID, image.Path), Path: image.Path, State: "unverified", Error: "Сначала проверьте ISO", InspectStorageID: image.StorageID}
		if record, ok := records[imageAuthorKey(image.StorageID, image.Path)]; ok {
			switch record.Kind {
			case "windows":
				choice.Kind, choice.DefaultTitle, choice.State, choice.Error, choice.InspectStorageID = BootProfileWindowsISO, "Windows x64", "pending", "", 0
			case "strelec":
				choice.Kind, choice.DefaultTitle, choice.State, choice.Error, choice.InspectStorageID = BootProfileWinPEISO, "Strelec x64", "pending", "", 0
			case "unsupported":
				choice.State, choice.Error = "unsupported", "Поддерживаемая структура ISO не найдена"
			}
		}
		if name := names[imageAuthorKey(image.StorageID, image.Path)]; name != "" {
			choice.Title = name
		}
		for _, storage := range catalog.Storages {
			if storage.ID == image.StorageID {
				choice.StorageName = storage.Name
				if storage.State != "ready" || !(storage.Kind == "smb" || storage.Kind == "nfs" || storage.Kind == "local" && localISOStorage(storage.Root)) {
					choice.State, choice.Error = "unavailable", "Хранилище недоступно"
				}
			}
		}
		if !image.Present {
			choice.State, choice.Error = "unavailable", "Исходный ISO недоступен"
		}
		if choice.State != "unavailable" {
			if _, inherited, locked := maintLookup.ImageLock(image.StorageID, image.Path); locked {
				if inherited {
					choice.State, choice.Error = "unavailable", "Хранилище в режиме обслуживания"
				} else {
					choice.State, choice.Error = "unavailable", "Образ в режиме обслуживания"
				}
			}
		}
		if choice.State == "unavailable" {
			choice.InspectStorageID = 0
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
		if _, inherited, locked := s.loadMaintenanceLookup(r.Context()).ImageLock(sid, parts[2]); locked {
			if inherited {
				return BootProfile{}, WindowsProfile{}, nil, fmt.Errorf("Хранилище в режиме обслуживания")
			}
			return BootProfile{}, WindowsProfile{}, nil, fmt.Errorf("Образ в режиме обслуживания")
		}
		catalog, err := s.Images.Catalog(r.Context())
		if err != nil {
			return BootProfile{}, WindowsProfile{}, nil, fmt.Errorf("Каталог ISO недоступен")
		}
		selected, err := SelectWindowsISO(catalog, sid, parts[2], "ISO", "")
		if err != nil {
			return BootProfile{}, WindowsProfile{}, nil, fmt.Errorf("Исходный ISO недоступен")
		}
		for _, p := range profiles {
			if p.StorageID == sid && p.Path == parts[2] {
				if err := windowsProfileSourceProblem(catalog, p); err != nil {
					return BootProfile{}, WindowsProfile{}, nil, err
				}
				for _, shared := range sources.BootProfiles {
					if shared.WindowsProfileID == p.ID && isISOBootKind(shared.Kind) {
						return shared, p, nil, nil
					}
				}
				return BootProfile{}, WindowsProfile{}, nil, fmt.Errorf("Источник загрузки недоступен")
			}
		}
		records, err := s.imageInspections(r.Context())
		if err != nil {
			return BootProfile{}, WindowsProfile{}, nil, fmt.Errorf("Не удалось прочитать результаты проверки ISO")
		}
		record, ok := currentImageInspection(catalog, records, sid, parts[2])
		if !ok || (record.Kind != "windows" && record.Kind != "strelec") {
			return BootProfile{}, WindowsProfile{}, nil, fmt.Errorf("Сначала проверьте ISO")
		}
		kind, title := BootProfileWindowsISO, "Windows x64"
		if record.Kind == "strelec" {
			kind, title = BootProfileWinPEISO, "Strelec x64"
			selected.BootMode = agent.ISOStrelecMode
		}
		selected.Title = title
		return BootProfile{Kind: kind, Title: title}, WindowsProfile{}, &selected, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return BootProfile{}, WindowsProfile{}, nil, fmt.Errorf("Выберите источник загрузки")
	}
	choices := s.bootProfileChoices(r.Context(), sources.BootProfiles, profiles)
	if r.PathValue("id") == "" {
		if s.Images != nil {
			if catalog, err := s.Images.Catalog(r.Context()); err == nil {
				applyCatalogStorageStatesToChoices(choices, profiles, catalog)
			}
		}
		applyMaintenanceLocksToChoices(choices, profiles, s.loadMaintenanceLookup(r.Context()))
	}
	for _, choice := range choices {
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
			return choice.BootProfile, windows, nil, nil
		}
	}
	return BootProfile{}, WindowsProfile{}, nil, fmt.Errorf("Выберите источник загрузки")
}
