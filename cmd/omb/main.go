package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	"omb/internal/ombcli"
	"omb/internal/setup"
)

var version = "dev"

func main() {
	unix.Umask(0o022)
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	defaultLang, _ := ombcli.DetectLang("", getenv)
	color := setup.ColorEnabled(stdout, getenv)

	if len(args) == 0 {
		printUsage(stdout, defaultLang, color)
		return 0
	}

	subcmd := args[0]
	rest := args[1:]

	switch subcmd {
	case "help", "-h", "--help":
		lang := defaultLang
		if len(rest) >= 2 && (rest[0] == "--lang" || rest[0] == "-lang") {
			if l, err := ombcli.DetectLang(rest[1], getenv); err == nil {
				lang = l
			}
		}
		printUsage(stdout, lang, color)
		return 0
	case "version", "-v", "--version":
		fmt.Fprintf(stdout, "omb %s\n", version)
		return 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	newApp := func(flagLang string) (*ombcli.App, error) {
		lang, err := ombcli.DetectLang(flagLang, getenv)
		if err != nil {
			return nil, err
		}
		return &ombcli.App{
			Version: version,
			Lang:    lang,
			Color:   color,
			In:      stdin,
			Out:     stdout,
			ErrOut:  stderr,
		}, nil
	}

	switch subcmd {
	case "status":
		fs := flag.NewFlagSet("omb status", flag.ContinueOnError)
		fs.SetOutput(stderr)
		langFlag := fs.String("lang", "", "output language (ru or en)")
		if err := fs.Parse(reorderArgs(rest, map[string]bool{"lang": true})); err != nil {
			if err == flag.ErrHelp {
				return 0
			}
			return 2
		}
		app, err := newApp(*langFlag)
		if err != nil {
			fmt.Fprintf(stderr, "omb: %v\n", err)
			return 2
		}
		if err := app.Status(ctx); err != nil {
			app.PrintError(err.Error())
			return 1
		}
		return 0

	case "reset-password":
		fs := flag.NewFlagSet("omb reset-password", flag.ContinueOnError)
		fs.SetOutput(stderr)
		langFlag := fs.String("lang", "", "output language (ru or en)")
		pwStdin := fs.Bool("password-stdin", false, "read new password from stdin")
		if err := fs.Parse(reorderArgs(rest, map[string]bool{"lang": true})); err != nil {
			if err == flag.ErrHelp {
				return 0
			}
			return 2
		}
		if fs.NArg() > 1 {
			fmt.Fprintf(stderr, "omb reset-password: too many arguments\n")
			return 2
		}
		app, err := newApp(*langFlag)
		if err != nil {
			fmt.Fprintf(stderr, "omb: %v\n", err)
			return 2
		}
		if !*pwStdin {
			app.ReadSecret = ttySecretReader(stdin)
		}
		if err := app.ResetPassword(ctx, ombcli.ResetPasswordOptions{
			Username:      fs.Arg(0),
			PasswordStdin: *pwStdin,
		}); err != nil {
			app.PrintError(err.Error())
			return 1
		}
		return 0

	case "reset-2fa":
		fs := flag.NewFlagSet("omb reset-2fa", flag.ContinueOnError)
		fs.SetOutput(stderr)
		langFlag := fs.String("lang", "", "output language (ru or en)")
		yes := fs.Bool("yes", false, "skip confirmation prompt")
		if err := fs.Parse(reorderArgs(rest, map[string]bool{"lang": true})); err != nil {
			if err == flag.ErrHelp {
				return 0
			}
			return 2
		}
		if fs.NArg() > 1 {
			fmt.Fprintf(stderr, "omb reset-2fa: too many arguments\n")
			return 2
		}
		app, err := newApp(*langFlag)
		if err != nil {
			fmt.Fprintf(stderr, "omb: %v\n", err)
			return 2
		}
		if err := app.Reset2FA(ctx, ombcli.Reset2FAOptions{
			Username: fs.Arg(0),
			Yes:      *yes,
		}); err != nil {
			app.PrintError(err.Error())
			return 1
		}
		return 0

	case "channel":
		fs := flag.NewFlagSet("omb channel", flag.ContinueOnError)
		fs.SetOutput(stderr)
		langFlag := fs.String("lang", "", "output language (ru or en)")
		noUpdate := fs.Bool("no-update", false, "switch channel without running an immediate update")
		yes := fs.Bool("yes", false, "automatically confirm immediate update")
		repo := fs.String("repo", "", "GitHub repository for releases (default: f0nwa/oh-my-boot_)")
		if err := fs.Parse(reorderArgs(rest, map[string]bool{"lang": true, "repo": true})); err != nil {
			if err == flag.ErrHelp {
				return 0
			}
			return 2
		}
		if fs.NArg() > 1 {
			fmt.Fprintf(stderr, "omb channel: too many arguments\n")
			return 2
		}
		app, err := newApp(*langFlag)
		if err != nil {
			fmt.Fprintf(stderr, "omb: %v\n", err)
			return 2
		}
		if err := app.Channel(ctx, ombcli.ChannelOptions{
			Target:   fs.Arg(0),
			NoUpdate: *noUpdate,
			Yes:      *yes,
			Repo:     *repo,
		}); err != nil {
			app.PrintError(err.Error())
			return 1
		}
		return 0

	case "update":
		fs := flag.NewFlagSet("omb update", flag.ContinueOnError)
		fs.SetOutput(stderr)
		langFlag := fs.String("lang", "", "output language (ru or en)")
		ch := fs.String("channel", "", "release channel (stable or dev)")
		ver := fs.String("version", "", "explicit release version or tag (e.g. v0.2.0)")
		repo := fs.String("repo", "", "GitHub repository for releases (default: f0nwa/oh-my-boot_)")
		yes := fs.Bool("yes", false, "skip confirmation prompt")
		if err := fs.Parse(reorderArgs(rest, map[string]bool{"lang": true, "channel": true, "version": true, "repo": true})); err != nil {
			if err == flag.ErrHelp {
				return 0
			}
			return 2
		}
		if fs.NArg() > 0 {
			fmt.Fprintf(stderr, "omb update: unexpected arguments: %s\n", strings.Join(fs.Args(), " "))
			return 2
		}
		app, err := newApp(*langFlag)
		if err != nil {
			fmt.Fprintf(stderr, "omb: %v\n", err)
			return 2
		}
		if err := app.Update(ctx, ombcli.UpdateOptions{
			Channel: *ch,
			Version: *ver,
			Repo:    *repo,
			Yes:     *yes,
		}); err != nil {
			app.PrintError(err.Error())
			return 1
		}
		return 0

	default:
		fmt.Fprintf(stderr, "omb: unknown command %q\n", subcmd)
		printUsage(stderr, defaultLang, false)
		return 2
	}
}

func ttySecretReader(stdin io.Reader) func() (string, error) {
	return func() (string, error) {
		if f, ok := stdin.(*os.File); ok && setup.IsTerminal(int(f.Fd())) {
			return setup.ReadPasswordTTY(int(f.Fd()))
		}
		tty, err := os.Open("/dev/tty")
		if err != nil {
			return "", fmt.Errorf("interactive password entry requires a TTY (use --password-stdin for automation)")
		}
		defer tty.Close()
		if !setup.IsTerminal(int(tty.Fd())) {
			return "", fmt.Errorf("interactive password entry requires a TTY (use --password-stdin for automation)")
		}
		return setup.ReadPasswordTTY(int(tty.Fd()))
	}
}

// reorderArgs moves flags before positional arguments so users can run both
// `omb channel dev --no-update` and `omb channel --no-update dev`.
func reorderArgs(args []string, valueFlags map[string]bool) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i:]...)
			break
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if !strings.Contains(name, "=") && valueFlags[name] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positional = append(positional, a)
	}
	return append(flags, positional...)
}

func printUsage(w io.Writer, lang string, color bool) {
	app := &ombcli.App{
		Lang:  lang,
		Color: color,
		Out:   w,
	}
	if lang == "ru" {
		app.PrintHeader("консольная утилита администрирования")
		fmt.Fprint(w, `Использование:
  omb <команда> [аргументы] [флаги]

Команды:
  status                       Показать версию, канал, режим установки и статус 7 служб
  reset-password [username]    Сбросить пароль локальной учётной записи (admin и др.)
  reset-2fa [username]         Сбросить 2FA (TOTP) локальной учётной записи
  channel [stable|dev]         Показать или переключить канал обновлений (stable/dev)
  update                       Выполнить онлайн-обновление OMB из GitHub Releases
  version                      Показать версию утилиты omb
  help                         Показать эту справку

Примеры:
  sudo omb status
  sudo omb reset-password
  sudo omb reset-password admin
  sudo omb reset-2fa admin
  sudo omb channel dev
  sudo omb update --yes
`)
		return
	}

	app.PrintHeader("administrative CLI")
	fmt.Fprint(w, `Usage:
  omb <command> [arguments] [flags]

Commands:
  status                       Show version, channel, install mode, and 7 service states
  reset-password [username]    Reset password for a local user account (admin, etc.)
  reset-2fa [username]         Reset 2FA (TOTP) for a local user account
  channel [stable|dev]         Show or switch the release channel (stable/dev)
  update                       Perform an online OMB update from GitHub Releases
  version                      Print omb version
  help                         Show this help message

Examples:
  sudo omb status
  sudo omb reset-password
  sudo omb reset-password admin
  sudo omb reset-2fa admin
  sudo omb channel dev
  sudo omb update --yes
`)
}
