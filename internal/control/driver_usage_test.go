package control

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"omb/internal/agent"
)

type driverUsageStore struct {
	*driverConfigStore
	draft    MenuDraft
	versions []MenuVersion
	usageErr error
}

func (s *driverUsageStore) MenuDraft(context.Context) (MenuDraft, bool, error) {
	return s.draft, true, s.usageErr
}
func (s *driverUsageStore) MenuVersions(context.Context) ([]MenuVersion, error) {
	return s.versions, nil
}

func TestDriverUsageIncludesSharedDownloadsAndFrozenMenuBindings(t *testing.T) {
	store := &driverUsageStore{driverConfigStore: configFixture()}
	store.user.Role = "admin"
	store.configs = []DriverConfig{{ID: 1, ProfileID: 17, State: "ready", PackageINFs: []agent.SelectedINF{{PackageSHA256: strings.Repeat("a", 64), INFPath: "net.inf"}}}, {ID: 2, ProfileID: 17, State: "error", Error: "Не удалось подготовить WinPE"}, {ID: 3, ProfileID: 17, State: "ready"}, {ID: 4, ProfileID: 17, State: "ready"}}
	store.installations = []Installation{{ID: 7, Title: "Lab", DriverConfigID: 1}, {ID: 8, Title: "Recovery", DriverConfigID: 1}}
	store.draft.Items = []MenuDraftItem{{InstallationID: 7, Components: &InstallationComponents{DriverConfigID: 4}}}
	store.versions = []MenuVersion{{Active: true, Items: []MenuVersionItem{{InstallationID: 8, Components: &InstallationComponents{DriverConfigID: 2}}}}}
	body := libraryGet(t, &Server{Store: store}, "/drivers")
	for _, expected := range []string{`href="/installations/7/edit"`, `href="/installations/8/edit"`, "Lab", "Recovery", "Windows 11", "Не удалось подготовить WinPE"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("missing %q in usage page", expected)
		}
	}
	for _, id := range []string{"1", "2", "4"} {
		if !regexp.MustCompile(`data-driver-delete-open="` + id + `"[^>]*disabled`).MatchString(body) {
			t.Fatalf("used configuration %s has active deletion", id)
		}
	}
	if !regexp.MustCompile(`data-driver-package-delete-open="` + strings.Repeat("a", 64) + `"[^>]*disabled`).MatchString(body) {
		t.Fatal("referenced source package has active deletion")
	}
	if regexp.MustCompile(`data-driver-delete-open="3"[^>]*disabled`).MatchString(body) {
		t.Fatal("unused configuration cannot be deleted")
	}
}

func TestDriverUsageFailureDisablesDeletion(t *testing.T) {
	store := &driverUsageStore{driverConfigStore: configFixture(), usageErr: errors.New("database unavailable")}
	store.user.Role = "admin"
	store.configs = []DriverConfig{{ID: 1, ProfileID: 17, State: "ready"}}
	body := libraryGet(t, &Server{Store: store}, "/drivers")
	if !regexp.MustCompile(`data-driver-delete-open="1"[^>]*disabled`).MatchString(body) {
		t.Fatal("unverified usage permits deletion")
	}
	if !strings.Contains(body, "Не удалось проверить использование") {
		t.Fatal("usage failure is hidden")
	}
}

func TestDeleteDriverConfigBlocksFrozenDraftBinding(t *testing.T) {
	store := &driverUsageStore{driverConfigStore: configFixture()}
	store.user.Role = "admin"
	store.configs = []DriverConfig{{ID: 1, ProfileID: 17, State: "ready"}}
	store.installations = []Installation{{ID: 7, Title: "Edited"}}
	store.draft.Items = []MenuDraftItem{{InstallationID: 7, Components: &InstallationComponents{DriverConfigID: 1}}}
	store.versions = []MenuVersion{{Active: true, Script: "#!ipxe\n", SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("#!ipxe\n")))}}
	cleaner := &deletionCleaner{}
	server := &Server{Store: store, Cleanup: cleaner, DeletionMenu: fixedDriverMenu("#!ipxe\n")}
	response := submitDeletion(t, server, "/drivers/configs/1/delete")
	if response.Code != http.StatusConflict || len(cleaner.calls) > 0 {
		t.Fatalf("draft dependency removed: %d calls=%v", response.Code, cleaner.calls)
	}
}

type fixedDriverMenu string

func (m fixedDriverMenu) CurrentMenu(context.Context) ([]byte, error) { return []byte(m), nil }

func (s *driverUsageStore) DeleteDriverConfig(context.Context, int64) error {
	s.configs = nil
	return nil
}

func TestDriverUsageDistinguishesSourcesWithGenericProfileTitles(t *testing.T) {
	profiles := []WindowsProfile{
		{ID: 17, WindowsProfileSource: WindowsProfileSource{Title: "Windows x64", Path: "Windows/Windows-11.iso"}},
		{ID: 18, WindowsProfileSource: WindowsProfileSource{Title: "Windows x64", Path: "Windows/Windows-10.iso"}},
	}
	usage := buildDriverUsage(profiles, nil, nil, MenuDraft{}, MenuDraft{})
	if usage.Profiles[17] != "Windows-11.iso" || usage.Profiles[18] != "Windows-10.iso" {
		t.Fatalf("ISO identity lost: %v", usage.Profiles)
	}
}

func TestDriversPageShowsAuthorCardAndProperties(t *testing.T) {
	store := &driverUsageStore{driverConfigStore: configFixture()}
	store.user = User{ID: 1, Username: "admin", Role: "admin", Enabled: true}
	store.profiles = []WindowsProfile{{
		ID:                   17,
		State:                "ready",
		WindowsProfileSource: WindowsProfileSource{Title: "Win 11", Path: "win/Win11_23H2.iso"},
	}}
	store.answers = []WindowsAnswerVariant{{ID: 4, Title: "Lab XML", State: "ready"}}
	wimSHA := strings.Repeat("c", 64)
	pkgSHA := strings.Repeat("a", 64)
	created := time.Date(2026, 11, 12, 9, 53, 0, 0, time.UTC)
	store.configs = []DriverConfig{
		{
			ID:          1,
			ProfileID:   17,
			AnswerID:    4,
			Title:       "Direct Config",
			State:       "ready",
			Author:      "vlad",
			CreatedAt:   created,
			PackageINFs: []agent.SelectedINF{{PackageSHA256: pkgSHA, INFPath: "net.inf"}},
			Build:       agent.DriverBuildResult{WIMSHA256: wimSHA},
		},
		{
			ID:          2,
			ProfileID:   17,
			Title:       "Inferred Config",
			State:       "ready",
			CreatedAt:   created,
			PackageINFs: []agent.SelectedINF{{PackageSHA256: pkgSHA, INFPath: "net.inf"}},
		},
		{
			ID:        3,
			ProfileID: 17,
			Title:     "Unknown Author",
			State:     "pending",
		},
	}
	store.installations = []Installation{{ID: 14, Title: "Boot Entry 14", DriverConfigID: 2}}
	s := &Server{Store: store}
	s.authorsCache.set(ResourceAuthors{
		Installations: map[int64]string{14: "fonwa"},
	}, time.Now())

	body := libraryGet(t, s, "/drivers")
	for _, want := range []string{
		`>Автор</a></th>`,
		`data-driver-config-open="1"`,
		`id="driver-config-dialog-1"`,
		`action="/drivers/configs/1/title"`,
		`data-driver-config-form`,
		`Win11_23H2.iso`,
		`Lab XML`,
		`vlad`,
		`fonwa`,
		`≈`,
		`Определён по загрузке, при сохранении которой появилась конфигурация`,
		`Boot Entry 14`,
		shortSHA(wimSHA),
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("drivers page missing %q", want)
		}
	}

	// Observer sees properties card without edit form or delete button.
	store.user.Role = "observer"
	obsBody := libraryGet(t, s, "/drivers")
	if !strings.Contains(obsBody, `id="driver-config-dialog-1"`) || strings.Contains(obsBody, `action="/drivers/configs/1/title"`) || strings.Contains(obsBody, `action="/drivers/configs/1/delete"`) {
		t.Fatalf("observer should see read-only config dialog without rename/delete actions")
	}
}

func TestDriversPageShowsConfigSizesAndSharedWIM(t *testing.T) {
	store := &driverUsageStore{driverConfigStore: configFixture()}
	store.user = User{ID: 1, Username: "admin", Role: "admin", Enabled: true}
	store.profiles = []WindowsProfile{{
		ID:                   17,
		State:                "ready",
		WindowsProfileSource: WindowsProfileSource{Title: "Win 11", Path: "win11.iso"},
	}}
	uniqueKey := strings.Repeat("1", 64)
	sharedKey := strings.Repeat("2", 64)
	store.configs = []DriverConfig{
		{ID: 1, ProfileID: 17, Title: "Unique", State: "ready", Build: agent.DriverBuildResult{WIMKey: uniqueKey, WIMSHA256: strings.Repeat("a", 64)}},
		{ID: 2, ProfileID: 17, Title: "Shared A", State: "ready", Build: agent.DriverBuildResult{WIMKey: sharedKey}},
		{ID: 3, ProfileID: 17, Title: "Shared B", State: "ready", Build: agent.DriverBuildResult{WIMKey: sharedKey}},
		{ID: 4, ProfileID: 17, Title: "Pending", State: "pending"},
	}
	disk := fakeDiskUsageAPI{
		report: agent.DiskUsageReport{
			WindowsDriverWIMs: map[string]int64{
				uniqueKey: 572 << 20,
				sharedKey: 599 << 20,
			},
			WindowsDriverConfigs: map[int64]int64{
				1: 12 << 10,
				2: 12 << 10,
				3: 12 << 10,
			},
		},
	}
	s := &Server{Store: store, DiskUsage: disk}
	body := libraryGet(t, s, "/drivers")
	for _, want := range []string{
		`>Размер</a></th>`,
		`Размер на сервере`,
		`572,0 МБ`,
		`599,0 МБ`,
		`boot.wim`,
		`общий boot.wim`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("drivers page missing %q", want)
		}
	}

	// English locale translates size labels and omits Russian text.
	store.user.Language = langEN
	enBody := libraryGet(t, s, "/drivers")
	for _, want := range []string{`>Size</a></th>`, `Size on server`, `shared boot.wim`, `572.0 MB`, `599.0 MB`} {
		if !strings.Contains(enBody, want) {
			t.Fatalf("EN drivers page missing %q", want)
		}
	}

	// Unavailable DiskUsage agent degrades gracefully to "—".
	store.user.Language = langRU
	s.DiskUsage = fakeDiskUsageAPI{err: errors.New("agent unavailable")}
	fallbackBody := libraryGet(t, s, "/drivers")
	if !strings.Contains(fallbackBody, `>Размер</a></th>`) || strings.Contains(fallbackBody, `572,0 МБ`) {
		t.Fatalf("expected graceful fallback when DiskUsage fails")
	}
}
