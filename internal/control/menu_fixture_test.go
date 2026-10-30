package control

import (
	"fmt"
	"net/url"
)

// Sources are persisted-model fixtures: the same ISO may have independent choices.
func testInstallationSources(profiles []WindowsProfile, answers []WindowsAnswerVariant, configs []DriverConfig) InstallationMenuSources {
	source := InstallationMenuSources{}
	for _, p := range profiles {
		source.BootProfiles = append(source.BootProfiles, BootProfile{ID: p.ID, Kind: BootProfileWindowsISO, Title: p.Title, WindowsProfileID: p.ID})
		source.Installations = append(source.Installations, Installation{ID: 1000 + p.ID, BootProfileID: p.ID, Title: p.Title, Folder: p.Folder})
	}
	for _, a := range answers {
		source.Installations = append(source.Installations, Installation{ID: 2000 + a.ID, BootProfileID: a.ProfileID, Title: a.Title, Folder: a.Folder, AnswerID: a.ID})
	}
	for _, c := range configs {
		source.Installations = append(source.Installations, Installation{ID: 3000 + c.ID, BootProfileID: c.ProfileID, Title: c.Title, Folder: c.Folder, DriverConfigID: c.ID})
	}
	return source
}

func buildTestMenuFromDraft(d MenuDraft, base string, profiles []WindowsProfile, answers []WindowsAnswerVariant, configs []DriverConfig, sources ...InstallationMenuSources) ([]byte, error) {
	if len(sources) == 0 {
		sources = []InstallationMenuSources{testInstallationSources(profiles, answers, configs)}
	}
	return BuildMenuFromDraft(d, base, profiles, answers, configs, sources[0])
}
func versionItems(d MenuDraft) []MenuVersionItem {
	items := make([]MenuVersionItem, 0, len(d.Items))
	for i, item := range d.Items {
		items = append(items, MenuVersionItem{InstallationID: item.InstallationID, Position: i, Title: item.Title, Folder: item.Folder})
	}
	return items
}
func buildTestMenuWithProfiles(form url.Values, base string, profiles []WindowsProfile) ([]byte, error) {
	return buildTestMenuWithDrivers(form, base, profiles, nil, nil)
}
func buildTestMenuWithAnswers(form url.Values, base string, profiles []WindowsProfile, answers []WindowsAnswerVariant) ([]byte, error) {
	return buildTestMenuWithDrivers(form, base, profiles, answers, nil)
}
func buildTestMenuFromForm(form url.Values, base string) ([]byte, error) {
	return buildTestMenuWithDrivers(form, base, nil, nil, nil)
}
func buildTestMenuWithDrivers(form url.Values, base string, profiles []WindowsProfile, answers []WindowsAnswerVariant, configs []DriverConfig) ([]byte, error) {
	source := testInstallationSources(profiles, answers, configs)
	source.BootProfiles = append(source.BootProfiles, BootProfile{ID: 999, Kind: BootProfileUbuntuBuiltin, Title: "Ubuntu Desktop"})
	source.Installations = append([]Installation{{ID: 999, BootProfileID: 999, Title: "Ubuntu Desktop 26.04.1"}}, source.Installations...)
	draft := MenuDraft{Title: form.Get("title"), Label: form.Get("label"), Message: form.Get("message")}
	selected := map[string]bool{}
	for key, values := range form {
		if len(values) > 0 && values[0] == "on" && len(key) > 8 && key[:8] == "install_" {
			selected[key] = true
		}
	}
	for _, item := range source.Installations {
		key := fmt.Sprintf("install_%d", item.ID)
		if !selected[key] {
			continue
		}
		folder := item.Folder
		if values, ok := form["folder_"+key]; ok && len(values) > 0 {
			folder = values[0]
		}
		draft.Items = append(draft.Items, MenuDraftItem{InstallationID: item.ID, Folder: folder})
		delete(selected, key)
	}
	for key := range selected {
		return nil, unknownMenuItemError(key)
	}
	return BuildMenuFromDraft(draft, base, profiles, answers, configs, source)
}
