package control

import (
	"strings"
	"testing"
	"time"

	"pxe-platform/internal/images"
)

func TestWindowsProfileSourceProblemExplainsCause(t *testing.T) {
	modified := time.Date(2025, 8, 26, 6, 9, 33, 0, time.UTC)
	profile := WindowsProfile{ID: 1, WindowsProfileSource: WindowsProfileSource{StorageID: 6, StorageRoot: "/mnt/nas", Path: "win10.iso", Size: 100, Modified: modified, Title: "Windows 10x64", Folder: "Windows"}}
	ready := images.Storage{ID: 6, Name: "NAS ISO", Kind: "smb", Root: "/mnt/nas", State: "ready"}
	image := images.Image{StorageID: 6, Path: "win10.iso", Size: 100, Modified: modified, Present: true}
	down := ready
	down.State = "unavailable"
	down.Error = "Хранилище недоступно: host is down"
	resized := image
	resized.Size = 101
	missing := image
	missing.Present = false
	for _, tc := range []struct {
		name    string
		catalog images.Catalog
		want    []string
		reject  string
	}{
		{"ok", images.Catalog{Storages: []images.Storage{ready}, Images: []images.Image{image}}, nil, ""},
		{"storage down", images.Catalog{Storages: []images.Storage{down}, Images: []images.Image{image}}, []string{"хранилище «NAS ISO» недоступно", "host is down"}, "изменился"},
		{"storage removed", images.Catalog{Images: []images.Image{image}}, []string{"хранилище профиля 1 не найдено"}, "изменился"},
		{"iso missing", images.Catalog{Storages: []images.Storage{ready}, Images: []images.Image{missing}}, []string{"исходный ISO профиля 1 не найден"}, "изменился"},
		{"iso changed", images.Catalog{Storages: []images.Storage{ready}, Images: []images.Image{resized}}, []string{"исходный ISO профиля 1 изменился"}, ""},
	} {
		err := windowsProfileSourceProblem(tc.catalog, profile)
		if tc.want == nil {
			if err != nil {
				t.Fatalf("%s: unexpected error %v", tc.name, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%s: no error", tc.name)
		}
		for _, want := range tc.want {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("%s: %q lacks %q", tc.name, err, want)
			}
		}
		if tc.reject != "" && strings.Contains(err.Error(), tc.reject) {
			t.Fatalf("%s: %q must not say %q", tc.name, err, tc.reject)
		}
	}
}
