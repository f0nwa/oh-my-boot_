package ombcli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"omb/internal/setup"
)

var (
	debFilenameRe  = regexp.MustCompile(`^omb_[A-Za-z0-9.+~-]+_amd64\.deb$`)
	sha256HexRe    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	devTagRe       = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+-dev\.[0-9]+$`)
	migrationIDRe  = regexp.MustCompile(`^[0-9]{4}_[a-z0-9_]+$`)
	migrationOrder = []string{"omb_boot", "omb_images", "omb_control"}
)

// UpdateOptions configures `omb update`.
type UpdateOptions struct {
	Channel            string
	Version            string
	Repo               string
	DebPath            string
	Checksums          string
	Yes                bool
	AllowChannelSwitch bool
}

// MigrationManifest matches /usr/share/omb/migrations/manifest.json.
type MigrationManifest struct {
	Format     int              `json:"format"`
	Migrations []MigrationEntry `json:"migrations"`
}

// MigrationEntry describes a single SQL migration in manifest.json.
type MigrationEntry struct {
	Database string `json:"database"`
	ID       string `json:"id"`
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
}

// AppliedMigration represents a row already present in public.schema_migrations.
type AppliedMigration struct {
	Database string
	ID       string
	SHA256   string
}

type githubRelease struct {
	TagName string `json:"tag_name"`
}

// NormalizeTag converts a version or tag (e.g. "0.2.1~dev.3" or "v0.2.0") into a GitHub release tag.
func NormalizeTag(v string) string {
	v = strings.TrimSpace(v)
	v = strings.ReplaceAll(v, "~", "-")
	if v != "" && !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}

// ParseSHA256Sums extracts the verified omb_*_amd64.deb filename and lowercase SHA-256 digest.
// Any .deb entry that fails the strict filename allowlist (e.g., path traversal) is rejected.
func ParseSHA256Sums(content []byte) (debName, debSHA string, err error) {
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		digest := strings.ToLower(fields[0])
		fname := strings.TrimPrefix(fields[1], "*")

		if strings.HasSuffix(fname, ".deb") {
			if !debFilenameRe.MatchString(fname) {
				return "", "", fmt.Errorf("invalid .deb filename in SHA256SUMS: %q", fname)
			}
			if !sha256HexRe.MatchString(digest) {
				return "", "", fmt.Errorf("invalid SHA-256 digest for %s in SHA256SUMS", fname)
			}
			if debName == "" {
				debName = fname
				debSHA = digest
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", "", err
	}
	if debName == "" {
		return "", "", fmt.Errorf("SHA256SUMS does not contain a valid omb_*_amd64.deb entry")
	}
	return debName, debSHA, nil
}

// LookupSHA256Sum finds the expected lowercase SHA-256 digest for targetFilename
// in a SHA256SUMS file (ignoring entries for other files, e.g. VM images).
func LookupSHA256Sum(content []byte, targetFilename string) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		digest := strings.ToLower(fields[0])
		fname := strings.TrimPrefix(fields[1], "*")
		if fname != targetFilename {
			continue
		}
		if !sha256HexRe.MatchString(digest) {
			return "", fmt.Errorf("invalid SHA-256 digest for %s in SHA256SUMS", fname)
		}
		return digest, nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("SHA256SUMS does not contain an entry for %s", targetFilename)
}

// PlanMigrations validates manifest integrity, checks applied migrations for missing entries,
// SHA-256 checksum drift, and append-only ordering, and returns pending migrations in execution order.
func PlanMigrations(manifest MigrationManifest, applied []AppliedMigration) ([]MigrationEntry, error) {
	if manifest.Format != 1 || manifest.Migrations == nil {
		return nil, fmt.Errorf("invalid migrations manifest format")
	}

	manifestByDB := make(map[string][]MigrationEntry, len(migrationOrder))
	manifestMap := make(map[string]map[string]MigrationEntry, len(migrationOrder))
	seenNumbers := make(map[string]map[string]bool, len(migrationOrder))
	for _, db := range migrationOrder {
		manifestMap[db] = make(map[string]MigrationEntry)
		seenNumbers[db] = make(map[string]bool)
	}

	for _, entry := range manifest.Migrations {
		if _, ok := manifestMap[entry.Database]; !ok {
			return nil, fmt.Errorf("unsupported migration database in manifest: %s", entry.Database)
		}
		if !migrationIDRe.MatchString(entry.ID) {
			return nil, fmt.Errorf("invalid migration id in manifest: %s", entry.ID)
		}
		if !sha256HexRe.MatchString(entry.SHA256) {
			return nil, fmt.Errorf("invalid migration sha256 in manifest: %s/%s", entry.Database, entry.ID)
		}
		expectedPath := fmt.Sprintf("%s/%s.sql", entry.Database, entry.ID)
		if entry.Path != expectedPath {
			return nil, fmt.Errorf("invalid migration path in manifest: %s/%s (%s)", entry.Database, entry.ID, entry.Path)
		}
		if _, dup := manifestMap[entry.Database][entry.ID]; dup {
			return nil, fmt.Errorf("duplicate migration id in manifest: %s/%s", entry.Database, entry.ID)
		}
		num := entry.ID[:4]
		if seenNumbers[entry.Database][num] {
			return nil, fmt.Errorf("duplicate migration number in manifest: %s/%s", entry.Database, num)
		}
		seenNumbers[entry.Database][num] = true
		manifestByDB[entry.Database] = append(manifestByDB[entry.Database], entry)
		manifestMap[entry.Database][entry.ID] = entry
	}

	appliedByDB := make(map[string][]AppliedMigration, len(migrationOrder))
	for _, item := range applied {
		if _, ok := manifestMap[item.Database]; !ok {
			return nil, fmt.Errorf("unsupported applied migration database: %s", item.Database)
		}
		appliedByDB[item.Database] = append(appliedByDB[item.Database], item)
	}

	var pending []MigrationEntry
	for _, db := range migrationOrder {
		byID := manifestMap[db]
		appliedIDs := make(map[string]bool, len(appliedByDB[db]))
		var maxApplied string
		for _, item := range appliedByDB[db] {
			entry, exists := byID[item.ID]
			if !exists {
				return nil, fmt.Errorf("applied migration %s/%s is missing from the new package", db, item.ID)
			}
			if entry.SHA256 != item.SHA256 {
				return nil, fmt.Errorf("migration checksum drift detected for %s/%s", db, item.ID)
			}
			appliedIDs[item.ID] = true
			if item.ID > maxApplied {
				maxApplied = item.ID
			}
		}

		sorted := append([]MigrationEntry(nil), manifestByDB[db]...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

		if maxApplied != "" {
			for _, entry := range sorted {
				if !appliedIDs[entry.ID] && entry.ID < maxApplied {
					return nil, fmt.Errorf("append-only violation: unapplied migration %s/%s precedes applied %s", db, entry.ID, maxApplied)
				}
			}
		}

		for _, entry := range sorted {
			if !appliedIDs[entry.ID] {
				pending = append(pending, entry)
			}
		}
	}

	return pending, nil
}

// ResolveReleaseTag resolves the target GitHub release tag for a channel or explicit version.
func (a *App) ResolveReleaseTag(ctx context.Context, repo, channel, explicitVersion string) (string, error) {
	if explicitVersion != "" {
		return NormalizeTag(explicitVersion), nil
	}

	client := a.httpClient()
	var url string
	if channel == "stable" {
		url = fmt.Sprintf("%s/repos/%s/releases/latest", a.apiBaseURL(), repo)
	} else {
		url = fmt.Sprintf("%s/repos/%s/releases?per_page=20", a.apiBaseURL(), repo)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "omb-cli")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("query GitHub releases (%s): %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub releases API returned HTTP %d for %s", resp.StatusCode, url)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", fmt.Errorf("read GitHub releases response: %w", err)
	}

	if channel == "stable" {
		var rel githubRelease
		if err := json.Unmarshal(body, &rel); err != nil {
			return "", fmt.Errorf("parse latest release JSON: %w", err)
		}
		if rel.TagName == "" {
			return "", fmt.Errorf("latest release response did not include tag_name")
		}
		return rel.TagName, nil
	}

	var releases []githubRelease
	if err := json.Unmarshal(body, &releases); err != nil {
		return "", fmt.Errorf("parse releases list JSON: %w", err)
	}
	for _, rel := range releases {
		if devTagRe.MatchString(rel.TagName) {
			return rel.TagName, nil
		}
	}
	return "", fmt.Errorf("no dev release (v*-dev.*) found in %s", repo)
}

func (a *App) acquireUpdateLock() (*os.File, error) {
	path := a.resolve("/run/lock/omb-offline-deploy.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("create deployment lock directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, fmt.Errorf("open deployment lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("%s: %w", a.t("идёт другая операция развёртывания OMB", "another OMB deployment operation is running"), err)
	}
	return file, nil
}

// Update downloads (or reads a local --deb), verifies, backs up, installs, migrates, and verifies an OMB update.
func (a *App) Update(ctx context.Context, opts UpdateOptions) error {
	if err := a.requireRoot(); err != nil {
		return err
	}

	localDeb := strings.TrimSpace(opts.DebPath)
	explicitSums := strings.TrimSpace(opts.Checksums)
	if localDeb == "" && explicitSums != "" {
		return fmt.Errorf("%s", a.t(
			"флаг --checksums можно использовать только вместе с --deb",
			"--checksums can only be used together with --deb",
		))
	}
	if localDeb != "" && (strings.TrimSpace(opts.Version) != "" || strings.TrimSpace(opts.Repo) != "") {
		return fmt.Errorf("%s", a.t(
			"флаг --deb нельзя сочетать с --version или --repo",
			"--deb cannot be combined with --version or --repo",
		))
	}

	lock, err := a.acquireUpdateLock()
	if err != nil {
		return err
	}
	defer lock.Close()
	if _, err := os.Lstat(filepath.Join(a.stateDir(), "PENDING")); !os.IsNotExist(err) {
		if err != nil {
			return fmt.Errorf("check deployment state: %w", err)
		}
		return fmt.Errorf("%s", a.t("Есть незавершённое обновление OMB. Сначала выполните rollback.", "An OMB update is pending. Run rollback first."))
	}

	currentChannel := a.CurrentChannel()
	targetChannel := strings.ToLower(strings.TrimSpace(opts.Channel))

	var (
		absDebPath    string
		debName       string
		targetVersion string
		expectedSHA   string
		releaseBase   string
		sumsPath      string
	)

	if localDeb != "" {
		var err error
		absDebPath, err = filepath.Abs(localDeb)
		if err != nil {
			return fmt.Errorf("resolve --deb path: %w", err)
		}
		info, err := os.Stat(absDebPath)
		if err != nil {
			return fmt.Errorf("%s: %w", a.t("не удалось открыть файл пакета", "cannot access package file"), err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s: %s", a.t("путь --deb должен указывать на обычный файл", "--deb must point to a regular file"), absDebPath)
		}
		debName = filepath.Base(absDebPath)
		if !debFilenameRe.MatchString(debName) {
			return fmt.Errorf("%s: %q", a.t("недопустимое имя файла пакета (ожидается omb_<версия>_amd64.deb)", "invalid package filename (expected omb_<version>_amd64.deb)"), debName)
		}
		targetVersion = strings.TrimSuffix(strings.TrimPrefix(debName, "omb_"), "_amd64.deb")
		if targetChannel == "" {
			if strings.Contains(targetVersion, "~dev.") {
				targetChannel = "dev"
			} else {
				targetChannel = "stable"
			}
		}
		if targetChannel != "stable" && targetChannel != "dev" {
			return fmt.Errorf("%s", a.t(
				fmt.Sprintf("неподдерживаемый канал %q (ожидается stable или dev)", targetChannel),
				fmt.Sprintf("unsupported channel %q (expected stable or dev)", targetChannel),
			))
		}

		if explicitSums != "" {
			sumsPath, err = filepath.Abs(explicitSums)
			if err != nil {
				return fmt.Errorf("resolve --checksums path: %w", err)
			}
			sumsInfo, err := os.Stat(sumsPath)
			if err != nil {
				return fmt.Errorf("%s: %w", a.t("не удалось открыть файл контрольных сумм", "cannot access checksums file"), err)
			}
			if !sumsInfo.Mode().IsRegular() {
				return fmt.Errorf("%s: %s", a.t("путь --checksums должен указывать на обычный файл", "--checksums must point to a regular file"), sumsPath)
			}
		} else {
			candidate := filepath.Join(filepath.Dir(absDebPath), "SHA256SUMS")
			if sumsInfo, err := os.Stat(candidate); err == nil && sumsInfo.Mode().IsRegular() {
				sumsPath = candidate
			}
		}

		if sumsPath != "" {
			sumsBytes, err := os.ReadFile(sumsPath)
			if err != nil {
				return fmt.Errorf("read %s: %w", sumsPath, err)
			}
			expectedSHA, err = LookupSHA256Sum(sumsBytes, debName)
			if err != nil {
				return err
			}
		}

		a.PrintHeader(a.t("обновление OMB из локального пакета", "local OMB package update"))
	} else {
		if targetChannel == "" {
			targetChannel = currentChannel
		}
		if targetChannel != "stable" && targetChannel != "dev" {
			return fmt.Errorf("%s", a.t(
				fmt.Sprintf("неподдерживаемый канал %q (ожидается stable или dev)", targetChannel),
				fmt.Sprintf("unsupported channel %q (expected stable or dev)", targetChannel),
			))
		}

		repo := strings.TrimSpace(opts.Repo)
		if repo == "" {
			repo = a.defaultRepo()
		}

		a.PrintHeader(a.t("онлайн-обновление OMB", "online OMB update"))
		a.PrintStep(a.t(
			fmt.Sprintf("Поиск выпуска (репозиторий: %s, канал: %s)...", repo, targetChannel),
			fmt.Sprintf("Resolving release (repo: %s, channel: %s)...", repo, targetChannel),
		))

		tag, err := a.ResolveReleaseTag(ctx, repo, targetChannel, opts.Version)
		if err != nil {
			return err
		}

		releaseBase = fmt.Sprintf("%s/%s/releases/download/%s", a.downloadBaseURL(), repo, tag)
		sumsBytes, err := a.downloadBytes(ctx, releaseBase+"/SHA256SUMS", 1<<20)
		if err != nil {
			return fmt.Errorf("download SHA256SUMS: %w", err)
		}
		debName, expectedSHA, err = ParseSHA256Sums(sumsBytes)
		if err != nil {
			return err
		}
		targetVersion = strings.TrimSuffix(strings.TrimPrefix(debName, "omb_"), "_amd64.deb")
	}

	tmpDir, err := os.MkdirTemp("", "omb-update-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	if err := os.Chmod(tmpDir, 0o755); err != nil {
		return fmt.Errorf("chmod temp dir: %w", err)
	}

	installedVersion, _, installedOK := a.queryInstalledPackage(ctx)
	channelChanged := targetChannel != currentChannel
	if installedOK && installedVersion == targetVersion && !channelChanged && !opts.AllowChannelSwitch {
		a.PrintOK(a.t(
			fmt.Sprintf("OMB уже актуален: версия %s (канал %s).", installedVersion, targetChannel),
			fmt.Sprintf("OMB is already up to date: version %s (channel %s).", installedVersion, targetChannel),
		))
		return nil
	}

	if !opts.Yes {
		reader := bufio.NewReader(a.in())
		for {
			promptText := a.t(
				fmt.Sprintf("Установить OMB %s (канал %s)?", targetVersion, targetChannel),
				fmt.Sprintf("Install OMB %s (channel %s)?", targetVersion, targetChannel),
			)
			if localDeb != "" {
				promptText = a.t(
					fmt.Sprintf("Установить OMB %s (канал %s) из %s?", targetVersion, targetChannel, debName),
					fmt.Sprintf("Install OMB %s (channel %s) from %s?", targetVersion, targetChannel, debName),
				)
			}
			a.printPrompt(promptText, "Y/n")
			ans, err := readLine(reader)
			if err != nil {
				return err
			}
			yes, ok := parseYesNo(ans, true)
			if !ok {
				a.PrintError(a.t("Введите Y или n.", "Enter Y or n."))
				continue
			}
			if !yes {
				a.PrintWarn(a.t("Обновление отменено пользователем.", "Update cancelled by user."))
				return nil
			}
			break
		}
	}

	debPath := filepath.Join(tmpDir, debName)
	var actualSHA string
	if localDeb != "" {
		a.PrintStep(a.t(
			fmt.Sprintf("Подготовка и проверка %s...", debName),
			fmt.Sprintf("Staging and verifying %s...", debName),
		))
		actualSHA, err = copyFileWithSHA256(absDebPath, debPath, 0o644)
		if err != nil {
			return fmt.Errorf("stage %s: %w", debName, err)
		}
		if expectedSHA != "" {
			if actualSHA != expectedSHA {
				return fmt.Errorf("SHA-256 mismatch for %s: got %s, want %s", debName, actualSHA, expectedSHA)
			}
			a.PrintOK(a.t(
				fmt.Sprintf("SHA-256 пакета подтверждён по %s.", filepath.Base(sumsPath)),
				fmt.Sprintf("Package SHA-256 verified against %s.", filepath.Base(sumsPath)),
			))
		} else {
			a.PrintWarn(a.t(
				"Файл SHA256SUMS не найден рядом с пакетом — проверка контрольной суммы пропущена.",
				"SHA256SUMS not found next to the package — skipping checksum verification.",
			))
		}
	} else {
		a.PrintStep(a.t(
			fmt.Sprintf("Загрузка и проверка %s...", debName),
			fmt.Sprintf("Downloading and verifying %s...", debName),
		))
		actualSHA, err = a.downloadFile(ctx, releaseBase+"/"+debName, debPath, 0o644)
		if err != nil {
			return fmt.Errorf("download %s: %w", debName, err)
		}
		if actualSHA != expectedSHA {
			return fmt.Errorf("SHA-256 mismatch for %s: got %s, want %s", debName, actualSHA, expectedSHA)
		}
		a.PrintOK(a.t("SHA-256 пакета подтверждён.", "Package SHA-256 verified."))
	}

	// Backup databases and /etc/omb before modifying anything.
	backupPrefix := "online."
	if localDeb != "" {
		backupPrefix = "local."
	}
	backupDir := filepath.Join(a.backupBaseDir(), backupPrefix+a.now().Format("20060102T150405Z"))
	a.PrintStep(a.t(
		fmt.Sprintf("Резервное копирование БД и /etc/omb в %s...", backupDir),
		fmt.Sprintf("Backing up databases and /etc/omb to %s...", backupDir),
	))
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}
	if err := os.Chmod(backupDir, 0o700); err != nil {
		return fmt.Errorf("chmod backup dir: %w", err)
	}
	for _, db := range migrationOrder {
		dumpBytes, err := a.run(ctx, "runuser", "-u", "postgres", "--", "pg_dump", "--no-password", "-Fc", "-d", db)
		if err != nil {
			return fmt.Errorf("pg_dump %s: %w", db, err)
		}
		if err := writeFileAtomic(filepath.Join(backupDir, db+".dump"), dumpBytes, 0o600); err != nil {
			return fmt.Errorf("write backup %s.dump: %w", db, err)
		}
	}
	if err := backupDirTree(a.resolve("/etc/omb"), filepath.Join(backupDir, "etc-omb")); err != nil {
		return fmt.Errorf("backup /etc/omb: %w", err)
	}
	a.PrintOK(a.t("Резервная копия создана: "+backupDir, "Backup created: "+backupDir))

	// Stop OMB services before package install.
	a.PrintStep(a.t("Остановка служб OMB...", "Stopping OMB services..."))
	if _, err := a.run(ctx, "systemctl", "stop",
		"omb-control.service", "omb-images.service", "omb-boot.service", "omb-agent.service"); err != nil {
		return fmt.Errorf("stop OMB services: %w", err)
	}

	// Install .deb package.
	a.PrintStep(a.t("Установка пакета "+debName+"...", "Installing package "+debName+"..."))
	aptArgs := []string{
		"-o", "DPkg::Lock::Timeout=60",
		"install", "-y", "--no-install-recommends",
	}
	if opts.AllowChannelSwitch || channelChanged || opts.Version != "" || localDeb != "" {
		aptArgs = append(aptArgs, "--allow-downgrades")
	}
	aptArgs = append(aptArgs, debPath)
	if _, err := a.runSpec(ctx, setup.CmdSpec{
		Name: "apt-get",
		Args: aptArgs,
		Env:  []string{"DEBIAN_FRONTEND=noninteractive"},
	}); err != nil {
		return fmt.Errorf("install %s: %w", debName, err)
	}

	// Plan and apply cumulative database migrations transactionally.
	a.PrintStep(a.t("Проверка и применение миграций БД...", "Checking and applying database migrations..."))
	appliedCount, err := a.applyPendingMigrations(ctx)
	if err != nil {
		return err
	}
	a.PrintOK(a.t(
		fmt.Sprintf("Миграции БД применены (%d).", appliedCount),
		fmt.Sprintf("Database migrations applied (%d).", appliedCount),
	))

	// Start OMB services and run health checks.
	a.PrintStep(a.t("Запуск служб OMB и проверка состояния...", "Starting OMB services and running health checks..."))
	if _, err := a.run(ctx, "systemctl", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w", err)
	}
	if _, err := a.run(ctx, "systemctl", "start",
		"omb-boot.service", "omb-agent.service", "omb-images.service", "omb-control.service"); err != nil {
		return fmt.Errorf("start OMB services: %w", err)
	}
	if err := a.checkUpdateHealth(ctx); err != nil {
		return fmt.Errorf("post-update health check failed: %w", err)
	}

	// Atomically update /var/lib/omb-deployment state metadata.
	pkgVersion := targetVersion
	pkgCommit := ""
	if meta, err := a.ReadReleaseMeta(); err == nil {
		if meta.Version != "" {
			pkgVersion = meta.Version
		}
		if meta.Commit != "" {
			pkgCommit = meta.Commit
		}
	}
	if pkgCommit == "" {
		if ver, cmt, ok := a.queryInstalledPackage(ctx); ok {
			if pkgVersion == "" && ver != "" {
				pkgVersion = ver
			}
			pkgCommit = cmt
		}
	}

	if err := writeFileAtomic(filepath.Join(a.stateDir(), "CHANNEL"), []byte(targetChannel+"\n"), 0o600); err != nil {
		return fmt.Errorf("write CHANNEL: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(a.stateDir(), "PACKAGE"), []byte(pkgVersion+"\n"), 0o600); err != nil {
		return fmt.Errorf("write PACKAGE: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(a.stateDir(), "PACKAGE_SHA256"), []byte(actualSHA+"\n"), 0o600); err != nil {
		return fmt.Errorf("write PACKAGE_SHA256: %w", err)
	}
	if pkgCommit != "" {
		if err := writeFileAtomic(filepath.Join(a.stateDir(), "COMMIT"), []byte(pkgCommit+"\n"), 0o600); err != nil {
			return fmt.Errorf("write COMMIT: %w", err)
		}
	}
	if err := copyFileAtomic(debPath, filepath.Join(a.stateDir(), "current.deb"), 0o644); err != nil {
		return fmt.Errorf("update current.deb: %w", err)
	}

	a.PrintOK(a.t(
		fmt.Sprintf("OMB обновлён до %s (канал %s).", pkgVersion, targetChannel),
		fmt.Sprintf("OMB updated to %s (channel %s).", pkgVersion, targetChannel),
	))
	return nil
}

func (a *App) applyPendingMigrations(ctx context.Context) (int, error) {
	manifestPath := a.resolve(filepath.Join(DefaultMigrationsDir, "manifest.json"))
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return 0, fmt.Errorf("read migrations manifest: %w", err)
	}
	var manifest MigrationManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return 0, fmt.Errorf("parse migrations manifest: %w", err)
	}

	var applied []AppliedMigration
	for _, db := range migrationOrder {
		dbUser := strings.ReplaceAll(db, "_", "-")
		out, err := a.run(ctx, "runuser", "-u", dbUser, "--",
			"psql", "-Xw", "-At", "-F", "\t", "-d", db,
			"-c", "SELECT id, sha256 FROM public.schema_migrations ORDER BY id")
		if err != nil {
			return 0, fmt.Errorf("query schema_migrations for %s: %w", db, err)
		}
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.Split(line, "\t")
			if len(parts) != 2 {
				return 0, fmt.Errorf("unexpected schema_migrations row in %s: %q", db, line)
			}
			applied = append(applied, AppliedMigration{
				Database: db,
				ID:       parts[0],
				SHA256:   parts[1],
			})
		}
	}

	pending, err := PlanMigrations(manifest, applied)
	if err != nil {
		return 0, err
	}

	for _, entry := range pending {
		sqlPath := a.resolve(filepath.Join(DefaultMigrationsDir, entry.Path))
		sqlBytes, err := os.ReadFile(sqlPath)
		if err != nil {
			return 0, fmt.Errorf("read migration %s/%s: %w", entry.Database, entry.ID, err)
		}
		sum := sha256.Sum256(sqlBytes)
		actual := hex.EncodeToString(sum[:])
		if actual != entry.SHA256 {
			return 0, fmt.Errorf("migration file checksum mismatch for %s/%s: got %s, want %s", entry.Database, entry.ID, actual, entry.SHA256)
		}

		var tx strings.Builder
		tx.WriteString("BEGIN;\n")
		tx.Write(sqlBytes)
		if len(sqlBytes) == 0 || sqlBytes[len(sqlBytes)-1] != '\n' {
			tx.WriteByte('\n')
		}
		tx.WriteString("INSERT INTO public.schema_migrations (id, sha256) VALUES (:'mig_id', :'mig_sha');\n")
		tx.WriteString("COMMIT;\n")

		dbUser := strings.ReplaceAll(entry.Database, "_", "-")
		if _, err := a.runSpec(ctx, setup.CmdSpec{
			Name: "runuser",
			Args: []string{
				"-u", dbUser, "--",
				"psql", "-Xw", "-v", "ON_ERROR_STOP=1", "-d", entry.Database,
				"-v", "mig_id=" + entry.ID,
				"-v", "mig_sha=" + entry.SHA256,
			},
			Stdin: []byte(tx.String()),
		}); err != nil {
			return 0, fmt.Errorf("apply migration %s/%s: %w", entry.Database, entry.ID, err)
		}
	}
	return len(pending), nil
}

func (a *App) checkUpdateHealth(ctx context.Context) error {
	mgmtIP := "127.0.0.1"
	if envMap, err := ParseEnvFile(a.resolve(DefaultControlEnvPath)); err == nil {
		if listen := strings.TrimSpace(envMap["OMB_CONTROL_LISTEN"]); listen != "" {
			if host, _, err := net.SplitHostPort(listen); err == nil && host != "" {
				mgmtIP = host
			}
		}
	}

	if a.HealthCheck != nil {
		return a.HealthCheck(ctx, mgmtIP)
	}

	crtPEM, err := os.ReadFile(a.resolve("/var/lib/omb-control/control.crt"))
	if err != nil {
		return fmt.Errorf("read control.crt: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(crtPEM) {
		return fmt.Errorf("parse control.crt")
	}

	httpsClient := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			Proxy:           nil,
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
	}
	httpClient := &http.Client{
		Timeout:   4 * time.Second,
		Transport: &http.Transport{Proxy: nil},
	}

	var lastErr error
	for attempt := 0; attempt < 20; attempt++ {
		err1 := checkHTTPHealth(ctx, httpsClient, fmt.Sprintf("https://%s:8443/login", mgmtIP), "")
		err2 := checkHTTPHealth(ctx, httpClient, "http://127.0.0.1:8080/boot/menu.ipxe", "#!ipxe")
		if err1 == nil && err2 == nil {
			return nil
		}
		if err1 != nil {
			lastErr = fmt.Errorf("omb-control /login: %w", err1)
		} else {
			lastErr = fmt.Errorf("omb-boot menu.ipxe: %w", err2)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
	return lastErr
}

func checkHTTPHealth(ctx context.Context, client *http.Client, rawURL, wantPrefix string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned HTTP %d", rawURL, resp.StatusCode)
	}
	if wantPrefix != "" {
		body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
		if err != nil {
			return err
		}
		if !strings.HasPrefix(string(body), wantPrefix) {
			return fmt.Errorf("%s did not start with %q", rawURL, wantPrefix)
		}
	}
	return nil
}

func (a *App) downloadBytes(ctx context.Context, rawURL string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "omb-cli")
	resp, err := a.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s returned HTTP %d", rawURL, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBytes))
}

func (a *App) downloadFile(ctx context.Context, rawURL, dst string, mode os.FileMode) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "omb-cli")
	resp, err := a.httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s returned HTTP %d", rawURL, resp.StatusCode)
	}

	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyFileWithSHA256(src, dst string, mode os.FileMode) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), in); err != nil {
		out.Close()
		return "", err
	}
	if err := out.Chmod(mode); err != nil {
		out.Close()
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func backupDirTree(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", src)
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return writeFileAtomic(target, data, 0o600)
	})
}
