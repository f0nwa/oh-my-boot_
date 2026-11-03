package control

import (
	"context"
	"os"
	"testing"

	"omb/internal/autoinstall"
)

func TestUbuntuDefaultDiskMigration(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	serial, err := store.CreateUbuntuAutoinstall(ctx, testAutoinstallFile(t, "serial-before"))
	if err != nil {
		t.Fatal(err)
	}
	interactive := defaultDiskAutoinstallFile(t, "interactive-before")
	interactive.Values.DiskMode = "interactive"
	interactive.UserData, err = autoinstall.Render(interactive.Values)
	if err != nil {
		t.Fatal(err)
	}
	interactive.SHA256 = fmtDigest(interactive.UserData)
	if _, err := store.CreateUbuntuAutoinstall(ctx, interactive); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		t.Helper()
		var value string
		if err := store.Pool.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(f) ORDER BY id)::text FROM ubuntu_autoinstall_files f`).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := snapshot()
	migration, err := os.ReadFile("../../deploy/sql/2026-11-02-ubuntu-default-disk.sql")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := store.Pool.Exec(ctx, string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	if after := snapshot(); after != before {
		t.Fatalf("migration changed existing files: %s != %s", after, before)
	}
	saved, err := store.CreateUbuntuAutoinstall(ctx, defaultDiskAutoinstallFile(t, "default-after"))
	if err != nil {
		t.Fatal(err)
	}
	if saved.Values.DiskMode != "default" || saved.Values.DiskSerial != "" {
		t.Fatalf("default disk changed: %+v", saved.Values)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO ubuntu_autoinstall_files(title,"values",user_data,sha256,state,disk_mode,disk_serial) SELECT 'stale',jsonb_set("values",'{DiskSerial}','"STALE"'),user_data,repeat('f',64),state,disk_mode,'STALE' FROM ubuntu_autoinstall_files WHERE id=$1`, saved.ID); err == nil {
		t.Fatal("default disk accepted a serial")
	}
	if _, err := store.Pool.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	if got, err := store.UbuntuAutoinstall(ctx, serial.ID); err != nil || got.Values.DiskMode != "serial" {
		t.Fatalf("existing serial file changed: %+v, %v", got.Values, err)
	}
}
