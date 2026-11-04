package control

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"omb/internal/autoinstall"
)

// A legacy menu must never be rebound to an unverified or guessed Ubuntu ISO.
func TestLegacyUbuntuMigrationRejectsMissingVerification(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	if err := store.EnsureBootProfiles(ctx); err != nil {
		t.Fatal(err)
	}
	var versionID int64
	const script = "#!ipxe\nchain http://boot/images/ubuntu-desktop.iso\n"
	if err := store.Pool.QueryRow(ctx, `INSERT INTO menu_versions(title,diagnostic_label,diagnostic_message,script,sha256,active) VALUES('Active','Diagnostics','Ready',$1,'original',true) RETURNING id`, script).Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO menu_versions(title,diagnostic_label,diagnostic_message,script,sha256,active) VALUES('History','Diagnostics','Ready',$1,'historic',false)`, script); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../deploy/sql/2026-11-04-multi-ubuntu.sql")
	if err != nil {
		t.Fatalf("migration is not available: %v", err)
	}
	if _, err := store.Pool.Exec(ctx, string(migration)); err == nil {
		t.Fatal("migration accepted an unverified legacy source")
	}
	var gotScript, gotHash string
	var active bool
	if err := store.Pool.QueryRow(ctx, `SELECT script,sha256,active FROM menu_versions WHERE id=$1`, versionID).Scan(&gotScript, &gotHash, &active); err != nil {
		t.Fatal(err)
	}
	if !active || gotScript != script || gotHash != "original" {
		t.Fatal("failed migration changed the published menu")
	}
}

func TestBootProfilesExposeIndividualUbuntuSourceIdentity(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	var profileID, sourceID int64
	if err := store.Pool.QueryRow(ctx, `INSERT INTO boot_profiles(kind,title) VALUES('ubuntu_iso','Ubuntu') RETURNING id`).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `INSERT INTO ubuntu_sources(boot_profile_id,storage_id,path,sha256,state) VALUES($1,7,'ubuntu.iso',$2,'ready') RETURNING id`, profileID, strings.Repeat("a", 64)).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	profiles, err := store.BootProfiles(ctx)
	if err != nil || len(profiles) != 1 {
		t.Fatalf("profiles=%v err=%v", profiles, err)
	}
	value := reflect.ValueOf(profiles[0])
	for name, want := range map[string]any{"UbuntuSourceID": sourceID, "UbuntuStorageID": int64(7), "UbuntuPath": "ubuntu.iso", "UbuntuSHA256": strings.Repeat("a", 64), "UbuntuState": "ready"} {
		field := value.FieldByName(name)
		if !field.IsValid() || field.Interface() != want {
			t.Fatalf("profile missing source field %s with value %v", name, want)
		}
	}
}

func TestLegacyUbuntuMigrationPreservesMenuWithVerifiedSource(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	if err := store.EnsureBootProfiles(ctx); err != nil {
		t.Fatal(err)
	}
	var profileID, installationID, versionID int64
	if err := store.Pool.QueryRow(ctx, `SELECT id FROM boot_profiles WHERE kind='ubuntu_builtin'`).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `INSERT INTO installations(boot_profile_id,title) VALUES($1,'Ubuntu') RETURNING id`, profileID).Scan(&installationID); err != nil {
		t.Fatal(err)
	}
	var customID int64
	if err := store.Pool.QueryRow(ctx, `INSERT INTO installations(boot_profile_id,title) VALUES($1,'Recovery') RETURNING id`, profileID).Scan(&customID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `ALTER TABLE menu_draft_items DROP COLUMN title_override`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO menu_draft_items(position,installation_id,title) VALUES(0,$1,'Ubuntu'),(1,$2,'Custom recovery')`, installationID, customID); err != nil {
		t.Fatal(err)
	}
	const script = "#!ipxe\nchain http://boot/images/ubuntu-desktop.iso\n"
	if err := store.Pool.QueryRow(ctx, `INSERT INTO menu_versions(title,diagnostic_label,diagnostic_message,script,sha256,active) VALUES('Active','Diagnostics','Ready',$1,'original',true) RETURNING id`, script).Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO menu_versions(title,diagnostic_label,diagnostic_message,script,sha256,active) VALUES('History','Diagnostics','Ready',$1,'historic',false)`, script); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../deploy/sql/2026-11-04-multi-ubuntu.sql")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := store.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	for key, value := range map[string]string{"omb.ubuntu_storage_id": "7", "omb.ubuntu_path": "ubuntu.iso", "omb.ubuntu_sha256": strings.Repeat("a", 64)} {
		if _, err := conn.Exec(ctx, `SELECT set_config($1,$2,false)`, key, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conn.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	var inherited *string
	var custom string
	if err := conn.QueryRow(ctx, `SELECT title_override FROM menu_draft_items WHERE installation_id=$1`, installationID).Scan(&inherited); err != nil || inherited != nil {
		t.Fatalf("matching title should inherit: %v %v", inherited, err)
	}
	if err := conn.QueryRow(ctx, `SELECT title_override FROM menu_draft_items WHERE installation_id=$1`, customID).Scan(&custom); err != nil || custom != "Custom recovery" {
		t.Fatalf("custom title was not preserved: %q %v", custom, err)
	}
	var gotProfile, storageID int64
	var path, digest, gotScript string
	if err := conn.QueryRow(ctx, `SELECT boot_profile_id,storage_id,path,sha256 FROM ubuntu_sources`).Scan(&gotProfile, &storageID, &path, &digest); err != nil {
		t.Fatal(err)
	}
	if gotProfile != profileID || storageID != 7 || path != "ubuntu.iso" || digest != strings.Repeat("a", 64) {
		t.Fatal("migration did not preserve the verified source identity")
	}
	if err := conn.QueryRow(ctx, `SELECT script FROM menu_versions WHERE id=$1 AND active`, versionID).Scan(&gotScript); err != nil || gotScript != script {
		t.Fatalf("active menu changed: %v", err)
	}
	var provenActive, provenHistory int
	if err := conn.QueryRow(ctx, `SELECT count(*) FILTER (WHERE active AND legacy_ubuntu_source_id IS NOT NULL),count(*) FILTER (WHERE NOT active AND legacy_ubuntu_source_id IS NOT NULL) FROM menu_versions`).Scan(&provenActive, &provenHistory); err != nil || provenActive != 1 || provenHistory != 0 {
		t.Fatalf("invalid legacy source attribution: active=%d history=%d err=%v", provenActive, provenHistory, err)
	}
	var gotInstallationID int64
	if err := conn.QueryRow(ctx, `SELECT id FROM installations WHERE boot_profile_id=$1`, profileID).Scan(&gotInstallationID); err != nil || gotInstallationID != installationID {
		t.Fatalf("installation changed: %v", err)
	}
	if _, err := conn.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
}

func TestUbuntuSourceSchemaSeparatesISOIdentities(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	for i, name := range []string{"first.iso", "second.iso"} {
		var profileID int64
		if err := store.Pool.QueryRow(ctx, `INSERT INTO boot_profiles(kind,title) VALUES('ubuntu_iso',$1) RETURNING id`, name).Scan(&profileID); err != nil {
			t.Fatal(err)
		}
		digest := strings.Repeat(string(rune('a'+i)), 64)
		if _, err := store.Pool.Exec(ctx, `INSERT INTO ubuntu_sources(boot_profile_id,storage_id,path,sha256,state) VALUES($1,7,$2,$3,'ready')`, profileID, name, digest); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM ubuntu_sources`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("want two isolated sources; got %d: %v", count, err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE ubuntu_sources SET sha256=$1 WHERE path='first.iso'`, strings.Repeat("c", 64)); err == nil {
		t.Fatal("allowed an existing source identity to be reassigned")
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE ubuntu_sources SET state='error',error='offline' WHERE path='first.iso'`); err != nil {
		t.Fatalf("source health could not change: %v", err)
	}
}

func TestCreateUbuntuSourceReusesExactIdentity(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	creator, ok := any(store).(interface {
		CreateUbuntuSource(context.Context, int64, string, string, string) (BootProfile, error)
	})
	if !ok {
		t.Fatal("store cannot create Ubuntu sources")
	}
	first, err := creator.CreateUbuntuSource(ctx, 7, "linux/ubuntu.iso", strings.Repeat("a", 64), "Ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := creator.CreateUbuntuSource(ctx, 7, "linux/ubuntu.iso", strings.Repeat("a", 64), "Ubuntu")
	if err != nil || first.ID != repeated.ID || first.UbuntuSourceID != repeated.UbuntuSourceID {
		t.Fatalf("same ISO not reused: first=%+v repeated=%+v err=%v", first, repeated, err)
	}
	other, err := creator.CreateUbuntuSource(ctx, 7, "linux/ubuntu.iso", strings.Repeat("b", 64), "Ubuntu")
	if err != nil || other.ID == first.ID || other.UbuntuSourceID == first.UbuntuSourceID {
		t.Fatalf("changed ISO reused old identity: %+v err=%v", other, err)
	}
	for _, bad := range []string{"../escape.iso", "/outside.iso", "linux/../escape.iso", ""} {
		if _, err := creator.CreateUbuntuSource(ctx, 7, bad, strings.Repeat("a", 64), "Ubuntu"); err == nil {
			t.Fatalf("accepted unsafe path %q", bad)
		}
	}
}

func TestCreateUbuntuSourceConcurrentRequestsReuseIdentity(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	creator, ok := any(store).(interface {
		CreateUbuntuSource(context.Context, int64, string, string, string) (BootProfile, error)
	})
	if !ok {
		t.Fatal("store cannot create Ubuntu sources")
	}
	type result struct {
		profile BootProfile
		err     error
	}
	results := make(chan result, 8)
	for i := 0; i < cap(results); i++ {
		go func() {
			profile, err := creator.CreateUbuntuSource(ctx, 7, "linux/ubuntu.iso", strings.Repeat("a", 64), "Ubuntu")
			results <- result{profile, err}
		}()
	}
	var firstID int64
	for i := 0; i < cap(results); i++ {
		got := <-results
		if got.err != nil {
			t.Fatal(got.err)
		}
		if firstID == 0 {
			firstID = got.profile.UbuntuSourceID
		} else if got.profile.UbuntuSourceID != firstID {
			t.Fatalf("duplicate source IDs: %d and %d", firstID, got.profile.UbuntuSourceID)
		}
	}
	var count int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM ubuntu_sources`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

func TestUbuntuSourceStateTracksValidationAndPreparation(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	creator, ok := any(store).(interface {
		CreateUbuntuSource(context.Context, int64, string, string, string) (BootProfile, error)
		SetUbuntuSourceState(context.Context, int64, string, string) error
	})
	if !ok {
		t.Fatal("store cannot record Ubuntu source state")
	}
	profile, err := creator.CreateUbuntuSource(ctx, 6, "ubuntu.iso", strings.Repeat("a", 64), "Ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"verified", "preparing", "error", "ready"} {
		if err := creator.SetUbuntuSourceState(ctx, profile.UbuntuSourceID, state, ""); err != nil {
			t.Fatalf("state %s: %v", state, err)
		}
	}
	if err := creator.SetUbuntuSourceState(ctx, profile.UbuntuSourceID, "invented", ""); err == nil {
		t.Fatal("accepted unsupported state")
	}
}

func TestLegacyUbuntuMenuWithoutProvenSourceCannotBeRestored(t *testing.T) {
	version := MenuVersion{
		ID: 45, Title: productMenuTitle, Label: "Diagnostics", Message: "Ready",
		Script: "#!ipxe\nchain http://boot/images/ubuntu-desktop.iso\n",
		Items:  []MenuVersionItem{},
	}
	if menuVersionRestorable(version, InstallationMenuSources{}, nil, nil, nil) {
		t.Fatal("legacy menu with no verified ISO identity was accepted for restore")
	}
	version.LegacyUbuntuSourceID = 17
	if menuVersionRestorable(version, InstallationMenuSources{}, nil, nil, nil) {
		t.Fatal("legacy menu with missing verified source was accepted for restore")
	}
}

type mismatchedLegacySourceStub struct{}

func (mismatchedLegacySourceStub) UbuntuSource(context.Context) (autoinstall.SourceCatalog, error) {
	return autoinstall.SourceCatalog{}, nil
}

func (mismatchedLegacySourceStub) UbuntuLegacySource(context.Context) (int64, string, error) {
	return 18, strings.Repeat("b", 64), nil
}

func TestLegacyUbuntuRestoreRejectsDifferentBootAlias(t *testing.T) {
	s, store, _ := unifiedMenuFixture(t)
	store.bootProfiles[0].UbuntuSourceID = 17
	store.bootProfiles[0].UbuntuSHA256 = strings.Repeat("a", 64)
	store.bootProfiles[0].UbuntuState = "ready"
	s.UbuntuSource = mismatchedLegacySourceStub{}
	version := MenuVersion{ID: 45, Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", LegacyUbuntuSourceID: 17, Script: "#!ipxe\nchain http://boot/images/ubuntu-desktop.iso\n", Items: []MenuVersionItem{}}
	if err := s.verifyMenuVersion(t.Context(), version); err == nil {
		t.Fatal("restored legacy menu with a different Boot alias")
	}
}

func TestPublishedLegacyMenuRetainsVerifiedSourceIdentity(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	version := MenuVersion{Title: "PXE", Label: "Diagnostics", Message: "Ready", Script: "#!ipxe\nchain http://boot/images/ubuntu-desktop.iso\n", SHA256: "saved", LegacyUbuntuSourceID: 17, Items: []MenuVersionItem{}}
	if err := store.SaveActiveMenu(ctx, 0, version); err != nil {
		t.Fatal(err)
	}
	versions, err := store.MenuVersions(ctx)
	if err != nil || len(versions) != 1 || versions[0].LegacyUbuntuSourceID != 17 {
		t.Fatalf("legacy source not retained: versions=%+v err=%v", versions, err)
	}
}

func TestMenuExplainsUnverifiedLegacyUbuntuSource(t *testing.T) {
	store := newDraftStore(t, "operator", nil)
	store.versions = []MenuVersion{{ID: 45, Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", Script: "#!ipxe\nchain http://boot/images/ubuntu-desktop.iso\n", Items: []MenuVersionItem{}}}
	s, _ := draftServer(t, store)
	body := libraryGet(t, s, "/menu")
	if !strings.Contains(body, "Источник Ubuntu не подтверждён") {
		t.Fatal("menu history does not explain the unverified legacy source")
	}
}

func TestMenuUsesIndependentUbuntuISOIdentities(t *testing.T) {
	digestA, digestB := strings.Repeat("a", 64), strings.Repeat("b", 64)
	sources := InstallationMenuSources{
		Installations: []Installation{{ID: 21, BootProfileID: 1, Title: "Ubuntu A"}, {ID: 22, BootProfileID: 2, Title: "Ubuntu B"}},
		BootProfiles: []BootProfile{
			{ID: 1, Kind: BootProfileUbuntuISO, UbuntuSourceID: 11, UbuntuStorageID: 6, UbuntuPath: "a.iso", UbuntuSHA256: digestA, UbuntuState: "ready"},
			{ID: 2, Kind: BootProfileUbuntuISO, UbuntuSourceID: 12, UbuntuStorageID: 6, UbuntuPath: "b.iso", UbuntuSHA256: digestB, UbuntuState: "ready"},
		},
	}
	draft := MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{InstallationID: 21}, {InstallationID: 22}}}
	script, err := BuildMenuFromDraft(draft, testBootURL, nil, nil, nil, sources)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"/images/ubuntu/11/" + digestA + ".iso", "/assets/ubuntu/11/" + digestA + "/vmlinuz",
		"/images/ubuntu/12/" + digestB + ".iso", "/assets/ubuntu/12/" + digestB + "/vmlinuz",
	} {
		if !strings.Contains(string(script), want) {
			t.Fatalf("missing source-specific URL %q", want)
		}
	}
	version := MenuVersion{ID: 45, Script: string(script), Items: []MenuVersionItem{}}
	if legacyUbuntuMenuWithoutSource(version, sources) {
		t.Fatal("source-specific Ubuntu URLs were mistaken for legacy URLs")
	}
}

func TestUbuntuMenuSnapshotRejectsChangedSourceReference(t *testing.T) {
	s, store, _ := unifiedMenuFixture(t)
	s.UbuntuSource = multiUbuntuSourceStub{}
	store.bootProfiles[0] = BootProfile{ID: 1, Kind: BootProfileUbuntuISO, UbuntuSourceID: 11, UbuntuStorageID: 6, UbuntuPath: "a.iso", UbuntuSHA256: strings.Repeat("a", 64), UbuntuState: "ready"}
	draft := MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{InstallationID: 71, Title: "Ubuntu"}}}
	sources := InstallationMenuSources{Installations: store.installations, BootProfiles: store.bootProfiles}
	script, err := BuildMenuFromDraft(draft, testBootURL, store.profiles, store.answers, store.configs, sources)
	if err != nil {
		t.Fatal(err)
	}
	version := MenuVersion{ID: 20, Title: draft.Title, Label: draft.Label, Message: draft.Message, Script: string(script), Items: versionItems(draft)}
	if err := s.verifyMenuVersion(t.Context(), version); err != nil {
		t.Fatalf("valid source rejected: %v", err)
	}
	version.Script = strings.ReplaceAll(version.Script, "/images/ubuntu/11/", "/images/ubuntu/12/")
	if err := s.verifyMenuVersion(t.Context(), version); err == nil {
		t.Fatal("restored menu whose ISO address differs from its selected source")
	}
	version.Script = strings.Replace(string(script), "initrd "+testBootURL+"/assets/ubuntu/11/", "initrd http://other.invalid/unsafe\ninitrd "+testBootURL+"/assets/ubuntu/11/", 1)
	if err := s.verifyMenuVersion(t.Context(), version); err == nil {
		t.Fatal("restored menu with an additional unverified initrd")
	}
}
