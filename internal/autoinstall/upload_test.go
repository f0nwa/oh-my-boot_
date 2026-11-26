package autoinstall

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestValidateUploadedUserDataValidCases(t *testing.T) {
	cases := []struct {
		name       string
		input      []byte
		wantMode   string
		wantSerial string
	}{
		{
			name: "interactive storage",
			input: []byte(`#cloud-config
autoinstall:
  version: 1
  interactive-sections:
    - storage
`),
			wantMode: "interactive",
		},
		{
			name: "layout with serial match",
			input: []byte(`#cloud-config
autoinstall:
  version: 1
  storage:
    layout:
      name: lvm
      match:
        serial: "SN-998877"
`),
			wantMode:   "serial",
			wantSerial: "SN-998877",
		},
		{
			name: "layout without serial",
			input: []byte(`#cloud-config
autoinstall:
  version: 1
  storage:
    layout:
      name: direct
`),
			wantMode: "default",
		},
		{
			name: "custom storage config",
			input: []byte(`#cloud-config
autoinstall:
  version: 1
  storage:
    config:
      - type: disk
        id: disk-0
        ptable: gpt
`),
			wantMode: "custom",
		},
		{
			name: "no storage section and UTF-8 BOM",
			input: append([]byte{0xef, 0xbb, 0xbf}, []byte(`#cloud-config
autoinstall:
  version: 1
  locale: en_US.UTF-8
`)...),
			wantMode: "unknown",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clean, meta, err := ValidateUploadedUserData(tc.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if bytes.HasPrefix(clean, []byte{0xef, 0xbb, 0xbf}) {
				t.Fatal("BOM was not stripped")
			}
			if meta.DiskMode != tc.wantMode || meta.DiskSerial != tc.wantSerial {
				t.Fatalf("meta = %+v, want mode=%q serial=%q", meta, tc.wantMode, tc.wantSerial)
			}
		})
	}
}

func TestValidateUploadedUserDataErrors(t *testing.T) {
	cases := []struct {
		name  string
		input []byte
		want  error
	}{
		{"empty", nil, ErrUploadEmpty},
		{"too large", bytes.Repeat([]byte("a"), MaxUploadBytes+1), ErrUploadTooLarge},
		{"invalid utf8", []byte{0xff, 0xfe}, ErrUploadEncoding},
		{"nul byte", []byte("#cloud-config\nautoinstall:\n  version: 1\x00\n"), ErrUploadEncoding},
		{"missing header", []byte("autoinstall:\n  version: 1\n"), ErrUploadCloudConfig},
		{"bad yaml", []byte("#cloud-config\nautoinstall: [unclosed\n"), ErrUploadYAML},
		{"multi document", []byte("#cloud-config\nautoinstall:\n  version: 1\n---\nfoo: bar\n"), ErrUploadYAML},
		{"missing autoinstall", []byte("#cloud-config\npackages:\n  - curl\n"), ErrUploadMissingSection},
		{"missing version", []byte("#cloud-config\nautoinstall:\n  locale: en_US.UTF-8\n"), ErrUploadVersion},
		{"wrong version", []byte("#cloud-config\nautoinstall:\n  version: 2\n"), ErrUploadVersion},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ValidateUploadedUserData(tc.input)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestValidateUploadedUserDataExactLimit(t *testing.T) {
	prefix := "#cloud-config\nautoinstall:\n  version: 1\n# "
	body := prefix + strings.Repeat("x", MaxUploadBytes-len(prefix))
	clean, meta, err := ValidateUploadedUserData([]byte(body))
	if err != nil {
		t.Fatalf("exact 64 KiB rejected: %v", err)
	}
	if len(clean) != MaxUploadBytes || meta.DiskMode != "unknown" {
		t.Fatalf("len=%d meta=%+v", len(clean), meta)
	}
}
