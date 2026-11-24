package ombcli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omb/internal/setup"
)

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestParseSHA256SumsValidation(t *testing.T) {
	validHash := sha256Hex([]byte("deb-payload"))

	// Valid entry with optional leading '*'.
	name, digest, err := ParseSHA256Sums([]byte(validHash + "  *omb_0.2.1~dev.3_amd64.deb\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "omb_0.2.1~dev.3_amd64.deb" || digest != validHash {
		t.Fatalf("got (%q, %q), want (omb_0.2.1~dev.3_amd64.deb, %q)", name, digest, validHash)
	}

	// Path traversal rejection.
	for _, bad := range []string{
		validHash + "  ../omb_0.2.0_amd64.deb\n",
		validHash + "  sub/omb_0.2.0_amd64.deb\n",
		validHash + "  evil_0.2.0_amd64.deb\n",
		"not-a-sha256  omb_0.2.0_amd64.deb\n",
		validHash + "  README.txt\n",
	} {
		if _, _, err := ParseSHA256Sums([]byte(bad)); err == nil {
			t.Fatalf("expected error for SHA256SUMS line %q", bad)
		}
	}
}

func TestPlanMigrationsRules(t *testing.T) {
	h1 := sha256Hex([]byte("sql-1"))
	h2 := sha256Hex([]byte("sql-2"))
	h3 := sha256Hex([]byte("sql-3"))
	h4 := sha256Hex([]byte("sql-4"))

	manifest := MigrationManifest{
		Format: 1,
		Migrations: []MigrationEntry{
			{Database: "omb_control", ID: "0002_add_totp", Path: "omb_control/0002_add_totp.sql", SHA256: h4},
			{Database: "omb_control", ID: "0001_init", Path: "omb_control/0001_init.sql", SHA256: h3},
			{Database: "omb_boot", ID: "0001_init", Path: "omb_boot/0001_init.sql", SHA256: h1},
			{Database: "omb_images", ID: "0001_init", Path: "omb_images/0001_init.sql", SHA256: h2},
		},
	}

	// 1. Ordered pending across omb_boot -> omb_images -> omb_control.
	applied := []AppliedMigration{
		{Database: "omb_control", ID: "0001_init", SHA256: h3},
	}
	pending, err := PlanMigrations(manifest, applied)
	if err != nil {
		t.Fatalf("PlanMigrations failed: %v", err)
	}
	if len(pending) != 3 {
		t.Fatalf("expected 3 pending migrations, got %d: %+v", len(pending), pending)
	}
	if pending[0].Database != "omb_boot" || pending[1].Database != "omb_images" || pending[2].Database != "omb_control" || pending[2].ID != "0002_add_totp" {
		t.Fatalf("unexpected pending order: %+v", pending)
	}

	// 2. SHA-256 drift check rejection.
	driftApplied := []AppliedMigration{
		{Database: "omb_control", ID: "0001_init", SHA256: sha256Hex([]byte("tampered"))},
	}
	if _, err := PlanMigrations(manifest, driftApplied); err == nil || !strings.Contains(err.Error(), "drift") {
		t.Fatalf("expected checksum drift error, got %v", err)
	}

	// 3. Append-only violation (0002 applied while 0001 is unapplied).
	appendViolation := []AppliedMigration{
		{Database: "omb_control", ID: "0002_add_totp", SHA256: h4},
	}
	if _, err := PlanMigrations(manifest, appendViolation); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("expected append-only error, got %v", err)
	}

	// 4. Applied migration missing from new package.
	missingApplied := []AppliedMigration{
		{Database: "omb_boot", ID: "0099_removed", SHA256: h1},
	}
	if _, err := PlanMigrations(manifest, missingApplied); err == nil || !strings.Contains(err.Error(), "missing from the new package") {
		t.Fatalf("expected missing migration error, got %v", err)
	}
}

func TestUpdateSHA256MismatchRejected(t *testing.T) {
	debBytes := []byte("corrupted-deb-bytes")
	wrongSHA := sha256Hex([]byte("expected-different-bytes"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/f0nwa/OMB/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v0.2.1"})
		case "/f0nwa/OMB/releases/download/v0.2.1/SHA256SUMS":
			fmt.Fprintf(w, "%s  omb_0.2.1_amd64.deb\n", wrongSHA)
		case "/f0nwa/OMB/releases/download/v0.2.1/omb_0.2.1_amd64.deb":
			_, _ = w.Write(debBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	app := &App{
		RootDir:         t.TempDir(),
		APIBaseURL:      srv.URL,
		DownloadBaseURL: srv.URL,
		HTTPClient:      srv.Client(),
		Lang:            "en",
		Out:             &bytes.Buffer{},
		ErrOut:          &bytes.Buffer{},
		Euid:            func() int { return 0 },
		Runner:          &mockRunner{},
	}

	err := app.Update(context.Background(), UpdateOptions{Channel: "stable", Yes: true})
	if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("expected SHA-256 mismatch error, got %v", err)
	}
}

func TestUpdateAlreadyUpToDate(t *testing.T) {
	debBytes := []byte("fake-deb")
	debSHA := sha256Hex(debBytes)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/f0nwa/OMB/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v0.2.0"})
		case "/f0nwa/OMB/releases/download/v0.2.0/SHA256SUMS":
			fmt.Fprintf(w, "%s  omb_0.2.0_amd64.deb\n", debSHA)
		default:
			t.Fatalf("unexpected HTTP request when already up to date: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	root := t.TempDir()
	stateDir := filepath.Join(root, "var", "lib", "omb-deployment")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "CHANNEL"), []byte("stable\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	runner := &mockRunner{
		fn: func(_ context.Context, spec setup.CmdSpec) ([]byte, error) {
			if spec.Name == "dpkg-query" {
				return []byte("installed\t0.2.0\tabcdef1234567890abcdef1234567890abcdef12\n"), nil
			}
			return nil, nil
		},
	}

	var out bytes.Buffer
	app := &App{
		RootDir:         root,
		APIBaseURL:      srv.URL,
		DownloadBaseURL: srv.URL,
		HTTPClient:      srv.Client(),
		Lang:            "en",
		Out:             &out,
		ErrOut:          &bytes.Buffer{},
		Euid:            func() int { return 0 },
		Runner:          runner,
	}

	if err := app.Update(context.Background(), UpdateOptions{Yes: true}); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if !strings.Contains(out.String(), "already up to date") {
		t.Fatalf("expected 'already up to date' message, got:\n%s", out.String())
	}
}

func TestUpdateFullMockedExecutionDevChannelSwitch(t *testing.T) {
	debBytes := []byte("valid-dev-deb-package-bytes")
	debSHA := sha256Hex(debBytes)
	newCommit := "1111111122222222333333334444444455555555"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/f0nwa/OMB/releases":
			_ = json.NewEncoder(w).Encode([]map[string]string{
				{"tag_name": "v0.2.0"},
				{"tag_name": "v0.2.1-dev.3"},
			})
		case "/f0nwa/OMB/releases/download/v0.2.1-dev.3/SHA256SUMS":
			fmt.Fprintf(w, "%s  omb_0.2.1~dev.3_amd64.deb\n", debSHA)
		case "/f0nwa/OMB/releases/download/v0.2.1-dev.3/omb_0.2.1~dev.3_amd64.deb":
			_, _ = w.Write(debBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	root := t.TempDir()
	writeTestControlEnv(t, root)

	// Pre-existing state: stable channel, online mode.
	stateDir := filepath.Join(root, "var", "lib", "omb-deployment")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "CHANNEL"), []byte("stable\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "INSTALL_MODE"), []byte("online\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Prepare migrations and release.json inside root (simulating installed package files).
	migDir := filepath.Join(root, "usr", "share", "omb", "migrations")
	if err := os.MkdirAll(filepath.Join(migDir, "omb_control"), 0o755); err != nil {
		t.Fatal(err)
	}
	sql1 := []byte("CREATE TABLE t1 (id int);\n")
	sql2 := []byte("ALTER TABLE t1 ADD COLUMN note text;\n")
	if err := os.WriteFile(filepath.Join(migDir, "omb_control", "0001_init.sql"), sql1, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(migDir, "omb_control", "0002_note.sql"), sql2, 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := MigrationManifest{
		Format: 1,
		Migrations: []MigrationEntry{
			{Database: "omb_control", ID: "0001_init", Path: "omb_control/0001_init.sql", SHA256: sha256Hex(sql1)},
			{Database: "omb_control", ID: "0002_note", Path: "omb_control/0002_note.sql", SHA256: sha256Hex(sql2)},
		},
	}
	manifestRaw, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(migDir, "manifest.json"), manifestRaw, 0o644); err != nil {
		t.Fatal(err)
	}

	releasePath := filepath.Join(root, "usr", "share", "omb", "release.json")
	if err := os.WriteFile(releasePath, []byte(fmt.Sprintf(`{"channel":"dev","commit":%q,"version":"0.2.1~dev.3"}`, newCommit)), 0o644); err != nil {
		t.Fatal(err)
	}

	var sawAllowDowngrades, sawMigrationTx bool
	var healthCalled bool
	runner := &mockRunner{
		fn: func(_ context.Context, spec setup.CmdSpec) ([]byte, error) {
			joined := strings.Join(spec.Args, " ")
			switch {
			case spec.Name == "dpkg-query":
				return []byte("installed\t0.2.0\t0000000000000000000000000000000000000000\n"), nil
			case spec.Name == "runuser" && strings.Contains(joined, "pg_dump"):
				return []byte("PGDMP-mock"), nil
			case spec.Name == "apt-get":
				if strings.Contains(joined, "--allow-downgrades") {
					sawAllowDowngrades = true
				}
				return nil, nil
			case spec.Name == "runuser" && strings.Contains(joined, "SELECT id, sha256 FROM public.schema_migrations"):
				if strings.Contains(joined, "-d omb_control") {
					return []byte(fmt.Sprintf("0001_init\t%s\n", sha256Hex(sql1))), nil
				}
				return nil, nil
			case spec.Name == "runuser" && strings.Contains(joined, "mig_id=0002_note"):
				if strings.Contains(string(spec.Stdin), "BEGIN;") &&
					strings.Contains(string(spec.Stdin), "ALTER TABLE t1 ADD COLUMN note text;") &&
					strings.Contains(string(spec.Stdin), "INSERT INTO public.schema_migrations") &&
					strings.Contains(string(spec.Stdin), "COMMIT;") {
					sawMigrationTx = true
				}
				return nil, nil
			}
			return nil, nil
		},
	}

	fixedTime := time.Date(2026, 11, 24, 19, 30, 0, 0, time.UTC)
	var out bytes.Buffer
	app := &App{
		RootDir:         root,
		APIBaseURL:      srv.URL,
		DownloadBaseURL: srv.URL,
		HTTPClient:      srv.Client(),
		Lang:            "ru",
		Out:             &out,
		ErrOut:          &bytes.Buffer{},
		Euid:            func() int { return 0 },
		Now:             func() time.Time { return fixedTime },
		Runner:          runner,
		HealthCheck: func(_ context.Context, mgmtIP string) error {
			healthCalled = true
			if mgmtIP != "192.168.10.135" {
				t.Errorf("unexpected health check IP: %q", mgmtIP)
			}
			return nil
		},
	}

	// Trigger via `Channel("dev", Yes: true)` which should switch CHANNEL and run Update with AllowChannelSwitch.
	if err := app.Channel(context.Background(), ChannelOptions{Target: "dev", Yes: true}); err != nil {
		t.Fatalf("Channel(dev, Yes: true) failed: %v", err)
	}

	if !sawAllowDowngrades {
		t.Error("expected apt-get to be called with --allow-downgrades on channel switch")
	}
	if !sawMigrationTx {
		t.Error("expected pending migration 0002_note to be applied transactionally")
	}
	if !healthCalled {
		t.Error("expected post-update health check to be called")
	}

	// Verify backup dir and dumps exist.
	backupDir := filepath.Join(root, "var", "backups", "omb", "online.20261124T193000Z")
	for _, f := range []string{"omb_boot.dump", "omb_images.dump", "omb_control.dump", filepath.Join("etc-omb", "control.env")} {
		if _, err := os.Stat(filepath.Join(backupDir, f)); err != nil {
			t.Errorf("expected backup file %s: %v", f, err)
		}
	}

	// Verify state dir updated atomically.
	checkState := func(name, want string) {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(stateDir, name))
		if err != nil {
			t.Fatalf("read state %s: %v", name, err)
		}
		if strings.TrimSpace(string(b)) != want {
			t.Errorf("state %s = %q, want %q", name, strings.TrimSpace(string(b)), want)
		}
	}
	checkState("CHANNEL", "dev")
	checkState("PACKAGE", "0.2.1~dev.3")
	checkState("PACKAGE_SHA256", debSHA)
	checkState("COMMIT", newCommit)

	curDeb, err := os.ReadFile(filepath.Join(stateDir, "current.deb"))
	if err != nil {
		t.Fatalf("read current.deb: %v", err)
	}
	if !bytes.Equal(curDeb, debBytes) {
		t.Errorf("current.deb content mismatch")
	}
}
