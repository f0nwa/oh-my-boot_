// Package ueficausbtest gives tests of other packages a ueficausb.Builder
// over a fake profile ISO and a fake wimlib-imagex.
package ueficausbtest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omb/internal/ueficausb"
)

type runner struct{}

func (runner) Run(_ context.Context, _ string, _ string, args ...string) ([]byte, error) {
	if args[0] == "export" {
		return nil, os.WriteFile(args[3], []byte("exported-wim"), 0644)
	}
	if args[0] == "extract" {
		for _, a := range args {
			if dest, ok := strings.CutPrefix(a, "--dest-dir="); ok {
				if err := os.MkdirAll(dest, 0755); err != nil {
					return nil, err
				}
				if strings.Contains(strings.Join(args, " "), "bootmgfw_EX.efi") {
					return nil, os.WriteFile(filepath.Join(dest, "bootmgfw_EX.efi"), []byte("ex-loader"), 0644)
				}
			}
		}
	}
	return nil, nil
}

// NewBuilder returns a builder whose MediaRoot holds a minimal ISO of profileID.
func NewBuilder(t *testing.T, profileID string) *ueficausb.Builder {
	t.Helper()
	media := t.TempDir()
	for name, data := range map[string]string{
		"bootmgr":              "bios-loader",
		"bootmgr.efi":          "pca2011-bootmgr",
		"boot/bcd":             "bios-bcd",
		"efi/boot/bootx64.efi": "pca2011-loader",
		"sources/boot.wim":     "source-wim",
	} {
		full := filepath.Join(media, profileID, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return &ueficausb.Builder{MediaRoot: media, CacheRoot: t.TempDir(), Runner: runner{}}
}
