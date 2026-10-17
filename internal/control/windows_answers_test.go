package control

import (
	"strings"
	"testing"
)

func TestValidateWindowsAnswer(t *testing.T) {
	valid := []byte(`<?xml version="1.0" encoding="utf-8"?><unattend xmlns="urn:schemas-microsoft-com:unattend"><settings pass="windowsPE"/></unattend>`)
	if err := ValidateWindowsAnswer(valid); err != nil {
		t.Fatalf("valid answer file rejected: %v", err)
	}
	t.Run("UTF-8 BOM", func(t *testing.T) {
		withBOM := append([]byte{0xef, 0xbb, 0xbf}, valid...)
		if err := ValidateWindowsAnswer(withBOM); err != nil {
			t.Fatalf("valid answer file with BOM rejected: %v", err)
		}
	})
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"empty", nil},
		{"malformed", []byte(`<unattend xmlns="urn:schemas-microsoft-com:unattend">`)},
		{"wrong root", []byte(`<other xmlns="urn:schemas-microsoft-com:unattend"/>`)},
		{"wrong namespace", []byte(`<unattend/>`)},
		{"DTD", []byte(`<!DOCTYPE unattend [<!ENTITY x "secret">]><unattend xmlns="urn:schemas-microsoft-com:unattend">&x;</unattend>`)},
		{"invalid UTF-8", []byte{0xff, 0xfe}},
		{"oversize", []byte(`<unattend xmlns="urn:schemas-microsoft-com:unattend">` + strings.Repeat(" ", 1<<20) + `</unattend>`)},
		{"two roots", []byte(`<unattend xmlns="urn:schemas-microsoft-com:unattend"/><unattend xmlns="urn:schemas-microsoft-com:unattend"/>`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateWindowsAnswer(tc.body); err == nil {
				t.Fatal("accepted invalid answer file")
			}
		})
	}
}
