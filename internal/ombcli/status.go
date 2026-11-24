package ombcli

import (
	"context"
	"fmt"
	"strings"

	"omb/internal/setup"
)

// StatusServices is the canonical list of 7 services inspected by `omb status`.
var StatusServices = []string{
	"postgresql",
	"omb-boot",
	"omb-agent",
	"omb-images",
	"omb-control",
	"omb-dnsmasq",
	"smbd",
}

// ServiceStatus holds the live systemd state of a single service.
type ServiceStatus struct {
	Name  string
	State string
}

// StatusReport contains the collected runtime status of an OMB host.
type StatusReport struct {
	Version     string
	Commit      string
	Channel     string
	InstallMode string
	UFWActive   bool
	Services    []ServiceStatus
}

// CollectStatus gathers installed release metadata, deployment state, and live service states.
func (a *App) CollectStatus(ctx context.Context) StatusReport {
	report := StatusReport{
		Version:     "unknown",
		Channel:     "unknown",
		InstallMode: "unknown",
	}

	if meta, err := a.ReadReleaseMeta(); err == nil {
		if meta.DisplayVersion != "" {
			report.Version = meta.DisplayVersion
		} else if meta.Version != "" {
			report.Version = meta.Version
		}
		if meta.Commit != "" {
			report.Commit = meta.Commit
		}
		if meta.Channel != "" {
			report.Channel = meta.Channel
		}
	}

	if report.Version == "unknown" || report.Commit == "" {
		if ver, commit, ok := a.queryInstalledPackage(ctx); ok {
			if report.Version == "unknown" && ver != "" {
				report.Version = ver
			}
			if report.Commit == "" && commit != "" {
				report.Commit = commit
			}
		}
	}

	if ch, err := a.ReadStateFile("CHANNEL"); err == nil && ch != "" {
		report.Channel = ch
	}
	if mode, err := a.ReadStateFile("INSTALL_MODE"); err == nil && mode != "" {
		report.InstallMode = mode
	}

	report.UFWActive, _ = setup.DetectUFWActive(ctx, a.runner())

	report.Services = make([]ServiceStatus, 0, len(StatusServices))
	for _, svc := range StatusServices {
		out, err := a.run(ctx, "systemctl", "is-active", svc)
		state := strings.TrimSpace(string(out))
		if state == "" {
			if err != nil {
				state = "unknown"
			} else {
				state = "active"
			}
		}
		report.Services = append(report.Services, ServiceStatus{
			Name:  svc,
			State: state,
		})
	}

	return report
}

// queryInstalledPackage queries dpkg-query for the installed omb package version and commit.
func (a *App) queryInstalledPackage(ctx context.Context) (version, commit string, ok bool) {
	out, err := a.run(ctx, "dpkg-query", "-W", "-f=${db:Status-Status}\t${Version}\t${X-OMB-Commit}\n", "omb")
	if err != nil {
		return "", "", false
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return "", "", false
	}
	parts := strings.Split(line, "\t")
	if len(parts) < 2 || parts[0] != "installed" {
		return "", "", false
	}
	ver := strings.TrimSpace(parts[1])
	var cmt string
	if len(parts) >= 3 {
		cmt = strings.TrimSpace(parts[2])
	}
	return ver, cmt, ver != ""
}

// Status collects and prints the OMB server status in Variant 1 «Инженерный пульт» style.
func (a *App) Status(ctx context.Context) error {
	report := a.CollectStatus(ctx)

	a.PrintHeader(a.t("статус сервера", "server status"))

	verText := a.white(report.Version)
	if report.Commit != "" {
		short := report.Commit
		if len(short) > 10 {
			short = short[:10]
		}
		verText += " " + a.dim("("+short+")")
	}
	a.printKV(a.t("Версия:", "Version:"), verText)

	chVal := report.Channel
	switch chVal {
	case "stable":
		chVal = a.green("stable")
	case "dev":
		chVal = a.amber("dev")
	}
	a.printKV(a.t("Канал:", "Channel:"), chVal)
	a.printKV(a.t("Режим установки:", "Install mode:"), report.InstallMode)

	var fwVal string
	if report.UFWActive {
		fwVal = a.amber(a.t(
			"UFW активен (проверьте правила: 8443/tcp, 8080/tcp, 67/69/4011/udp, 445/tcp)",
			"UFW active (check rules: 8443/tcp, 8080/tcp, 67/69/4011/udp, 445/tcp)",
		))
	} else {
		fwVal = a.dim(a.t(
			"UFW выключен (порты открыты)",
			"UFW inactive (ports open)",
		))
	}
	a.printKV(a.t("Файрвол:", "Firewall:"), fwVal)

	fmt.Fprintf(a.out(), "\n  %s\n", a.white(a.t("Службы:", "Services:")))
	for _, svc := range report.Services {
		var icon, stateText string
		switch svc.State {
		case "active":
			icon = a.green("✓")
			stateText = a.green(svc.State)
		case "inactive", "deactivating":
			icon = a.dim("·")
			stateText = a.dim(svc.State)
		case "activating", "reloading":
			icon = a.amber("▸")
			stateText = a.amber(svc.State)
		default:
			icon = a.red("✗")
			stateText = a.red(svc.State)
		}
		fmt.Fprintf(a.out(), "    %s %s %s\n", icon, padRightRunes(svc.Name, 16), stateText)
	}
	fmt.Fprintf(a.out(), "%s\n", a.dim(ruleLine))
	return nil
}
