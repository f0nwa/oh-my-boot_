package control

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

func TestPendingRemoveUsesPublishedPlacement(t *testing.T) {
	sources := InstallationMenuSources{BootProfiles: []BootProfile{{ID: 1, Kind: BootProfileUbuntuBuiltin, Title: "Ubuntu"}}, Installations: []Installation{{ID: 7, BootProfileID: 1, Title: "Source title", Folder: "Source"}}}
	active := MenuDraft{Items: []MenuDraftItem{{InstallationID: 7, Title: "Published title", Folder: "Published"}}}
	rows := installationRows(nil, nil, nil, MenuDraft{}, active, sources)
	if len(rows) != 1 || rows[0].Status != "pending_remove" || rows[0].Title != "Published title" || rows[0].Folder != "Published" {
		t.Fatalf("rows=%+v", rows)
	}
}

func TestNilVersionStructureCannotBypassReadiness(t *testing.T) {
	store := newDraftStore(t, "operator", nil)
	server, _ := draftServer(t, store)
	version := MenuVersion{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", Script: "#!ipxe\nitem install_7 Ubuntu\n"}
	if err := server.verifyMenuVersion(context.Background(), version); err == nil {
		t.Fatal("version without structural items accepted")
	}
	if menuVersionRestorable(version, InstallationMenuSources{}, nil, nil, nil) {
		t.Fatal("version without structural items offered for restoration")
	}
	if got := draftFromVersion(version); len(got.Items) != 0 || strings.Contains(got.Label, "Ubuntu") {
		t.Fatalf("parsed script: %+v", got)
	}
}

type referencedDeletionStore struct {
	*deletionStore
	source InstallationMenuSources
}

func (s *referencedDeletionStore) Installations(context.Context) ([]Installation, error) {
	return s.source.Installations, nil
}
func (s *referencedDeletionStore) BootProfiles(context.Context) ([]BootProfile, error) {
	return s.source.BootProfiles, nil
}

func TestDeletionProtectsInstallationComponentsBeforeCleanup(t *testing.T) {
	for _, tc := range []struct {
		path string
		item Installation
	}{
		{"/drivers/configs/3/delete", Installation{ID: 7, BootProfileID: 1, Title: "Driver", DriverConfigID: 3}},
		{"/images/windows-answers/4/delete", Installation{ID: 7, BootProfileID: 1, Title: "XML", AnswerID: 4}},
		{"/images/windows-profiles/17/delete", Installation{ID: 7, BootProfileID: 1, Title: "Manual"}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			base := &deletionStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}, profiles: []WindowsProfile{{ID: 17}}, answers: []WindowsAnswerVariant{{ID: 4, ProfileID: 17}}, configs: []DriverConfig{{ID: 3, ProfileID: 17}}}
			store := &referencedDeletionStore{deletionStore: base, source: InstallationMenuSources{BootProfiles: []BootProfile{{ID: 1, Kind: BootProfileWindowsISO, WindowsProfileID: 17}}, Installations: []Installation{tc.item}}}
			cleaner := &deletionCleaner{}
			server := deletionTestServer(base, cleaner)
			server.Store = store
			response := submitDeletion(t, server, tc.path)
			if response.Code != 409 || len(cleaner.calls) != 0 || len(base.deleted) != 0 {
				t.Fatalf("status=%d cleanup=%v deleted=%v", response.Code, cleaner.calls, base.deleted)
			}
		})
	}
}

func TestUnifiedSchemaHasOnlyStructuralMenu(t *testing.T) {
	store := installationTestStore(t)
	ctx := context.Background()
	var legacy int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND ((table_name='menu_draft' AND column_name IN ('ubuntu','ubuntu_folder')) OR (table_name='menu_draft_items' AND column_name='key'))`).Scan(&legacy); err != nil || legacy != 0 {
		t.Fatalf("legacy columns=%d error=%v", legacy, err)
	}
	var nullable string
	if err := store.Pool.QueryRow(ctx, `SELECT is_nullable FROM information_schema.columns WHERE table_schema='public' AND table_name='menu_draft_items' AND column_name='installation_id'`).Scan(&nullable); err != nil || nullable != "NO" {
		t.Fatalf("installation_id nullable=%s err=%v", nullable, err)
	}
	if err := store.SaveMenuDraft(ctx, MenuDraft{Title: "Invalid", Items: []MenuDraftItem{{InstallationID: 0}}}); err == nil {
		t.Fatal("zero identity accepted")
	}
	if _, ok, err := store.MenuDraft(ctx); err != nil || ok {
		t.Fatalf("invalid save left draft: ok=%v err=%v", ok, err)
	}
}

func TestUnifiedLibraryUsageUsesInstallationReferences(t *testing.T) {
	profiles, answers, configs := draftFixture()
	source := testInstallationSources(profiles, answers, configs)
	active := MenuDraft{Items: []MenuDraftItem{{InstallationID: 1017}, {InstallationID: 2031}, {InstallationID: 3002}}}
	profileUse, answerUse := menuUsage(active, configs, source)
	if profileUse[17] != 3 || answerUse[31] != 2 {
		t.Fatalf("profiles=%v answers=%v", profileUse, answerUse)
	}
	if rows := installationRows(profiles, answers, configs, MenuDraft{}, MenuDraft{}, InstallationMenuSources{}); len(rows) != 0 {
		t.Fatalf("library invented installations: %+v", rows)
	}
}

func TestLegacyMenuRoutesAreRejected(t *testing.T) {
	store := newDraftStore(t, "operator", nil)
	server, _ := draftServer(t, store)
	if response := menuPost(t, server, "/menu/settings", url.Values{}); response.Code != 404 {
		t.Fatalf("removed Ubuntu route status=%d", response.Code)
	}
	for _, path := range []string{"/installations/winiso_17/add", "/installations/winanswer_31/add", "/installations/windriver_1/add"} {
		if response := menuPost(t, server, path, url.Values{}); response.Code != 400 {
			t.Fatalf("old key accepted: %s status=%d", path, response.Code)
		}
	}
	if store.draft != nil || len(store.saved) != 0 {
		t.Fatal("legacy route mutated draft")
	}
}
