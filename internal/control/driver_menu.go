package control

import (
	"encoding/base64"
	"encoding/json"
	"strings"
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
