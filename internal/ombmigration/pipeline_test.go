package ombmigration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type memoryDB struct {
	data   Snapshot
	writes int
}

func (d *memoryDB) Snapshot(_ context.Context, _ bool) (Snapshot, error) {
	return cloneSnapshot(d.data), nil
}
func (d *memoryDB) Commit(_ context.Context, before, after Snapshot) error {
	if snapshotHash(d.data) != snapshotHash(before) {
		return os.ErrInvalid
	}
	d.data = cloneSnapshot(after)
	d.writes++
	return nil
}
func fixture(t *testing.T) (*Engine, *memoryDB, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	script := "#!ipxe\n# pxe-platform:folders-v1 e30\n# pxe-platform:look pult-4\nitem a Keep pxe-platform text\n"
	snapshot := Snapshot{"pxe_boot": {"boot_events": {}}, "pxe_images": {"storages": {{"id": int64(1), "name": "Local", "kind": "local", "root": "/var/lib/pxe-platform-images/iso", "state": "ready"}}}, "pxe_control": {"menu_versions": {{"id": int64(44), "script": script, "sha256": hashBytes([]byte(script)), "active": true, "author_id": int64(7), "created_at": "original-time"}}, "menu_draft_items": {{"position": int64(0), "title": "Different draft"}}, "windows_profiles": {}, "windows_answer_variants": {}, "winpe_driver_configs": {}, "ubuntu_autoinstall_files": {}, "opaque_auth": {{"ciphertext": "preserve-private-value"}}}}
	m := Manifest{SchemaVersion: 1, SourceHost: "fixture", TableCounts: map[string]map[string]int64{}, Storages: []Object{{"id": int64(1), "name": "Local", "kind": "local", "root": "/var/lib/pxe-platform-images/iso", "state": "ready"}}, Menus: []Object{{"id": int64(44), "sha256": hashBytes([]byte(script)), "bytes": int64(len(script)), "active": true}}, MenuIntegrity: []Object{{"id": int64(44), "stored_sha256": hashBytes([]byte(script)), "actual_sha256": hashBytes([]byte(script))}}, Boot: Object{"path": "/var/lib/pxe-platform-boot/menu.ipxe", "sha256": hashBytes([]byte(script))}, Digest: strings.Repeat("b", 64)}
	for db, tables := range snapshot {
		m.TableCounts[db] = map[string]int64{}
		for name, rows := range tables {
			m.TableCounts[db][name] = int64(len(rows))
		}
	}
	paths := map[string]string{"/var/lib/omb-boot/menu.ipxe": script, "/var/lib/omb-control/integrations.key": "secret unchanged", "/etc/omb/storage-recovery.json": `{"enabled":false}`, "/var/lib/omb-agent/network.json": `{"tftp_root":"/srv/pxe/tftp","custom":"pxe-platform"}`}
	for p, b := range paths {
		full := filepath.Join(root, p)
		os.MkdirAll(filepath.Dir(full), 0700)
		if e := os.WriteFile(full, []byte(b), 0600); e != nil {
			t.Fatal(e)
		}
	}
	os.MkdirAll(filepath.Join(root, "srv/omb/tftp"), 0755)
	backup := filepath.Join(root, "backup")
	os.MkdirAll(backup, 0700)
	manifestPath := filepath.Join(root, "manifest.json")
	os.WriteFile(manifestPath, []byte("fixture"), 0600)
	state, _ := json.Marshal(map[string]any{"backup_dir": backup, "backup_complete": true, "backup_verified": true, "system_complete": true, "manifest_sha256": m.Digest, "other": "preserve"})
	os.WriteFile(manifestPath+".state.json", state, 0600)
	d := &memoryDB{data: snapshot}
	engine := &Engine{Root: root, Manifest: m, ManifestPath: manifestPath, DB: d, ServicesStopped: func(context.Context) error { return nil }, Rebuilder: NoWindowsFixture{}}
	return engine, d, root
}

type NoWindowsFixture struct{}

func (NoWindowsFixture) Rebuild(_ context.Context, in BuildInput) (BuildResult, error) {
	return BuildResult{Answers: map[int64]AnswerChange{}, Drivers: map[int64]DriverChange{}}, nil
}
func TestPipelinePlansAppliesVerifiesAndRerunsWithoutWrites(t *testing.T) {
	e, d, root := fixture(t)
	ctx := context.Background()
	plan, err := e.Plan(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Ready {
		t.Fatal(plan)
	}
	if _, err = e.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Verify(ctx); err != nil {
		t.Fatal(err)
	}
	script := text(d.data.rows("pxe_control", "menu_versions")[0], "script")
	if !strings.Contains(script, "# omb:folders-v1") || !strings.Contains(script, "Keep pxe-platform text") {
		t.Fatal(script)
	}
	if text(d.data.rows("pxe_control", "opaque_auth")[0], "ciphertext") != "preserve-private-value" || text(d.data.rows("pxe_control", "menu_draft_items")[0], "title") != "Different draft" {
		t.Fatal("preservation failed")
	}
	boot, _ := os.ReadFile(filepath.Join(root, "var/lib/omb-boot/menu.ipxe"))
	if string(boot) != script {
		t.Fatal("Boot differs")
	}
	key, _ := os.ReadFile(filepath.Join(root, "var/lib/omb-control/integrations.key"))
	if string(key) != "secret unchanged" {
		t.Fatal("integration key changed")
	}
	before := d.writes
	journalInfo, _ := os.Stat(e.journalPath())
	if _, err = e.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	afterInfo, _ := os.Stat(e.journalPath())
	if d.writes != before || !journalInfo.ModTime().Equal(afterInfo.ModTime()) {
		t.Fatal("completed rerun wrote state")
	}
}
func TestPipelineRejectsDriftAndMissingBackupBeforeMutation(t *testing.T) {
	for _, reason := range []string{"count", "menu", "backup"} {
		t.Run(reason, func(t *testing.T) {
			e, d, _ := fixture(t)
			switch reason {
			case "count":
				d.data["pxe_control"]["opaque_auth"] = append(d.data["pxe_control"]["opaque_auth"], Object{"ciphertext": "new"})
			case "menu":
				d.data.rows("pxe_control", "menu_versions")[0]["script"] = "changed"
			case "backup":
				os.Remove(e.ManifestPath + ".state.json")
			}
			if _, err := e.Apply(context.Background()); err == nil {
				t.Fatal("accepted unsafe source")
			}
			if d.writes != 0 {
				t.Fatal("mutated DB")
			}
			if _, err := os.Stat(e.journalPath()); !os.IsNotExist(err) {
				t.Fatal("created journal before preflight")
			}
		})
	}
}

type failsOnceBuilder struct{ attempt int }

func (b *failsOnceBuilder) Rebuild(_ context.Context, in BuildInput) (BuildResult, error) {
	b.attempt++
	if b.attempt == 1 {
		os.WriteFile(filepath.Join(in.Stage, "incomplete"), []byte("partial"), 0600)
		return BuildResult{}, os.ErrInvalid
	}
	return (NoWindowsFixture{}).Rebuild(context.Background(), in)
}
func TestInterruptedStagingResumesWithoutTouchingCanonicalData(t *testing.T) {
	e, d, root := fixture(t)
	builder := &failsOnceBuilder{}
	e.Rebuilder = builder
	before := snapshotHash(d.data)
	if _, err := e.Apply(context.Background()); err == nil {
		t.Fatal("accepted failed build")
	}
	if d.writes != 0 || snapshotHash(d.data) != before {
		t.Fatal("failed staging changed canonical DB")
	}
	boot, _ := os.ReadFile(filepath.Join(root, "var/lib/omb-boot/menu.ipxe"))
	if !strings.Contains(string(boot), "# pxe-platform:") {
		t.Fatal("failed staging changed Boot")
	}
	if _, err := e.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if builder.attempt != 2 {
		t.Fatal("did not resume staging")
	}
}
func TestNoCloudRejectsUnpairedUserDataBeforeMutation(t *testing.T) {
	root := t.TempDir()
	data := []byte("#cloud-config\npassword: unchanged\n")
	digest := hashBytes(data)
	p := filepath.Join(root, "assets/ubuntu-autoinstall/7", digest)
	os.MkdirAll(p, 0755)
	os.WriteFile(filepath.Join(p, "user-data"), data, 0600)
	if err := validateNoCloud(root, "/assets", Snapshot{}); err == nil {
		t.Fatal("accepted missing meta-data")
	}
	os.WriteFile(filepath.Join(p, "meta-data"), []byte("instance-id: pxe-installation-7-"+digest+"\n"), 0600)
	if err := validateNoCloud(root, "/assets", Snapshot{}); err != nil {
		t.Fatal(err)
	}
	if err := convertNoCloudStage(filepath.Join(root, "assets/ubuntu-autoinstall")); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(p, "user-data"))
	if !bytes.Equal(got, data) {
		t.Fatal("changed user-data")
	}
	meta, _ := os.ReadFile(filepath.Join(p, "meta-data"))
	if string(meta) != "instance-id: omb-installation-7-"+digest+"\n" {
		t.Fatal(string(meta))
	}
}
func TestHistoricalUnavailableReferenceBlocksBeforeMutation(t *testing.T) {
	e, d, _ := fixture(t)
	encoded := base64.RawURLEncoding.EncodeToString([]byte(`[{"id":99,"profile_id":22,"sha256":"` + strings.Repeat("a", 64) + `"}]`))
	script := "#!ipxe\n# pxe-platform:folders-v1 e30\n# pxe-platform:winanswers-v1 " + encoded + "\n"
	d.data["pxe_control"]["menu_versions"] = append(d.data["pxe_control"]["menu_versions"], Object{"id": int64(43), "script": script, "sha256": hashBytes([]byte(script)), "active": false})
	e.Manifest.Menus = append(e.Manifest.Menus, Object{"id": int64(43), "sha256": hashBytes([]byte(script)), "bytes": int64(len(script)), "active": false})
	e.Manifest.TableCounts["pxe_control"]["menu_versions"] = 2
	if _, err := e.Apply(context.Background()); err == nil || !strings.Contains(err.Error(), "menu 43") {
		t.Fatal("unsupported history was not explicitly blocked", err)
	}
	if d.writes != 0 {
		t.Fatal("mutated database before resolving unavailable history")
	}
	if _, err := os.Stat(e.journalPath()); !os.IsNotExist(err) {
		t.Fatal("created journal before history preflight")
	}
}
func TestCompletedApplyRepairsMissingSharedCompletionFlag(t *testing.T) {
	e, _, _ := fixture(t)
	if _, err := e.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := MergeState(e.ManifestPath+".state.json", map[string]any{"data_complete": false}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err := e.state()
	if err != nil || state["data_complete"] != true {
		t.Fatal("completion flag not repaired", err)
	}
}
func TestDatabaseCommitRejectsOpaqueMutationBeforeAnyTransaction(t *testing.T) {
	_, d, _ := fixture(t)
	after := cloneSnapshot(d.data)
	after.rows("pxe_images", "storages")[0]["root"] = "/var/lib/omb-images/iso"
	after.rows("pxe_control", "opaque_auth")[0]["ciphertext"] = "changed"
	calls := 0
	p := PSQL{Runner: RunFunc(func(context.Context, string, []string, []byte) ([]byte, error) { calls++; return nil, nil })}
	if err := p.Commit(context.Background(), d.data, after); err == nil {
		t.Fatal("accepted opaque auth mutation")
	}
	if calls != 0 {
		t.Fatal("started transaction before validating all changes")
	}
}

func TestDataApplyRejectsSystemStateBoundToDifferentManifest(t *testing.T) {
	e, d, _ := fixture(t)
	if err := MergeState(e.ManifestPath+".state.json", map[string]any{"manifest_sha256": strings.Repeat("c", 64)}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(context.Background()); err == nil {
		t.Fatal("accepted system backup state for another manifest")
	}
	if d.writes != 0 {
		t.Fatal("mutated database before verifying manifest binding")
	}
	if _, err := os.Stat(e.journalPath()); !os.IsNotExist(err) {
		t.Fatal("created migration journal before validating backup binding")
	}
}
