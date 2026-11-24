package ombcli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"omb/internal/setup"
)

const localUsersQuery = "SELECT username, role, enabled, totp_enabled FROM public.users WHERE auth_source = 'local' ORDER BY id"

// LocalUser represents a local account stored in omb_control.public.users.
type LocalUser struct {
	Username    string
	Role        string
	Enabled     bool
	TOTPEnabled bool
}

// ResetPasswordOptions configures `omb reset-password`.
type ResetPasswordOptions struct {
	Username      string
	PasswordStdin bool
}

// Reset2FAOptions configures `omb reset-2fa`.
type Reset2FAOptions struct {
	Username string
	Yes      bool
}

// ReadControlDSN reads OMB_CONTROL_DATABASE_URL from /etc/omb/control.env.
func (a *App) ReadControlDSN() (string, error) {
	path := a.resolve(DefaultControlEnvPath)
	envMap, err := ParseEnvFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", DefaultControlEnvPath, err)
	}
	dsn := strings.TrimSpace(envMap["OMB_CONTROL_DATABASE_URL"])
	if dsn == "" {
		return "", fmt.Errorf("OMB_CONTROL_DATABASE_URL is missing in %s", DefaultControlEnvPath)
	}
	return dsn, nil
}

// ListLocalUsers queries all local accounts from omb_control.public.users.
func (a *App) ListLocalUsers(ctx context.Context, dsn string) ([]LocalUser, error) {
	out, err := a.run(ctx, "runuser", "-u", "omb-control", "--",
		"psql", "-Xw", "-At", "-F", "\t", "-d", dsn, "-c", localUsersQuery)
	if err != nil {
		return nil, fmt.Errorf("list local users: %w", err)
	}
	var users []LocalUser
	for _, raw := range strings.Split(string(out), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 4 {
			return nil, fmt.Errorf("unexpected user row from psql: %q", line)
		}
		users = append(users, LocalUser{
			Username:    parts[0],
			Role:        parts[1],
			Enabled:     parsePGTruthy(parts[2]),
			TOTPEnabled: parsePGTruthy(parts[3]),
		})
	}
	return users, nil
}

func parsePGTruthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "t", "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

func (a *App) lookupAuthSource(ctx context.Context, dsn, username string) string {
	const q = "SELECT auth_source FROM public.users WHERE username = :'target_user' LIMIT 1;\n"
	out, err := a.runSpec(ctx, setup.CmdSpec{
		Name: "runuser",
		Args: []string{
			"-u", "omb-control", "--",
			"psql", "-Xw", "-At", "-d", dsn,
			"-v", "target_user=" + username,
		},
		Stdin: []byte(q),
	})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (a *App) selectLocalUser(ctx context.Context, dsn, requested string, reader *bufio.Reader, showTOTP bool) (LocalUser, error) {
	if requested != "" && strings.ContainsAny(requested, "\r\n\x00") {
		return LocalUser{}, fmt.Errorf("%s", a.t("некорректное имя пользователя", "invalid username"))
	}

	users, err := a.ListLocalUsers(ctx, dsn)
	if err != nil {
		return LocalUser{}, err
	}

	if requested != "" {
		for _, u := range users {
			if u.Username == requested {
				return u, nil
			}
		}
		if src := a.lookupAuthSource(ctx, dsn, requested); src != "" && src != "local" {
			return LocalUser{}, fmt.Errorf("%s", a.t(
				fmt.Sprintf("пользователь %q является внешним (%s); локальный сброс доступен только для учётных записей auth_source=local", requested, src),
				fmt.Sprintf("user %q is an external account (%s); local reset is only supported for auth_source=local accounts", requested, src),
			))
		}
		return LocalUser{}, fmt.Errorf("%s", a.t(
			fmt.Sprintf("локальный пользователь %q не найден", requested),
			fmt.Sprintf("local user %q not found", requested),
		))
	}

	if len(users) == 0 {
		return LocalUser{}, fmt.Errorf("%s", a.t(
			"локальные пользователи не найдены",
			"no local users found",
		))
	}

	fmt.Fprintf(a.out(), "  %s\n", a.white(a.t("Локальные пользователи:", "Local users:")))
	for i, u := range users {
		status := a.t("активен", "enabled")
		if !u.Enabled {
			status = a.t("отключён", "disabled")
		}
		meta := fmt.Sprintf("(%s, %s", u.Role, status)
		if showTOTP {
			if u.TOTPEnabled {
				meta += ", 2FA: " + a.t("вкл", "on")
			} else {
				meta += ", 2FA: " + a.t("выкл", "off")
			}
		}
		meta += ")"
		fmt.Fprintf(a.out(), "    %s %s  %s\n", a.amber(fmt.Sprintf("%d)", i+1)), a.white(u.Username), a.dim(meta))
	}
	fmt.Fprintln(a.out())

	for {
		a.printPrompt(a.t("Выберите пользователя", "Select user"), "1")
		ans, err := readLine(reader)
		if err != nil {
			return LocalUser{}, fmt.Errorf("%s: %w", a.t("не удалось прочитать выбор пользователя", "failed to read user selection"), err)
		}
		if ans == "" {
			ans = "1"
		}
		if idx, err := strconv.Atoi(ans); err == nil && idx >= 1 && idx <= len(users) {
			return users[idx-1], nil
		}
		for _, u := range users {
			if u.Username == ans {
				return u, nil
			}
		}
		a.PrintError(a.t("Введите номер из списка или имя пользователя.", "Enter a number from the list or a username."))
	}
}

func (a *App) readSecretPassword() (string, error) {
	if a.ReadSecret != nil {
		return a.ReadSecret()
	}
	if f, ok := a.in().(*os.File); ok && setup.IsTerminal(int(f.Fd())) {
		return setup.ReadPasswordTTY(int(f.Fd()))
	}
	return "", fmt.Errorf("%s", a.t(
		"интерактивный ввод пароля требует TTY (используйте --password-stdin для автоматизации)",
		"interactive password entry requires a TTY (use --password-stdin for automation)",
	))
}

// ResetPassword resets a local user's password via omb-controlctl.
func (a *App) ResetPassword(ctx context.Context, opts ResetPasswordOptions) error {
	if err := a.requireRoot(); err != nil {
		return err
	}
	dsn, err := a.ReadControlDSN()
	if err != nil {
		return err
	}

	if opts.PasswordStdin && opts.Username == "" {
		return fmt.Errorf("%s", a.t(
			"при использовании --password-stdin необходимо явно указать имя пользователя",
			"username must be specified explicitly when using --password-stdin",
		))
	}

	a.PrintHeader(a.t("сброс пароля локального пользователя", "local user password reset"))

	reader := bufio.NewReader(a.in())
	target, err := a.selectLocalUser(ctx, dsn, opts.Username, reader, false)
	if err != nil {
		return err
	}

	var password string
	if opts.PasswordStdin {
		raw, err := io.ReadAll(io.LimitReader(a.in(), 4096))
		if err != nil {
			return fmt.Errorf("read password from stdin: %w", err)
		}
		pw := strings.TrimSuffix(string(raw), "\n")
		pw = strings.TrimSuffix(pw, "\r")
		if err := ValidatePassword(pw); err != nil {
			return fmt.Errorf("%s: %w", a.t("некорректный пароль", "invalid password"), err)
		}
		password = pw
	} else {
		fmt.Fprintf(a.out(), "  %s\n\n", a.dim(a.t(
			"Минимум 12 символов. Ввод скрыт.",
			"At least 12 characters. Input is hidden.",
		)))
		for {
			a.printPrompt(a.t("Новый пароль для "+target.Username, "New password for "+target.Username), "")
			pw1, err := a.readSecretPassword()
			fmt.Fprintln(a.out())
			if err != nil {
				return err
			}
			if err := ValidatePassword(pw1); err != nil {
				if strings.ContainsAny(pw1, "\r\n") {
					a.PrintError(a.t("Пароль не должен содержать переносы строк.", "Password must not contain newline characters."))
				} else {
					a.PrintError(a.t("Пароль должен содержать минимум 12 символов.", "Password must be at least 12 characters."))
				}
				continue
			}

			a.printPrompt(a.t("Повторите пароль", "Repeat password"), "")
			pw2, err := a.readSecretPassword()
			fmt.Fprintln(a.out())
			if err != nil {
				return err
			}
			if pw1 != pw2 {
				a.PrintError(a.t("Пароли не совпадают. Попробуйте снова.", "Passwords do not match. Please try again."))
				continue
			}
			password = pw1
			break
		}
	}

	if _, err := a.runSpec(ctx, setup.CmdSpec{
		Name: "runuser",
		Args: []string{
			"-u", "omb-control", "--",
			"env", "OMB_CONTROL_DATABASE_URL=" + dsn,
			"/usr/bin/omb-controlctl", "reset-password", "-username", target.Username,
		},
		Stdin: []byte(password + "\n"),
	}); err != nil {
		return fmt.Errorf("reset password for %s: %w", target.Username, err)
	}

	a.PrintOK(a.t(
		fmt.Sprintf("Пароль пользователя %s изменён; прежние сеансы завершены.", target.Username),
		fmt.Sprintf("Password for user %s has been updated; previous sessions revoked.", target.Username),
	))
	return nil
}

// Reset2FA resets TOTP 2FA for a local user via omb-controlctl.
func (a *App) Reset2FA(ctx context.Context, opts Reset2FAOptions) error {
	if err := a.requireRoot(); err != nil {
		return err
	}
	dsn, err := a.ReadControlDSN()
	if err != nil {
		return err
	}

	a.PrintHeader(a.t("сброс 2FA локального пользователя", "local user 2FA reset"))

	reader := bufio.NewReader(a.in())
	target, err := a.selectLocalUser(ctx, dsn, opts.Username, reader, true)
	if err != nil {
		return err
	}

	if !opts.Yes {
		for {
			a.printPrompt(a.t(
				fmt.Sprintf("Сбросить 2FA для пользователя %s?", target.Username),
				fmt.Sprintf("Reset 2FA for user %s?", target.Username),
			), "y/N")
			ans, err := readLine(reader)
			if err != nil {
				return err
			}
			yes, ok := parseYesNo(ans, false)
			if !ok {
				a.PrintError(a.t("Введите y или N.", "Enter y or N."))
				continue
			}
			if !yes {
				a.PrintWarn(a.t("Сброс 2FA отменён.", "2FA reset cancelled."))
				return nil
			}
			break
		}
	}

	if _, err := a.runSpec(ctx, setup.CmdSpec{
		Name: "runuser",
		Args: []string{
			"-u", "omb-control", "--",
			"env", "OMB_CONTROL_DATABASE_URL=" + dsn,
			"/usr/bin/omb-controlctl", "reset-2fa", "-username", target.Username,
		},
	}); err != nil {
		return fmt.Errorf("reset 2FA for %s: %w", target.Username, err)
	}

	a.PrintOK(a.t(
		fmt.Sprintf("2FA пользователя %s сброшена; прежние сеансы завершены.", target.Username),
		fmt.Sprintf("2FA for user %s has been reset; previous sessions revoked.", target.Username),
	))
	return nil
}
