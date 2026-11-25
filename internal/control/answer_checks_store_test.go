package control

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func answerCheckFixtures(t *testing.T, store Store) (int64, WindowsAnswerVariant, UbuntuAutoinstallFile, WindowsProfile) {
	t.Helper()
	ctx := context.Background()
	var user int64
	if err := store.Pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,role) VALUES('checker','unused','operator') RETURNING id`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	profile, err := store.CreateWindowsProfile(ctx, WindowsProfileSource{StorageID: 1, Path: "check.iso", StorageRoot: "/iso", Size: 100, Modified: time.Now(), Title: "Check ISO", Folder: "Windows"})
	if err != nil {
		t.Fatal(err)
	}
	answer, err := store.CreateWindowsAnswer(ctx, WindowsAnswerVariant{Title: "Check", SHA256: strings.Repeat("a", 64), Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetWindowsAnswerState(ctx, answer.ID, "ready", ""); err != nil {
		t.Fatal(err)
	}
	ubuntu, err := store.CreateUbuntuAutoinstall(ctx, testAutoinstallFile(t, "check"))
	if err != nil {
		t.Fatal(err)
	}
	return user, answer, ubuntu, profile
}

// Catches conflating OS-specific IDs, overwriting history, trusting form authors,
// and assigning a result to changed content or a new copy.
func TestAnswerChecksStore(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	user, win, ubuntu, profile := answerCheckFixtures(t, store)
	profile2, err := store.CreateWindowsProfile(ctx, WindowsProfileSource{StorageID: 1, Path: "check2.iso", StorageRoot: "/iso", Size: 200, Modified: time.Now(), Title: "Check ISO 2", Folder: "Windows"})
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []AnswerCheck{
		{Kind: "windows", AnswerID: win.ID, SHA256: win.SHA256, WindowsProfileID: profile.ID, Result: "success", UserID: user, Author: "forged", Machine: "VM 113", Comment: "<script>alert(1)</script>"},
		{Kind: "ubuntu", AnswerID: ubuntu.ID, SHA256: ubuntu.SHA256, ProfileTitle: "Ubuntu Desktop 24.04", Result: "success", UserID: user},
		{Kind: "windows", AnswerID: win.ID, SHA256: win.SHA256, WindowsProfileID: profile.ID, Result: "failure", UserID: user, Comment: "No disk"},
		{Kind: "windows", AnswerID: win.ID, SHA256: win.SHA256, WindowsProfileID: profile2.ID, Result: "success", UserID: user},
	} {
		if err := store.SaveAnswerCheck(ctx, check); err != nil {
			t.Fatal(err)
		}
	}
	latest, err := store.LatestAnswerChecks(ctx)
	if err != nil || len(latest) != 2 {
		t.Fatalf("latest=%+v err=%v", latest, err)
	}
	byProfile, err := store.LatestAnswerChecksByProfile(ctx)
	if err != nil || len(byProfile) != 3 {
		t.Fatalf("byProfile=%+v err=%v", byProfile, err)
	}
	history, err := store.AnswerCheckHistory(ctx, "windows", win.ID, 0)
	if err != nil || len(history) != 3 || history[0].WindowsProfileID != profile2.ID || history[0].ProfileTitle != "Check ISO 2" || history[1].Result != "failure" || history[2].Comment != "<script>alert(1)</script>" {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	older, err := store.AnswerCheckHistory(ctx, "windows", win.ID, history[0].ID)
	if err != nil || len(older) != 2 || older[1].ID != history[2].ID {
		t.Fatalf("older=%+v %v", older, err)
	}
	profile3, err := store.CreateWindowsProfile(ctx, WindowsProfileSource{StorageID: 1, Path: "check3.iso", StorageRoot: "/iso", Size: 300, Modified: time.Now(), Title: "AAA Check ISO 3", Folder: "Windows"})
	if err != nil {
		t.Fatal(err)
	}
	profile4, err := store.CreateWindowsProfile(ctx, WindowsProfileSource{StorageID: 1, Path: "check4.iso", StorageRoot: "/iso", Size: 400, Modified: time.Now(), Title: "AAB Check ISO 4", Folder: "Windows"})
	if err != nil {
		t.Fatal(err)
	}
	for _, pid := range []int64{profile3.ID, profile4.ID} {
		if err := store.SaveAnswerCheck(ctx, AnswerCheck{Kind: "windows", AnswerID: win.ID, SHA256: win.SHA256, WindowsProfileID: pid, Result: "success", UserID: user}); err != nil {
			t.Fatal(err)
		}
	}
	orderedByProfile, err := store.LatestAnswerChecksByProfile(ctx)
	if err != nil || len(orderedByProfile) != 5 {
		t.Fatalf("orderedByProfile=%+v err=%v", orderedByProfile, err)
	}
	for i := 0; i+1 < len(orderedByProfile); i++ {
		if orderedByProfile[i].ID <= orderedByProfile[i+1].ID {
			t.Fatalf("LatestAnswerChecksByProfile must be newest-first, got %+v", orderedByProfile)
		}
	}
	bad := AnswerCheck{Kind: "windows", AnswerID: win.ID, SHA256: strings.Repeat("b", 64), Result: "success", UserID: user}
	if err := store.SaveAnswerCheck(ctx, bad); !errors.Is(err, errAnswerCheckConflict) {
		t.Fatalf("stale digest accepted: %v", err)
	}
	copy, err := store.CreateWindowsAnswer(ctx, WindowsAnswerVariant{Title: "Copy", SHA256: strings.Repeat("c", 64), Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := store.AnswerCheckHistory(ctx, "windows", copy.ID, 0); err != nil || len(got) != 0 {
		t.Fatalf("copy inherited: %+v %v", got, err)
	}
	bad.SHA256 = win.SHA256
	if _, err := store.Pool.Exec(ctx, `UPDATE users SET role='observer' WHERE id=$1`, user); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAnswerCheck(ctx, bad); err == nil {
		t.Fatal("observer recorded a result")
	}
	if err := store.DeleteWindowsAnswer(ctx, win.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := store.AnswerCheckHistory(ctx, "windows", win.ID, 0); err != nil || len(got) != 0 {
		t.Fatalf("deleted file history: %+v %v", got, err)
	}
	got, err := store.LatestAnswerChecks(ctx)
	if err != nil || len(got) != 1 || got[0].Kind != "ubuntu" {
		t.Fatalf("deletion crossed kinds: %+v %v", got, err)
	}
}

func TestAnswerChecksMigration(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	user, win, _, profile := answerCheckFixtures(t, store)
	if err := store.SaveAnswerCheck(ctx, AnswerCheck{Kind: "windows", AnswerID: win.ID, SHA256: win.SHA256, WindowsProfileID: profile.ID, Result: "success", UserID: user}); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../deploy/sql/control.sql")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := store.Pool.Exec(ctx, string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.LatestAnswerChecksByProfile(ctx)
	if err != nil || len(got) != 1 || got[0].Author != "checker" || got[0].ProfileTitle != "Check ISO" {
		t.Fatalf("migration changed checks: %+v %v", got, err)
	}
}

func TestDeleteUserPreservesAnswerChecksAndProtectsAdmins(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	user, win, _, profile := answerCheckFixtures(t, store)

	var adminID int64
	if err := store.Pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,role,enabled) VALUES('admin1','unused','admin',true) RETURNING id`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES('tok-checker',$1,now()+interval '1 hour')`, user); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAnswerCheck(ctx, AnswerCheck{Kind: "windows", AnswerID: win.ID, SHA256: win.SHA256, WindowsProfileID: profile.ID, Result: "success", UserID: user}); err != nil {
		t.Fatal(err)
	}

	migration, err := os.ReadFile("../../deploy/sql/control.sql")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := store.Pool.Exec(ctx, string(migration)); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := store.DeleteUser(ctx, adminID, adminID); !errors.Is(err, errSelfDelete) {
		t.Fatalf("expected errSelfDelete, got %v", err)
	}
	if _, err := store.DeleteUser(ctx, adminID, user); !errors.Is(err, errLastAdmin) {
		t.Fatalf("expected errLastAdmin when deleting sole active admin, got %v", err)
	}

	var builtInAdminID int64
	if err := store.Pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,role,enabled) VALUES('admin','unused','admin',true) RETURNING id`).Scan(&builtInAdminID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteUser(ctx, builtInAdminID, adminID); !errors.Is(err, errProtectedAdmin) {
		t.Fatalf("expected errProtectedAdmin when deleting built-in admin, got %v", err)
	}

	deletedName, err := store.DeleteUser(ctx, user, adminID)
	if err != nil || deletedName != "checker" {
		t.Fatalf("DeleteUser(checker) = %q, %v", deletedName, err)
	}
	if _, err := store.DeleteUser(ctx, user, adminID); !errors.Is(err, errUserNotFound) {
		t.Fatalf("expected errUserNotFound on second delete, got %v", err)
	}

	var sessionCount int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id=$1`, user).Scan(&sessionCount); err != nil || sessionCount != 0 {
		t.Fatalf("sessions remaining = %d, err = %v", sessionCount, err)
	}

	checks, err := store.LatestAnswerChecksByProfile(ctx)
	if err != nil || len(checks) != 1 || checks[0].Author != "checker" || checks[0].UserID != 0 {
		t.Fatalf("checks after user delete = %+v, err = %v", checks, err)
	}
}
