package ombmigration

import (
	"context"
	"encoding/json"
	"fmt"
	"omb/internal/agent"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Opt-in integration rehearsal: all writes go to a private filesystem copy and
// separately restored databases. This never relaxes the live CLI's stop proof.
func TestSameHostDataRehearsal(t *testing.T) {
	manifestPath := os.Getenv("OMB_REHEARSAL_MANIFEST")
	if manifestPath == "" {
		t.Skip("requires isolated same-host rehearsal inputs")
	}
	root := os.Getenv("OMB_REHEARSAL_ROOT")
	if root == "/" || !filepath.IsAbs(root) || !strings.Contains(root, "/rehearsal-") {
		t.Fatal("unsafe rehearsal root")
	}
	if filepath.Dir(root) != filepath.Dir(manifestPath) || filepath.Base(root) != "root" || filepath.Base(manifestPath) != "source.json" {
		t.Fatal("rehearsal manifest must be separate from the live migration state")
	}
	if _, err := privateRead(filepath.Join(root, ".omb-rehearsal")); err != nil {
		t.Fatal("missing private rehearsal ownership marker")
	}
	var mapping map[string]string
	if err := json.Unmarshal([]byte(os.Getenv("OMB_REHEARSAL_DATABASES")), &mapping); err != nil || len(mapping) != 3 {
		t.Fatal("invalid rehearsal DB mapping")
	}
	for _, source := range []string{"pxe_boot", "pxe_control", "pxe_images"} {
		db := mapping[source]
		if !strings.HasPrefix(db, "_omb_migration_check_rehearsal_") || !identifier(db) {
			t.Fatal("unsafe rehearsal DB name")
		}
	}
	raw, err := privateRead(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	m, err := ParseManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	runner := RunFunc(func(ctx context.Context, name string, args []string, input []byte) ([]byte, error) {
		if name != "runuser" {
			return nil, fmt.Errorf("unexpected database process")
		}
		forwarded := append([]string{}, args...)
		replaced := false
		for i := 0; i+1 < len(forwarded); i++ {
			if forwarded[i] == "-d" {
				source := strings.Replace(forwarded[i+1], "omb_", "pxe_", 1)
				target, ok := mapping[source]
				if !ok {
					return nil, fmt.Errorf("database is not an isolated copy")
				}
				forwarded[i+1] = target
				replaced = true
			}
		}
		if !replaced {
			return nil, fmt.Errorf("database not scoped")
		}
		return (ExecRunner{}).Run(ctx, name, forwarded, input)
	})
	database := PSQL{Runner: runner}
	e := &Engine{Root: root, ManifestPath: manifestPath, Manifest: m, DB: database, Rebuilder: RuntimeRebuilder{}, ServicesStopped: func(context.Context) error { return nil }}
	ctx := context.Background()
	if _, err = e.Plan(ctx, true); err != nil {
		t.Fatal(err)
	}
	report, err := e.Apply(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Complete {
		t.Fatal("rehearsal incomplete")
	}
	if _, err = e.Verify(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := database.Snapshot(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	manager := agent.WindowsISOManager{AssetRoot: filepath.Join(root, "srv/omb/tftp/windows-profiles"), AnswerRoot: filepath.Join(root, "srv/omb/tftp/windows-answers")}
	for _, row := range after.rows("pxe_control", "windows_answer_variants") {
		if text(row, "state") != "ready" {
			continue
		}
		if err = manager.CheckAnswer(ctx, agent.WindowsAnswerCheck{ID: number(row, "id"), ProfileID: number(row, "profile_id"), SHA256: text(row, "sha256")}); err != nil {
			t.Fatal("rebuilt answer", number(row, "id"), err)
		}
	}
	drivers := agent.DriverManager{Root: filepath.Join(root, "var/lib/omb-agent/drivers"), ProfileRoot: manager.AssetRoot, AnswerRoot: manager.AnswerRoot, WIMRoot: filepath.Join(root, "srv/omb/tftp/windows-driver-wims"), ConfigRoot: filepath.Join(root, "srv/omb/tftp/windows-driver-configs")}
	for _, row := range after.rows("pxe_control", "winpe_driver_configs") {
		if text(row, "state") != "ready" {
			continue
		}
		var selected []agent.SelectedINF
		if err = decodeJSON(mustJSON(row["selected_infs"]), &selected); err != nil {
			t.Fatal(err)
		}
		req := agent.DriverBuildRequest{ConfigID: number(row, "id"), ProfileID: number(row, "profile_id"), AnswerID: number(row, "answer_id"), PackageINFs: selected}
		result := agent.DriverBuildResult{WIMKey: text(row, "wim_key"), WIMSHA256: text(row, "wim_sha256"), ScriptSHA256: text(row, "script_sha256")}
		if err = drivers.CheckConfig(ctx, req, result); err != nil {
			t.Fatal("rebuilt drivers", req.ConfigID, err)
		}
	}
	if _, err = e.Apply(ctx); err != nil {
		t.Fatal("idempotent repeat", err)
	}
	t.Logf("real data rehearsal complete: %d profiles, %d answers, %d driver configs; SQL and asset checks passed", len(m.Profiles), len(m.Answers), len(m.Drivers))
}
