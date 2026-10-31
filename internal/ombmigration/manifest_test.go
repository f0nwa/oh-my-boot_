package ombmigration

import (
	"context"
	"encoding/json"
	"omb/internal/agent"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManifestRejectsMissingEvidenceAndAcceptsExtraObservations(t *testing.T) {
	raw := `{"schema_version":1,"source_host":"host","collected_at":"2026-10-31T19:16:01Z","table_counts":{"pxe_boot":{"boot_events":0},"pxe_control":{"menu_versions":0,"windows_profiles":0,"windows_answer_variants":0,"winpe_driver_configs":0,"menu_draft_items":0},"pxe_images":{"storages":0}},"storages":[],"windows_profiles":[],"windows_answers":[],"driver_configs":[],"menu_versions":[],"menu_integrity":[],"menu_draft_items":[],"profile_configs":[],"recovery":{},"boot_snapshot":{"path":"/var/lib/pxe-platform-boot/menu.ipxe","sha256":"` + strings.Repeat("a", 64) + `"},"extra":true}`
	if _, e := ParseManifest([]byte(raw)); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{strings.Replace(raw, `"schema_version":1`, `"schema_version":2`, 1), strings.Replace(raw, `"windows_answers":[],`, "", 1), raw + `{}`} {
		if _, e := ParseManifest([]byte(bad)); e == nil {
			t.Fatal("accepted incomplete manifest")
		}
	}
}
func TestPSQLAdapterUsesPeerAndSQLOnStdin(t *testing.T) {
	var name, input string
	var argv []string
	p := PSQL{Runner: RunFunc(func(_ context.Context, n string, args []string, in []byte) ([]byte, error) {
		name = n
		argv = args
		input = string(in)
		return []byte(`[]`), nil
	})}
	if _, e := p.Query(context.Background(), "pxe_control", "SELECT 'private value'"); e != nil {
		t.Fatal(e)
	}
	if name != "runuser" || strings.Join(argv, " ") != "-u postgres -- psql -XAt -v ON_ERROR_STOP=1 -d pxe_control" || input != "SELECT 'private value'" {
		t.Fatalf("adapter contract violated: %s %v", name, argv)
	}
	if _, e := p.Query(context.Background(), "arbitrary", "SELECT 1"); e == nil {
		t.Fatal("accepted arbitrary DB")
	}
}
func TestAtomicPrivateStatePreservesUnrelatedSystemFields(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "manifest.state.json")
	if e := os.WriteFile(p, []byte(`{"backup_complete":true,"system_complete":true,"backup_dir":"/backup/run","external":"keep"}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e := MergeState(p, map[string]any{"data_complete": true}); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(p)
	var v map[string]any
	json.Unmarshal(b, &v)
	if v["external"] != "keep" || v["data_complete"] != true {
		t.Fatal(string(b))
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	if e := os.Chmod(p, 0644); e != nil {
		t.Fatal(e)
	}
	if e := MergeState(p, map[string]any{}); e == nil {
		t.Fatal("accepted public state")
	}
}
func TestManifestRejectsOmittedObjectObservations(t *testing.T) {
	b, err := os.ReadFile("../../.superpowers/sdd/2026-10-31-omb-technical-migration/source-inventory.json")
	if os.IsNotExist(err) {
		t.Skip("private source inventory not distributed")
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ParseManifest(b); err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	json.Unmarshal(b, &raw)
	raw["windows_profiles"] = json.RawMessage(`[]`)
	altered, _ := json.Marshal(raw)
	if _, err = ParseManifest(altered); err == nil {
		t.Fatal("accepted missing profile observations")
	}
}
func TestStateLockRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "manifest.state.json")
	os.WriteFile(p, []byte(`{}`), 0600)
	target := filepath.Join(root, "unrelated")
	os.WriteFile(target, []byte("private"), 0600)
	os.Symlink(target, p+".lock")
	if err := MergeState(p, map[string]any{"data_complete": true}); err == nil {
		t.Fatal("followed symlink lock")
	}
}

func TestProfileConfigRejectsFieldsThatWouldBeLostOnRewrite(t *testing.T) {
	var req agent.WindowsISORequest
	if err := decodeProfileConfig([]byte(`{"id":1,"storage_root":"/mnt/pxe-platform-smb/key","relative_path":"win.iso","size":1,"modified":"2026-10-31T00:00:00Z","custom":"keep"}`), &req); err == nil {
		t.Fatal("accepted profile extension that the target serializer would discard")
	}
}
