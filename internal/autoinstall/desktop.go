package autoinstall

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

type desktopInstall struct {
	Install bool `yaml:"install"`
}
type desktopSource struct {
	ID string `yaml:"id"`
}

var packagePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]*(:[a-z0-9][a-z0-9-]*)?$`)
var sourcePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

func uniqueStrings(input []string) []string {
	var result []string
	seen := map[string]bool{}
	for _, s := range input {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}
func validateDesktop(v Values) error {
	layouts := strings.Split(v.Keyboard, ",")
	if len(layouts) > 4 {
		return errors.New("keyboard: at most four layouts")
	}
	seen := map[string]bool{}
	for _, l := range layouts {
		if !keyboardPattern.MatchString(l) || seen[l] {
			return errors.New("keyboard: invalid or duplicate layout")
		}
		seen[l] = true
	}
	if v.KeyboardVariant != "" {
		variants := strings.Split(v.KeyboardVariant, ",")
		if len(variants) != len(layouts) {
			return errors.New("keyboard variant: one position per layout required")
		}
		for _, x := range variants {
			if x != "" && !keyboardPattern.MatchString(x) {
				return errors.New("keyboard variant: invalid variant")
			}
		}
	}
	switch v.KeyboardToggle {
	case "", "alt_shift_toggle", "ctrl_shift_toggle", "win_space_toggle":
	default:
		return errors.New("keyboard toggle: invalid shortcut")
	}
	if !validText(v.RealName) || utf8.RuneCountInString(v.RealName) > 128 {
		return errors.New("realname: invalid full name")
	}
	if v.SourceID != "" && !sourcePattern.MatchString(v.SourceID) {
		return errors.New("source: invalid identifier")
	}
	if len(v.Packages) > 128 {
		return errors.New("packages: at most 128 packages")
	}
	for _, p := range v.Packages {
		if len(p) > 128 || !packagePattern.MatchString(p) {
			return errors.New("packages: invalid package name")
		}
	}
	if len(v.PostInstallScript) > 16*1024 || !utf8.ValidString(v.PostInstallScript) || strings.ContainsRune(v.PostInstallScript, 0) {
		return errors.New("script: invalid or larger than 16 KiB")
	}
	switch v.Shutdown {
	case "", "reboot", "poweroff":
	default:
		return errors.New("shutdown: invalid action")
	}
	return nil
}
func applyDesktop(ai *config, v Values) {
	if v.SourceID != "" {
		ai.Source = &desktopSource{ID: v.SourceID}
	}
	if v.Drivers {
		ai.Drivers = &desktopInstall{Install: true}
	}
	if v.Codecs {
		ai.Codecs = &desktopInstall{Install: true}
	}
	ai.Packages = v.Packages
	ai.Shutdown = v.Shutdown
	if v.PostInstallScript != "" {
		ai.LateCommands = [][]string{{"curtin", "in-target", "--", "/bin/sh", "-eu", "-c", v.PostInstallScript}}
	}
}
