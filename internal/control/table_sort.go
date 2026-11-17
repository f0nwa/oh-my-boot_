package control

import (
	"net/url"
	"strings"
	"time"
)

// TableColumn represents a sortable table header column.
type TableColumn struct {
	Label  string
	Link   string
	Active bool
	Desc   bool
}

// MakeSortCol generates a TableColumn for a table header with bidirectional toggle.
// - baseURL: the page URL, e.g. "/installations"
// - currentSort: the column currently being sorted
// - currentDir: the current direction ("asc" or "desc")
// - targetCol: the column this header controls
// - label: display text for the column
// - defaultDir: initial sort direction when clicking an inactive column ("asc" or "desc")
// - queryParams: additional query params (q, show, etc.) to preserve
func MakeSortCol(baseURL, currentSort, currentDir, targetCol, label, defaultDir string, queryParams url.Values) TableColumn {
	if defaultDir != "desc" {
		defaultDir = "asc"
	}
	active := currentSort == targetCol
	var desc bool
	var nextDir string

	if active {
		// When active, currentDir determines current state; clicking toggles to opposite
		if currentDir == "desc" {
			desc = true
			nextDir = "asc"
		} else {
			desc = false
			nextDir = "desc"
		}
	} else {
		// When inactive, clicking activates it with defaultDir
		desc = defaultDir == "desc"
		nextDir = defaultDir
	}

	v := url.Values{}
	if queryParams != nil {
		for k, vals := range queryParams {
			if k == "sort" || k == "dir" {
				continue
			}
			for _, val := range vals {
				if val != "" {
					v.Add(k, val)
				}
			}
		}
	}
	v.Set("sort", targetCol)
	if nextDir != "" {
		v.Set("dir", nextDir)
	}

	link := baseURL
	if encoded := v.Encode(); encoded != "" {
		link += "?" + encoded
	}

	return TableColumn{
		Label:  label,
		Link:   link,
		Active: active,
		Desc:   desc,
	}
}

// compareStrings compares two strings case-insensitively, placing empty values last.
func compareStrings(a, b string, desc bool) int {
	var res int
	switch {
	case a == "" && b == "":
		res = 0
	case a == "":
		res = 1 // empty values placed last
	case b == "":
		res = -1
	default:
		res = strings.Compare(strings.ToLower(a), strings.ToLower(b))
	}
	if desc {
		return -res
	}
	return res
}

// compareInts compares two integers.
func compareInts(a, b int, desc bool) int {
	if desc {
		return b - a
	}
	return a - b
}

// compareInt64s compares two 64-bit integers.
func compareInt64s(a, b int64, desc bool) int {
	if desc {
		if b > a {
			return 1
		} else if b < a {
			return -1
		}
		return 0
	}
	if a > b {
		return 1
	} else if a < b {
		return -1
	}
	return 0
}

// compareTimes compares two timestamps, placing zero timestamps last.
func compareTimes(a, b time.Time, desc bool) int {
	var res int
	switch {
	case a.IsZero() && b.IsZero():
		res = 0
	case a.IsZero():
		res = 1
	case b.IsZero():
		res = -1
	default:
		res = a.Compare(b)
	}
	if desc {
		return -res
	}
	return res
}
