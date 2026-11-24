package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"

	"omb/internal/setup"
)

var version = "dev"

func main() {
	unix.Umask(0o022)
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("omb-setup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var (
		lang               = fs.String("lang", "", "UI and initial admin language (ru or en)")
		managementIP       = fs.String("management-ip", "", "IPv4 address for the HTTPS control panel (:8443)")
		pxeInterface       = fs.String("pxe-interface", "", "Network interface for PXE boot (or none)")
		dhcpMode           = fs.String("dhcp-mode", "", "DHCP mode: proxy, full, or skip")
		dhcpStart          = fs.String("dhcp-start", "", "Start IPv4 address for full DHCP pool")
		dhcpEnd            = fs.String("dhcp-end", "", "End IPv4 address for full DHCP pool")
		adminPasswordStdin = fs.Bool("admin-password-stdin", false, "Read initial admin password from stdin")
		skipPXE            = fs.Bool("skip-pxe", false, "Skip PXE/DHCP configuration and configure later in web panel")
		nonInteractive     = fs.Bool("non-interactive", false, "Run without interactive prompts")
		stateDir           = fs.String("state-dir", "/var/lib/omb-deployment", "Directory for deployment state metadata")
		installMode        = fs.String("install-mode", "online", "Installation mode recorded in state dir (online or offline)")
		channel            = fs.String("channel", "", "Release channel recorded in state dir (stable or dev)")
		showVersion        = fs.Bool("version", false, "Print version and exit")
	)

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	if *showVersion {
		fmt.Printf("omb-setup %s\n", version)
		return 0
	}

	resolvedChannel, err := setup.ResolveChannel(*channel, "/usr/share/omb/release.json")
	if err != nil {
		ui := setup.NewUI(os.Stderr, *lang, setup.ColorEnabled(os.Stderr, os.Getenv), version)
		ui.Error(err.Error())
		return 1
	}

	color := setup.ColorEnabled(os.Stdout, os.Getenv)

	installer := &setup.Installer{}
	if err := installer.Preflight(); err != nil {
		ui := setup.NewUI(os.Stderr, *lang, setup.ColorEnabled(os.Stderr, os.Getenv), version)
		ui.Error(fmt.Sprintf("preflight failed: %v", err))
		return 1
	}

	ifaces, err := setup.DetectInterfaces()
	if err != nil {
		ui := setup.NewUI(os.Stderr, *lang, setup.ColorEnabled(os.Stderr, os.Getenv), version)
		ui.Error(fmt.Sprintf("detect network interfaces: %v", err))
		return 1
	}

	preset := setup.Options{
		Lang:         *lang,
		ManagementIP: *managementIP,
		PXEInterface: *pxeInterface,
		DHCPMode:     *dhcpMode,
		DHCPStart:    *dhcpStart,
		DHCPEnd:      *dhcpEnd,
		StateDir:     *stateDir,
		InstallMode:  *installMode,
		Channel:      resolvedChannel,
		SkipPXE:      *skipPXE,
		Version:      version,
	}

	var opts setup.Options
	if *nonInteractive {
		opts, err = setup.ResolveNonInteractive(preset, ifaces, os.Stdin, *adminPasswordStdin)
		if err != nil {
			ui := setup.NewUI(os.Stderr, *lang, setup.ColorEnabled(os.Stderr, os.Getenv), version)
			ui.Error(err.Error())
			return 1
		}
	} else {
		in, ttyFile, err := openInteractiveInput()
		if err != nil {
			ui := setup.NewUI(os.Stderr, *lang, setup.ColorEnabled(os.Stderr, os.Getenv), version)
			ui.Error(err.Error())
			return 1
		}
		if ttyFile != nil && ttyFile != os.Stdin {
			defer ttyFile.Close()
		}

		readSecret := func() (string, error) {
			if ttyFile != nil {
				return setup.ReadPasswordTTY(int(ttyFile.Fd()))
			}
			return "", fmt.Errorf("interactive password entry requires a TTY")
		}

		var confirmed bool
		opts, confirmed, err = setup.RunWizard(in, os.Stdout, readSecret, ifaces, preset, color)
		if err != nil {
			ui := setup.NewUI(os.Stderr, opts.Lang, setup.ColorEnabled(os.Stderr, os.Getenv), version)
			ui.Error(err.Error())
			return 1
		}
		if !confirmed {
			ui := setup.NewUI(os.Stdout, opts.Lang, color, version)
			if opts.Lang == "en" {
				ui.Warn("Installation cancelled by user.")
			} else {
				ui.Warn("Установка отменена пользователем.")
			}
			return 0
		}
	}

	ui := setup.NewUI(os.Stdout, opts.Lang, color, version)
	if *nonInteractive {
		ui.Header(version)
	}
	ui.ProgressHeader()
	installer.Progress = func(step, msg string) {
		ui.ProgressStep(step, msg)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := installer.Execute(ctx, opts); err != nil {
		errUI := setup.NewUI(os.Stderr, opts.Lang, setup.ColorEnabled(os.Stderr, os.Getenv), version)
		errUI.Error(fmt.Sprintf("installation failed: %v", err))
		return 1
	}

	ui.ReadyBanner(opts)
	return 0
}

// openInteractiveInput returns an interactive TTY reader, falling back to /dev/tty
// when stdin is a pipe (e.g. `curl ... | sudo bash`).
func openInteractiveInput() (io.Reader, *os.File, error) {
	if setup.IsTerminal(int(os.Stdin.Fd())) {
		return os.Stdin, os.Stdin, nil
	}
	tty, err := os.Open("/dev/tty")
	if err != nil {
		return nil, nil, fmt.Errorf("interactive mode requires a terminal (stdin is not a TTY and /dev/tty could not be opened: %w); use --non-interactive for automated setup", err)
	}
	if !setup.IsTerminal(int(tty.Fd())) {
		tty.Close()
		return nil, nil, fmt.Errorf("interactive mode requires a terminal; use --non-interactive for automated setup")
	}
	return tty, tty, nil
}
