package control

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var errAnswerCheckConflict = errors.New("answer file changed or is not ready")

// AnswerCheck records an operator's observation about one saved answer file.
// It is independent of boot entries, menu publication and technical readiness.
type AnswerCheck struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	AnswerID  int64     `json:"answer_id"`
	SHA256    string    `json:"sha256"`
	Result    string    `json:"result"`
	Machine   string    `json:"machine"`
	Comment   string    `json:"comment"`
	UserID    int64     `json:"-"`
	Author    string    `json:"author"`
	CreatedAt time.Time `json:"created_at"`
}

func validAnswerCheck(c AnswerCheck) bool {
	if (c.Kind != "windows" && c.Kind != "ubuntu") || c.AnswerID <= 0 || c.UserID <= 0 || !validAnswerHash(c.SHA256) || (c.Result != "success" && c.Result != "failure") {
		return false
	}
	for _, text := range []struct {
		value     string
		max       int
		multiline bool
	}{{c.Machine, 200, false}, {c.Comment, 2000, true}} {
		if !utf8.ValidString(text.value) || utf8.RuneCountInString(text.value) > text.max {
			return false
		}
		for _, r := range text.value {
			if unicode.IsControl(r) && !(text.multiline && (r == '\n' || r == '\r' || r == '\t')) {
				return false
			}
		}
	}
	return true
}

func (c AnswerCheck) Label() string {
	switch c.Result {
	case "success":
		return "Установка успешна"
	case "failure":
		return "Ошибка установки"
	}
	return "Не проверено"
}

func checkFileColumn(kind string) (string, error) {
	switch kind {
	case "windows":
		return "windows_answer_id", nil
	case "ubuntu":
		return "ubuntu_autoinstall_id", nil
	}
	return "", fmt.Errorf("invalid answer file kind")
}

func normalizedAnswerCheck(c AnswerCheck) AnswerCheck {
	c.Machine = strings.TrimSpace(c.Machine)
	c.Comment = strings.TrimSpace(c.Comment)
	return c
}
