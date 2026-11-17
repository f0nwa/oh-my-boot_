package control

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"omb/internal/agent"
	"omb/internal/images"
)

const (
	artifactCategoryBootWIM = "boot_wim"
	artifactCategoryDrivers = "drivers"
	artifactCategoryUbuntu  = "ubuntu"
	artifactCategoryCache   = "cache"
)

type storageSpaceInput struct {
	Catalog             images.Catalog
	Report              *agent.DiskUsageReport
	AgentError          string
	WindowsProfiles     []WindowsProfile
	DriverConfigs       []DriverConfig
	DriverPackagesCount int
	BootProfiles        []BootProfile
	Installations       []Installation
	Observer            bool
}

type localDiskView struct {
	TotalBytes          int64
	FreeBytes           int64
	UsedBytes           int64
	UsedPercent         int
	OMBBytes            int64
	OSOtherBytes        int64
	ProfileWIMBytes     int64
	ProfileWIMCount     int
	DriverAndPkgBytes   int64
	DriverWIMsCount     int
	DriverPackagesCount int
	UbuntuBytes         int64
	UbuntuCount         int
	UEFICABytes         int64
	ProfileWIMPct       string
	DriverPct           string
	UbuntuPct           string
	UEFICAPct           string
	OSOtherPct          string
	MultiFS             bool
}

type storageRowView struct {
	images.Storage
	ISOCount     int
	ISOBytes     int64
	OtherBytes   int64
	OutsideBytes int64
	ISOPct       string
	OtherPct     string
	OutsidePct   string
	ServerBytes  int64
	ServerKinds  []string
}

type artifactLink struct {
	Label string
	Href  string
}

type serverArtifactRow struct {
	Category    string
	Title       string
	TitleSuffix string
	Detail      string
	StorageName string
	Links       []artifactLink
	Bytes       int64
	Orphan      bool
}

type storageSpaceView struct {
	Disks         []localDiskView
	Storages      []storageRowView
	Artifacts     []serverArtifactRow
	TotalOMBBytes int64
	ServerError   string
}

func fmtPct(part, total int64) string {
	if part <= 0 || total <= 0 {
		return "0"
	}
	v := float64(part) * 100 / float64(total)
	if v > 100 {
		v = 100
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}

func pctValue(part, total int64) float64 {
	if part <= 0 || total <= 0 {
		return 0
	}
	v := float64(part) * 100 / float64(total)
	if v > 100 {
		return 100
	}
	return v
}

// buildStorageSpaceView joins storage catalog metrics with local omb-agent
// artifact usage, deduplicating shared driver WIMs and clamping negative sizes.
func buildStorageSpaceView(in storageSpaceInput) storageSpaceView {
	view := storageSpaceView{
		ServerError: in.AgentError,
	}

	isoCountByStorage := map[int64]int{}
	isoBytesByStorage := map[int64]int64{}
	storageNameByID := map[int64]string{}
	for _, st := range in.Catalog.Storages {
		storageNameByID[st.ID] = st.Name
	}
	for _, img := range in.Catalog.Images {
		if !img.Present {
			continue
		}
		isoCountByStorage[img.StorageID]++
		isoBytesByStorage[img.StorageID] += img.Size
	}

	storageServerBytes := map[int64]int64{}
	storageKinds := map[int64]map[string]bool{}
	addStorageKind := func(storageID int64, kind string) {
		if storageID <= 0 {
			return
		}
		if storageKinds[storageID] == nil {
			storageKinds[storageID] = map[string]bool{}
		}
		storageKinds[storageID][kind] = true
	}

	if in.Report != nil {
		report := in.Report
		profileByID := make(map[int64]WindowsProfile, len(in.WindowsProfiles))
		for _, p := range in.WindowsProfiles {
			profileByID[p.ID] = p
		}

		winBootProfileID := map[int64]int64{}
		ubuntuBootProfiles := map[int64]BootProfile{}
		for _, bp := range in.BootProfiles {
			if bp.WindowsProfileID > 0 {
				winBootProfileID[bp.WindowsProfileID] = bp.ID
			}
			if bp.UbuntuSourceID > 0 {
				if _, exists := ubuntuBootProfiles[bp.UbuntuSourceID]; !exists {
					ubuntuBootProfiles[bp.UbuntuSourceID] = bp
				}
			}
		}

		installLink := func(inst Installation) artifactLink {
			if in.Observer {
				return artifactLink{
					Label: inst.Title,
					Href:  "/installations?q=" + url.QueryEscape("install_"+strconv.FormatInt(inst.ID, 10)),
				}
			}
			return artifactLink{
				Label: inst.Title,
				Href:  "/installations/" + strconv.FormatInt(inst.ID, 10) + "/edit",
			}
		}

		instByBootProfile := map[int64][]Installation{}
		instByDriverConfig := map[int64][]Installation{}
		for _, inst := range in.Installations {
			if inst.BootProfileID > 0 {
				instByBootProfile[inst.BootProfileID] = append(instByBootProfile[inst.BootProfileID], inst)
			}
			if inst.DriverConfigID > 0 {
				instByDriverConfig[inst.DriverConfigID] = append(instByDriverConfig[inst.DriverConfigID], inst)
			}
		}

		var profileWIMBytes int64
		var profileWIMCount int
		seenProfiles := map[int64]bool{}
		for _, p := range in.WindowsProfiles {
			b := report.WindowsProfiles[p.ID]
			seenProfiles[p.ID] = true
			profileWIMBytes += b
			if b <= 0 {
				continue
			}
			profileWIMCount++
			storageServerBytes[p.StorageID] += b
			addStorageKind(p.StorageID, "boot.wim")
			var links []artifactLink
			if bpID := winBootProfileID[p.ID]; bpID > 0 {
				for _, inst := range instByBootProfile[bpID] {
					if inst.DriverConfigID == 0 {
						links = append(links, installLink(inst))
					}
				}
			}
			view.Artifacts = append(view.Artifacts, serverArtifactRow{
				Category:    artifactCategoryBootWIM,
				Title:       p.Title,
				Detail:      "базовый boot.wim",
				StorageName: storageNameByID[p.StorageID],
				Links:       links,
				Bytes:       b,
			})
		}

		var orphanProfileIDs []int64
		for id, b := range report.WindowsProfiles {
			if !seenProfiles[id] && b > 0 {
				orphanProfileIDs = append(orphanProfileIDs, id)
			}
		}
		sort.Slice(orphanProfileIDs, func(i, j int) bool { return orphanProfileIDs[i] < orphanProfileIDs[j] })
		for _, id := range orphanProfileIDs {
			b := report.WindowsProfiles[id]
			profileWIMBytes += b
			profileWIMCount++
			view.Artifacts = append(view.Artifacts, serverArtifactRow{
				Category:    artifactCategoryBootWIM,
				Title:       "Профиль",
				TitleSuffix: fmt.Sprintf("#%d", id),
				Detail:      "без привязки к профилю",
				Bytes:       b,
				Orphan:      true,
			})
		}

		if report.WindowsAnswersBytes > 0 {
			profileWIMBytes += report.WindowsAnswersBytes
			view.Artifacts = append(view.Artifacts, serverArtifactRow{
				Category: artifactCategoryBootWIM,
				Title:    "Файлы ответов Windows (XML)",
				Links:    []artifactLink{{Label: "Скрипты установки", Href: "/answers"}},
				Bytes:    report.WindowsAnswersBytes,
			})
		}

		// Group driver configurations by deduplicated WIMKey so each WIM is counted once.
		configsByWIMKey := map[string][]DriverConfig{}
		var wimKeys []string
		seenConfigs := map[int64]bool{}
		for _, cfg := range in.DriverConfigs {
			if cfg.Build.WIMKey == "" {
				continue
			}
			if _, ok := configsByWIMKey[cfg.Build.WIMKey]; !ok {
				wimKeys = append(wimKeys, cfg.Build.WIMKey)
			}
			configsByWIMKey[cfg.Build.WIMKey] = append(configsByWIMKey[cfg.Build.WIMKey], cfg)
		}
		sort.Strings(wimKeys)

		var driverWIMsBytes int64
		var driverWIMsCount int
		seenWIMKeys := map[string]bool{}
		for _, key := range wimKeys {
			cfgs := configsByWIMKey[key]
			wimBytes := report.WindowsDriverWIMs[key]
			seenWIMKeys[key] = true
			var cfgBytes int64
			var links []artifactLink
			seenLinks := map[int64]bool{}
			for _, cfg := range cfgs {
				seenConfigs[cfg.ID] = true
				cfgBytes += report.WindowsDriverConfigs[cfg.ID]
				for _, inst := range instByDriverConfig[cfg.ID] {
					if !seenLinks[inst.ID] {
						seenLinks[inst.ID] = true
						links = append(links, installLink(inst))
					}
				}
			}
			totalBytes := wimBytes + cfgBytes
			driverWIMsBytes += totalBytes
			if totalBytes <= 0 {
				continue
			}
			driverWIMsCount++
			first := cfgs[0]
			p := profileByID[first.ProfileID]
			storageServerBytes[p.StorageID] += totalBytes
			addStorageKind(p.StorageID, "boot.wim")
			title := first.Title
			if p.Title != "" {
				title = p.Title
			}
			view.Artifacts = append(view.Artifacts, serverArtifactRow{
				Category:    artifactCategoryDrivers,
				Title:       title,
				Detail:      "драйверный boot.wim",
				StorageName: storageNameByID[p.StorageID],
				Links:       links,
				Bytes:       totalBytes,
			})
		}

		var orphanWIMKeys []string
		for key, b := range report.WindowsDriverWIMs {
			if !seenWIMKeys[key] && b > 0 {
				orphanWIMKeys = append(orphanWIMKeys, key)
			}
		}
		sort.Strings(orphanWIMKeys)
		for _, key := range orphanWIMKeys {
			b := report.WindowsDriverWIMs[key]
			driverWIMsBytes += b
			driverWIMsCount++
			view.Artifacts = append(view.Artifacts, serverArtifactRow{
				Category: artifactCategoryDrivers,
				Title:    "Драйверный boot.wim",
				Detail:   "без привязки к профилю",
				Bytes:    b,
				Orphan:   true,
			})
		}

		var orphanConfigIDs []int64
		for id, b := range report.WindowsDriverConfigs {
			if !seenConfigs[id] && b > 0 {
				orphanConfigIDs = append(orphanConfigIDs, id)
			}
		}
		sort.Slice(orphanConfigIDs, func(i, j int) bool { return orphanConfigIDs[i] < orphanConfigIDs[j] })
		for _, id := range orphanConfigIDs {
			b := report.WindowsDriverConfigs[id]
			driverWIMsBytes += b
			view.Artifacts = append(view.Artifacts, serverArtifactRow{
				Category:    artifactCategoryDrivers,
				Title:       "Конфигурация драйверов",
				TitleSuffix: fmt.Sprintf("#%d", id),
				Detail:      "без привязки к профилю",
				Bytes:       b,
				Orphan:      true,
			})
		}

		driverPkgBytes := report.DriverPackagesBytes
		if driverPkgBytes > 0 {
			suffix := ""
			if in.DriverPackagesCount > 0 {
				suffix = fmt.Sprintf("(%d)", in.DriverPackagesCount)
			}
			view.Artifacts = append(view.Artifacts, serverArtifactRow{
				Category:    artifactCategoryDrivers,
				Title:       "Пакеты драйверов WinPE",
				TitleSuffix: suffix,
				Links:       []artifactLink{{Label: "Драйверы", Href: "/drivers"}},
				Bytes:       driverPkgBytes,
			})
		}

		var ubuntuBytes int64
		var ubuntuCount int
		seenUbuntu := map[int64]bool{}
		var ubuntuIDs []int64
		for id := range ubuntuBootProfiles {
			ubuntuIDs = append(ubuntuIDs, id)
		}
		sort.Slice(ubuntuIDs, func(i, j int) bool { return ubuntuIDs[i] < ubuntuIDs[j] })
		for _, srcID := range ubuntuIDs {
			bp := ubuntuBootProfiles[srcID]
			b := report.UbuntuSources[srcID]
			seenUbuntu[srcID] = true
			ubuntuBytes += b
			if b <= 0 {
				continue
			}
			ubuntuCount++
			storageServerBytes[bp.UbuntuStorageID] += b
			addStorageKind(bp.UbuntuStorageID, "Ubuntu")
			var links []artifactLink
			for _, inst := range instByBootProfile[bp.ID] {
				links = append(links, installLink(inst))
			}
			view.Artifacts = append(view.Artifacts, serverArtifactRow{
				Category:    artifactCategoryUbuntu,
				Title:       bp.Title,
				Detail:      "ядро и initrd",
				StorageName: storageNameByID[bp.UbuntuStorageID],
				Links:       links,
				Bytes:       b,
			})
		}

		var orphanUbuntuIDs []int64
		for id, b := range report.UbuntuSources {
			if !seenUbuntu[id] && b > 0 {
				orphanUbuntuIDs = append(orphanUbuntuIDs, id)
			}
		}
		sort.Slice(orphanUbuntuIDs, func(i, j int) bool { return orphanUbuntuIDs[i] < orphanUbuntuIDs[j] })
		for _, id := range orphanUbuntuIDs {
			b := report.UbuntuSources[id]
			ubuntuBytes += b
			ubuntuCount++
			view.Artifacts = append(view.Artifacts, serverArtifactRow{
				Category:    artifactCategoryUbuntu,
				Title:       "Источник Ubuntu",
				TitleSuffix: fmt.Sprintf("#%d", id),
				Detail:      "без привязки к профилю",
				Bytes:       b,
				Orphan:      true,
			})
		}

		uefiBytes := report.UEFICAUSBBytes
		if uefiBytes > 0 {
			view.Artifacts = append(view.Artifacts, serverArtifactRow{
				Category: artifactCategoryCache,
				Title:    "Кэш флешки UEFI CA",
				Links:    []artifactLink{{Label: "USB-носители", Href: "/usb"}},
				Bytes:    uefiBytes,
			})
		}

		sort.SliceStable(view.Artifacts, func(i, j int) bool {
			if view.Artifacts[i].Bytes != view.Artifacts[j].Bytes {
				return view.Artifacts[i].Bytes > view.Artifacts[j].Bytes
			}
			return view.Artifacts[i].Title < view.Artifacts[j].Title
		})

		driverAndPkgBytes := driverWIMsBytes + driverPkgBytes
		totalOMB := profileWIMBytes + driverAndPkgBytes + ubuntuBytes + uefiBytes
		view.TotalOMBBytes = totalOMB

		var fsTotal, fsFree int64
		for _, fs := range report.Filesystems {
			fsTotal += fs.TotalBytes
			fsFree += fs.FreeBytes
		}
		if fsTotal > 0 || totalOMB > 0 {
			usedFS := max(fsTotal-fsFree, 0)
			osOther := max(usedFS-totalOMB, 0)
			denom := max(fsTotal, totalOMB+osOther+fsFree)
			view.Disks = append(view.Disks, localDiskView{
				TotalBytes:          fsTotal,
				FreeBytes:           fsFree,
				UsedBytes:           usedFS,
				UsedPercent:         usedPercent(fsTotal, fsFree),
				OMBBytes:            totalOMB,
				OSOtherBytes:        osOther,
				ProfileWIMBytes:     profileWIMBytes,
				ProfileWIMCount:     profileWIMCount,
				DriverAndPkgBytes:   driverAndPkgBytes,
				DriverWIMsCount:     driverWIMsCount,
				DriverPackagesCount: in.DriverPackagesCount,
				UbuntuBytes:         ubuntuBytes,
				UbuntuCount:         ubuntuCount,
				UEFICABytes:         uefiBytes,
				ProfileWIMPct:       fmtPct(profileWIMBytes, denom),
				DriverPct:           fmtPct(driverAndPkgBytes, denom),
				UbuntuPct:           fmtPct(ubuntuBytes, denom),
				UEFICAPct:           fmtPct(uefiBytes, denom),
				OSOtherPct:          fmtPct(osOther, denom),
				MultiFS:             len(report.Filesystems) > 1,
			})
		}
	}

	for _, st := range in.Catalog.Storages {
		isoBytes := max(st.ISOBytes, isoBytesByStorage[st.ID])
		otherBytes := max(st.OtherBytes, 0)
		var outsideBytes int64
		var isoPct, otherPct, outsidePct float64
		if st.Total > 0 {
			usedFS := max(st.Total-st.Free, 0)
			outsideBytes = max(usedFS-isoBytes-otherBytes, 0)
			denom := max(st.Total, isoBytes+otherBytes+outsideBytes+st.Free)
			isoPct = pctValue(isoBytes, denom)
			otherPct = pctValue(otherBytes, denom)
			outsidePct = pctValue(outsideBytes, denom)
		}
		var kinds []string
		for _, candidate := range []string{"boot.wim", "Ubuntu"} {
			if storageKinds[st.ID][candidate] {
				kinds = append(kinds, candidate)
			}
		}
		view.Storages = append(view.Storages, storageRowView{
			Storage:      st,
			ISOCount:     isoCountByStorage[st.ID],
			ISOBytes:     isoBytes,
			OtherBytes:   otherBytes,
			OutsideBytes: outsideBytes,
			ISOPct:       strconv.FormatFloat(isoPct, 'f', 2, 64),
			OtherPct:     strconv.FormatFloat(otherPct, 'f', 2, 64),
			OutsidePct:   strconv.FormatFloat(outsidePct, 'f', 2, 64),
			ServerBytes:  storageServerBytes[st.ID],
			ServerKinds:  kinds,
		})
	}

	return view
}

// loadStorageSpace enriches pageData for /storages and /system with storage
// occupancy breakdown and local server artifact sizes.
func (s *Server) loadStorageSpace(ctx context.Context, data *pageData, catalog *images.Catalog) {
	in := storageSpaceInput{
		WindowsProfiles: data.WindowsProfiles,
		Observer:        data.Info.User.Role == "observer",
	}
	if catalog != nil {
		in.Catalog = *catalog
	} else {
		in.Catalog = images.Catalog{Storages: data.Storages}
	}
	diskAPI := s.DiskUsage
	if diskAPI == nil && s.Agent != nil {
		diskAPI = s.Agent
	}
	if diskAPI != nil {
		if configs, err := s.Store.DriverConfigs(ctx); err == nil {
			in.DriverConfigs = configs
		}
		if pkgs, err := s.Store.DriverPackages(ctx); err == nil {
			in.DriverPackagesCount = len(pkgs)
		}
		if bps, err := s.Store.BootProfiles(ctx); err == nil {
			in.BootProfiles = bps
		}
		if insts, err := s.Store.Installations(ctx); err == nil {
			in.Installations = insts
		}
		report, err := diskAPI.DiskUsage(ctx)
		if err != nil {
			in.AgentError = "Сведения о локальном диске временно недоступны"
		} else {
			in.Report = &report
		}
	}
	view := buildStorageSpaceView(in)
	data.StorageSpace = &view
}

func joinStrings(items []string, sep string) string {
	return strings.Join(items, sep)
}

func sortServerArtifacts(rows []serverArtifactRow, sortBy, dir string) {
	desc := dir == "desc"
	titleOf := func(r serverArtifactRow) string {
		if r.TitleSuffix != "" {
			return r.Title + " " + r.TitleSuffix
		}
		return r.Title
	}
	switch sortBy {
	case "artifact":
		sort.SliceStable(rows, func(i, j int) bool {
			if c := compareStrings(titleOf(rows[i]), titleOf(rows[j]), desc); c != 0 {
				return c < 0
			}
			return rows[i].Bytes > rows[j].Bytes
		})
	case "storage":
		sort.SliceStable(rows, func(i, j int) bool {
			if c := compareStrings(rows[i].StorageName, rows[j].StorageName, desc); c != 0 {
				return c < 0
			}
			return compareStrings(titleOf(rows[i]), titleOf(rows[j]), false) < 0
		})
	case "downloads":
		sort.SliceStable(rows, func(i, j int) bool {
			if c := compareInts(len(rows[i].Links), len(rows[j].Links), desc); c != 0 {
				return c < 0
			}
			return rows[i].Bytes > rows[j].Bytes
		})
	case "size":
		sort.SliceStable(rows, func(i, j int) bool {
			if c := compareInt64s(rows[i].Bytes, rows[j].Bytes, desc); c != 0 {
				return c < 0
			}
			return compareStrings(titleOf(rows[i]), titleOf(rows[j]), false) < 0
		})
	}
}
