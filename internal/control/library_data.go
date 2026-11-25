package control

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

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
		catalog, err := s.Images.Catalog(ctx)
		if err == nil {
			data.Storages = catalog.Storages
			data.catalogCache = &catalog
			return &catalog
		}
		log.Printf("load library catalog: %v", err)
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
	data.Sort = r.URL.Query().Get("sort")
	data.Dir = r.URL.Query().Get("dir")
	if data.Query != "" {
		q := strings.ToLower(data.Query)
		filteredWin := []WindowsAnswerVariant{}
		for _, ans := range data.WindowsAnswerVariants {
			disk := ans.DiskSummary()
			chips := strings.Join(ans.Target.Chips(), " ")
			haystack := strings.ToLower(ans.Title + " " + chips + " " + ans.Author + " " + disk.Target + " " + disk.Title() + " " + ans.SHA256)
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
	if data.Sort != "" {
		sortWindowsAnswers(data.WindowsAnswerVariants, data.AnswerFileUsage, data.Sort, data.Dir)
		sortUbuntuAutoinstalls(data.UbuntuAutoinstalls, data.AnswerFileUsage, data.Sort, data.Dir)
	}

	return data
}

func sortWindowsAnswers(rows []WindowsAnswerVariant, usage answerFileUsage, sortBy, dir string) {
	desc := dir == "desc"
	switch sortBy {
	case "title":
		sort.SliceStable(rows, func(i, j int) bool {
			if c := compareStrings(rows[i].Title, rows[j].Title, desc); c != 0 {
				return c < 0
			}
			return rows[i].ID < rows[j].ID
		})
	case "target":
		sort.SliceStable(rows, func(i, j int) bool {
			tI := strings.Join(rows[i].Target.Chips(), " ")
			tJ := strings.Join(rows[j].Target.Chips(), " ")
			if c := compareStrings(tI, tJ, desc); c != 0 {
				return c < 0
			}
			return compareStrings(rows[i].Title, rows[j].Title, false) < 0
		})
	case "author":
		sort.SliceStable(rows, func(i, j int) bool {
			if c := compareStrings(rows[i].Author, rows[j].Author, desc); c != 0 {
				return c < 0
			}
			return compareStrings(rows[i].Title, rows[j].Title, false) < 0
		})
	case "created":
		sort.SliceStable(rows, func(i, j int) bool {
			if c := compareTimes(rows[i].CreatedAt, rows[j].CreatedAt, desc); c != 0 {
				return c < 0
			}
			return compareStrings(rows[i].Title, rows[j].Title, false) < 0
		})
	case "uses":
		sort.SliceStable(rows, func(i, j int) bool {
			uI := len(usage.Windows[rows[i].ID].Downloads)
			uJ := len(usage.Windows[rows[j].ID].Downloads)
			if c := compareInts(uI, uJ, desc); c != 0 {
				return c < 0
			}
			return compareStrings(rows[i].Title, rows[j].Title, false) < 0
		})
	case "state":
		sort.SliceStable(rows, func(i, j int) bool {
			if c := compareStrings(rows[i].State, rows[j].State, desc); c != 0 {
				return c < 0
			}
			return compareStrings(rows[i].Title, rows[j].Title, false) < 0
		})
	case "sha":
		sort.SliceStable(rows, func(i, j int) bool {
			if c := compareStrings(rows[i].SHA256, rows[j].SHA256, desc); c != 0 {
				return c < 0
			}
			return compareStrings(rows[i].Title, rows[j].Title, false) < 0
		})
	}
}

func sortUbuntuAutoinstalls(rows []UbuntuAutoinstallSummary, usage answerFileUsage, sortBy, dir string) {
	desc := dir == "desc"
	switch sortBy {
	case "title":
		sort.SliceStable(rows, func(i, j int) bool {
			if c := compareStrings(rows[i].Title, rows[j].Title, desc); c != 0 {
				return c < 0
			}
			return rows[i].ID < rows[j].ID
		})
	case "author":
		sort.SliceStable(rows, func(i, j int) bool {
			if c := compareStrings(rows[i].Author, rows[j].Author, desc); c != 0 {
				return c < 0
			}
			return compareStrings(rows[i].Title, rows[j].Title, false) < 0
		})
	case "created":
		sort.SliceStable(rows, func(i, j int) bool {
			if c := compareTimes(rows[i].CreatedAt, rows[j].CreatedAt, desc); c != 0 {
				return c < 0
			}
			return compareStrings(rows[i].Title, rows[j].Title, false) < 0
		})
	case "uses":
		sort.SliceStable(rows, func(i, j int) bool {
			uI := len(usage.Ubuntu[rows[i].ID].Downloads)
			uJ := len(usage.Ubuntu[rows[j].ID].Downloads)
			if c := compareInts(uI, uJ, desc); c != 0 {
				return c < 0
			}
			return compareStrings(rows[i].Title, rows[j].Title, false) < 0
		})
	case "state":
		sort.SliceStable(rows, func(i, j int) bool {
			if c := compareStrings(rows[i].State, rows[j].State, desc); c != 0 {
				return c < 0
			}
			return compareStrings(rows[i].Title, rows[j].Title, false) < 0
		})
	case "sha":
		sort.SliceStable(rows, func(i, j int) bool {
			if c := compareStrings(rows[i].SHA256, rows[j].SHA256, desc); c != 0 {
				return c < 0
			}
			return compareStrings(rows[i].Title, rows[j].Title, false) < 0
		})
	}
}

func (s *Server) imagesLibraryData(r *http.Request) pageData {
	data := s.profileLibraryData(r)
	q := r.URL.Query()
	if r.Method == http.MethodPost {
		data.ReturnTo = safeImagesReturnURL(r.PostFormValue("return_to"))
		if u, err := url.Parse(data.ReturnTo); err == nil {
			q = u.Query()
		}
	} else {
		data.ReturnTo = safeImagesReturnURL(r.URL.RequestURI())
	}
	data.Query = strings.TrimSpace(q.Get("q"))
	data.Tab = q.Get("show")
	data.Sort = q.Get("sort")
	data.Dir = q.Get("dir")
	data.ImageCounts = map[string]int{}
	catalog := s.loadLibraryCatalog(r.Context(), &data)
	if catalog == nil {
		return data
	}
	data.HasAnyISOImages = len(catalog.Images) > 0
	authors := s.resourceAuthors(r.Context())
	storagesByID := map[int64]images.Storage{}
	for _, storage := range catalog.Storages {
		storagesByID[storage.ID] = storage
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
	activeMigrations := map[string]*images.MigrationJob{}
	if s.Images != nil {
		if list, err := s.Images.ListMigrations(r.Context()); err == nil {
			for _, m := range list {
				if m.State == "copying" || m.State == "ready_to_rebind" || m.State == "rebinding" {
					activeMigrations[imageAuthorKey(m.SourceStorageID, m.Path)] = m
				}
			}
		}
	}
	maintLookup := s.loadMaintenanceLookup(r.Context())
	drainCounts := s.countActiveDrainClients(r.Context())
	data.MaintenanceStorages = make(map[int64]bool, len(maintLookup.byStorage))
	for sid := range maintLookup.byStorage {
		data.MaintenanceStorages[sid] = true
	}
	for _, image := range catalog.Images {
		key := imageAuthorKey(image.StorageID, image.Path)
		kind, problem, arch := inspectionLabel("")
		if record, ok := inspections[key]; ok {
			kind, problem, arch = inspectionLabel(record.Kind)
		}
		storage, storageFound := storagesByID[image.StorageID]
		row := imageRow{
			Image:           image,
			StorageName:     storage.Name,
			StorageKind:     storage.Kind,
			StorageUploads:  storage.Uploads,
			StorageState:    storage.State,
			Author:          authors.Images[imageAuthorKey(image.StorageID, image.Path)],
			Kind:            kind,
			Problem:         problem,
			DisplayName:     namesByImage[key],
			Arch:            arch,
			ActiveMigration: activeMigrations[key],
		}
		switch {
		case !storageFound || storage.State == "unavailable":
			row.StorageUnavailable = true
		case storage.State == "scanning":
			row.StorageScanning = true
		case storage.State == "unknown":
			row.StorageUnknown = true
		}
		row.MaintenanceActiveClients = drainCounts.byImage[key]
		if lock, inherited, ok := maintLookup.ImageLock(image.StorageID, image.Path); ok {
			row.Maintenance = true
			row.MaintenanceInherited = inherited
			row.MaintenanceReason = lock.Reason
		}
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
		if !row.Image.Present || row.Problem != "" || row.StorageBlocked() || row.Maintenance {
			data.ImageCounts["attention"]++
		}
		if row.MatchesTab(data.Tab) {
			data.ISOImages = append(data.ISOImages, row)
		}
	}
	if data.Sort != "" {
		sortImages(data.ISOImages, data.Sort, data.Dir)
	}
	return data
}

func sortImages(rows []imageRow, sortBy, dir string) {
	desc := dir == "desc"
	nameOf := func(r imageRow) string {
		if r.DisplayName != "" {
			return r.DisplayName
		}
		return r.Image.Path
	}
	switch sortBy {
	case "name":
		sort.SliceStable(rows, func(i, j int) bool {
			if c := compareStrings(nameOf(rows[i]), nameOf(rows[j]), desc); c != 0 {
				return c < 0
			}
			return rows[i].Image.Path < rows[j].Image.Path
		})
	case "kind":
		sort.SliceStable(rows, func(i, j int) bool {
			if c := compareStrings(rows[i].Kind, rows[j].Kind, desc); c != 0 {
				return c < 0
			}
			return compareStrings(nameOf(rows[i]), nameOf(rows[j]), false) < 0
		})
	case "size":
		sort.SliceStable(rows, func(i, j int) bool {
			if c := compareInt64s(rows[i].Image.Size, rows[j].Image.Size, desc); c != 0 {
				return c < 0
			}
			return compareStrings(nameOf(rows[i]), nameOf(rows[j]), false) < 0
		})
	case "storage":
		sort.SliceStable(rows, func(i, j int) bool {
			if c := compareStrings(rows[i].StorageName, rows[j].StorageName, desc); c != 0 {
				return c < 0
			}
			return compareStrings(nameOf(rows[i]), nameOf(rows[j]), false) < 0
		})
	case "added":
		sort.SliceStable(rows, func(i, j int) bool {
			var tI, tJ time.Time
			if rows[i].AddedAt != nil {
				tI = *rows[i].AddedAt
			}
			if rows[j].AddedAt != nil {
				tJ = *rows[j].AddedAt
			}
			if c := compareTimes(tI, tJ, desc); c != 0 {
				return c < 0
			}
			return compareStrings(nameOf(rows[i]), nameOf(rows[j]), false) < 0
		})
	case "downloads":
		sort.SliceStable(rows, func(i, j int) bool {
			if c := compareInts(len(rows[i].Downloads), len(rows[j].Downloads), desc); c != 0 {
				return c < 0
			}
			return compareStrings(nameOf(rows[i]), nameOf(rows[j]), false) < 0
		})
	}
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
	if row.StorageUnavailable {
		parts = append(parts, "Хранилище недоступно", translate(langEN, "Хранилище недоступно"))
	} else if row.StorageScanning {
		parts = append(parts, "Проверяется хранилище", translate(langEN, "Проверяется хранилище"))
	} else if row.StorageUnknown {
		parts = append(parts, "Хранилище не проверено", translate(langEN, "Хранилище не проверено"))
	}
	for _, d := range row.Downloads {
		parts = append(parts, d.Title)
	}
	return strings.Contains(strings.ToLower(strings.Join(parts, " ")), q)
}
