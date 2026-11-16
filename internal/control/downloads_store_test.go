package control

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDownloadStoreEditsOptionsWithoutChangingIdentityOrOtherDownloads(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	if _, err := store.CreateWindowsProfile(ctx, WindowsProfileSource{StorageID: 7, Path: "Windows.iso", StorageRoot: "/iso", Size: 100, Modified: time.Now(), Title: "Windows"}); err != nil {
		t.Fatal(err)
	}
	profiles, err := store.BootProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bootID := profiles[0].ID
	original, err := store.CreateInstallation(ctx, Installation{BootProfileID: bootID, Title: "Original"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateInstallation(ctx, Installation{BootProfileID: bootID, Title: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	answer, err := store.CreateWindowsAnswer(ctx, WindowsAnswerVariant{Title: "XML", SHA256: strings.Repeat("a", 64), Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	changed := original
	changed.Title, changed.Folder, changed.AnswerID = "Changed", "Lab", answer.ID
	if err := store.UpdateInstallation(ctx, changed); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Installations(ctx)
	if err != nil || !reflect.DeepEqual(saved, []Installation{changed, other}) {
		t.Fatalf("downloads=%+v error=%v", saved, err)
	}
	// Invalid component updates must roll back the title and existing options.
	invalid := changed
	invalid.Title = "Must not persist"
	invalid.AnswerID = 999
	if err := store.UpdateInstallation(ctx, invalid); err == nil {
		t.Fatal("accepted an unavailable answer")
	}
	saved, err = store.Installations(ctx)
	if err != nil || !reflect.DeepEqual(saved, []Installation{changed, other}) {
		t.Fatalf("failed update changed downloads: %+v error=%v", saved, err)
	}
	// Removing optional components replaces the old options rather than adding a row.
	changed.AnswerID = 0
	if err := store.UpdateInstallation(ctx, changed); err != nil {
		t.Fatal(err)
	}
	saved, err = store.Installations(ctx)
	if err != nil || !reflect.DeepEqual(saved, []Installation{changed, other}) {
		t.Fatalf("cleared options=%+v error=%v", saved, err)
	}
	var optionRows int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM windows_installation_options WHERE installation_id=$1`, changed.ID).Scan(&optionRows); err != nil || optionRows != 1 {
		t.Fatalf("options=%d error=%v", optionRows, err)
	}
}

func applyDownloadComponentsMigration(t *testing.T, store Store) {
	t.Helper()
	sql, err := os.ReadFile("../../deploy/sql/2026-11-03-download-components.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(context.Background(), string(sql)); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadMigrationCapturesComponentsOnceWithoutChangingMenu(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	if _, err := store.CreateWindowsProfile(ctx, WindowsProfileSource{StorageID: 7, Path: "Windows.iso", StorageRoot: "/iso", Size: 100, Modified: time.Now(), Title: "Windows"}); err != nil {
		t.Fatal(err)
	}
	profiles, err := store.BootProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	answer, err := store.CreateWindowsAnswer(ctx, WindowsAnswerVariant{Title: "XML", SHA256: strings.Repeat("a", 64), Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := store.CreateInstallation(ctx, Installation{BootProfileID: profiles[0].ID, Title: "Published", AnswerID: answer.ID})
	if err != nil {
		t.Fatal(err)
	}
	draft := MenuDraft{Title: "PXE", LocalTimeout: 10, Items: []MenuDraftItem{{InstallationID: entry.ID, Title: entry.Title, Folder: "Lab"}}}
	if err := store.SaveMenuDraft(ctx, draft); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveActiveMenu(ctx, 0, MenuVersion{Title: "PXE", Script: "#!ipxe\n# unchanged", SHA256: "original", Items: versionItems(draft)}); err != nil {
		t.Fatal(err)
	}
	// Simulate the installed schema before the additive migration.
	if _, err := store.Pool.Exec(ctx, `ALTER TABLE menu_draft_items DROP COLUMN components; ALTER TABLE menu_version_items DROP COLUMN components`); err != nil {
		t.Fatal(err)
	}
	applyDownloadComponentsMigration(t, store)
	versions, err := store.MenuVersions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old := versions[0]
	if old.Script != "#!ipxe\n# unchanged" || old.SHA256 != "original" || old.Items[0].Components == nil || old.Items[0].Components.AnswerID != answer.ID {
		t.Fatalf("migration lost snapshot: %+v items=%+v", old, old.Items)
	}
	entry.AnswerID = 0
	if err := store.UpdateInstallation(ctx, entry); err != nil {
		t.Fatal(err)
	}
	applyDownloadComponentsMigration(t, store)
	versions, err = store.MenuVersions(ctx)
	if err != nil || !reflect.DeepEqual(versions[0], old) {
		t.Fatalf("repeat migration rewrote published snapshot: before=%+v after=%+v error=%v", old, versions, err)
	}
	saved, _, err := store.MenuDraft(ctx)
	if err != nil || saved.Items[0].Components.AnswerID != answer.ID {
		t.Fatalf("migration rewrote draft components: %+v error=%v", saved, err)
	}
	saved.Items[0].Components = installationComponents(entry)
	if err := store.SaveMenuDraft(ctx, saved); err != nil {
		t.Fatal(err)
	}
	saved, _, err = store.MenuDraft(ctx)
	if err != nil || saved.Items[0].Components == nil || saved.Items[0].Components.AnswerID != 0 || len(diffMenuDraft(draftFromVersion(old), saved)) == 0 {
		t.Fatalf("edited snapshot did not round-trip: %+v error=%v", saved, err)
	}
}
