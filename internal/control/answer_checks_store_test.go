package control

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func answerCheckFixtures(t *testing.T, store Store) (int64, WindowsAnswerVariant, UbuntuAutoinstallFile) {
	t.Helper()
	ctx := context.Background()
	var user int64
	if err := store.Pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,role) VALUES('checker','unused','operator') RETURNING id`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	profile, err := store.CreateWindowsProfile(ctx, WindowsProfileSource{StorageID: 1, Path: "check.iso", StorageRoot: "/iso", Size: 100, Modified: time.Now(), Title: "Check", Folder: "Windows"})
	if err != nil {
		t.Fatal(err)
	}
	answer, err := store.CreateWindowsAnswer(ctx, WindowsAnswerVariant{ProfileID: profile.ID, Title: "Check", SHA256: strings.Repeat("a", 64), Size: 10})
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
	return user, answer, ubuntu
}

// Catches conflating OS-specific IDs, overwriting history, trusting form authors,
// and assigning a result to changed content or a new copy.
func TestAnswerChecksStore(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	user, win, ubuntu := answerCheckFixtures(t, store)
	for _, check := range []AnswerCheck{
		{Kind: "windows", AnswerID: win.ID, SHA256: win.SHA256, Result: "success", UserID: user, Author: "forged", Machine: "VM 113", Comment: "<script>alert(1)</script>"},
		{Kind: "ubuntu", AnswerID: ubuntu.ID, SHA256: ubuntu.SHA256, Result: "success", UserID: user},
		{Kind: "windows", AnswerID: win.ID, SHA256: win.SHA256, Result: "failure", UserID: user, Comment: "No disk"},
	} {
		if err := store.SaveAnswerCheck(ctx, check); err != nil {
			t.Fatal(err)
		}
	}
	latest, err := store.LatestAnswerChecks(ctx)
	if err != nil || len(latest) != 2 {
		t.Fatalf("latest=%+v err=%v", latest, err)
	}
	for _, check := range latest {
		if check.Author != "checker" || check.CreatedAt.IsZero() || check.Kind == "windows" && check.Result != "failure" {
			t.Fatalf("wrong latest: %+v", check)
		}
	}
	history, err := store.AnswerCheckHistory(ctx, "windows", win.ID, 0)
	if err != nil || len(history) != 2 || history[0].Result != "failure" || history[1].Comment != "<script>alert(1)</script>" {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	older, err := store.AnswerCheckHistory(ctx, "windows", win.ID, history[0].ID)
	if err != nil || len(older) != 1 || older[0].ID != history[1].ID {
		t.Fatalf("older=%+v %v", older, err)
	}
	bad := AnswerCheck{Kind: "windows", AnswerID: win.ID, SHA256: strings.Repeat("b", 64), Result: "success", UserID: user}
	if err := store.SaveAnswerCheck(ctx, bad); !errors.Is(err, errAnswerCheckConflict) {
		t.Fatalf("stale digest accepted: %v", err)
	}
	copy, err := store.CreateWindowsAnswer(ctx, WindowsAnswerVariant{ProfileID: win.ProfileID, Title: "Copy", SHA256: win.SHA256, Size: 10})
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
	user, win, _ := answerCheckFixtures(t, store)
	if err := store.SaveAnswerCheck(ctx, AnswerCheck{Kind: "windows", AnswerID: win.ID, SHA256: win.SHA256, Result: "success", UserID: user}); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../deploy/sql/2026-11-14-answer-checks.sql")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := store.Pool.Exec(ctx, string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.LatestAnswerChecks(ctx)
	if err != nil || len(got) != 1 || got[0].Author != "checker" {
		t.Fatalf("migration changed checks: %+v %v", got, err)
	}
}
