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
	return BuildMenuFromFormWithDriverConfigs(form, baseURL, available, answers, nil)
}

func BuildMenuFromFormWithDriverConfigs(form url.Values, baseURL string, available []WindowsProfile, answers []WindowsAnswerVariant, configs []DriverConfig) ([]byte, error) {
	selected := map[string]bool{}
	for key, values := range form {
		if len(values) == 0 || values[0] != "on" {
			continue
		}
		for _, prefix := range []string{"winiso_", "winanswer_", "windriver_"} {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			id, err := strconv.ParseInt(strings.TrimPrefix(key, prefix), 10, 64)
			if err != nil || id <= 0 {
				return nil, fmt.Errorf("invalid menu selection %s", key)
			}
			selected[key] = true
		}
	}
	if len(selected) == 0 {
		return BuildMenuFromForm(form, baseURL)
	}
	// Keep the historical order: profiles, then answers, then driver configurations.
	var keys []string
	for _, p := range available {
		keys = append(keys, fmt.Sprintf("winiso_%d", p.ID))
	}
	for _, a := range answers {
		keys = append(keys, fmt.Sprintf("winanswer_%d", a.ID))
	}
	for _, c := range configs {
		keys = append(keys, fmt.Sprintf("windriver_%d", c.ID))
	}
	draft := MenuDraft{Title: form.Get("title"), Label: form.Get("label"), Message: form.Get("message"), Ubuntu: form.Get("ubuntu") == "on", UbuntuFolder: form.Get("folder_ubuntu")}
	for _, key := range keys {
		if !selected[key] {
			continue
		}
		item := MenuDraftItem{Key: key, Folder: defaultMenuFolder}
		if submitted, ok := form["folder_"+key]; ok && len(submitted) > 0 {
			item.Folder = submitted[0]
		}
		draft.Items = append(draft.Items, item)
		delete(selected, key)
	}
	for key := range selected {
		return nil, unknownMenuItemError(key)
	}
	return BuildMenuFromDraft(draft, baseURL, available, answers, configs)
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
