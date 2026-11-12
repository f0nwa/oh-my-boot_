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
