package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
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
	want := []UbuntuAutoinstallSummary{{ID: saved.ID, Title: saved.Title, SourceKind: "editor", SHA256: saved.SHA256, State: "validated", DiskMode: "serial", DiskSerial: "DISK123", CreatedAt: saved.CreatedAt}}
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
		`UPDATE ubuntu_autoinstall_files SET user_data='changed' WHERE id=$1`,
		`UPDATE ubuntu_autoinstall_files SET sha256=repeat('b',64) WHERE id=$1`,
		`UPDATE ubuntu_autoinstall_files SET "values"='{}' WHERE id=$1`,
		`UPDATE ubuntu_autoinstall_files SET disk_mode='interactive', disk_serial='' WHERE id=$1`,
	} {
		if _, err := store.Pool.Exec(ctx, query, saved.ID); err == nil {
			t.Fatalf("mutable library: %s", query)
		}
	}
	if err := store.SetUbuntuAutoinstallTitle(ctx, saved.ID, "Новое имя Ubuntu"); err != nil {
		t.Fatalf("rename title: %v", err)
	}
	renamed, err := store.UbuntuAutoinstall(ctx, saved.ID)
	if err != nil || renamed.Title != "Новое имя Ubuntu" || renamed.SHA256 != saved.SHA256 {
		t.Fatalf("after rename=%+v err=%v", renamed, err)
	}
	if err := store.SetUbuntuAutoinstallTitle(ctx, saved.ID, saved.Title); err != nil {
		t.Fatalf("restore title: %v", err)
	}
	if err := store.SetUbuntuAutoinstallTitle(ctx, 999999, "Missing"); err == nil {
		t.Fatal("expected error for missing ubuntu autoinstall id")
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
