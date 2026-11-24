package ombcli

import (
	"bufio"
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// ChannelOptions configures `omb channel [stable|dev]`.
type ChannelOptions struct {
	Target   string
	NoUpdate bool
	Yes      bool
	Repo     string
}

// CurrentChannel returns the configured release channel from /var/lib/omb-deployment/CHANNEL,
// falling back to /usr/share/omb/release.json, and finally "stable".
func (a *App) CurrentChannel() string {
	if ch, err := a.ReadStateFile("CHANNEL"); err == nil && (ch == "stable" || ch == "dev") {
		return ch
	}
	if meta, err := a.ReadReleaseMeta(); err == nil && (meta.Channel == "stable" || meta.Channel == "dev") {
		return meta.Channel
	}
	return "stable"
}

// CurrentInstallMode returns the recorded install mode ("online", "offline", or "online" if unset).
func (a *App) CurrentInstallMode() string {
	if mode, err := a.ReadStateFile("INSTALL_MODE"); err == nil && (mode == "online" || mode == "offline") {
		return mode
	}
	return "online"
}

// Channel inspects or switches the OMB release channel.
func (a *App) Channel(ctx context.Context, opts ChannelOptions) error {
	current := a.CurrentChannel()
	installMode := a.CurrentInstallMode()

	version := "unknown"
	if meta, err := a.ReadReleaseMeta(); err == nil {
		if meta.DisplayVersion != "" {
			version = meta.DisplayVersion
		} else if meta.Version != "" {
			version = meta.Version
		}
	} else if ver, _, ok := a.queryInstalledPackage(ctx); ok {
		version = ver
	}

	target := strings.ToLower(strings.TrimSpace(opts.Target))
	if target == "" {
		a.PrintHeader(a.t("канал обновлений", "release channel"))
		chStyled := current
		if current == "stable" {
			chStyled = a.green("stable")
		} else if current == "dev" {
			chStyled = a.amber("dev")
		}
		a.printKV(a.t("Канал:", "Channel:"), chStyled)
		a.printKV(a.t("Версия:", "Version:"), a.white(version))
		a.printKV(a.t("Режим установки:", "Install mode:"), installMode)
		fmt.Fprintf(a.out(), "%s\n", a.dim(ruleLine))
		return nil
	}

	if target != "stable" && target != "dev" {
		return fmt.Errorf("%s", a.t(
			fmt.Sprintf("неподдерживаемый канал %q (ожидается stable или dev)", opts.Target),
			fmt.Sprintf("unsupported channel %q (expected stable or dev)", opts.Target),
		))
	}

	if err := a.requireRoot(); err != nil {
		return err
	}

	a.PrintHeader(a.t("переключение канала обновлений", "switch release channel"))

	channelPath := filepath.Join(a.stateDir(), "CHANNEL")
	if err := writeFileAtomic(channelPath, []byte(target+"\n"), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", channelPath, err)
	}

	if target == current {
		a.PrintOK(a.t(
			fmt.Sprintf("Канал уже установлен на %s (%s).", target, channelPath),
			fmt.Sprintf("Channel is already set to %s (%s).", target, channelPath),
		))
		return nil
	}

	a.PrintOK(a.t(
		fmt.Sprintf("Канал обновлений переключён: %s → %s.", current, target),
		fmt.Sprintf("Release channel switched: %s -> %s.", current, target),
	))

	if installMode == "offline" {
		a.PrintWarn(a.t(
			"Сервер установлен в офлайн-режиме; автоматическая загрузка из сети отключена.",
			"Server is in offline install mode; online download is disabled.",
		))
		fmt.Fprintf(a.out(), "  %s %s\n",
			a.muted(a.t("Следующий шаг:", "Next step:")),
			a.white("sudo bash offline-deploy.sh update --allow-channel-switch <repo> <public-key>"),
		)
		return nil
	}

	if opts.NoUpdate {
		fmt.Fprintf(a.out(), "  %s %s\n",
			a.muted(a.t("Чтобы установить пакет из новой ветки, выполните:", "To install a package from the new channel, run:")),
			a.white("sudo omb update"),
		)
		return nil
	}

	runUpdate := opts.Yes
	if !runUpdate {
		reader := bufio.NewReader(a.in())
		for {
			a.printPrompt(a.t(
				fmt.Sprintf("Обновить OMB до последнего выпуска канала %s сейчас?", target),
				fmt.Sprintf("Update OMB to the latest %s release now?", target),
			), "Y/n")
			ans, err := readLine(reader)
			if err != nil {
				return err
			}
			yes, ok := parseYesNo(ans, true)
			if !ok {
				a.PrintError(a.t("Введите Y или n.", "Enter Y or n."))
				continue
			}
			runUpdate = yes
			break
		}
	}

	if !runUpdate {
		fmt.Fprintf(a.out(), "  %s %s\n",
			a.muted(a.t("Вы можете выполнить обновление позже командой:", "You can run the update later with:")),
			a.white("sudo omb update"),
		)
		return nil
	}

	return a.Update(ctx, UpdateOptions{
		Channel:            target,
		Repo:               opts.Repo,
		Yes:                true,
		AllowChannelSwitch: true,
	})
}
