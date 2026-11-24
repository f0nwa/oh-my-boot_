package ombcli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"omb/internal/setup"
)

const (
	DefaultStateDir        = "/var/lib/omb-deployment"
	DefaultBackupBaseDir   = "/var/backups/omb"
	DefaultReleaseJSONPath = "/usr/share/omb/release.json"
	DefaultControlEnvPath  = "/etc/omb/control.env"
	DefaultMigrationsDir   = "/usr/share/omb/migrations"
	DefaultRepo            = "f0nwa/OMB"
	DefaultAPIBaseURL      = "https://api.github.com"
	DefaultDownloadBaseURL = "https://github.com"

	ruleLine = "────────────────────────────────────────────────────────────────"
)

// ReleaseMeta describes /usr/share/omb/release.json shipped inside omb_*.deb.
type ReleaseMeta struct {
	Channel        string `json:"channel"`
	Commit         string `json:"commit"`
	DisplayVersion string `json:"display_version"`
	Package        string `json:"package"`
	Version        string `json:"version"`
}

// App holds dependencies and configuration for executing omb CLI commands.
type App struct {
	RootDir         string
	StateDir        string
	BackupBaseDir   string
	APIBaseURL      string
	DownloadBaseURL string
	DefaultRepo     string
	Version         string
	Lang            string
	Color           bool

	In          io.Reader
	Out         io.Writer
	ErrOut      io.Writer
	ReadSecret  func() (string, error)
	Runner      setup.CmdRunner
	HTTPClient  *http.Client
	Euid        func() int
	Now         func() time.Time
	HealthCheck func(ctx context.Context, managementIP string) error
}

// DetectLang selects "ru" or "en" from explicit --lang flag or locale environment variables.
func DetectLang(flagLang string, getenv func(string) string) (string, error) {
	if flagLang != "" {
		l := strings.ToLower(strings.TrimSpace(flagLang))
		if l != "ru" && l != "en" {
			return "", fmt.Errorf("unsupported --lang %q (expected ru or en)", flagLang)
		}
		return l, nil
	}
	if getenv != nil {
		for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
			val := strings.TrimSpace(getenv(key))
			if val == "" {
				continue
			}
			if strings.HasPrefix(strings.ToLower(val), "ru") {
				return "ru", nil
			}
			return "en", nil
		}
	}
	return "en", nil
}

// ValidatePassword verifies that pw has at least 12 UTF-8 runes and no line breaks.
func ValidatePassword(pw string) error {
	if strings.ContainsAny(pw, "\r\n") {
		return fmt.Errorf("password must not contain newline characters")
	}
	if utf8.RuneCountInString(pw) < 12 {
		return fmt.Errorf("password must be at least 12 characters")
	}
	return nil
}

func (a *App) lang() string {
	if a.Lang == "ru" {
		return "ru"
	}
	return "en"
}

func (a *App) t(ru, en string) string {
	if a.lang() == "ru" {
		return ru
	}
	return en
}

func (a *App) out() io.Writer {
	if a.Out != nil {
		return a.Out
	}
	return os.Stdout
}

func (a *App) errOut() io.Writer {
	if a.ErrOut != nil {
		return a.ErrOut
	}
	return os.Stderr
}

func (a *App) in() io.Reader {
	if a.In != nil {
		return a.In
	}
	return os.Stdin
}

func (a *App) runner() setup.CmdRunner {
	if a.Runner != nil {
		return a.Runner
	}
	return setup.ExecRunner{}
}

func (a *App) euid() int {
	if a.Euid != nil {
		return a.Euid()
	}
	return os.Geteuid()
}

func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now().UTC()
}

func (a *App) httpClient() *http.Client {
	if a.HTTPClient != nil {
		return a.HTTPClient
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (a *App) rootDir() string {
	if a.RootDir == "" {
		return "/"
	}
	return filepath.Clean(a.RootDir)
}

func (a *App) resolve(p string) string {
	root := a.rootDir()
	if root == "/" {
		return p
	}
	if p == root || strings.HasPrefix(p, root+string(filepath.Separator)) {
		return p
	}
	return filepath.Join(root, strings.TrimPrefix(p, "/"))
}

func (a *App) stateDir() string {
	if a.StateDir != "" {
		return a.resolve(a.StateDir)
	}
	return a.resolve(DefaultStateDir)
}

func (a *App) backupBaseDir() string {
	if a.BackupBaseDir != "" {
		return a.resolve(a.BackupBaseDir)
	}
	return a.resolve(DefaultBackupBaseDir)
}

func (a *App) apiBaseURL() string {
	if a.APIBaseURL != "" {
		return strings.TrimRight(a.APIBaseURL, "/")
	}
	return DefaultAPIBaseURL
}

func (a *App) downloadBaseURL() string {
	if a.DownloadBaseURL != "" {
		return strings.TrimRight(a.DownloadBaseURL, "/")
	}
	return DefaultDownloadBaseURL
}

func (a *App) defaultRepo() string {
	if a.DefaultRepo != "" {
		return a.DefaultRepo
	}
	return DefaultRepo
}

func (a *App) requireRoot() error {
	if a.euid() != 0 {
		return fmt.Errorf("%s", a.t(
			"эта команда требует прав root (запустите через sudo omb ...)",
			"this command requires root privileges (run with sudo omb ...)",
		))
	}
	return nil
}

func (a *App) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return a.runner().Run(ctx, setup.CmdSpec{Name: name, Args: args})
}

func (a *App) runSpec(ctx context.Context, spec setup.CmdSpec) ([]byte, error) {
	return a.runner().Run(ctx, spec)
}

// Styling helpers — Variant 1 («Инженерный пульт»).

func (a *App) style(code, s string) string {
	if !a.Color || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (a *App) white(s string) string     { return a.style("1;97", s) }
func (a *App) dim(s string) string       { return a.style("2", s) }
func (a *App) muted(s string) string     { return a.style("37", s) }
func (a *App) amber(s string) string     { return a.style("33", s) }
func (a *App) amberBold(s string) string { return a.style("1;33", s) }
func (a *App) green(s string) string     { return a.style("32", s) }
func (a *App) red(s string) string       { return a.style("31", s) }
func (a *App) cyan(s string) string      { return a.style("36", s) }

// PrintHeader renders the Variant 1 «oh my boot_» banner with subtitle.
func (a *App) PrintHeader(subtitle string) {
	fmt.Fprintf(a.out(), "%s%s  %s\n%s\n",
		a.white("oh my boot"),
		a.amberBold("_"),
		a.dim(subtitle),
		a.dim(ruleLine),
	)
}

// PrintOK renders a green checkmark line.
func (a *App) PrintOK(msg string) {
	fmt.Fprintf(a.out(), "  %s %s\n", a.green("✓"), msg)
}

// PrintStep renders an active step line with amber arrow.
func (a *App) PrintStep(msg string) {
	fmt.Fprintf(a.out(), "  %s %s\n", a.amberBold("▸"), msg)
}

// PrintWarn renders an amber warning line.
func (a *App) PrintWarn(msg string) {
	fmt.Fprintf(a.out(), "  %s %s\n", a.amber("!"), a.amber(msg))
}

// PrintError renders a red error line to ErrOut.
func (a *App) PrintError(msg string) {
	fmt.Fprintf(a.errOut(), "  %s %s\n", a.red("✗"), a.red(msg))
}

func (a *App) printPrompt(label, def string) {
	if def != "" {
		fmt.Fprintf(a.out(), "  %s %s %s: ", a.amberBold("▸"), label, a.dim("["+def+"]"))
		return
	}
	fmt.Fprintf(a.out(), "  %s %s: ", a.amberBold("▸"), label)
}

func (a *App) printKV(label, val string) {
	fmt.Fprintf(a.out(), "  %s %s\n", a.muted(padRightRunes(label, 18)), val)
}

func padRightRunes(s string, width int) string {
	n := utf8.RuneCountInString(s)
	if n >= width {
		return s
	}
	return s + strings.Repeat(" ", width-n)
}

// ReadReleaseMeta reads /usr/share/omb/release.json if present.
func (a *App) ReadReleaseMeta() (ReleaseMeta, error) {
	raw, err := os.ReadFile(a.resolve(DefaultReleaseJSONPath))
	if err != nil {
		return ReleaseMeta{}, err
	}
	var meta ReleaseMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return ReleaseMeta{}, fmt.Errorf("parse %s: %w", DefaultReleaseJSONPath, err)
	}
	return meta, nil
}

// ReadStateFile reads a trimmed single-line state file from the deployment state directory.
func (a *App) ReadStateFile(name string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(a.stateDir(), name))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

// ParseEnvFile parses KEY=VALUE lines (with optional single or double quotes) from an env file.
func ParseEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	values := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 {
			if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
				v = v[1 : len(v)-1]
			}
		}
		if k != "" {
			values[k] = v
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".omb-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	success = true
	return nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyFileAtomic(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return writeFileAtomic(dst, data, mode)
}

func readLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if errors.Is(err, io.EOF) && line == "" {
		return "", io.EOF
	}
	return strings.TrimSpace(line), nil
}

func parseYesNo(ans string, defaultYes bool) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(ans)) {
	case "":
		return defaultYes, true
	case "y", "yes", "д", "да":
		return true, true
	case "n", "no", "н", "нет":
		return false, true
	default:
		return false, false
	}
}
