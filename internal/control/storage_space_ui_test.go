package control

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"omb/internal/agent"
	"omb/internal/images"
)

type fakeDiskUsageAPI struct {
	report agent.DiskUsageReport
	err    error
}

func (f fakeDiskUsageAPI) DiskUsage(context.Context) (agent.DiskUsageReport, error) {
	return f.report, f.err
}

type spaceTestStore struct {
	authOnlyStore
	profiles      []WindowsProfile
	configs       []DriverConfig
	packages      []DriverPackage
	bootProfiles  []BootProfile
	installations []Installation
}

func (s spaceTestStore) WindowsProfiles(context.Context) ([]WindowsProfile, error) {
	return s.profiles, nil
}
func (s spaceTestStore) DriverConfigs(context.Context) ([]DriverConfig, error) { return s.configs, nil }
func (s spaceTestStore) DriverPackages(context.Context) ([]DriverPackage, error) {
	return s.packages, nil
}
func (s spaceTestStore) BootProfiles(context.Context) ([]BootProfile, error) {
	return s.bootProfiles, nil
}
func (s spaceTestStore) Installations(context.Context) ([]Installation, error) {
	return s.installations, nil
}
func (s spaceTestStore) Users(context.Context) ([]User, error) { return nil, nil }

func sampleSpaceTestServer(role, lang string, diskAPI DiskUsageAPI) *Server {
	wimKey := strings.Repeat("b", 64)
	api := &fakeImagesAPI{
		catalog: images.Catalog{
			Storages: []images.Storage{
				{
					ID:         5,
					Name:       "NAS Synology",
					Kind:       "smb",
					Server:     "192.168.10.10",
					Share:      "Downloads",
					Subpath:    "iso",
					State:      "ready",
					Uploads:    true,
					Total:      385 << 30,
					Free:       115 << 30,
					ISOBytes:   46 << 30,
					OtherBytes: 12 << 30,
				},
				{
					ID:         6,
					Name:       "NFS Archive",
					Kind:       "nfs",
					Server:     "192.168.10.20",
					ExportPath: "/volume1/iso",
					NFSVersion: "4.1",
					State:      "ready",
					Uploads:    false,
					Total:      200 << 30,
					Free:       80 << 30,
					ISOBytes:   20 << 30,
					OtherBytes: 5 << 30,
				},
			},
			Images: []images.Image{
				{StorageID: 5, Path: "win11.iso", Size: 46 << 30, Present: true},
			},
		},
	}
	store := spaceTestStore{
		authOnlyStore: authOnlyStore{user: User{ID: 1, Role: role, Language: lang, Enabled: true}},
		profiles: []WindowsProfile{
			{WindowsProfileSource: WindowsProfileSource{StorageID: 5, Path: "win11.iso", Title: "Windows 11 Pro"}, ID: 17},
		},
		configs: []DriverConfig{
			{ID: 14, ProfileID: 17, Title: "Intel LAN", Build: agent.DriverBuildResult{WIMKey: wimKey}},
		},
		packages: []DriverPackage{{SHA256: "p1"}, {SHA256: "p2"}},
		bootProfiles: []BootProfile{
			{ID: 101, Kind: BootProfileWindowsISO, Title: "Windows 11 Pro", WindowsProfileID: 17},
			{ID: 102, Kind: BootProfileUbuntuISO, Title: "Ubuntu 24.04", UbuntuSourceID: 4, UbuntuStorageID: 5, UbuntuPath: "ubuntu.iso"},
		},
		installations: []Installation{
			{ID: 21, BootProfileID: 101, Title: "Win 11 Clean"},
			{ID: 22, BootProfileID: 101, Title: "Win 11 Intel", DriverConfigID: 14},
			{ID: 23, BootProfileID: 102, Title: "Ubuntu Auto"},
		},
	}
	return &Server{
		Store:     store,
		Images:    api,
		DiskUsage: diskAPI,
	}
}

func TestStoragesPageRendersLocalDiskStorageBreakdownAndArtifacts(t *testing.T) {
	wimKey := strings.Repeat("b", 64)
	s := sampleSpaceTestServer("admin", langRU, fakeDiskUsageAPI{
		report: agent.DiskUsageReport{
			Filesystems:         []agent.LocalFilesystem{{ID: "d1", MountSample: "/srv/omb/tftp", TotalBytes: 48 << 30, FreeBytes: 18 << 30}},
			WindowsProfiles:     map[int64]int64{17: 580 << 20},
			WindowsDriverWIMs:   map[string]int64{wimKey: 680 << 20},
			UbuntuSources:       map[int64]int64{4: 412 << 20},
			DriverPackagesBytes: 520 << 20,
			UEFICAUSBBytes:      440 << 20,
		},
	})

	body := libraryGet(t, s, "/storages")
	for _, want := range []string{
		"Локальный диск сервера",
		"Подключённые хранилища",
		"Артефакты на сервере",
		"ISO OMB",
		"Прочее в папке",
		"Остальное на диске",
		"Вне папки:",
		"свободно 115,0 ГБ из 385,0 ГБ",
		"/installations/22/edit",
		"Win 11 Intel",
		"Ubuntu Auto",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("/storages lacks %q:\n%s", want, body)
		}
	}

	// Ensure observer receives filter links and no new <details> blocks exist inside the space cards.
	obs := sampleSpaceTestServer("observer", langRU, fakeDiskUsageAPI{
		report: agent.DiskUsageReport{
			Filesystems:     []agent.LocalFilesystem{{ID: "d1", MountSample: "/srv/omb/tftp", TotalBytes: 48 << 30, FreeBytes: 18 << 30}},
			WindowsProfiles: map[int64]int64{17: 580 << 20},
		},
	})
	obsBody := libraryGet(t, obs, "/storages")
	if !strings.Contains(obsBody, "/installations?q=install_21") {
		t.Fatalf("observer link missing in /storages: %s", obsBody)
	}
	if start := strings.Index(obsBody, `id="server-disk-card"`); start >= 0 {
		end := strings.Index(obsBody[start:], "Автоматическое восстановление подключений")
		chunk := obsBody[start:]
		if end > 0 {
			chunk = obsBody[start : start+end]
		}
		if strings.Contains(chunk, "<details") || strings.Contains(chunk, "<summary") {
			t.Fatalf("new storage space sections must not use <details>/<summary>: %s", chunk)
		}
	} else {
		t.Fatal("missing #server-disk-card")
	}

	// Graceful degradation when omb-agent is unavailable.
	degraded := sampleSpaceTestServer("admin", langRU, fakeDiskUsageAPI{err: errors.New("socket down")})
	degBody := libraryGet(t, degraded, "/storages")
	if !strings.Contains(degBody, "NAS Synology") || !strings.Contains(degBody, "Сведения о локальном диске временно недоступны") {
		t.Fatalf("degraded /storages failed: %s", degBody)
	}
}

func TestSystemOverviewRendersUpdatedStoragesCard(t *testing.T) {
	s := sampleSpaceTestServer("admin", langRU, fakeDiskUsageAPI{
		report: agent.DiskUsageReport{
			Filesystems:     []agent.LocalFilesystem{{ID: "d1", MountSample: "/srv/omb/tftp", TotalBytes: 48 << 30, FreeBytes: 18 << 30}},
			WindowsProfiles: map[int64]int64{17: 580 << 20},
			UEFICAUSBBytes:  440 << 20,
		},
	})
	body := libraryGet(t, s, "/system")
	for _, want := range []string{"NAS Synology", "Артефакты сервера", "свободно 115,0 ГБ из 385,0 ГБ", "свободно 18,0 ГБ из 48,0 ГБ"} {
		if !strings.Contains(body, want) {
			t.Fatalf("/system lacks %q:\n%s", want, body)
		}
	}
}

func TestStorageSpacePagesEnglishTranslation(t *testing.T) {
	cyr := regexp.MustCompile(`\p{Cyrillic}+`)
	wimKey := strings.Repeat("b", 64)
	s := sampleSpaceTestServer("admin", langEN, fakeDiskUsageAPI{
		report: agent.DiskUsageReport{
			Filesystems:         []agent.LocalFilesystem{{ID: "d1", MountSample: "/srv/omb/tftp", TotalBytes: 48 << 30, FreeBytes: 18 << 30}},
			WindowsProfiles:     map[int64]int64{17: 580 << 20},
			WindowsDriverWIMs:   map[string]int64{wimKey: 680 << 20},
			UbuntuSources:       map[int64]int64{4: 412 << 20},
			DriverPackagesBytes: 520 << 20,
			UEFICAUSBBytes:      440 << 20,
		},
	})
	for _, path := range []string{"/storages", "/system"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
		res := httptest.NewRecorder()
		s.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("%s status %d", path, res.Code)
		}
		body := strings.ReplaceAll(res.Body.String(), "Русский", "")
		if found := cyr.FindAllString(body, 5); len(found) > 0 {
			t.Fatalf("%s EN has untranslated cyrillic %q", path, found)
		}
	}
}
