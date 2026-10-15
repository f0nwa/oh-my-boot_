package control

import (
	"fmt"
	"strings"
	"unicode"
)

func safeMenuText(value string, max int) bool {
	if value == "" || len([]rune(value)) > max || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || strings.ContainsRune(".,!?:-_()", r) {
			continue
		}
		return false
	}
	return true
}

func BuildMenu(title, label, message string) ([]byte, error) {
	if !safeMenuText(title, 80) || !safeMenuText(label, 80) || !safeMenuText(message, 120) {
		return nil, fmt.Errorf("menu text contains unsupported characters or is too long")
	}
	script := "#!ipxe\n" +
		"menu " + title + "\n" +
		"item diagnostics " + label + "\n" +
		"item local Boot local disk\n" +
		"choose target || goto local\n" +
		"goto ${target}\n" +
		":diagnostics\n" +
		"echo " + message + "\n" +
		"sleep 3\n" +
		"goto local\n" +
		":local\n" +
		"exit\n"
	return []byte(script), nil
}
