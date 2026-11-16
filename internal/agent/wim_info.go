package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// WindowsWIMInfo holds normalized installation image metadata extracted from a
// Windows install.wim or install.esd file.
type WindowsWIMInfo struct {
	Known  bool              `json:"known"`
	Images []WindowsWIMImage `json:"images,omitempty"`
}

// WindowsWIMImage describes a single Windows image index inside install.wim/esd.
type WindowsWIMImage struct {
	Index           int      `json:"index"`
	Name            string   `json:"name"`
	DisplayName     string   `json:"display_name,omitempty"`
	Architecture    string   `json:"architecture"`
	EditionID       string   `json:"edition_id,omitempty"`
	Languages       []string `json:"languages,omitempty"`
	DefaultLanguage string   `json:"default_language,omitempty"`
	Build           int      `json:"build,omitempty"`
}

// ParseWIMInfo parses the text output of `wimlib-imagex info`.
func ParseWIMInfo(raw []byte) (WindowsWIMInfo, error) {
	var images []WindowsWIMImage
	var current *WindowsWIMImage

	flush := func() {
		if current != nil && current.Index > 0 {
			images = append(images, *current)
		}
		current = nil
	}

	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		key, val, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if key == "Index" {
			flush()
			idx, err := strconv.Atoi(val)
			if err != nil || idx <= 0 {
				continue
			}
			current = &WindowsWIMImage{Index: idx}
			continue
		}
		if current == nil {
			continue
		}
		switch key {
		case "Name":
			current.Name = val
		case "Display Name":
			current.DisplayName = val
		case "Architecture":
			current.Architecture = normalizeWIMArchitecture(val)
		case "Edition ID":
			current.EditionID = val
		case "Languages":
			if fields := strings.Fields(val); len(fields) > 0 {
				current.Languages = fields
			}
		case "Default Language":
			current.DefaultLanguage = val
		case "Build":
			if build, err := strconv.Atoi(val); err == nil && build > 0 {
				current.Build = build
			}
		}
	}
	flush()
	if len(images) == 0 {
		return WindowsWIMInfo{}, fmt.Errorf("WIM image list is empty")
	}
	return WindowsWIMInfo{Known: true, Images: images}, nil
}

func normalizeWIMArchitecture(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "x86_64", "amd64":
		return "amd64"
	case "arm64", "aarch64":
		return "arm64"
	case "x86", "i386":
		return "x86"
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

func inspectWindowsWIMInfo(ctx context.Context, runner DriverCommandRunner, wimPath string) (WindowsWIMInfo, error) {
	if runner == nil {
		runner = ExecDriverCommandRunner{}
	}
	out, err := runner.Run(ctx, "", "wimlib-imagex", "info", wimPath)
	if err != nil {
		return WindowsWIMInfo{}, err
	}
	return ParseWIMInfo(out)
}
