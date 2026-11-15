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
	authors ResourceAuthors
	names   map[string]string
}

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
		"<th>Файл ISO</th>",
		"<th>Образ</th>",
		"<th>Размер</th>",
		"<th>Хранилище</th>",
		"<th>Добавлен</th>",
		"<th>Загрузки и состояние</th>",
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
		"<th>ISO file</th>",
		"<th>Image</th>",
		"<th>Size</th>",
		"<th>Storage</th>",
		"<th>Added</th>",
		"<th>Boot entries &amp; status</th>",
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
