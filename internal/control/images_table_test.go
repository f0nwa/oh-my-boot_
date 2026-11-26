package control

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"omb/internal/agent"
	"omb/internal/images"
)

type imagesTableStore struct {
	*downloadsStore
	authors       ResourceAuthors
	names         map[string]string
	bootImages    []WindowsBootImage
	installImages []WindowsInstallImage
}

func (s *imagesTableStore) ListWindowsBootImages(context.Context) ([]WindowsBootImage, error) {
	return s.bootImages, nil
}
func (s *imagesTableStore) GetWindowsBootImage(_ context.Context, id int64) (WindowsBootImage, error) {
	for _, img := range s.bootImages {
		if img.ID == id {
			return img, nil
		}
	}
	return WindowsBootImage{}, errors.New("not found")
}
func (s *imagesTableStore) UpsertFileBootImage(context.Context, WindowsBootImageSource) (WindowsBootImage, error) {
	return WindowsBootImage{}, nil
}
func (s *imagesTableStore) SetBootImageReady(context.Context, int64, string, bool, agent.WindowsWIMInfo) error {
	return nil
}
func (s *imagesTableStore) SetBootImageError(context.Context, int64, string, string) error {
	return nil
}
func (s *imagesTableStore) SetBootImageTitle(context.Context, int64, string) error { return nil }
func (s *imagesTableStore) DeleteWindowsBootImage(context.Context, int64) error    { return nil }
func (s *imagesTableStore) ListWindowsInstallImages(context.Context) ([]WindowsInstallImage, error) {
	return s.installImages, nil
}
func (s *imagesTableStore) GetWindowsInstallImage(_ context.Context, id int64) (WindowsInstallImage, error) {
	for _, img := range s.installImages {
		if img.ID == id {
			return img, nil
		}
	}
	return WindowsInstallImage{}, errors.New("not found")
}
func (s *imagesTableStore) UpsertWindowsInstallImage(context.Context, WindowsInstallImageSource) (WindowsInstallImage, error) {
	return WindowsInstallImage{}, nil
}
func (s *imagesTableStore) SetInstallImageReady(context.Context, int64, string, agent.WindowsWIMInfo) error {
	return nil
}
func (s *imagesTableStore) SetInstallImageError(context.Context, int64, string, string) error {
	return nil
}
func (s *imagesTableStore) SetInstallImageTitle(context.Context, int64, string) error { return nil }
func (s *imagesTableStore) DeleteWindowsInstallImage(context.Context, int64) error    { return nil }

func (s *imagesTableStore) ResourceAuthors(context.Context) (ResourceAuthors, error) {
	return s.authors, nil
}

func (s *imagesTableStore) ImageNames(context.Context) (map[string]string, error) {
	return s.names, nil
}

func (s *imagesTableStore) SetImageName(context.Context, int64, string, string) error {
	return nil
}

func setupImagesTableServer(t *testing.T, role, lang string) (*Server, *imagesTableStore) {
	t.Helper()
	s, base, _ := downloadsServer(t)
	base.user = User{ID: 1, Username: role, Role: role, Enabled: true, Language: lang}

	winMod := time.Date(2026, 11, 10, 11, 20, 0, 0, time.UTC)
	oldMod := time.Date(2026, 11, 8, 9, 15, 0, 0, time.UTC)
	ubMod := time.Date(2026, 11, 12, 14, 0, 0, 0, time.UTC)
	brokenMod := time.Date(2026, 11, 1, 7, 0, 0, 0, time.UTC)

	base.profiles = []WindowsProfile{
		{
			ID:                   10,
			WindowsProfileSource: WindowsProfileSource{StorageID: 7, StorageRoot: "/mnt/omb-smb/key7", Path: "win11.iso", Size: 5 << 30, Modified: winMod, Title: "Windows 11"},
			State:                "ready",
			InstallFile:          "install.wim",
		},
		{
			ID:                   11,
			WindowsProfileSource: WindowsProfileSource{StorageID: 5, StorageRoot: "/var/lib/omb/images", Path: "old-win10.iso", Size: 4 << 30, Modified: oldMod, Title: "Old Win10"},
			State:                "ready",
			InstallFile:          "install.wim",
		},
	}
	base.bootProfiles = []BootProfile{
		{ID: 50, Kind: BootProfileWindowsISO, WindowsProfileID: 10, Title: "Windows 11"},
		{ID: 51, Kind: BootProfileWindowsISO, WindowsProfileID: 11, Title: "Old Win10"},
		{ID: 60, Kind: BootProfileUbuntuISO, Title: "Ubuntu Desktop 24", UbuntuSourceID: 9, UbuntuStorageID: 7, UbuntuPath: "linux/ubuntu-24.04.iso", UbuntuSHA256: strings.Repeat("a", 64), UbuntuState: "verified"},
		{ID: 61, Kind: BootProfileUbuntuISO, Title: "Ubuntu Server Duplicate", UbuntuSourceID: 9, UbuntuStorageID: 7, UbuntuPath: "linux/ubuntu-24.04.iso", UbuntuSHA256: strings.Repeat("a", 64), UbuntuState: "verified"},
	}
	base.installations = []Installation{
		{ID: 101, BootProfileID: 50, Title: "Windows 11 Clean"},
	}
	base.inspections = map[string]ImageInspection{
		imageAuthorKey(7, "win11.iso"):              {StorageID: 7, Path: "win11.iso", StorageRoot: "/mnt/omb-smb/key7", Kind: "windows", Size: 5 << 30, Modified: winMod},
		imageAuthorKey(5, "old-win10.iso"):          {StorageID: 5, Path: "old-win10.iso", StorageRoot: "/var/lib/omb/images", Kind: "windows", Size: 4 << 30, Modified: oldMod},
		imageAuthorKey(7, "linux/ubuntu-24.04.iso"): {StorageID: 7, Path: "linux/ubuntu-24.04.iso", StorageRoot: "/mnt/omb-smb/key7", Kind: "ubuntu", Size: 3 << 30, Modified: ubMod},
	}

	s.Images = &fakeImagesAPI{
		catalog: images.Catalog{
			Storages: []images.Storage{
				{ID: 5, Name: "Локальный", Kind: "local", Root: "/var/lib/omb/images", State: "ready"},
				{ID: 7, Name: "NAS-Share", Kind: "smb", Root: "/mnt/omb-smb/key7", State: "ready"},
			},
			Images: []images.Image{
				{StorageID: 7, Path: "win11.iso", Size: 5 << 30, Modified: winMod, Present: true},
				{StorageID: 5, Path: "old-win10.iso", Size: 4 << 30, Modified: oldMod, Present: true},
				{StorageID: 7, Path: "linux/ubuntu-24.04.iso", Size: 3 << 30, Modified: ubMod, FirstSeenAt: &ubMod, Present: true},
				{StorageID: 7, Path: "missing.iso", Size: 1 << 30, Modified: brokenMod, Present: false},
			},
		},
	}
	s.DiskUsage = fakeDiskUsageAPI{
		report: agent.DiskUsageReport{
			WindowsProfiles: map[int64]int64{
				10: 520 << 20,
				11: 400 << 20,
			},
			UbuntuSources: map[int64]int64{
				9: 180 << 20,
			},
		},
	}

	store := &imagesTableStore{
		downloadsStore: base,
		names: map[string]string{
			imageAuthorKey(7, "win11.iso"): "Windows 11 Pro",
		},
		authors: ResourceAuthors{
			Images: map[string]string{
				imageAuthorKey(7, "win11.iso"): "vlad",
			},
			ImageUploadedAt: map[string]time.Time{
				imageAuthorKey(7, "win11.iso"): winMod,
			},
		},
	}
	s.Store = store
	return s, store
}

func TestImagesTableLayoutAndNoDetailsOrUTC(t *testing.T) {
	tmpl, err := os.ReadFile("templates/images.html")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(tmpl), "<details") || strings.Contains(string(tmpl), "<summary") {
		t.Fatal("images template must not use details/summary")
	}

	s, _ := setupImagesTableServer(t, "admin", langRU)
	page := libraryGet(t, s, "/images")

	for _, want := range []string{
		">Файл ISO</a></th>",
		">Образ</a></th>",
		">Размер</a></th>",
		">Хранилище</a></th>",
		">Добавлен</a></th>",
		">Загрузки и состояние</a></th>",
		`<th class="iso-actions">Действия</th>`,
		"Windows 11 Pro",
		"NAS-Share",
		"Локальный",
		"Загружен · vlad",
		"Обнаружен",
		"на сервере:",
		humanSize(langRU, 520<<20),
		"boot.wim",
		humanSize(langRU, 180<<20),
		"источник Ubuntu",
		"Windows 11 Clean",
		"Отсутствует",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("missing %q in /images", want)
		}
	}
	resultsIdx := strings.Index(page, `<div id="image-results"`)
	if resultsIdx < 0 {
		t.Fatal("missing #image-results")
	}
	resultsHTML := page[resultsIdx:]
	for _, unwanted := range []string{"<details", "<summary", ">Автор<", ">Файл ответа и драйверы<", "UTC+", "UTC-"} {
		if strings.Contains(resultsHTML, unwanted) {
			t.Fatalf("unexpected %q in /images results", unwanted)
		}
	}

	// Ensure UbuntuSourceID 9 is not double-counted when referenced by two boot profiles.
	doubleSize := humanSize(langRU, 360<<20)
	if strings.Contains(page, doubleSize) {
		t.Fatalf("unexpected double-counted ubuntu source size %q", doubleSize)
	}

	// Observer sees table without Actions column or row menus.
	sObs, _ := setupImagesTableServer(t, "observer", langRU)
	obsPage := libraryGet(t, sObs, "/images")
	for _, hidden := range []string{`<th class="iso-actions">`, `popovertarget="image-menu-`, `data-image-delete-open`} {
		if strings.Contains(obsPage, hidden) {
			t.Fatalf("observer must not see %q", hidden)
		}
	}
}

func TestImagesTableDiskUsageFailureDegradesGracefully(t *testing.T) {
	s, _ := setupImagesTableServer(t, "admin", langRU)
	s.DiskUsage = fakeDiskUsageAPI{err: errors.New("agent unavailable")}
	page := libraryGet(t, s, "/images")
	if !strings.Contains(page, "на сервере: —") || !strings.Contains(page, "Windows 11 Pro") {
		t.Fatalf("page did not degrade gracefully on DiskUsage error")
	}
}

func TestImagesTableCleanupActionInsideMenuForAdminOnly(t *testing.T) {
	for _, tc := range []struct {
		role        string
		wantCleanup bool
	}{
		{"admin", true},
		{"operator", false},
		{"observer", false},
	} {
		t.Run(tc.role, func(t *testing.T) {
			s, _ := setupImagesTableServer(t, tc.role, langRU)
			page := libraryGet(t, s, "/images")
			hasCleanup := strings.Contains(page, `data-image-delete-open="image-cleanup-`) &&
				strings.Contains(page, `/images/windows-profiles/11/delete`) &&
				strings.Contains(page, "Удалить подготовленные файлы")
			if hasCleanup != tc.wantCleanup {
				t.Fatalf("role=%s hasCleanup=%v want=%v", tc.role, hasCleanup, tc.wantCleanup)
			}
		})
	}
}

func TestImagesTableSearchAndFilters(t *testing.T) {
	s, _ := setupImagesTableServer(t, "admin", langRU)

	allPage := libraryGet(t, s, "/images")
	for _, countWant := range []string{
		`Все <span class="mono">4</span>`,
		`В загрузках <span class="mono">1</span>`,
		`Свободные <span class="mono">3</span>`,
		`Требуют внимания <span class="mono">1</span>`,
	} {
		if !strings.Contains(allPage, countWant) {
			t.Fatalf("missing filter count %q", countWant)
		}
	}

	usedPage := libraryGet(t, s, "/images?show=used")
	if !strings.Contains(usedPage, "win11.iso") || strings.Contains(usedPage, "old-win10.iso") || strings.Contains(usedPage, "missing.iso") {
		t.Fatalf("unexpected rows for ?show=used")
	}

	freePage := libraryGet(t, s, "/images?show=free")
	if !strings.Contains(freePage, "old-win10.iso") || !strings.Contains(freePage, "ubuntu-24.04.iso") || !strings.Contains(freePage, "missing.iso") || strings.Contains(freePage, "win11.iso") {
		t.Fatalf("unexpected rows for ?show=free")
	}

	attentionPage := libraryGet(t, s, "/images?show=attention")
	if !strings.Contains(attentionPage, "missing.iso") || strings.Contains(attentionPage, "win11.iso") {
		t.Fatalf("unexpected rows for ?show=attention")
	}

	byAuthor := libraryGet(t, s, "/images?q=vlad")
	if !strings.Contains(byAuthor, "win11.iso") || strings.Contains(byAuthor, "old-win10.iso") {
		t.Fatalf("search by author failed")
	}

	byStorage := libraryGet(t, s, "/images?q=%D0%9B%D0%BE%D0%BA%D0%B0%D0%BB%D1%8C%D0%BD%D1%8B%D0%B9")
	if !strings.Contains(byStorage, "old-win10.iso") || strings.Contains(byStorage, "win11.iso") {
		t.Fatalf("search by storage failed")
	}

	combined := libraryGet(t, s, "/images?show=used&q=vlad")
	if !strings.Contains(combined, "win11.iso") || !strings.Contains(combined, `<input type="hidden" name="show" value="used">`) {
		t.Fatalf("combined filter+search failed")
	}

	noMatch := libraryGet(t, s, "/images?q=nonexistent-image-xyz")
	if !strings.Contains(noMatch, "Ничего не найдено.") || strings.Contains(noMatch, "ISO пока не найдены.") {
		t.Fatalf("filtered empty state wrong")
	}

	// Empty library shows initial empty state instead of "Ничего не найдено."
	s.Images = &fakeImagesAPI{catalog: images.Catalog{Storages: s.Images.(*fakeImagesAPI).catalog.Storages}}
	emptyLib := libraryGet(t, s, "/images")
	if !strings.Contains(emptyLib, "ISO пока не найдены.") || strings.Contains(emptyLib, "Ничего не найдено.") {
		t.Fatalf("empty library message wrong")
	}
}

func TestImagesTableEnglishTranslation(t *testing.T) {
	s, _ := setupImagesTableServer(t, "admin", langEN)
	page := libraryGet(t, s, "/images")
	for _, want := range []string{
		">ISO file</a></th>",
		">Image</a></th>",
		">Size</a></th>",
		">Storage</a></th>",
		">Added</a></th>",
		">Boot entries &amp; status</a></th>",
		"on server:",
		"Ubuntu source",
		"In boot entries",
		"Unused",
		"Need attention",
		"Title, file, type, storage or author",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("missing English label %q", want)
		}
	}
}

func TestImagesUploadTargetShowsStorageKind(t *testing.T) {
	s := &Server{
		Store: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}},
		Images: &fakeImagesAPI{
			catalog: images.Catalog{
				Storages: []images.Storage{
					{ID: 1, Name: "NAS-SMB", Kind: "smb", Uploads: true, Free: 500 << 30, Total: 1000 << 30},
					{ID: 2, Name: "NAS-NFS", Kind: "nfs", NFSVersion: "4.1", Uploads: true, Free: 300 << 30, Total: 1000 << 30},
					{ID: 3, Name: "NFS-RO", Kind: "nfs", NFSVersion: "4.1", Uploads: false, Free: 100 << 30, Total: 1000 << 30},
					{ID: 4, Name: "Local-Storage", Kind: "local", Uploads: true, Free: 50 << 30, Total: 100 << 30},
				},
			},
		},
	}
	body := libraryGet(t, s, "/images")
	for _, want := range []string{
		`NAS-SMB · SMB`,
		`NAS-NFS · NFS`,
		`Local-Storage · Локальный каталог`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("/images missing upload target %q", want)
		}
	}
	if strings.Contains(body, "NFS-RO") {
		t.Fatalf("/images should not include read-only storage in upload targets")
	}
}

func TestImagesPageRendersLiveRefresh(t *testing.T) {
	s, _ := setupImagesTableServer(t, "admin", "ru")
	body := libraryGet(t, s, "/images")
	if !strings.Contains(body, `data-live-refresh="15"`) {
		t.Errorf("expected data-live-refresh=\"15\" in /images")
	}
	if !strings.Contains(body, `data-live-targets="#image-results,#image-filters"`) {
		t.Errorf("expected data-live-targets=\"#image-results,#image-filters\" in /images")
	}
	if !strings.Contains(body, `<script defer src="/assets/live-refresh.js"></script>`) {
		t.Errorf("expected script /assets/live-refresh.js in /images")
	}
}

func TestImagesPageRendersISOBootAndInstallTabs(t *testing.T) {
	s, store := setupImagesTableServer(t, "admin", langRU)
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	store.bootImages = []WindowsBootImage{
		{
			ID:          1,
			SourceKind:  WIMSourceKindFile,
			StorageID:   5,
			Path:        "winpe/win11-26h2-boot.wim",
			Size:        548 << 20,
			Title:       "Win11 26H2 Universal Boot",
			SHA256:      strings.Repeat("b", 64),
			HasSetupExe: true,
			State:       "ready",
			WIMInfo: agent.WindowsWIMInfo{
				Known: true,
				Images: []agent.WindowsWIMImage{
					{Index: 2, Name: "Microsoft Windows Setup (amd64)", Architecture: "amd64", Build: 26100},
				},
			},
		},
		{
			ID:          2,
			SourceKind:  WIMSourceKindFile,
			StorageID:   5,
			Path:        "winpe/adk-winpe11-x64.wim",
			Size:        392 << 20,
			Title:       "ADK WinPE 11 (чистый)",
			SHA256:      strings.Repeat("c", 64),
			HasSetupExe: false,
			State:       "ready",
			WIMInfo: agent.WindowsWIMInfo{
				Known: true,
				Images: []agent.WindowsWIMImage{
					{Index: 1, Name: "Microsoft Windows PE (amd64)", Architecture: "amd64", Build: 26100},
				},
			},
		},
		{
			ID:               3,
			SourceKind:       WIMSourceKindISO,
			WindowsProfileID: 10,
			Title:            "boot.wim · Windows 11 Pro",
			SHA256:           strings.Repeat("d", 64),
			HasSetupExe:      true,
			State:            "ready",
			WIMInfo: agent.WindowsWIMInfo{
				Known: true,
				Images: []agent.WindowsWIMImage{
					{Index: 2, Name: "Microsoft Windows Setup (amd64)", Architecture: "amd64", Build: 26100},
				},
			},
		},
	}
	store.installImages = []WindowsInstallImage{
		{
			ID:          10,
			StorageID:   5,
			Path:        "wims/win11-corp.wim",
			InstallFile: "install.wim",
			Size:        8 << 30,
			Modified:    now,
			Title:       "Windows 11 Corp Gold",
			State:       "ready",
			WIMInfo: agent.WindowsWIMInfo{
				Known: true,
				Images: []agent.WindowsWIMImage{
					{Index: 1, Name: "Windows 11 Pro", Architecture: "amd64", Build: 26100},
				},
			},
		},
		{
			ID:          11,
			StorageID:   7,
			Path:        "wims/win11-multi.esd",
			InstallFile: "install.esd",
			Size:        5 << 30,
			Modified:    now,
			Title:       "Windows 11 24H2 Multi",
			State:       "ready",
			WIMInfo: agent.WindowsWIMInfo{
				Known: true,
				Images: []agent.WindowsWIMImage{
					{Index: 1, Name: "Windows 11 Home", Architecture: "amd64", Build: 26100},
					{Index: 2, Name: "Windows 11 Pro", Architecture: "amd64", Build: 26100},
				},
			},
		},
	}
	// Also add the WIM/ESD files into catalog so they are recognized as present
	// and verify they do NOT appear on the ISO tab.
	imgAPI := s.Images.(*fakeImagesAPI)
	imgAPI.catalog.Images = append(imgAPI.catalog.Images,
		images.Image{StorageID: 5, Path: "winpe/win11-26h2-boot.wim", Kind: images.ImageKindWIM, Size: 548 << 20, Modified: now, Present: true},
		images.Image{StorageID: 5, Path: "winpe/adk-winpe11-x64.wim", Kind: images.ImageKindWIM, Size: 392 << 20, Modified: now, Present: true},
		images.Image{StorageID: 5, Path: "wims/win11-corp.wim", Kind: images.ImageKindWIM, Size: 8 << 30, Modified: now, Present: true},
		images.Image{StorageID: 7, Path: "wims/win11-multi.esd", Kind: images.ImageKindESD, Size: 5 << 30, Modified: now, Present: true},
	)

	// 1. ISO tab (default)
	isoPage := libraryGet(t, s, "/images")
	for _, want := range []string{
		`class="tabs"`,
		`href="/images?tab=iso"`,
		`href="/images?tab=boot"`,
		`href="/images?tab=install"`,
		`+ Загрузить ISO`,
		`data-expected-kind="iso"`,
		"win11.iso",
	} {
		if !strings.Contains(isoPage, want) {
			t.Fatalf("ISO tab missing %q", want)
		}
	}
	if strings.Contains(isoPage, "win11-corp.wim") || strings.Contains(isoPage, "adk-winpe11-x64.wim") {
		t.Fatalf("ISO tab must not list .wim/.esd files")
	}

	// 2. boot.wim tab
	bootPage := libraryGet(t, s, "/images?tab=boot")
	for _, want := range []string{
		`+ Загрузить boot.wim`,
		`data-expected-kind="wim"`,
		"Win11 26H2 Universal Boot",
		"ADK WinPE 11 (чистый)",
		"boot.wim · Windows 11 Pro",
		"Отдельный файл",
		"Из ISO (без копии)",
		"Есть setup.exe",
		"Только WinPE · нужен ISO",
		"10.0.26100",
		"Отдельные",
		"Из ISO",
	} {
		if !strings.Contains(bootPage, want) {
			t.Fatalf("boot.wim tab missing %q", want)
		}
	}
	if strings.Contains(bootPage, "<details") || strings.Contains(bootPage, "<summary") {
		t.Fatalf("boot.wim tab must not contain <details>/<summary>")
	}
	bootISOOnly := libraryGet(t, s, "/images?tab=boot&show=iso")
	if !strings.Contains(bootISOOnly, "boot.wim · Windows 11 Pro") || strings.Contains(bootISOOnly, "ADK WinPE 11 (чистый)") {
		t.Fatalf("boot.wim ?show=iso filter failed")
	}

	// 3. install.wim tab
	instPage := libraryGet(t, s, "/images?tab=install")
	for _, want := range []string{
		`+ Загрузить install.wim / .esd`,
		`data-expected-kind="wim_or_esd"`,
		"Windows 11 Corp Gold",
		"Windows 11 24H2 Multi",
		"Windows 11 Pro",
		"ESD",
		".wim",
		".esd",
	} {
		if !strings.Contains(instPage, want) {
			t.Fatalf("install.wim tab missing %q", want)
		}
	}
	if strings.Contains(instPage, "<details") || strings.Contains(instPage, "<summary") {
		t.Fatalf("install.wim tab must not contain <details>/<summary>")
	}
	instESDOnly := libraryGet(t, s, "/images?tab=install&show=esd")
	if !strings.Contains(instESDOnly, "Windows 11 24H2 Multi") || strings.Contains(instESDOnly, "Windows 11 Corp Gold") {
		t.Fatalf("install.wim ?show=esd filter failed")
	}
}
