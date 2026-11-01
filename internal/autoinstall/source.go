package autoinstall

import (
	"errors"
	"go.yaml.in/yaml/v3"
	"sort"
	"strings"
	"unicode/utf8"
)

type SourceOption struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Default bool   `json:"default"`
}
type SourceCatalog struct {
	ISOSHA256 string         `json:"iso_sha256"`
	Sources   []SourceOption `json:"sources"`
}

func ParseInstallSources(data []byte) ([]SourceOption, error) {
	if len(data) > 64*1024 {
		return nil, errors.New("source: metadata too large")
	}
	var rows []struct {
		ID      string `yaml:"id"`
		Name    any    `yaml:"name"`
		Default bool   `yaml:"default"`
	}
	if e := yaml.Unmarshal(data, &rows); e != nil || len(rows) == 0 || len(rows) > 32 {
		return nil, errors.New("source: invalid metadata")
	}
	out := []SourceOption{}
	seen := map[string]bool{}
	defaults := 0
	for _, row := range rows {
		if !sourcePattern.MatchString(row.ID) || seen[row.ID] {
			return nil, errors.New("source: invalid or duplicate identifier")
		}
		seen[row.ID] = true
		if row.ID != "ubuntu-desktop" && !strings.HasPrefix(row.ID, "ubuntu-desktop-") {
			continue
		}
		name := ""
		switch n := row.Name.(type) {
		case string:
			name = n
		case map[string]any:
			if en, ok := n["en"].(string); ok {
				name = en
			} else {
				keys := []string{}
				for k := range n {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					if v, ok := n[k].(string); ok {
						name = v
						break
					}
				}
			}
		}
		if name == "" {
			name = row.ID
		}
		if !validText(name) || utf8.RuneCountInString(name) > 256 {
			return nil, errors.New("source: invalid name")
		}
		if row.Default {
			defaults++
		}
		out = append(out, SourceOption{ID: row.ID, Name: name, Default: row.Default})
	}
	if len(out) == 0 || defaults > 1 {
		return nil, errors.New("source: desktop sources unavailable")
	}
	return out, nil
}
func ValidateSource(v Values, c SourceCatalog) error {
	if v.SourceID == "" {
		return nil
	}
	if len(c.ISOSHA256) != 64 {
		return errors.New("source: verified ISO metadata unavailable")
	}
	for _, s := range c.Sources {
		if s.ID == v.SourceID {
			return nil
		}
	}
	return errors.New("source: selected applications unavailable in ISO")
}
