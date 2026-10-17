package control

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

func BuildMenuFromFormWithProfiles(form url.Values, baseURL string, available []WindowsProfile) ([]byte, error) {
	return BuildMenuFromFormWithProfilesAndAnswers(form, baseURL, available, nil)
}

func BuildMenuFromFormWithProfilesAndAnswers(form url.Values, baseURL string, available []WindowsProfile, answers []WindowsAnswerVariant) ([]byte, error) {
	selected := map[int64]bool{}
	selectedAnswers := map[int64]bool{}
	byID := map[int64]WindowsProfile{}
	for _, p := range available {
		byID[p.ID] = p
	}
	for key, values := range form {
		if strings.HasPrefix(key, "winanswer_") && len(values) > 0 && values[0] == "on" {
			id, err := strconv.ParseInt(strings.TrimPrefix(key, "winanswer_"), 10, 64)
			if err != nil || id <= 0 {
				return nil, fmt.Errorf("invalid Windows answer selection")
			}
			selectedAnswers[id] = true
			continue
		}
		if !strings.HasPrefix(key, "winiso_") || len(values) == 0 || values[0] != "on" {
			continue
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(key, "winiso_"), 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("invalid Windows profile selection")
		}
		selected[id] = true
	}
	if len(selected) == 0 && len(selectedAnswers) == 0 {
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
	for _, answer := range answers {
		if !selectedAnswers[answer.ID] {
			continue
		}
		parent, ok := byID[answer.ProfileID]
		if !ok || parent.State != "ready" || (parent.InstallFile != "install.wim" && parent.InstallFile != "install.esd") || answer.State != "ready" || answer.Size <= 0 || !safeMenuText(answer.Title, 80) || !isASCII(answer.Title) || !validAnswerHash(answer.SHA256) {
			return nil, fmt.Errorf("Windows answer %d is not ready for PXE", answer.ID)
		}
		key := fmt.Sprintf("winanswer_%d", answer.ID)
		folder := answer.Folder
		if submitted, ok := form["folder_"+key]; ok && len(submitted) > 0 {
			folder = submitted[0]
		}
		if !isASCII(folder) {
			return nil, fmt.Errorf("Windows answer folder must be ASCII")
		}
		profiles = append(profiles, menuProfile{id: key, title: answer.Title, base: base, windowsISOID: answer.ProfileID, answerID: answer.ID, answerSHA256: answer.SHA256})
		folders[key] = folder
		delete(selectedAnswers, answer.ID)
	}
	if len(selectedAnswers) != 0 {
		return nil, fmt.Errorf("unknown Windows answer")
	}
	for _, folder := range folders {
		if err := validateMenuFolder(folder); err != nil {
			return nil, err
		}
	}
	return buildMenuTreeWithProfiles(form.Get("title"), form.Get("label"), form.Get("message"), profiles, folders)
}

func validAnswerHash(hash string) bool {
	if len(hash) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(hash)
	return err == nil && hex.EncodeToString(decoded) == hash
}

type menuWindowsAnswerRef struct {
	ID        int64  `json:"id"`
	ProfileID int64  `json:"profile_id"`
	SHA256    string `json:"sha256"`
}

func menuWindowsAnswerRefs(script string) map[int64]menuWindowsAnswerRef {
	selected := map[int64]menuWindowsAnswerRef{}
	for _, line := range strings.Split(script, "\n") {
		if !strings.HasPrefix(line, menuAnswerMarker) {
			continue
		}
		data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(line, menuAnswerMarker))
		if err != nil {
			return selected
		}
		var refs []menuWindowsAnswerRef
		if json.Unmarshal(data, &refs) != nil {
			return selected
		}
		for _, ref := range refs {
			if ref.ID > 0 && ref.ProfileID > 0 && validAnswerHash(ref.SHA256) {
				selected[ref.ID] = ref
			}
		}
		return selected
	}
	return selected
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
