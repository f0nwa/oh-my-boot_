package control

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// These tests catch ID identity collapsing Ubuntu installations into one flag,
// script parsing replacing saved structure, and partial metadata/item writes.
func TestStructuredMenuStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	if err := store.EnsureBootProfiles(ctx); err != nil {
		t.Fatal(err)
	}
	win, err := store.CreateWindowsProfile(ctx, WindowsProfileSource{StorageID: 1, Path: "win.iso", StorageRoot: "/iso", Size: 100, Modified: time.Now(), Title: "Windows"})
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := store.BootProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var windowsID, ubuntuID int64
	for _, p := range profiles {
		if p.WindowsProfileID == win.ID {
			windowsID = p.ID
		}
		if p.Kind == "ubuntu_builtin" {
			ubuntuID = p.ID
		}
	}
	var ids []int64
	for _, item := range []Installation{{BootProfileID: windowsID, Title: "Windows"}, {BootProfileID: ubuntuID, Title: "Ubuntu Lab"}, {BootProfileID: ubuntuID, Title: "Ubuntu Recovery"}} {
		created, err := store.CreateInstallation(ctx, item)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, created.ID)
	}
	want := MenuDraft{Title: productMenuTitle, Label: "Tools", Message: "Choose", NoDiagnostics: true, LocalTimeout: 23, NoLocalTimer: true, DefaultInstallationID: ids[2], Items: []MenuDraftItem{
		{InstallationID: ids[2], Title: "Recovery", Folder: "Linux/Recovery"},
		{InstallationID: ids[0], Title: "Windows Lab", Folder: "Windows"},
		{InstallationID: ids[1], Title: "Ubuntu Lab", Folder: "Linux/Lab"},
	}}
	if err := store.SaveMenuDraft(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.MenuDraft(ctx)
	if err != nil || !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("draft = %+v ok=%v err=%v; want %+v", got, ok, err, want)
	}
	version := MenuVersion{Title: want.Title, Label: want.Label, Message: want.Message, Script: "#!ipxe\n# structure deliberately absent\n", SHA256: "saved", NoDiagnostics: true, LocalTimeout: 23, NoLocalTimer: true, DefaultInstallationID: ids[2], Look: menuLook, Items: []MenuVersionItem{
		{InstallationID: ids[2], Position: 0, Title: "Recovery", Folder: "Linux/Recovery"},
		{InstallationID: ids[0], Position: 1, Title: "Windows Lab", Folder: "Windows"},
		{InstallationID: ids[1], Position: 2, Title: "Ubuntu Lab", Folder: "Linux/Lab"},
	}}
	if err := store.SaveActiveMenu(ctx, 0, version); err != nil {
		t.Fatal(err)
	}
	versions, err := store.MenuVersions(ctx)
	if err != nil || len(versions) != 1 {
		t.Fatalf("versions=%+v err=%v", versions, err)
	}
	saved, err := store.MenuByID(ctx, versions[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved.Items, versions[0].Items) || len(saved.Items) != 3 {
		t.Fatalf("loaded structured items: %+v / %+v", saved.Items, versions[0].Items)
	}
	for i, item := range saved.Items {
		if item.VersionID != saved.ID || item.InstallationID != version.Items[i].InstallationID || item.Position != i || item.Title != version.Items[i].Title || item.Folder != version.Items[i].Folder {
			t.Fatalf("version item %d = %+v", i, item)
		}
	}
	restored := draftFromVersion(saved)
	want.look = menuLook
	if !reflect.DeepEqual(restored, want) {
		t.Fatalf("restored=%+v want=%+v", restored, want)
	}
	if err := store.SaveMenuDraft(ctx, MenuDraft{Title: "Edited", Items: []MenuDraftItem{{InstallationID: ids[0], Title: "Edited"}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMenuDraft(ctx, restored); err != nil {
		t.Fatal(err)
	}
	restored.look = "" // Draft look is supplied by currentDraft, not stored.
	got, _, err = store.MenuDraft(ctx)
	if err != nil || !reflect.DeepEqual(got, restored) {
		t.Fatalf("discard/restore=%+v err=%v want=%+v", got, err, restored)
	}
	// A missing installation must not deactivate the active version or leave metadata.
	bad := version
	bad.Title = "Failed"
	bad.Items = []MenuVersionItem{{InstallationID: ids[0], Position: 0}, {InstallationID: 999999, Position: 1}}
	if err := store.SaveActiveMenu(ctx, 0, bad); err == nil {
		t.Fatal("saved version with nonexistent installation")
	}
	versions, err = store.MenuVersions(ctx)
	if err != nil || len(versions) != 1 || !versions[0].Active || versions[0].Title != want.Title || len(versions[0].Items) != 3 {
		t.Fatalf("partial version persisted: %+v err=%v", versions, err)
	}
	if err := store.SaveMenuDraft(ctx, MenuDraft{Title: "Failed", Items: []MenuDraftItem{{InstallationID: 999999}}}); err == nil {
		t.Fatal("saved missing draft installation")
	}
	got, _, err = store.MenuDraft(ctx)
	if err != nil || !reflect.DeepEqual(got, restored) {
		t.Fatalf("partial draft persisted: %+v err=%v", got, err)
	}
	// Empty structure remains empty even when the script contains legacy items.
	empty := MenuVersion{Title: "Empty", Label: "Tools", Message: "Ready", Script: "#!ipxe\nitem ubuntu Legacy\n", SHA256: "empty", Items: []MenuVersionItem{}}
	if err := store.SaveActiveMenu(ctx, 0, empty); err != nil {
		t.Fatal(err)
	}
	versions, err = store.MenuVersions(ctx)
	if err != nil || len(versions) != 2 || !versions[0].Active || versions[0].Items == nil || len(versions[0].Items) != 0 || versions[1].Active || len(versions[1].Items) != 3 {
		t.Fatalf("versions after empty publication=%+v err=%v", versions, err)
	}
	if got := draftFromVersion(versions[0]); len(got.Items) != 0 {
		t.Fatalf("empty publication restored legacy items: %+v", got)
	}
	// Restoring a version stores a fresh version and keeps its structural items.
	if err := store.SaveActiveMenu(ctx, 0, saved); err != nil {
		t.Fatal(err)
	}
	versions, err = store.MenuVersions(ctx)
	if err != nil || len(versions) != 3 || !versions[0].Active || versions[0].ID == saved.ID || len(versions[0].Items) != 3 || versions[0].Items[0].VersionID != versions[0].ID {
		t.Fatalf("restored version=%+v err=%v", versions, err)
	}
	if err := store.SaveMenuDraft(ctx, MenuDraft{Title: productMenuTitle}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteInstallation(ctx, ids[0]); err == nil {
		t.Fatal("deleted installation referenced by active version")
	}
	if _, err := store.Pool.Exec(ctx, `DELETE FROM menu_versions`); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteInstallation(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
}
