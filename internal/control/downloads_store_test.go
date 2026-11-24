package control

import (
	"context"
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
