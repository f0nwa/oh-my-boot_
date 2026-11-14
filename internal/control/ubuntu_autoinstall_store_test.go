package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"omb/internal/autoinstall"
)

func TestUbuntuAutoinstallSchema(t *testing.T) {
	store := installationTestStore(t)
	if _, err := store.Pool.Exec(context.Background(), `SELECT id,title,"values",user_data,sha256,state,disk_mode,disk_serial,created_at FROM ubuntu_autoinstall_files`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(context.Background(), `SELECT autoinstall_id FROM installations`); err != nil {
		t.Fatal(err)
	}
}

func testAutoinstallFile(t *testing.T, hostname string) UbuntuAutoinstallFile {
	t.Helper()
	values, data, err := autoinstall.Compile(autoinstall.Form{Locale: "en_US.UTF-8", Keyboard: "us", TimeZone: "UTC", Hostname: hostname, Username: "operator", Password: "never-persist-this-password", DiskMode: "serial", DiskSerial: "DISK123"})
	if err != nil {
		t.Fatal(err)
	}
	return UbuntuAutoinstallFile{Title: hostname, Values: values, UserData: data, SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), State: "validated"}
}

func TestUbuntuAutoinstallModel(t *testing.T) {
	ubuntu := BootProfile{ID: 1, Kind: BootProfileUbuntuBuiltin}
	windows := BootProfile{ID: 2, Kind: BootProfileWindowsISO, WindowsProfileID: 3}
	files := []UbuntuAutoinstallSummary{{ID: 4, State: "validated"}, {ID: 5, State: "pending"}}
	for _, tc := range []struct {
		name    string
		profile BootProfile
		id      int64
		valid   bool
	}{
		{"manual", ubuntu, 0, true}, {"validated", ubuntu, 4, true}, {"missing", ubuntu, 6, false}, {"unvalidated", ubuntu, 5, false}, {"negative", ubuntu, -1, false}, {"Windows", windows, 4, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateInstallation(Installation{BootProfileID: tc.profile.ID, Title: "Lab", AutoinstallID: tc.id}, tc.profile, nil, nil, files)
			if (err == nil) != tc.valid {
				t.Fatalf("validation = %v; valid=%v", err, tc.valid)
			}
		})
	}
}

func TestUbuntuAutoinstallStore(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	file := testAutoinstallFile(t, "lab")
	saved, err := store.CreateUbuntuAutoinstall(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID <= 0 || saved.CreatedAt.IsZero() || !bytes.Equal(saved.UserData, file.UserData) || saved.SHA256 != file.SHA256 || !reflect.DeepEqual(saved.Values, file.Values) {
		t.Fatalf("saved = %+v", saved)
	}
	got, err := store.UbuntuAutoinstall(ctx, saved.ID)
	if err != nil || !reflect.DeepEqual(got, saved) {
		t.Fatalf("round trip = %+v, err=%v", got, err)
	}
	summaries, err := store.UbuntuAutoinstalls(ctx)
	want := []UbuntuAutoinstallSummary{{ID: saved.ID, Title: saved.Title, SHA256: saved.SHA256, State: "validated", DiskMode: "serial", DiskSerial: "DISK123", CreatedAt: saved.CreatedAt}}
	if err != nil || !reflect.DeepEqual(summaries, want) {
		t.Fatalf("summaries=%+v err=%v", summaries, err)
	}
	var json string
	if err := store.Pool.QueryRow(ctx, `SELECT "values"::text FROM ubuntu_autoinstall_files WHERE id=$1`, saved.ID).Scan(&json); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(json, "never-persist-this-password") || strings.Contains(string(got.UserData), "never-persist-this-password") || !strings.Contains(json, saved.Values.PasswordHash) {
		t.Fatal("plaintext or missing hash in stored data")
	}
	if _, err := store.CreateUbuntuAutoinstall(ctx, file); err == nil {
		t.Fatal("duplicate digest accepted")
	}
	for _, query := range []string{
		`UPDATE ubuntu_autoinstall_files SET title='changed' WHERE id=$1`,
		`UPDATE ubuntu_autoinstall_files SET user_data='changed' WHERE id=$1`,
		`UPDATE ubuntu_autoinstall_files SET sha256=repeat('b',64) WHERE id=$1`,
		`UPDATE ubuntu_autoinstall_files SET "values"='{}' WHERE id=$1`,
	} {
		if _, err := store.Pool.Exec(ctx, query, saved.ID); err == nil {
			t.Fatalf("mutable library: %s", query)
		}
	}
	for _, mutate := range []func(*UbuntuAutoinstallFile){
		func(f *UbuntuAutoinstallFile) { f.UserData = []byte("plaintext") },
		func(f *UbuntuAutoinstallFile) { f.SHA256 = strings.Repeat("b", 64) },
		func(f *UbuntuAutoinstallFile) { f.Values.PasswordHash = "plaintext" },
		func(f *UbuntuAutoinstallFile) { f.State = "pending" },
		func(f *UbuntuAutoinstallFile) { f.Title = "" },
	} {
		invalid := testAutoinstallFile(t, "invalid")
		mutate(&invalid)
		if _, err := store.CreateUbuntuAutoinstall(ctx, invalid); err == nil {
			t.Fatal("invalid file accepted")
		}
	}
	other, err := store.CreateUbuntuAutoinstall(ctx, testAutoinstallFile(t, "other"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureBootProfiles(ctx); err != nil {
		t.Fatal(err)
	}
	profiles, err := store.BootProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ubuntu := profiles[0]
	win, err := store.CreateWindowsProfile(ctx, WindowsProfileSource{StorageID: 1, Path: "win.iso", StorageRoot: "/iso", Size: 100, Modified: time.Now(), Title: "Windows"})
	if err != nil {
		t.Fatal(err)
	}
	var winID int64
	if err := store.Pool.QueryRow(ctx, `SELECT boot_profile_id FROM windows_boot_profiles WHERE windows_profile_id=$1`, win.ID).Scan(&winID); err != nil {
		t.Fatal(err)
	}
	var expected []Installation
	for i, id := range []int64{saved.ID, saved.ID, other.ID, 0} {
		item, err := store.CreateInstallation(ctx, Installation{BootProfileID: ubuntu.ID, Title: fmt.Sprintf("Lab %d", i), AutoinstallID: id})
		if err != nil {
			t.Fatal(err)
		}
		expected = append(expected, item)
	}
	items, err := store.Installations(ctx)
	if err != nil || !reflect.DeepEqual(items, expected) {
		t.Fatalf("bindings=%+v err=%v", items, err)
	}
	for _, item := range []Installation{{BootProfileID: winID, Title: "Bad", AutoinstallID: saved.ID}, {BootProfileID: ubuntu.ID, Title: "Missing", AutoinstallID: 999}, {BootProfileID: ubuntu.ID, Title: "Negative", AutoinstallID: -1}} {
		if _, err := store.CreateInstallation(ctx, item); err == nil {
			t.Fatalf("accepted invalid binding %+v", item)
		}
	}
	// Boot-entry bindings can change; immutable answer-file bytes remain protected.
	for _, id := range []int64{0, other.ID, saved.ID} {
		item := items[0]
		item.AutoinstallID = id
		if err := store.UpdateInstallation(ctx, item); err != nil {
			t.Fatalf("edit answer binding: %v", err)
		}
	}
	item := items[3]
	item.AutoinstallID = saved.ID
	if err := store.UpdateInstallation(ctx, item); err != nil {
		t.Fatalf("edit manual binding: %v", err)
	}

	if _, err := store.Pool.Exec(ctx, `DELETE FROM ubuntu_autoinstall_files WHERE id=$1`, saved.ID); err == nil {
		t.Fatal("deleted referenced file")
	}
}

func TestUbuntuAutoinstallMigration(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	// Reproduce the unified schema immediately before this additive migration.
	if _, err := store.Pool.Exec(ctx, `DROP TABLE answer_installation_checks; DROP TRIGGER IF EXISTS installation_autoinstall_immutable ON installations; ALTER TABLE installations DROP COLUMN autoinstall_id; DROP TABLE ubuntu_autoinstall_files`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `
 INSERT INTO windows_profiles(storage_id,path,storage_root,size_bytes,modified_at,title) VALUES(1,'win.iso','/iso',100,now(),'Windows');
 INSERT INTO windows_answer_variants(profile_id,title,sha256,size_bytes) VALUES(1,'XML',repeat('a',64),10);
 INSERT INTO winpe_driver_configs(profile_id,answer_id,title,selected_infs) VALUES(1,1,'Drivers','[{}]');
 INSERT INTO boot_profiles(kind,title) VALUES('windows_iso','Windows'),('ubuntu_builtin','Ubuntu');
 INSERT INTO windows_boot_profiles VALUES(1,1);
 INSERT INTO installations(boot_profile_id,title) VALUES(1,'Windows'),(2,'Ubuntu');
 INSERT INTO windows_installation_options VALUES(1,1,NULL);
 INSERT INTO menu_draft(id,title,label,message) VALUES(1,'Draft','Tools','Ready');
 INSERT INTO menu_draft_items(position,installation_id,title,folder) VALUES(0,1,'Draft Windows','Lab');
 INSERT INTO menu_versions(title,diagnostic_label,diagnostic_message,script,sha256,active) VALUES('Published','Tools','Ready','#!ipxe','old',true);
 INSERT INTO menu_version_items(version_id,installation_id,position,title,folder) VALUES(1,2,0,'Old Ubuntu','Recovery');`); err != nil {
		t.Fatal(err)
	}
	snapshots := map[string]string{}
	tables := []string{"windows_profiles", "windows_answer_variants", "winpe_driver_configs", "boot_profiles", "windows_boot_profiles", "windows_installation_options", "menu_draft", "menu_draft_items", "menu_versions", "menu_version_items"}
	snapshot := func(table string) string {
		t.Helper()
		var result string
		if err := store.Pool.QueryRow(ctx, "SELECT jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text)::text FROM "+table+" t").Scan(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	for _, table := range tables {
		snapshots[table] = snapshot(table)
	}
	var before string
	if err := store.Pool.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(t) ORDER BY id)::text FROM installations t`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../deploy/sql/2026-10-30-ubuntu-autoinstall.sql")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := store.Pool.Exec(ctx, string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	for table, want := range snapshots {
		if got := snapshot(table); got != want {
			t.Fatalf("%s changed: %s != %s", table, got, want)
		}
	}
	var after string
	if err := store.Pool.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(t)-'autoinstall_id' ORDER BY id)::text FROM installations t`).Scan(&after); err != nil || before != after {
		t.Fatalf("installations changed: %s != %s; err=%v", before, after, err)
	}
	saved, err := store.CreateUbuntuAutoinstall(ctx, testAutoinstallFile(t, "migrated"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.CreateInstallation(ctx, Installation{BootProfileID: 2, Title: "Migrated", AutoinstallID: saved.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	got, err := store.UbuntuAutoinstall(ctx, saved.ID)
	if err != nil || !reflect.DeepEqual(got, saved) {
		t.Fatalf("rerun changed library: %+v err=%v", got, err)
	}
	items, err := store.Installations(ctx)
	if err != nil || len(items) != 3 || items[2] != item {
		t.Fatalf("rerun changed binding: %+v err=%v", items, err)
	}
}

func TestUbuntuExtendedValuesPreserveStoredBytes(t *testing.T) {
	store := installationTestStore(t)
	ctx := context.Background()
	file := testAutoinstallFile(t, "desktop")
	file.Values.RealName = "Рабочая станция"
	file.Values.Packages = []string{"curl", "vim"}
	file.Values.Keyboard = "us,ru"
	file.Values.KeyboardVariant = ","
	file.Values.KeyboardToggle = "alt_shift_toggle"
	data, err := autoinstall.Render(file.Values)
	if err != nil {
		t.Fatal(err)
	}
	file.UserData = data
	file.SHA256 = fmtDigest(data)
	saved, err := store.CreateUbuntuAutoinstall(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.UbuntuAutoinstall(ctx, saved.ID)
	if err != nil || !bytes.Equal(got.UserData, data) || !reflect.DeepEqual(got.Values, file.Values) {
		t.Fatalf("extended values lost: %v", err)
	}
}
