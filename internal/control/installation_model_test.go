package control

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These cases catch a shared profile being mistaken for an installation,
// Windows options leaking to Ubuntu, and components crossing ISO profiles.
func TestInstallationModelOptions(t *testing.T) {
	windows := BootProfile{ID: 1, Kind: "windows_iso", Title: "Windows", WindowsProfileID: 42}
	ubuntu := BootProfile{ID: 2, Kind: "ubuntu_builtin", Title: "Ubuntu Desktop"}
	answers := []WindowsAnswerVariant{{ID: 10, ProfileID: 42}, {ID: 11, ProfileID: 43}}
	configs := []DriverConfig{{ID: 20, ProfileID: 42, AnswerID: 10}, {ID: 21, ProfileID: 43}, {ID: 22, ProfileID: 42, AnswerID: 11}}
	for _, tc := range []struct {
		name    string
		profile BootProfile
		item    Installation
		valid   bool
	}{
		{"Windows manual", windows, Installation{BootProfileID: 1, Title: "Manual"}, true},
		{"Windows XML", windows, Installation{BootProfileID: 1, Title: "XML", AnswerID: 10}, true},
		{"Windows drivers", windows, Installation{BootProfileID: 1, Title: "Drivers", DriverConfigID: 20}, true},
		{"Ubuntu manual", ubuntu, Installation{BootProfileID: 2, Title: "Ubuntu"}, true},
		{"two components", windows, Installation{BootProfileID: 1, Title: "Both", AnswerID: 10, DriverConfigID: 20}, false},
		{"foreign XML", windows, Installation{BootProfileID: 1, Title: "Wrong", AnswerID: 11}, false},
		{"missing XML", windows, Installation{BootProfileID: 1, Title: "Missing", AnswerID: 99}, false},
		{"foreign drivers", windows, Installation{BootProfileID: 1, Title: "Wrong", DriverConfigID: 21}, false},
		{"foreign driver XML", windows, Installation{BootProfileID: 1, Title: "Wrong", DriverConfigID: 22}, false},
		{"missing drivers", windows, Installation{BootProfileID: 1, Title: "Missing", DriverConfigID: 99}, false},
		{"Ubuntu XML", ubuntu, Installation{BootProfileID: 2, Title: "Wrong", AnswerID: 10}, false},
		{"Ubuntu drivers", ubuntu, Installation{BootProfileID: 2, Title: "Wrong", DriverConfigID: 20}, false},
		{"negative component", windows, Installation{BootProfileID: 1, Title: "Wrong", AnswerID: -1}, false},
		{"wrong profile", windows, Installation{BootProfileID: 2, Title: "Wrong"}, false},
		{"empty title", ubuntu, Installation{BootProfileID: 2}, false},
		{"unknown kind", BootProfile{ID: 3, Kind: "unknown"}, Installation{BootProfileID: 3, Title: "Unknown"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateInstallation(tc.item, tc.profile, answers, configs)
			if (err == nil) != tc.valid {
				t.Fatalf("ValidateInstallation error = %v, valid = %v", err, tc.valid)
			}
		})
	}
}

func TestInstallationModelStore(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	if err := store.EnsureBootProfiles(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureBootProfiles(ctx); err != nil {
		t.Fatal(err)
	}
	win, err := store.CreateWindowsProfile(ctx, WindowsProfileSource{StorageID: 1, Path: "win.iso", StorageRoot: "/iso", Size: 100, Modified: time.Now(), Title: "Windows", Folder: "Windows"})
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := store.BootProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 2 {
		t.Fatalf("profiles = %+v; want unique Ubuntu and newly created Windows", profiles)
	}
	var windows, ubuntu BootProfile
	for _, p := range profiles {
		if p.Kind == "windows_iso" {
			windows = p
		} else if p.Kind == "ubuntu_builtin" {
			ubuntu = p
		}
	}
	if windows.WindowsProfileID != win.ID || ubuntu.WindowsProfileID != 0 {
		t.Fatalf("profile links = %+v", profiles)
	}
	answer, err := store.CreateWindowsAnswer(ctx, WindowsAnswerVariant{ProfileID: win.ID, Title: "XML", SHA256: strings.Repeat("a", 64), Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	var configID int64
	if err := store.Pool.QueryRow(ctx, `INSERT INTO winpe_driver_configs(profile_id,answer_id,title,selected_infs) VALUES($1,$2,'Drivers','[{}]') RETURNING id`, win.ID, answer.ID).Scan(&configID); err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	var wantItems []Installation
	for _, item := range []Installation{
		{BootProfileID: windows.ID, Title: "Manual", Folder: "Windows"},
		{BootProfileID: windows.ID, Title: "XML", AnswerID: answer.ID},
		{BootProfileID: windows.ID, Title: "Drivers", DriverConfigID: configID},
		{BootProfileID: ubuntu.ID, Title: "Ubuntu Lab", Folder: "Lab"},
		{BootProfileID: ubuntu.ID, Title: "Ubuntu Recovery", Folder: "Recovery"},
	} {
		created, err := store.CreateInstallation(ctx, item)
		if err != nil {
			t.Fatal(err)
		}
		if created.ID <= 0 || seen[created.ID] {
			t.Fatalf("non-unique installation ID: %+v", created)
		}
		seen[created.ID] = true
		wantItems = append(wantItems, created)
	}
	items, err := store.Installations(ctx)
	if err != nil || len(items) != 5 {
		t.Fatalf("installations = %+v, err = %v", items, err)
	}
	if !reflect.DeepEqual(items, wantItems) {
		t.Fatalf("round trip installations = %+v, want %+v", items, wantItems)
	}
	var options int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM windows_installation_options`).Scan(&options); err != nil || options != 3 {
		t.Fatalf("Windows option rows = %d, err = %v", options, err)
	}
	for _, invalid := range []Installation{
		{BootProfileID: ubuntu.ID, Title: "Bad", AnswerID: answer.ID},
		{BootProfileID: windows.ID, Title: "Bad", AnswerID: answer.ID, DriverConfigID: configID},
		{BootProfileID: windows.ID, Title: "Bad", AnswerID: 999},
		{BootProfileID: 999, Title: "Bad"},
	} {
		if _, err := store.CreateInstallation(ctx, invalid); err == nil {
			t.Fatalf("accepted %+v", invalid)
		}
	}
	items, err = store.Installations(ctx)
	if err != nil || len(items) != 5 {
		t.Fatalf("failed creations left rows: %+v, err = %v", items, err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO boot_profiles(kind,title) VALUES('ubuntu_builtin','Duplicate')`); err == nil {
		t.Fatal("duplicate Ubuntu profile accepted")
	}

	if err := store.EnsureBootProfiles(ctx); err != nil {
		t.Fatal(err)
	}
	profiles, err = store.BootProfiles(ctx)
	if err != nil || len(profiles) != 2 {
		t.Fatalf("Ensure duplicated profiles: %+v, err=%v", profiles, err)
	}
	// A failure after inserting the base row must roll back that row as well.
	if _, err := store.Pool.Exec(ctx, `ALTER TABLE windows_installation_options RENAME TO unavailable_windows_options`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateInstallation(ctx, Installation{BootProfileID: windows.ID, Title: "Failed"}); err == nil {
		t.Fatal("created Windows installation without options")
	}
	var count int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM installations`).Scan(&count); err != nil || count != 5 {
		t.Fatalf("partial installation persisted: count=%d err=%v", count, err)
	}
}

// Run against a real disposable database when PostgreSQL tools are available.
func installationTestStore(t *testing.T) Store {
	t.Helper()
	for _, binary := range []string{"initdb", "pg_ctl"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("PostgreSQL integration requires %s", binary)
		}
	}
	root, err := os.MkdirTemp("/tmp", "pxe-installations-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	data := filepath.Join(root, "db")
	run := func(name string, args ...string) {
		t.Helper()
		out, err := exec.Command(name, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
	}
	run("initdb", "-D", data, "-A", "trust", "--no-locale", "-U", "postgres")
	run("pg_ctl", "-D", data, "-l", filepath.Join(root, "server.log"), "-o", fmt.Sprintf("-F -h '' -k %s", root), "-w", "start")
	t.Cleanup(func() {
		out, err := exec.Command("pg_ctl", "-D", data, "-m", "immediate", "-w", "stop").CombinedOutput()
		if err != nil {
			t.Errorf("stop PostgreSQL: %v\n%s", err, out)
		}
	})
	pool, err := pgxpool.New(context.Background(), fmt.Sprintf("host=%s user=postgres dbname=postgres", root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	sql, err := os.ReadFile("../../deploy/sql/control.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), string(sql)); err != nil {
		t.Fatal(err)
	}
	return Store{Pool: pool}
}

func TestInstallationModelMigration(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	if _, err := store.Pool.Exec(ctx, `DROP TABLE windows_installation_options,installations,windows_boot_profiles,boot_profiles`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `
 INSERT INTO windows_profiles(storage_id,path,storage_root,size_bytes,modified_at,title,folder) VALUES
 (1,'one.iso','/iso',100,now(),'Windows One','Windows'),(1,'two.iso','/iso',100,now(),'Windows Two','Lab');
 INSERT INTO windows_answer_variants(profile_id,title,folder,sha256,size_bytes) SELECT id,'Automated','Auto',repeat('a',64),10 FROM windows_profiles WHERE path='one.iso';
 INSERT INTO winpe_driver_configs(profile_id,answer_id,title,folder,selected_infs) SELECT profile_id,id,'VirtIO','Drivers','[{}]' FROM windows_answer_variants;
 INSERT INTO menu_versions(title,diagnostic_label,diagnostic_message,script,sha256) VALUES('Saved','Diagnostics','Test','#!ipxe','saved');
 INSERT INTO menu_draft_items(position,key,folder,title) VALUES(0,'winiso_1','Windows','Draft');`); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../deploy/sql/2026-10-30-unified-installations.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	profiles, err := store.BootProfiles(ctx)
	if err != nil || len(profiles) != 3 {
		t.Fatalf("profiles = %+v, err = %v", profiles, err)
	}
	items, err := store.Installations(ctx)
	if err != nil || len(items) != 5 {
		t.Fatalf("imported installations = %+v, err = %v", items, err)
	}
	byTitle := map[string]Installation{}
	for _, item := range items {
		byTitle[item.Title] = item
	}
	if byTitle["Windows One"].Folder != "Windows" || byTitle["Windows Two"].Folder != "Lab" || byTitle["Automated"].Folder != "Auto" || byTitle["VirtIO"].Folder != "Drivers" {
		t.Fatalf("lost imported titles/folders: %+v", items)
	}
	if byTitle["Windows One"].BootProfileID != byTitle["Automated"].BootProfileID || byTitle["Windows One"].BootProfileID != byTitle["VirtIO"].BootProfileID {
		t.Fatalf("shared ISO duplicated: %+v", items)
	}
	if byTitle["Automated"].AnswerID == 0 || byTitle["Automated"].DriverConfigID != 0 || byTitle["VirtIO"].DriverConfigID == 0 || byTitle["VirtIO"].AnswerID != 0 {
		t.Fatalf("bad imported options: %+v", items)
	}

	if _, err := store.Pool.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	repeated, err := store.Installations(ctx)
	if err != nil || !reflect.DeepEqual(items, repeated) {
		t.Fatalf("migration rerun changed installations: %+v, err=%v", repeated, err)
	}
	for table, want := range map[string]int{"windows_profiles": 2, "windows_answer_variants": 1, "winpe_driver_configs": 1, "menu_versions": 1, "menu_draft_items": 1, "windows_installation_options": 4} {
		var count int
		if err := store.Pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != want {
			t.Fatalf("%s count=%d want=%d err=%v", table, count, want, err)
		}
	}
}

func TestInstallationModelWindowsCreationAtomic(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	if _, err := store.Pool.Exec(ctx, `ALTER TABLE windows_boot_profiles RENAME TO unavailable_windows_boot_profiles`); err != nil {
		t.Fatal(err)
	}
	_, err := store.CreateWindowsProfile(ctx, WindowsProfileSource{StorageID: 1, Path: "failed.iso", StorageRoot: "/iso", Size: 100, Modified: time.Now(), Title: "Failed"})
	if err == nil {
		t.Fatal("creation succeeded without the common profile link")
	}
	var count int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM windows_profiles`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial Windows profile persisted: count=%d err=%v", count, err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM boot_profiles`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial common profile persisted: count=%d err=%v", count, err)
	}
}

// Deletion must release an unused profile's mandatory common link without
// weakening installation/library references or leaving partial deletes.
func TestInstallationModelWindowsDeletion(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	create := func(t *testing.T, title string) (WindowsProfile, int64) {
		t.Helper()
		profile, err := store.CreateWindowsProfile(ctx, WindowsProfileSource{StorageID: 1, Path: title + ".iso", StorageRoot: "/iso", Size: 100, Modified: time.Now(), Title: title})
		if err != nil {
			t.Fatal(err)
		}
		var bootID int64
		if err := store.Pool.QueryRow(ctx, `SELECT boot_profile_id FROM windows_boot_profiles WHERE windows_profile_id=$1`, profile.ID).Scan(&bootID); err != nil {
			t.Fatal(err)
		}
		return profile, bootID
	}
	assertRows := func(t *testing.T, profileID, bootID int64, want int) {
		t.Helper()
		for _, query := range []string{
			`SELECT count(*) FROM windows_profiles WHERE id=$1`,
			`SELECT count(*) FROM windows_boot_profiles WHERE windows_profile_id=$1`,
		} {
			var count int
			if err := store.Pool.QueryRow(ctx, query, profileID).Scan(&count); err != nil || count != want {
				t.Fatalf("profile rows=%d want=%d err=%v query=%s", count, want, err, query)
			}
		}
		var count int
		if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM boot_profiles WHERE id=$1`, bootID).Scan(&count); err != nil || count != want {
			t.Fatalf("common profile rows=%d want=%d err=%v", count, want, err)
		}
	}
	t.Run("unused profile removes all profile rows", func(t *testing.T) {
		profile, bootID := create(t, "Unused")
		if err := store.DeleteWindowsProfile(ctx, profile.ID); err != nil {
			t.Fatalf("delete unused Windows profile: %v", err)
		}
		assertRows(t, profile.ID, bootID, 0)
		if err := store.DeleteWindowsProfile(ctx, profile.ID); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("delete missing profile = %v, want ErrNoRows", err)
		}
	})
	t.Run("installation prevents profile deletion", func(t *testing.T) {
		profile, bootID := create(t, "InUse")
		installation, err := store.CreateInstallation(ctx, Installation{BootProfileID: bootID, Title: "Manual"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.DeleteWindowsProfile(ctx, profile.ID); err == nil {
			t.Fatal("deleted a profile referenced by an installation")
		}
		assertRows(t, profile.ID, bootID, 1)
		var count int
		if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM installations WHERE id=$1 AND boot_profile_id=$2`, installation.ID, bootID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("installation lost: count=%d err=%v", count, err)
		}
	})
	t.Run("library reference rolls back common profile deletion", func(t *testing.T) {
		profile, bootID := create(t, "WithXML")
		answer, err := store.CreateWindowsAnswer(ctx, WindowsAnswerVariant{ProfileID: profile.ID, Title: "XML", SHA256: strings.Repeat("a", 64), Size: 10})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.DeleteWindowsProfile(ctx, profile.ID); err == nil {
			t.Fatal("deleted a profile referenced by a library answer")
		}
		assertRows(t, profile.ID, bootID, 1)
		if _, err := store.WindowsAnswer(ctx, answer.ID); err != nil {
			t.Fatalf("library answer lost: %v", err)
		}
	})
}
