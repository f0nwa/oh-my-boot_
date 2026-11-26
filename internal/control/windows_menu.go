package control

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
)

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

func menuWindowsAnswerRefs(script string) []menuWindowsAnswerRef {
	var selected []menuWindowsAnswerRef
	seen := map[[2]int64]bool{}
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
			if ref.ID > 0 && ref.ProfileID >= 0 && validAnswerHash(ref.SHA256) {
				key := [2]int64{ref.ID, ref.ProfileID}
				if !seen[key] {
					seen[key] = true
					selected = append(selected, ref)
				}
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

type menuWindowsWIMRef struct {
	BootImageID    int64  `json:"boot_image_id,omitempty"`
	BootSHA256     string `json:"boot_sha256,omitempty"`
	InstallImageID int64  `json:"install_image_id,omitempty"`
	InstallFile    string `json:"install_file,omitempty"`
	BareWIM        bool   `json:"bare_wim,omitempty"`
}

func menuWindowsWIMRefs(script string) []menuWindowsWIMRef {
	var selected []menuWindowsWIMRef
	seen := map[menuWindowsWIMRef]bool{}
	for _, line := range strings.Split(script, "\n") {
		if !strings.HasPrefix(line, menuWIMMarker) {
			continue
		}
		data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(line, menuWIMMarker))
		if err != nil {
			return selected
		}
		var refs []menuWindowsWIMRef
		if json.Unmarshal(data, &refs) != nil {
			return selected
		}
		for _, ref := range refs {
			if ref.BootImageID < 0 || ref.InstallImageID < 0 || (ref.BootImageID == 0 && ref.InstallImageID == 0) {
				continue
			}
			if ref.BootSHA256 != "" && !validAnswerHash(ref.BootSHA256) {
				continue
			}
			if !seen[ref] {
				seen[ref] = true
				selected = append(selected, ref)
			}
		}
		return selected
	}
	return selected
}
