package control

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"omb/internal/agent"
)

func TestStrelecMigrationPreservesInstallerSourcesAndStoresServiceKind(t *testing.T) {
	store := installationTestStore(t)
	ctx := context.Background()
	original, err := store.CreateWindowsProfile(ctx, WindowsProfileSource{StorageID: 7, Path: "Windows.iso", StorageRoot: "/iso", Size: 100, Modified: time.Now(), Title: "Windows"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `ALTER TABLE windows_profiles DROP COLUMN boot_mode; ALTER TABLE boot_profiles DROP CONSTRAINT boot_profiles_kind_check; ALTER TABLE boot_profiles ADD CONSTRAINT boot_profiles_kind_check CHECK (kind IN ('windows_iso','ubuntu_builtin'))`); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../deploy/sql/2026-11-03-strelec-pxe.sql")
	if err != nil {
		t.Fatal(err)
	}
	apply := func() {
		t.Helper()
		if _, err := store.Pool.Exec(ctx, string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	apply()
	saved, err := store.WindowsProfile(ctx, original.ID)
	if err != nil || saved.BootMode != "" || saved.Path != original.Path {
		t.Fatalf("installer changed: %+v %v", saved, err)
	}
	service, err := store.CreateWindowsProfile(ctx, WindowsProfileSource{BootMode: agent.ISOStrelecMode, StorageID: 7, Path: "Strelec.iso", StorageRoot: "/iso", Size: 100, Modified: time.Now(), Title: "Strelec"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetWindowsProfileResult(ctx, service.ID, "ready", "", ""); err != nil {
		t.Fatal(err)
	}
	profiles, err := store.BootProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var boot BootProfile
	for _, p := range profiles {
		if p.WindowsProfileID == service.ID {
			boot = p
		}
	}
	if boot.Kind != BootProfileWinPEISO {
		t.Fatalf("wrong boot kind: %+v", boot)
	}
	entry, err := store.CreateInstallation(ctx, Installation{BootProfileID: boot.ID, Title: "Strelec"})
	if err != nil {
		t.Fatal(err)
	}
	answer, err := store.CreateWindowsAnswer(ctx, WindowsAnswerVariant{Title: "XML", SHA256: strings.Repeat("a", 64), Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	entry.AnswerID = answer.ID
	if err := store.UpdateInstallation(ctx, entry); err == nil {
		t.Fatal("service source accepted installer XML")
	}
	apply()
	saved, err = store.WindowsProfile(ctx, service.ID)
	if err != nil || saved.BootMode != agent.ISOStrelecMode || !profileBootable(saved) {
		t.Fatalf("repeat migration changed service source: %+v %v", saved, err)
	}
}

func TestStrelecDriverSelectionPersistsAndRejectsInstallerAnswers(t *testing.T) {
	store := installationTestStore(t)
	ctx := context.Background()
	p, err := store.CreateWindowsProfile(ctx, WindowsProfileSource{BootMode: agent.ISOStrelecMode, StorageID: 7, Path: "Strelec.iso", StorageRoot: "/iso", Size: 100, Modified: time.Now(), Title: "Strelec"})
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := store.BootProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var profile BootProfile
	for _, item := range profiles {
		if item.WindowsProfileID == p.ID {
			profile = item
		}
	}
	config, err := store.CreateDriverConfig(ctx, DriverConfig{ProfileID: p.ID, Title: "Strelec VirtIO", Folder: "Tools", PackageINFs: []agent.SelectedINF{{PackageSHA256: strings.Repeat("a", 64), INFPath: "netkvm.inf"}}})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := store.CreateInstallation(ctx, Installation{BootProfileID: profile.ID, Title: "Strelec", Folder: "Tools", DriverConfigID: config.ID})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Installations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range saved {
		if item.ID == entry.ID {
			found = item.DriverConfigID == config.ID && item.AnswerID == 0
		}
	}
	if !found {
		t.Fatal("service driver binding not persisted")
	}
	entry.DriverConfigID = 0
	if err := store.UpdateInstallation(ctx, entry); err != nil {
		t.Fatal(err)
	}
	saved, err = store.Installations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range saved {
		if item.ID == entry.ID && item.DriverConfigID != 0 {
			t.Fatal("driver removal not persisted")
		}
	}
	answer, err := store.CreateWindowsAnswer(ctx, WindowsAnswerVariant{Title: "XML", SHA256: strings.Repeat("b", 64), Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	config.AnswerID = answer.ID
	config.ID = 0
	bad, err := store.CreateDriverConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	entry.DriverConfigID = bad.ID
	if err := store.UpdateInstallation(ctx, entry); err == nil {
		t.Fatal("service driver config accepted installer answer")
	}
}
