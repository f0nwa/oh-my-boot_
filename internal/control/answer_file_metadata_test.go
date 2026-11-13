package control

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAnswerFileMetadata(t *testing.T) {
	store := installationTestStore(t)
	ctx := context.Background()
	migration, err := os.ReadFile("../../deploy/sql/2026-11-13-answer-disk-summary.sql")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := store.Pool.Exec(ctx, string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	var pid int64
	if err := store.Pool.QueryRow(ctx, `INSERT INTO windows_profiles(storage_id,path,storage_root,size_bytes,modified_at,title) VALUES(1,'test.iso','/test',1,now(),'Windows') RETURNING id`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	disk := AnswerFileDisk{Kind: "wipe", Target: "Диск 2"}
	saved, err := store.CreateWindowsAnswer(ctx, WindowsAnswerVariant{ProfileID: pid, Title: "Test", SHA256: strings.Repeat("a", 64), Size: 10, Disk: disk})
	if err != nil {
		t.Fatal(err)
	}
	if saved.CreatedAt.IsZero() || time.Since(saved.CreatedAt) > time.Minute {
		t.Fatalf("created=%v", saved.CreatedAt)
	}
	one, err := store.WindowsAnswer(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	list, err := store.WindowsAnswers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if one.Disk != disk || !one.CreatedAt.Equal(saved.CreatedAt) || len(list) != 1 || list[0].Disk != disk || !list[0].CreatedAt.Equal(saved.CreatedAt) {
		t.Fatalf("metadata lost: one=%+v list=%+v", one, list)
	}
	var uid int64
	if err := store.Pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,role) VALUES('creator','unused','operator') RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	if err := store.Audit(ctx, uid, "ubuntu-autoinstall.create", "ok", "44"); err != nil {
		t.Fatal(err)
	}
	authors, err := store.ResourceAuthors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if authors.UbuntuAnswers[44] != "creator" || authors.clone().UbuntuAnswers[44] != "creator" {
		t.Fatalf("ubuntu author=%+v", authors)
	}
}
