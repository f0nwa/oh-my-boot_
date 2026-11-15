package control

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"omb/internal/agent"
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
	s.loadAnswerChecks(r.Context(), &data)

	data.Query = strings.TrimSpace(r.URL.Query().Get("q"))
	if data.Query != "" {
		q := strings.ToLower(data.Query)
		profileNames := map[int64]string{}
		for _, p := range data.WindowsProfiles {
			profileNames[p.ID] = p.Title
		}
		filteredWin := []WindowsAnswerVariant{}
		for _, ans := range data.WindowsAnswerVariants {
			imageName := profileNames[ans.ProfileID]
			disk := ans.DiskSummary()
			haystack := strings.ToLower(ans.Title + " " + imageName + " " + ans.Author + " " + disk.Target + " " + disk.Title() + " " + ans.SHA256)
			if strings.Contains(haystack, q) || searchMatchSHA(q, ans.SHA256) {
				filteredWin = append(filteredWin, ans)
			}
		}
		data.WindowsAnswerVariants = filteredWin

		filteredUbuntu := []UbuntuAutoinstallSummary{}
		for _, file := range data.UbuntuAutoinstalls {
			disk := file.DiskSummary()
			haystack := strings.ToLower(file.Title + " " + file.Author + " " + disk.Target + " " + disk.Title() + " " + file.SHA256)
			if strings.Contains(haystack, q) || searchMatchSHA(q, file.SHA256) {
				filteredUbuntu = append(filteredUbuntu, file)
			}
		}
		data.UbuntuAutoinstalls = filteredUbuntu
	}

	return data
}

func (s *Server) imagesLibraryData(r *http.Request) pageData {
	data := s.profileLibraryData(r)
	data.Query = strings.TrimSpace(r.URL.Query().Get("q"))
	data.Tab = r.URL.Query().Get("show")
	data.ImageCounts = map[string]int{}
	catalog := s.loadLibraryCatalog(r.Context(), &data)
	if catalog == nil {
		return data
	}
	data.HasAnyISOImages = len(catalog.Images) > 0
	authors := s.resourceAuthors(r.Context())
	names := map[int64]string{}
	for _, storage := range catalog.Storages {
		names[storage.ID] = storage.Name
	}
	sources, usageErr := s.libraryMenuReferences(r.Context())
	uses := imageDownloadsBySource(data.WindowsProfiles, sources)
	if usageErr != nil {
		data.Error = "Не удалось проверить загрузки"
	}
	var diskReport *agent.DiskUsageReport
	diskAPI := s.DiskUsage
	if diskAPI == nil && s.Agent != nil {
		diskAPI = s.Agent
	}
	if diskAPI != nil {
		if rep, err := diskAPI.DiskUsage(r.Context()); err == nil {
			diskReport = &rep
		}
	}
	ubuntuSourceIDs := map[string][]int64{}
	seenUbuntuSource := map[string]map[int64]bool{}
	for _, bp := range sources.BootProfiles {
		if (bp.Kind == BootProfileUbuntuISO || bp.Kind == BootProfileUbuntuBuiltin) && bp.UbuntuStorageID > 0 && bp.UbuntuPath != "" && bp.UbuntuSourceID > 0 {
			key := imageAuthorKey(bp.UbuntuStorageID, bp.UbuntuPath)
			if seenUbuntuSource[key] == nil {
				seenUbuntuSource[key] = map[int64]bool{}
			}
			if !seenUbuntuSource[key][bp.UbuntuSourceID] {
				seenUbuntuSource[key][bp.UbuntuSourceID] = true
				ubuntuSourceIDs[key] = append(ubuntuSourceIDs[key], bp.UbuntuSourceID)
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
		row.Downloads = uses[key]
		if usageErr != nil {
			row.DeleteBlockedReason = "Не удалось проверить загрузки"
		} else if len(row.Downloads) > 0 {
			row.DeleteBlockedReason = "Образ используется загрузками"
		}
		if p, ok := byPath[key]; ok {
			if row.DeleteBlockedReason == "" {
				row.CleanupProfileID = p.ID
			}
			if diskReport != nil && diskReport.WindowsProfiles[p.ID] > 0 {
				row.ServerBytes += diskReport.WindowsProfiles[p.ID]
				row.ServerNote = "boot.wim"
			}
		}
		if diskReport != nil {
			for _, srcID := range ubuntuSourceIDs[key] {
				if b := diskReport.UbuntuSources[srcID]; b > 0 {
					row.ServerBytes += b
					if row.ServerNote == "" {
						row.ServerNote = "источник Ubuntu"
					}
				}
			}
		}
		if data.Query != "" && !imageMatchesQuery(row, data.Query) {
			continue
		}
		data.ImageCounts["all"]++
		if len(row.Downloads) > 0 {
			data.ImageCounts["used"]++
		} else {
			data.ImageCounts["free"]++
		}
		if !row.Image.Present || row.Problem != "" {
			data.ImageCounts["attention"]++
		}
		if row.MatchesTab(data.Tab) {
			data.ISOImages = append(data.ISOImages, row)
		}
	}
	return data
}

func imageMatchesQuery(row imageRow, query string) bool {
	q := strings.ToLower(query)
	parts := []string{
		row.DisplayName,
		row.Image.Path,
		row.Kind,
		translate(langEN, row.Kind),
		row.Arch,
		row.StorageName,
		row.Author,
		row.AddedVia,
		translate(langEN, row.AddedVia),
		row.Problem,
	}
	for _, d := range row.Downloads {
		parts = append(parts, d.Title)
	}
	return strings.Contains(strings.ToLower(strings.Join(parts, " ")), q)
}
