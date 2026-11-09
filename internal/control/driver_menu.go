package control

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"omb/internal/agent"
)

type menuDriverConfigRef struct {
	ID           int64  `json:"id"`
	ProfileID    int64  `json:"profile_id"`
	AnswerID     int64  `json:"answer_id"`
	AnswerSHA256 string `json:"answer_sha256"`
	WIMKey       string `json:"wim_key"`
	WIMSHA256    string `json:"wim_sha256"`
	ScriptSHA256 string `json:"script_sha256"`
}

func menuDriverConfigRefs(script string) map[int64]menuDriverConfigRef {
	selected := map[int64]menuDriverConfigRef{}
	for _, line := range strings.Split(script, "\n") {
		if !strings.HasPrefix(line, menuDriverMarker) {
			continue
		}
		data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(line, menuDriverMarker))
		if err != nil {
			return selected
		}
		var refs []menuDriverConfigRef
		if json.Unmarshal(data, &refs) != nil {
			return selected
		}
		for _, ref := range refs {
			if ref.ID > 0 && ref.ProfileID > 0 && validAnswerHash(ref.WIMKey) && validAnswerHash(ref.WIMSHA256) && validAnswerHash(ref.ScriptSHA256) && (ref.AnswerID == 0 || (ref.AnswerID > 0 && validAnswerHash(ref.AnswerSHA256))) {
				selected[ref.ID] = ref
			}
		}
		return selected
	}
	return selected
}

func updateMenuDriverScriptHashes(script string, updates []agent.DriverScriptUpdate) (string, bool, error) {
	if len(updates) == 0 || !strings.Contains(script, menuDriverMarker) {
		return script, false, nil
	}
	byID := make(map[int64]agent.DriverScriptUpdate, len(updates))
	for _, u := range updates {
		byID[u.ConfigID] = u
	}
	lines := strings.Split(script, "\n")
	changed := false
	seen := false
	for i, line := range lines {
		trimmed := strings.TrimSuffix(line, "\r")
		if !strings.HasPrefix(trimmed, menuDriverMarker) {
			continue
		}
		if seen {
			return "", false, fmt.Errorf("duplicate driver reference marker in menu script")
		}
		seen = true
		ending := ""
		if strings.HasSuffix(line, "\r") {
			ending = "\r"
		}
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(trimmed, menuDriverMarker))
		if err != nil {
			return "", false, err
		}
		var refs []menuDriverConfigRef
		if err := json.Unmarshal(raw, &refs); err != nil {
			return "", false, err
		}
		for j := range refs {
			u, ok := byID[refs[j].ID]
			if !ok {
				continue
			}
			if u.OldSHA != u.NewSHA && refs[j].ScriptSHA256 != u.OldSHA && refs[j].ScriptSHA256 != u.NewSHA {
				return "", false, fmt.Errorf("menu driver reference %d checksum mismatch", refs[j].ID)
			}
			if refs[j].ScriptSHA256 != u.NewSHA {
				refs[j].ScriptSHA256 = u.NewSHA
				changed = true
			}
		}
		if changed {
			encoded, err := json.Marshal(refs)
			if err != nil {
				return "", false, err
			}
			lines[i] = menuDriverMarker + base64.RawURLEncoding.EncodeToString(encoded) + ending
		}
	}
	if !changed {
		return script, false, nil
	}
	return strings.Join(lines, "\n"), true, nil
}
