package control

import "strconv"

// imageDownloadsBySource resolves saved downloads by storage and ISO path for
// both profile-backed Windows/WinPE images and direct Ubuntu ISO sources.
func imageDownloadsBySource(profiles []WindowsProfile, sources InstallationMenuSources) map[string][]imageDownload {
	profileKeys := make(map[int64]string, len(profiles))
	for _, profile := range profiles {
		profileKeys[profile.ID] = imageAuthorKey(profile.StorageID, profile.Path)
	}
	bootKeys := make(map[int64]string, len(sources.BootProfiles))
	for _, profile := range sources.BootProfiles {
		if isISOBootKind(profile.Kind) {
			bootKeys[profile.ID] = profileKeys[profile.WindowsProfileID]
		} else if (profile.Kind == BootProfileUbuntuISO || profile.Kind == BootProfileUbuntuBuiltin) && profile.UbuntuStorageID > 0 && profile.UbuntuPath != "" {
			bootKeys[profile.ID] = imageAuthorKey(profile.UbuntuStorageID, profile.UbuntuPath)
		}
	}
	uses := make(map[string][]imageDownload)
	for _, item := range sources.Installations {
		if key := bootKeys[item.BootProfileID]; key != "" {
			uses[key] = append(uses[key], imageDownload{ID: item.ID, Title: item.Title})
		}
	}
	return uses
}

// imageDownloadsByWIMSource resolves saved downloads referencing standalone
// boot.wim or install.wim/esd files by storage and path.
func imageDownloadsByWIMSource(bootImages []WindowsBootImage, installImages []WindowsInstallImage, sources InstallationMenuSources) map[string][]imageDownload {
	bootKeys := make(map[int64]string, len(bootImages))
	for _, img := range bootImages {
		if img.SourceKind == WIMSourceKindFile && img.StorageID > 0 && img.Path != "" {
			bootKeys[img.ID] = imageAuthorKey(img.StorageID, img.Path)
		}
	}
	installKeys := make(map[int64]string, len(installImages))
	for _, img := range installImages {
		if img.StorageID > 0 && img.Path != "" {
			installKeys[img.ID] = imageAuthorKey(img.StorageID, img.Path)
		}
	}
	bpInstallKeys := make(map[int64]string, len(sources.BootProfiles))
	for _, bp := range sources.BootProfiles {
		if bp.Kind == BootProfileWindowsWIM && bp.InstallImageID > 0 {
			if key := installKeys[bp.InstallImageID]; key != "" {
				bpInstallKeys[bp.ID] = key
			}
		}
	}
	uses := make(map[string][]imageDownload)
	seen := make(map[string]bool)
	add := func(key string, item Installation) {
		if key == "" {
			return
		}
		dedup := key + "#" + strconv.FormatInt(item.ID, 10)
		if seen[dedup] {
			return
		}
		seen[dedup] = true
		uses[key] = append(uses[key], imageDownload{ID: item.ID, Title: item.Title})
	}
	for _, item := range sources.Installations {
		if item.BootImageID > 0 {
			add(bootKeys[item.BootImageID], item)
		}
		if item.InstallImageID > 0 {
			add(installKeys[item.InstallImageID], item)
		}
		if key := bpInstallKeys[item.BootProfileID]; key != "" {
			add(key, item)
		}
	}
	return uses
}
