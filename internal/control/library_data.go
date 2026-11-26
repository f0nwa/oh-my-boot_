package control

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path"
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
		data.ImageError = "Не удалось прочитать скрипты Windows"
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
	data.ReturnTo = safeAnswersReturnURL(r.URL.RequestURI())
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
	data.ImageTab = normalizeImageTab(q.Get("tab"))
	data.Tab = q.Get("show")
	data.Sort = q.Get("sort")
	data.Dir = q.Get("dir")
	data.ImageCounts = map[string]int{}
	data.ImageTabCounts = map[string]int{}
	data.HashIndexEnabled = true
	if s.Images != nil {
		if settings, err := s.Images.CatalogSettings(r.Context()); err == nil {
			data.HashIndexEnabled = settings.HashIndexEnabled
			data.HashIndexRateMBps = settings.HashIndexRateMBps
		}
	}
	catalog := s.loadLibraryCatalog(r.Context(), &data)
	if catalog == nil {
		return data
	}
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
	profilesByID := map[int64]WindowsProfile{}
	for _, p := range data.WindowsProfiles {
		byPath[fmt.Sprintf("%d:%s", p.StorageID, p.Path)] = p
		profilesByID[p.ID] = p
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

	catalogByKey := make(map[string]images.Image, len(catalog.Images))
	bootByProfileID := map[int64]WindowsBootImage{}
	if data.ImageTab == "iso" {
		if wimStore, ok := s.windowsWIMStore(); ok {
			if list, err := wimStore.ListWindowsBootImages(r.Context()); err == nil {
				for _, b := range list {
					if b.SourceKind == WIMSourceKindISO && b.WindowsProfileID > 0 {
						bootByProfileID[b.WindowsProfileID] = b
					}
				}
			}
		}
	}
	var catalogWIMs []images.Image
	for _, image := range catalog.Images {
		key := imageAuthorKey(image.StorageID, image.Path)
		catalogByKey[key] = image
		imgKind := catalogImageKind(image)
		if imgKind != images.ImageKindISO {
			catalogWIMs = append(catalogWIMs, image)
			continue
		}
		data.HasAnyISOImages = true
		data.ImageTabCounts["iso"]++
		if data.ImageTab != "iso" {
			continue
		}
		kind, problem, arch := inspectionLabel("")
		var versionArch, summary string
		inspected := false
		if record, ok := inspections[key]; ok {
			kind, problem, arch = inspectionLabel(record.Kind)
			if record.Arch != "" {
				arch = record.Arch
			}
			inspected = record.Kind != "" && record.Kind != "error"
			summary = record.Summary
			if record.Version != "" {
				if arch != "" && arch != "—" {
					versionArch = record.Version + " · " + arch
				} else {
					versionArch = record.Version
				}
			} else if record.WIMInfo.Known {
				versionArch = wimVersionSummary(record.WIMInfo)
			}
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
			VersionArch:     versionArch,
			Summary:         summary,
			Inspected:       inspected,
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
			sameProfileFile := p.State == "ready" && p.Size == image.Size && p.Modified.Truncate(time.Microsecond).Equal(image.Modified.Truncate(time.Microsecond))
			if sameProfileFile {
				if row.Kind == "Не проверен" {
					if p.BootMode == agent.ISOStrelecMode {
						row.Kind = "WinPE Strelec"
					} else {
						row.Kind = "Windows"
					}
					row.Arch = "x64"
					row.Inspected = true
				}
				if row.VersionArch == "" && p.WIMInfo.Known {
					row.VersionArch = wimVersionSummary(p.WIMInfo)
				}
				if row.Summary == "" && p.WIMInfo.Known {
					ed := wimEditionsSummary(p.WIMInfo)
					if ed != "" && p.InstallFile != "" {
						row.Summary = ed + " · " + p.InstallFile
					} else if ed != "" {
						row.Summary = ed
					}
				}
				if bi, ok := bootByProfileID[p.ID]; ok && bi.WIMInfo.Known {
					row.BootBuild = wimVersionSummary(bi.WIMInfo)
				}
			}
			if row.DeleteBlockedReason == "" {
				row.CleanupProfileID = p.ID
			}
			if diskReport != nil && diskReport.WindowsProfiles[p.ID] > 0 {
				row.ServerBytes += diskReport.WindowsProfiles[p.ID]
				row.ServerNote = "boot.wim"
			}
		}
		if len(ubuntuSourceIDs[key]) > 0 && row.Kind == "Не проверен" {
			row.Kind = "Ubuntu"
			row.Arch = "x64"
			row.Inspected = true
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
	if data.ImageTab == "iso" && data.Sort != "" {
		sortImages(data.ISOImages, data.Sort, data.Dir)
	}

	s.populateWIMImageTabs(r.Context(), &data, catalogWIMs, catalogByKey, storagesByID, profilesByID, sources, usageErr, diskReport, maintLookup, drainCounts)
	return data
}

func catalogImageKind(img images.Image) string {
	switch strings.ToLower(strings.TrimSpace(img.Kind)) {
	case images.ImageKindISO, images.ImageKindWIM, images.ImageKindESD:
		return strings.ToLower(strings.TrimSpace(img.Kind))
	}
	switch strings.ToLower(path.Ext(img.Path)) {
	case ".wim":
		return images.ImageKindWIM
	case ".esd":
		return images.ImageKindESD
	default:
		return images.ImageKindISO
	}
}

func (s *Server) populateWIMImageTabs(
	ctx context.Context,
	data *pageData,
	catalogWIMs []images.Image,
	catalogByKey map[string]images.Image,
	storagesByID map[int64]images.Storage,
	profilesByID map[int64]WindowsProfile,
	sources InstallationMenuSources,
	usageErr error,
	diskReport *agent.DiskUsageReport,
	maintLookup maintenanceLookup,
	drainCounts maintenanceDrainCounts,
) {
	var bootImages []WindowsBootImage
	var installImages []WindowsInstallImage
	if wimStore, ok := s.windowsWIMStore(); ok {
		if list, err := wimStore.ListWindowsBootImages(ctx); err != nil {
			log.Printf("list windows boot images: %v", err)
			data.Error = "Не удалось прочитать образы boot.wim"
		} else {
			bootImages = list
		}
		if list, err := wimStore.ListWindowsInstallImages(ctx); err != nil {
			log.Printf("list windows install images: %v", err)
			data.Error = "Не удалось прочитать образы install.wim"
		} else {
			installImages = list
		}
	}

	bootByIDUses := map[int64][]imageDownload{}
	isoBootByProfileUses := map[int64][]imageDownload{}
	installByIDUses := map[int64][]imageDownload{}
	bpByID := make(map[int64]BootProfile, len(sources.BootProfiles))
	for _, bp := range sources.BootProfiles {
		bpByID[bp.ID] = bp
	}
	for _, item := range sources.Installations {
		dl := imageDownload{ID: item.ID, Title: item.Title}
		bp := bpByID[item.BootProfileID]
		if item.BootImageID > 0 {
			bootByIDUses[item.BootImageID] = append(bootByIDUses[item.BootImageID], dl)
		} else if bp.Kind == BootProfileWindowsISO && bp.WindowsProfileID > 0 {
			isoBootByProfileUses[bp.WindowsProfileID] = append(isoBootByProfileUses[bp.WindowsProfileID], dl)
		}
		if item.InstallImageID > 0 {
			installByIDUses[item.InstallImageID] = append(installByIDUses[item.InstallImageID], dl)
		} else if bp.Kind == BootProfileWindowsWIM && bp.InstallImageID > 0 {
			installByIDUses[bp.InstallImageID] = append(installByIDUses[bp.InstallImageID], dl)
		}
	}

	knownBootKeys := map[string]bool{}
	knownInstallKeys := map[string]bool{}
	for _, b := range bootImages {
		if b.SourceKind == WIMSourceKindFile && b.StorageID > 0 && b.Path != "" {
			knownBootKeys[imageAuthorKey(b.StorageID, b.Path)] = true
		}
	}
	for _, inst := range installImages {
		if inst.StorageID > 0 && inst.Path != "" {
			knownInstallKeys[imageAuthorKey(inst.StorageID, inst.Path)] = true
		}
	}

	// Build boot.wim rows
	var allBoot []bootWIMRow
	for _, b := range bootImages {
		row := bootWIMRow{
			ID:               b.ID,
			SourceKind:       b.SourceKind,
			WindowsProfileID: b.WindowsProfileID,
			StorageID:        b.StorageID,
			Path:             b.Path,
			Title:            b.Title,
			SHA256:           b.SHA256,
			Size:             b.Size,
			VersionArch:      wimVersionSummary(b.WIMInfo),
			HasSetupExe:      b.HasSetupExe,
			State:            b.State,
			Error:            b.Error,
			Present:          true,
		}
		if b.SourceKind == WIMSourceKindISO {
			if p, ok := profilesByID[b.WindowsProfileID]; ok {
				row.SourceISOPath = p.Path
				if row.VersionArch == "" && p.WIMInfo.Known {
					row.VersionArch = wimVersionSummary(p.WIMInfo)
				}
				if st, ok := storagesByID[p.StorageID]; ok {
					row.StorageName = st.Name
				}
				if row.Size == 0 && diskReport != nil && diskReport.WindowsProfiles[p.ID] > 0 {
					row.Size = diskReport.WindowsProfiles[p.ID]
				}
			}
			row.Downloads = append(append([]imageDownload(nil), bootByIDUses[b.ID]...), isoBootByProfileUses[b.WindowsProfileID]...)
		} else {
			key := imageAuthorKey(b.StorageID, b.Path)
			if st, ok := storagesByID[b.StorageID]; ok {
				row.StorageName = st.Name
				row.StorageBlocked = st.State == "unavailable" || st.State == "scanning" || st.State == "unknown"
			} else {
				row.StorageBlocked = true
			}
			if cimg, ok := catalogByKey[key]; ok {
				row.Present = cimg.Present
				if cimg.SHA256 != "" {
					row.SHA256 = cimg.SHA256
				}
				if row.Size == 0 {
					row.Size = cimg.Size
				}
			} else {
				row.Present = false
			}
			row.MaintenanceActiveClients = drainCounts.byImage[key]
			if _, inherited, ok := maintLookup.ImageLock(b.StorageID, b.Path); ok {
				row.Maintenance = true
				row.MaintenanceInherited = inherited
			}
			row.Downloads = bootByIDUses[b.ID]
			if usageErr != nil {
				row.DeleteBlockedReason = "Не удалось проверить загрузки"
			} else if len(row.Downloads) > 0 {
				row.DeleteBlockedReason = "Образ используется загрузками"
			}
		}
		allBoot = append(allBoot, row)
	}
	for _, cimg := range catalogWIMs {
		if catalogImageKind(cimg) != images.ImageKindWIM {
			continue
		}
		key := imageAuthorKey(cimg.StorageID, cimg.Path)
		if knownBootKeys[key] || knownInstallKeys[key] {
			continue
		}
		st, stFound := storagesByID[cimg.StorageID]
		row := bootWIMRow{
			SourceKind:     "catalog",
			StorageID:      cimg.StorageID,
			StorageName:    st.Name,
			StorageBlocked: !stFound || st.State == "unavailable" || st.State == "scanning" || st.State == "unknown",
			Path:           cimg.Path,
			Title:          path.Base(cimg.Path),
			SHA256:         cimg.SHA256,
			Size:           cimg.Size,
			State:          "uninspected",
			Present:        cimg.Present,
		}
		row.MaintenanceActiveClients = drainCounts.byImage[key]
		if _, inherited, ok := maintLookup.ImageLock(cimg.StorageID, cimg.Path); ok {
			row.Maintenance = true
			row.MaintenanceInherited = inherited
		}
		allBoot = append(allBoot, row)
	}
	data.HasAnyBootWIMImages = len(allBoot) > 0
	data.ImageTabCounts["boot"] = len(allBoot)

	// Build install.wim rows
	var allInstall []installWIMRow
	for _, inst := range installImages {
		key := imageAuthorKey(inst.StorageID, inst.Path)
		st, stFound := storagesByID[inst.StorageID]
		row := installWIMRow{
			ID:             inst.ID,
			StorageID:      inst.StorageID,
			StorageName:    st.Name,
			StorageBlocked: !stFound || st.State == "unavailable" || st.State == "scanning" || st.State == "unknown",
			Path:           inst.Path,
			Title:          inst.Title,
			InstallFile:    inst.InstallFile,
			IsESD:          strings.EqualFold(path.Ext(inst.Path), ".esd") || strings.EqualFold(inst.InstallFile, "install.esd"),
			Size:           inst.Size,
			Editions:       wimEditionsSummary(inst.WIMInfo),
			IndexCount:     len(inst.WIMInfo.Images),
			VersionArch:    wimVersionSummary(inst.WIMInfo),
			State:          inst.State,
			Error:          inst.Error,
			Present:        true,
		}
		if cimg, ok := catalogByKey[key]; ok {
			row.Present = cimg.Present
			row.SHA256 = cimg.SHA256
		} else {
			row.Present = false
		}
		row.MaintenanceActiveClients = drainCounts.byImage[key]
		if _, inherited, ok := maintLookup.ImageLock(inst.StorageID, inst.Path); ok {
			row.Maintenance = true
			row.MaintenanceInherited = inherited
		}
		row.Downloads = installByIDUses[inst.ID]
		if usageErr != nil {
			row.DeleteBlockedReason = "Не удалось проверить загрузки"
		} else if len(row.Downloads) > 0 {
			row.DeleteBlockedReason = "Образ используется загрузками"
		}
		allInstall = append(allInstall, row)
	}
	for _, cimg := range catalogWIMs {
		key := imageAuthorKey(cimg.StorageID, cimg.Path)
		if knownInstallKeys[key] || knownBootKeys[key] {
			continue
		}
		st, stFound := storagesByID[cimg.StorageID]
		isESD := catalogImageKind(cimg) == images.ImageKindESD
		installFile := "install.wim"
		if isESD {
			installFile = "install.esd"
		}
		row := installWIMRow{
			StorageID:      cimg.StorageID,
			StorageName:    st.Name,
			StorageBlocked: !stFound || st.State == "unavailable" || st.State == "scanning" || st.State == "unknown",
			Path:           cimg.Path,
			Title:          path.Base(cimg.Path),
			InstallFile:    installFile,
			IsESD:          isESD,
			SHA256:         cimg.SHA256,
			Size:           cimg.Size,
			State:          "uninspected",
			Present:        cimg.Present,
		}
		row.MaintenanceActiveClients = drainCounts.byImage[key]
		if _, inherited, ok := maintLookup.ImageLock(cimg.StorageID, cimg.Path); ok {
			row.Maintenance = true
			row.MaintenanceInherited = inherited
		}
		allInstall = append(allInstall, row)
	}
	data.HasAnyInstallWIMImages = len(allInstall) > 0
	data.ImageTabCounts["install"] = len(allInstall)

	switch data.ImageTab {
	case "boot":
		for _, row := range allBoot {
			if data.Query != "" && !bootWIMMatchesQuery(row, data.Query) {
				continue
			}
			data.ImageCounts["all"]++
			if row.SourceKind == WIMSourceKindISO {
				data.ImageCounts["iso"]++
			} else {
				data.ImageCounts["file"]++
			}
			if row.MatchesFilter(data.Tab) {
				data.BootWIMImages = append(data.BootWIMImages, row)
			}
		}
	case "install":
		for _, row := range allInstall {
			if data.Query != "" && !installWIMMatchesQuery(row, data.Query) {
				continue
			}
			data.ImageCounts["all"]++
			if row.IsESD {
				data.ImageCounts["esd"]++
			} else {
				data.ImageCounts["wim"]++
			}
			if row.MatchesFilter(data.Tab) {
				data.InstallWIMImages = append(data.InstallWIMImages, row)
			}
		}
	}
}

func bootWIMMatchesQuery(row bootWIMRow, query string) bool {
	q := strings.ToLower(query)
	parts := []string{row.Title, row.Path, row.SourceISOPath, row.StorageName, row.VersionArch, row.State, row.Error}
	for _, d := range row.Downloads {
		parts = append(parts, d.Title)
	}
	return strings.Contains(strings.ToLower(strings.Join(parts, " ")), q)
}

func installWIMMatchesQuery(row installWIMRow, query string) bool {
	q := strings.ToLower(query)
	parts := []string{row.Title, row.Path, row.InstallFile, row.Editions, row.StorageName, row.VersionArch, row.State, row.Error}
	for _, d := range row.Downloads {
		parts = append(parts, d.Title)
	}
	return strings.Contains(strings.ToLower(strings.Join(parts, " ")), q)
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
		row.VersionArch,
		row.Summary,
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
