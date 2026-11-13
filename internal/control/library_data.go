package control

import (
	"context"
	"fmt"
	"net/http"

	"omb/internal/images"
)

func libraryPageData(r *http.Request) pageData {
	return pageData{Info: requestInfo(r), Message: r.URL.Query().Get("message"), Error: pageError(r)}
}

// profileLibraryData is enough for USB: the prepared profiles, without the
// source ISO catalog or unrelated library resources.
func (s *Server) profileLibraryData(r *http.Request) pageData {
	data := libraryPageData(r)
	profiles, err := s.Store.WindowsProfiles(r.Context())
	if err != nil {
		data.ImageError = "Не удалось прочитать профили Windows ISO"
	} else {
		data.WindowsProfiles = profiles
	}
	return data
}

func (s *Server) loadLibraryCatalog(ctx context.Context, data *pageData) *images.Catalog {
	if s.Images != nil {
		if catalog, err := s.Images.Catalog(ctx); err == nil {
			data.Storages = catalog.Storages
			data.catalogCache = &catalog
			return &catalog
		}
	}
	data.ImageError = "Сервис образов недоступен"
	return nil
}

// storageLibraryData supplies storage status and the raw catalog used by
// occupancy calculations. The home page uses the same profiles and statuses
// for alerts; neither page needs ISO rows, answer files or authors.
func (s *Server) storageLibraryData(r *http.Request) pageData {
	data := s.profileLibraryData(r)
	s.loadLibraryCatalog(r.Context(), &data)
	s.loadStorageRecovery(r.Context(), &data)
	return data
}

// libraryMenuReferences omits Autoinstall contents: usage links only depend on
// installation and boot-profile identities.
func (s *Server) libraryMenuReferences(ctx context.Context) (InstallationMenuSources, error) {
	items, err := s.Store.Installations(ctx)
	if err != nil {
		return InstallationMenuSources{}, err
	}
	profiles, err := s.Store.BootProfiles(ctx)
	if err != nil {
		return InstallationMenuSources{}, err
	}
	return InstallationMenuSources{Installations: items, BootProfiles: profiles}, nil
}

func (s *Server) answersLibraryData(r *http.Request) pageData {
	data := s.profileLibraryData(r)
	authors := s.resourceAuthors(r.Context())
	answers, err := s.Store.WindowsAnswers(r.Context())
	if err != nil {
		data.ImageError = "Не удалось прочитать сценарии Windows"
	} else {
		data.WindowsAnswerVariants = answers
		for i := range data.WindowsAnswerVariants {
			data.WindowsAnswerVariants[i].Author = authors.Answers[data.WindowsAnswerVariants[i].ID]
		}
	}
	files, err := s.Store.UbuntuAutoinstalls(r.Context())
	if err != nil {
		data.ImageError = "Не удалось прочитать файлы Autoinstall"
	} else {
		data.UbuntuAutoinstalls = files
		for i := range data.UbuntuAutoinstalls {
			data.UbuntuAutoinstalls[i].Author = authors.UbuntuAnswers[data.UbuntuAutoinstalls[i].ID]
		}
	}
	data.AnswerFileUsage = s.loadAnswerFileUsage(r.Context())

	return data
}

func (s *Server) imagesLibraryData(r *http.Request) pageData {
	data := s.profileLibraryData(r)
	catalog := s.loadLibraryCatalog(r.Context(), &data)
	if catalog == nil {
		return data
	}
	authors := s.resourceAuthors(r.Context())
	names := map[int64]string{}
	for _, storage := range catalog.Storages {
		names[storage.ID] = storage.Name
	}
	uses := map[int64][]imageDownload{}
	ubuntuUses := map[string][]imageDownload{}
	if sources, e := s.libraryMenuReferences(r.Context()); e == nil {
		byID := map[int64]int64{}
		ubuntuByID := map[int64]string{}
		for _, p := range sources.BootProfiles {
			if isISOBootKind(p.Kind) {
				byID[p.ID] = p.WindowsProfileID
			}
			if (p.Kind == BootProfileUbuntuISO || p.Kind == BootProfileUbuntuBuiltin) && p.UbuntuStorageID > 0 && p.UbuntuPath != "" {
				key := fmt.Sprintf("%d:%s", p.UbuntuStorageID, p.UbuntuPath)
				ubuntuByID[p.ID] = key
			}
		}
		for _, item := range sources.Installations {
			if id := byID[item.BootProfileID]; id > 0 {
				uses[id] = append(uses[id], imageDownload{ID: item.ID, Title: item.Title})
			}
			if key := ubuntuByID[item.BootProfileID]; key != "" {
				ubuntuUses[key] = append(ubuntuUses[key], imageDownload{ID: item.ID, Title: item.Title})
			}
		}
	}
	namesByImage, namesErr := s.imageNames(r.Context())
	if namesErr != nil {
		data.Error = "Не удалось прочитать названия образов"
	}
	inspections, inspectionsErr := s.imageInspections(r.Context())
	if inspectionsErr != nil {
		data.Error = "Не удалось прочитать результаты проверки ISO"
	}
	inspections = matchingImageInspections(*catalog, inspections)
	byPath := map[string]WindowsProfile{}
	for _, p := range data.WindowsProfiles {
		byPath[fmt.Sprintf("%d:%s", p.StorageID, p.Path)] = p
	}
	for _, image := range catalog.Images {
		key := imageAuthorKey(image.StorageID, image.Path)
		kind, problem, arch := inspectionLabel("")
		if record, ok := inspections[key]; ok {
			kind, problem, arch = inspectionLabel(record.Kind)
		}
		row := imageRow{Image: image, StorageName: names[image.StorageID], Author: authors.Images[imageAuthorKey(image.StorageID, image.Path)], Kind: kind, Problem: problem, DisplayName: namesByImage[key], Arch: arch}
		if uploaded, ok := authors.ImageUploadedAt[key]; ok && !uploaded.IsZero() {
			row.AddedAt, row.AddedVia = &uploaded, "Загружен"
		} else if image.FirstSeenAt != nil {
			row.AddedAt, row.AddedVia = image.FirstSeenAt, "Обнаружен"
		}
		row.Downloads = append(row.Downloads, ubuntuUses[key]...)
		if p, ok := byPath[key]; ok {
			row.Downloads = append(row.Downloads, uses[p.ID]...)
			if len(row.Downloads) == 0 {
				row.CleanupProfileID = p.ID
			}
		}
		data.ISOImages = append(data.ISOImages, row)
	}
	return data
}
