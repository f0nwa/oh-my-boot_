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
		case "/repos/f0nwa/oh-my-boot_/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v0.2.1"})
		case "/f0nwa/oh-my-boot_/releases/download/v0.2.1/SHA256SUMS":
			fmt.Fprintf(w, "%s  omb_0.2.1_amd64.deb\n", wrongSHA)
		case "/f0nwa/oh-my-boot_/releases/download/v0.2.1/omb_0.2.1_amd64.deb":
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
		case "/repos/f0nwa/oh-my-boot_/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v0.2.0"})
		case "/f0nwa/oh-my-boot_/releases/download/v0.2.0/SHA256SUMS":
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
		case "/repos/f0nwa/oh-my-boot_/releases":
			_ = json.NewEncoder(w).Encode([]map[string]string{
				{"tag_name": "v0.2.0"},
				{"tag_name": "v0.2.1-dev.3"},
			})
		case "/f0nwa/oh-my-boot_/releases/download/v0.2.1-dev.3/SHA256SUMS":
			fmt.Fprintf(w, "%s  omb_0.2.1~dev.3_amd64.deb\n", debSHA)
		case "/f0nwa/oh-my-boot_/releases/download/v0.2.1-dev.3/omb_0.2.1~dev.3_amd64.deb":
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
			if mgmtIP != "192.0.2.10" {
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

func TestLookupSHA256Sum(t *testing.T) {
	debHash := sha256Hex([]byte("deb-content"))
	ovaHash := sha256Hex([]byte("ova-content"))
	sums := fmt.Sprintf("%s  omb_0.2.5_amd64.deb\n%s  *omb_0.2.5_ubuntu24.04_amd64.ova\n", debHash, ovaHash)

	got, err := LookupSHA256Sum([]byte(sums), "omb_0.2.5_amd64.deb")
	if err != nil || got != debHash {
		t.Fatalf("LookupSHA256Sum(deb) = (%q, %v), want (%q, nil)", got, err, debHash)
	}
	got, err = LookupSHA256Sum([]byte(sums), "omb_0.2.5_ubuntu24.04_amd64.ova")
	if err != nil || got != ovaHash {
		t.Fatalf("LookupSHA256Sum(ova) = (%q, %v), want (%q, nil)", got, err, ovaHash)
	}
	if _, err := LookupSHA256Sum([]byte(sums), "omb_0.2.6_amd64.deb"); err == nil {
		t.Fatalf("expected error for missing filename")
	}
	if _, err := LookupSHA256Sum([]byte("bad-digest  omb_0.2.5_amd64.deb\n"), "omb_0.2.5_amd64.deb"); err == nil {
		t.Fatalf("expected error for invalid digest")
	}
}

func TestUpdateLocalDebWithAutoSHA256Sums(t *testing.T) {
	debBytes := []byte("local-stable-deb-bytes")
	debSHA := sha256Hex(debBytes)
	newCommit := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	pkgDir := t.TempDir()
	localDebPath := filepath.Join(pkgDir, "omb_0.2.5_amd64.deb")
	if err := os.WriteFile(localDebPath, debBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	// Multi-entry SHA256SUMS (just like GitHub Releases with VM images).
	sumsContent := fmt.Sprintf("%s  omb_0.2.5_amd64.deb\n%s  omb_0.2.5_ubuntu24.04_amd64.ova\n",
		debSHA, sha256Hex([]byte("other-ova")))
	if err := os.WriteFile(filepath.Join(pkgDir, "SHA256SUMS"), []byte(sumsContent), 0o644); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	writeTestControlEnv(t, root)
	stateDir := filepath.Join(root, "var", "lib", "omb-deployment")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "CHANNEL"), []byte("stable\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	migDir := filepath.Join(root, "usr", "share", "omb", "migrations")
	if err := os.MkdirAll(migDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(migDir, "manifest.json"), []byte(`{"format":1,"migrations":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	releasePath := filepath.Join(root, "usr", "share", "omb", "release.json")
	if err := os.WriteFile(releasePath, []byte(fmt.Sprintf(`{"channel":"stable","commit":%q,"version":"0.2.5"}`, newCommit)), 0o644); err != nil {
		t.Fatal(err)
	}
	bundledPubKey := filepath.Join(root, "usr", "share", "omb", "release-public.pem")
	if err := os.WriteFile(bundledPubKey, []byte("-----BEGIN PUBLIC KEY-----\nTEST\n-----END PUBLIC KEY-----\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "etc", "omb"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc", "omb", "control.env"), []byte("OMB_CONTROL_LISTEN=192.0.2.15:8443\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var sawAptInstall, sawDaemonReload, sawUpdateSocket bool
	runner := &mockRunner{
		fn: func(_ context.Context, spec setup.CmdSpec) ([]byte, error) {
			joined := strings.Join(spec.Args, " ")
			switch {
			case spec.Name == "dpkg-query":
				return []byte("installed\t0.2.4\t0000000000000000000000000000000000000000\n"), nil
			case spec.Name == "runuser" && strings.Contains(joined, "pg_dump"):
				return []byte("PGDMP-mock"), nil
			case spec.Name == "apt-get":
				sawAptInstall = true
				return nil, nil
			case spec.Name == "systemctl" && joined == "daemon-reload":
				sawDaemonReload = true
				return nil, nil
			case spec.Name == "systemctl" && joined == "enable --now omb-update.socket":
				sawUpdateSocket = true
				return nil, nil
			}
			return nil, nil
		},
	}

	fixedTime := time.Date(2026, 11, 25, 18, 10, 0, 0, time.UTC)
	var out bytes.Buffer
	app := &App{
		RootDir: root,
		Lang:    "ru",
		Out:     &out,
		ErrOut:  &bytes.Buffer{},
		Euid:    func() int { return 0 },
		Now:     func() time.Time { return fixedTime },
		Runner:  runner,
		HealthCheck: func(_ context.Context, _ string) error {
			return nil
		},
	}

	if err := app.Update(context.Background(), UpdateOptions{DebPath: localDebPath, Yes: true}); err != nil {
		t.Fatalf("local Update failed: %v", err)
	}
	if !sawAptInstall {
		t.Error("expected apt-get install to be invoked")
	}
	if !sawDaemonReload {
		t.Error("expected systemctl daemon-reload to be invoked")
	}
	if !sawUpdateSocket {
		t.Error("expected systemctl enable --now omb-update.socket to be invoked")
	}
	if raw, err := os.ReadFile(filepath.Join(root, "etc", "omb", "update.env")); err != nil || string(raw) != "OMB_UPDATE_MANAGEMENT_IP=192.0.2.15\n" {
		t.Errorf("unexpected update.env: %q (err=%v)", string(raw), err)
	}
	if raw, err := os.ReadFile(filepath.Join(root, "etc", "omb", "release-public.pem")); err != nil || !strings.Contains(string(raw), "BEGIN PUBLIC KEY") {
		t.Errorf("unexpected release-public.pem: %q (err=%v)", string(raw), err)
	}
	if !strings.Contains(out.String(), "SHA-256 пакета подтверждён по SHA256SUMS") {
		t.Errorf("expected SHA256SUMS confirmation in output, got:\n%s", out.String())
	}

	backupDir := filepath.Join(root, "var", "backups", "omb", "local.20261125T181000Z")
	for _, f := range []string{"omb_boot.dump", "omb_images.dump", "omb_control.dump"} {
		if _, err := os.Stat(filepath.Join(backupDir, f)); err != nil {
			t.Errorf("expected backup file %s: %v", f, err)
		}
	}

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
	checkState("CHANNEL", "stable")
	checkState("PACKAGE", "0.2.5")
	checkState("PACKAGE_SHA256", debSHA)
	checkState("COMMIT", newCommit)
}

func TestUpdateLocalDebChecksumMismatchRejected(t *testing.T) {
	pkgDir := t.TempDir()
	localDebPath := filepath.Join(pkgDir, "omb_0.2.5_amd64.deb")
	if err := os.WriteFile(localDebPath, []byte("actual-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	wrongSHA := sha256Hex([]byte("different-bytes"))
	if err := os.WriteFile(filepath.Join(pkgDir, "SHA256SUMS"), []byte(wrongSHA+"  omb_0.2.5_amd64.deb\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	app := &App{
		RootDir: t.TempDir(),
		Lang:    "en",
		Out:     &bytes.Buffer{},
		ErrOut:  &bytes.Buffer{},
		Euid:    func() int { return 0 },
		Runner:  &mockRunner{},
	}

	err := app.Update(context.Background(), UpdateOptions{DebPath: localDebPath, Yes: true})
	if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("expected SHA-256 mismatch error, got %v", err)
	}
}

func TestUpdateLocalDebWithoutSHA256SumsWarnsAndInfersDevChannel(t *testing.T) {
	pkgDir := t.TempDir()
	debBytes := []byte("dev-deb-without-sums")
	localDebPath := filepath.Join(pkgDir, "omb_0.2.5~dev.4_amd64.deb")
	if err := os.WriteFile(localDebPath, debBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	writeTestControlEnv(t, root)
	migDir := filepath.Join(root, "usr", "share", "omb", "migrations")
	if err := os.MkdirAll(migDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(migDir, "manifest.json"), []byte(`{"format":1,"migrations":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	app := &App{
		RootDir: root,
		Lang:    "en",
		Out:     &out,
		ErrOut:  &bytes.Buffer{},
		Euid:    func() int { return 0 },
		Runner: &mockRunner{
			fn: func(_ context.Context, spec setup.CmdSpec) ([]byte, error) {
				if spec.Name == "runuser" && strings.Contains(strings.Join(spec.Args, " "), "pg_dump") {
					return []byte("PGDMP"), nil
				}
				return nil, nil
			},
		},
		HealthCheck: func(_ context.Context, _ string) error { return nil },
	}

	if err := app.Update(context.Background(), UpdateOptions{DebPath: localDebPath, Yes: true}); err != nil {
		t.Fatalf("Update without SHA256SUMS failed: %v", err)
	}
	if !strings.Contains(out.String(), "skipping checksum verification") {
		t.Errorf("expected checksum skip warning, got:\n%s", out.String())
	}
	ch, err := os.ReadFile(filepath.Join(root, "var", "lib", "omb-deployment", "CHANNEL"))
	if err != nil || strings.TrimSpace(string(ch)) != "dev" {
		t.Fatalf("CHANNEL = (%q, %v), want dev", strings.TrimSpace(string(ch)), err)
	}
}

func TestUpdateLocalDebFlagValidation(t *testing.T) {
	app := &App{
		RootDir: t.TempDir(),
		Lang:    "en",
		Out:     &bytes.Buffer{},
		ErrOut:  &bytes.Buffer{},
		Euid:    func() int { return 0 },
	}

	if err := app.Update(context.Background(), UpdateOptions{Checksums: "/tmp/SHA256SUMS"}); err == nil {
		t.Fatal("expected error for --checksums without --deb")
	}
	if err := app.Update(context.Background(), UpdateOptions{DebPath: "/tmp/omb_0.2.5_amd64.deb", Version: "v0.2.5"}); err == nil {
		t.Fatal("expected error for --deb with --version")
	}
	if err := app.Update(context.Background(), UpdateOptions{DebPath: "/tmp/omb_0.2.5_amd64.deb", Repo: "a/b"}); err == nil {
		t.Fatal("expected error for --deb with --repo")
	}

	badName := filepath.Join(t.TempDir(), "not-omb.deb")
	if err := os.WriteFile(badName, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := app.Update(context.Background(), UpdateOptions{DebPath: badName}); err == nil {
		t.Fatal("expected error for invalid package filename")
	}
}

func TestUpdateGuardsDeploymentLockAndPending(t *testing.T) {
	app := &App{RootDir: t.TempDir(), Lang: "en", Euid: func() int { return 0 }}
	lock, err := app.acquireUpdateLock()
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Update(context.Background(), UpdateOptions{DebPath: "/missing.deb"}); err == nil || !strings.Contains(err.Error(), "another OMB deployment") {
		t.Fatalf("concurrent update error = %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(app.stateDir(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app.stateDir(), "PENDING"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := app.Update(context.Background(), UpdateOptions{DebPath: "/missing.deb"}); err == nil || !strings.Contains(err.Error(), "update is pending") {
		t.Fatalf("pending update error = %v", err)
	}
}
