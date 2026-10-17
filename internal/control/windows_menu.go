package control

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

func BuildMenuFromFormWithProfiles(form url.Values, baseURL string, available []WindowsProfile) ([]byte, error) {
	selected := map[int64]bool{}
	byID := map[int64]WindowsProfile{}
	for _, p := range available {
		byID[p.ID] = p
	}
	for key, values := range form {
		if !strings.HasPrefix(key, "winiso_") || len(values) == 0 || values[0] != "on" {
			continue
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(key, "winiso_"), 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("invalid Windows profile selection")
		}
		selected[id] = true
	}
	if len(selected) == 0 {
		return BuildMenuFromForm(form, baseURL)
	}
	base, err := validatedBootURL(baseURL)
	if err != nil {
		return nil, err
	}
	profiles := []menuProfile{}
	folders := map[string]string{}
	if form.Get("ubuntu") == "on" {
		profiles = append(profiles, menuProfile{id: "ubuntu", title: "Ubuntu Desktop 26.04.1", base: base})
		folders["ubuntu"] = form.Get("folder_ubuntu")
	}
	if form.Get("windows10") == "on" {
		profiles = append(profiles, menuProfile{id: "windows10", title: "Windows 10 installer - WinPE", base: base})
		folders["windows10"] = form.Get("folder_windows10")
	}
	if form.Get("windows11") == "on" {
		profiles = append(profiles, menuProfile{id: "windows11", title: "Windows 11 installer - WinPE", base: base})
		folders["windows11"] = form.Get("folder_windows11")
	}
	for _, p := range available {
		if !selected[p.ID] {
			continue
		}
		if p.State != "ready" || (p.InstallFile != "install.wim" && p.InstallFile != "install.esd") || !safeMenuText(p.Title, 80) || !isASCII(p.Title) {
			return nil, fmt.Errorf("Windows profile %d is not ready for PXE", p.ID)
		}
		key := fmt.Sprintf("winiso_%d", p.ID)
		folder := p.Folder
		if submitted, ok := form["folder_"+key]; ok && len(submitted) > 0 {
			folder = submitted[0]
		}
		profiles = append(profiles, menuProfile{id: key, title: p.Title, base: base, windowsISOID: p.ID})
		folders[key] = folder
		delete(selected, p.ID)
	}
	if len(selected) != 0 {
		return nil, fmt.Errorf("unknown Windows profile")
	}
	for _, folder := range folders {
		if err := validateMenuFolder(folder); err != nil {
			return nil, err
		}
	}
	return buildMenuTreeWithProfiles(form.Get("title"), form.Get("label"), form.Get("message"), profiles, folders)
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}

func menuWindowsProfileIDs(script string) map[int64]bool {
	selected := map[int64]bool{}
	for _, line := range strings.Split(script, "\n") {
		if !strings.HasPrefix(line, menuWindowsMarker) {
			continue
		}
		data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(line, menuWindowsMarker))
		if err != nil {
			return selected
		}
		var ids []int64
		if json.Unmarshal(data, &ids) != nil {
			return selected
		}
		for _, id := range ids {
			if id > 0 {
				selected[id] = true
			}
		}
		return selected
	}
	return selected
}
