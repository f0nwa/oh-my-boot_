package control

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
