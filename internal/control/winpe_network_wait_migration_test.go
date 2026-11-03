package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func winpeMigrationSQL(t *testing.T, drivers, menus []map[string]any, reverse bool) string {
	t.Helper()
	path, err := filepath.Abs("../../scripts/update-winpe-network-wait.py")
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]any{"drivers": drivers, "menus": menus, "reverse": reverse})
	if err != nil {
		t.Fatal(err)
	}
	code := `import importlib.util,json,sys
s=importlib.util.spec_from_file_location("winpe_update",sys.argv[1]);m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
v=json.load(sys.stdin);sys.stdout.write(m.build_sql(v["drivers"],v["menus"],v["reverse"]))`
	command := exec.Command("python3", "-I", "-c", code, path)
	command.Stdin = bytes.NewReader(input)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generate migration SQL: %v\n%s", err, out)
	}
	return string(out)
}

func TestWinPENetworkWaitMigrationPreservesLibraryAndMenuHistory(t *testing.T) {
	store := installationTestStore(t)
	ctx := context.Background()
	hash := func(data string) string { sum := sha256.Sum256([]byte(data)); return hex.EncodeToString(sum[:]) }
	oldHash, newHash := hash("old startup"), hash("new startup")
	var profileID, configID, menuID int64
	if err := store.Pool.QueryRow(ctx, `INSERT INTO windows_profiles(storage_id,path,storage_root,size_bytes,modified_at,title,state) VALUES(1,'win.iso','/iso',100,now(),'Keep ISO','ready') RETURNING id`).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `INSERT INTO winpe_driver_configs(profile_id,title,selected_infs,state,wim_key,wim_sha256,script_sha256) VALUES($1,'Keep driver config','[{}]','ready',repeat('a',64),repeat('b',64),$2) RETURNING id`, profileID, oldHash).Scan(&configID); err != nil {
		t.Fatal(err)
	}
	menu := func(digest string) string {
		metadata := fmt.Sprintf(`[{"id":%d,"profile_id":%d,"answer_id":0,"answer_sha256":"","wim_key":"%s","wim_sha256":"%s","script_sha256":"%s"}]`, configID, profileID, string(bytes.Repeat([]byte("a"), 64)), string(bytes.Repeat([]byte("b"), 64)), digest)
		return "#!ipxe\n# omb:windrivers-v1 " + base64.RawURLEncoding.EncodeToString([]byte(metadata)) + "\nmenu Keep 'quotes' and identity\nitem install_1 Keep item\n"
	}
	oldMenu, newMenu := menu(oldHash), menu(newHash)
	if err := store.Pool.QueryRow(ctx, `INSERT INTO menu_versions(title,diagnostic_label,diagnostic_message,script,sha256,active) VALUES('Keep version','Keep label','Keep message',$1,$2,true) RETURNING id`, oldMenu, hash(oldMenu)).Scan(&menuID); err != nil {
		t.Fatal(err)
	}
	var created time.Time
	if err := store.Pool.QueryRow(ctx, `SELECT created_at FROM menu_versions WHERE id=$1`, menuID).Scan(&created); err != nil {
		t.Fatal(err)
	}
	drivers := []map[string]any{{"id": configID, "old": oldHash, "new": newHash}}
	menus := []map[string]any{{"id": menuID, "old_script": oldMenu, "new_script": newMenu, "old_sha": hash(oldMenu), "new_sha": hash(newMenu)}}
	if _, err := store.Pool.Exec(ctx, winpeMigrationSQL(t, drivers, menus, false)); err != nil {
		t.Fatal(err)
	}
	var gotHash, wimKey, wimHash, title, state string
	if err := store.Pool.QueryRow(ctx, `SELECT script_sha256,wim_key,wim_sha256,title,state FROM winpe_driver_configs WHERE id=$1`, configID).Scan(&gotHash, &wimKey, &wimHash, &title, &state); err != nil {
		t.Fatal(err)
	}
	if gotHash != newHash || wimKey != string(bytes.Repeat([]byte("a"), 64)) || wimHash != string(bytes.Repeat([]byte("b"), 64)) || title != "Keep driver config" || state != "ready" {
		t.Fatalf("driver metadata changed unexpectedly: %s %s %s %s %s", gotHash, wimKey, wimHash, title, state)
	}
	var script, menuHash, menuTitle string
	var active bool
	var after time.Time
	if err := store.Pool.QueryRow(ctx, `SELECT script,sha256,title,active,created_at FROM menu_versions WHERE id=$1`, menuID).Scan(&script, &menuHash, &menuTitle, &active, &after); err != nil {
		t.Fatal(err)
	}
	if script != newMenu || menuHash != hash(newMenu) || menuTitle != "Keep version" || !active || !after.Equal(created) {
		t.Fatal("saved menu identity/history changed")
	}
	// A stale forward plan must not overwrite a later state.
	if _, err := store.Pool.Exec(ctx, winpeMigrationSQL(t, drivers, menus, false)); err == nil {
		t.Fatal("stale migration accepted")
	}
	if _, err := store.Pool.Exec(ctx, winpeMigrationSQL(t, drivers, menus, true)); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT script_sha256 FROM winpe_driver_configs WHERE id=$1`, configID).Scan(&gotHash); err != nil || gotHash != oldHash {
		t.Fatalf("driver rollback=%s %v", gotHash, err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT script,sha256 FROM menu_versions WHERE id=$1`, menuID).Scan(&script, &menuHash); err != nil || script != oldMenu || menuHash != hash(oldMenu) {
		t.Fatal("saved menu rollback failed")
	}
	// Recovery is idempotent when a failure happened before the database commit.
	if _, err := store.Pool.Exec(ctx, winpeMigrationSQL(t, drivers, menus, true)); err != nil {
		t.Fatal(err)
	}
}
