package setup

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const ruleLine = "────────────────────────────────────────────────────────────────"

// IsTerminal reports whether fd refers to an interactive terminal device.
func IsTerminal(fd int) bool {
	_, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	return err == nil
}

// ColorEnabled determines whether ANSI color sequences should be emitted to out.
// It honors NO_COLOR (any non-empty value), TERM=dumb, and TTY detection.
func ColorEnabled(out io.Writer, env func(string) string) bool {
	if env != nil {
		if env("NO_COLOR") != "" {
			return false
		}
		if strings.EqualFold(strings.TrimSpace(env("TERM")), "dumb") {
			return false
		}
	}
	f, ok := out.(*os.File)
	if !ok {
		return false
	}
	return IsTerminal(int(f.Fd()))
}

// UI renders Variant 1 («Инженерный пульт») terminal output in RU or EN.
type UI struct {
	Out     io.Writer
	Color   bool
	Lang    string
	Version string
}

// NewUI constructs a UI writer for the given language and color mode.
func NewUI(out io.Writer, lang string, color bool, version string) *UI {
	if lang != "en" {
		lang = "ru"
	}
	return &UI{
		Out:     out,
		Color:   color,
		Lang:    lang,
		Version: version,
	}
}

func (u *UI) t(ru, en string) string {
	if u.Lang == "en" {
		return en
	}
	return ru
}

func (u *UI) style(code, s string) string {
	if !u.Color || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (u *UI) white(s string) string     { return u.style("1;97", s) }
func (u *UI) dim(s string) string       { return u.style("2", s) }
func (u *UI) muted(s string) string     { return u.style("37", s) }
func (u *UI) amber(s string) string     { return u.style("33", s) }
func (u *UI) amberBold(s string) string { return u.style("1;33", s) }
func (u *UI) green(s string) string     { return u.style("32", s) }
func (u *UI) red(s string) string       { return u.style("31", s) }
func (u *UI) cyan(s string) string      { return u.style("36", s) }
func (u *UI) okBadge(s string) string   { return u.style("1;30;42", s) }
func (u *UI) warnBadge(s string) string { return u.style("1;30;43", s) }

// Header prints the top «oh my boot_» banner and horizontal rule.
func (u *UI) Header(version string) {
	if version == "" {
		version = u.Version
	}
	meta := u.t("первичная настройка сервера", "initial server setup")
	if version != "" {
		meta = version + " · " + meta
	}
	fmt.Fprintf(u.Out, "%s%s  %s\n%s\n\n",
		u.white("oh my boot"),
		u.amberBold("_"),
		u.dim(meta),
		u.dim(ruleLine),
	)
}

// StepTitle prints a numbered step heading such as `[1/5] ...`.
func (u *UI) StepTitle(num, total int, title string) {
	fmt.Fprintf(u.Out, "\n%s %s\n",
		u.dim(fmt.Sprintf("[%d/%d]", num, total)),
		u.white(title),
	)
}

// OK prints a compact green checkmark confirmation line for a completed step.
func (u *UI) OK(label, value, detail string) {
	if detail != "" {
		fmt.Fprintf(u.Out, "%s %s %s %s\n",
			u.green("✓"),
			u.muted(label+":"),
			value,
			u.dim("("+detail+")"),
		)
		return
	}
	fmt.Fprintf(u.Out, "%s %s %s\n",
		u.green("✓"),
		u.muted(label+":"),
		value,
	)
}

// Warn prints an amber warning line.
func (u *UI) Warn(msg string) {
	fmt.Fprintf(u.Out, "  %s %s\n", u.warnBadge("!"), u.amber(msg))
}

// Error prints a validation or runtime error line.
func (u *UI) Error(msg string) {
	fmt.Fprintf(u.Out, "  %s %s\n", u.red("✗"), u.red(msg))
}

// Prompt prints an input prompt without trailing newline.
func (u *UI) Prompt(label, def string) {
	if def != "" {
		fmt.Fprintf(u.Out, "  %s %s %s: ", u.amberBold("▸"), label, u.dim("["+def+"]"))
		return
	}
	fmt.Fprintf(u.Out, "  %s %s: ", u.amberBold("▸"), label)
}

// OptionLine prints a numbered menu option.
func (u *UI) OptionLine(num int, primary, note string, highlightNote bool) {
	if note == "" {
		fmt.Fprintf(u.Out, "    %s %s\n", u.amber(fmt.Sprintf("%d)", num)), primary)
		return
	}
	styledNote := u.dim(note)
	if highlightNote {
		styledNote = u.green(note)
	}
	fmt.Fprintf(u.Out, "    %s %s  %s\n", u.amber(fmt.Sprintf("%d)", num)), primary, styledNote)
}

func padRightRunes(s string, width int) string {
	n := utf8.RuneCountInString(s)
	if n >= width {
		return s
	}
	return s + strings.Repeat(" ", width-n)
}

// SummaryBox prints the step 5 tabular summary before installation.
func (u *UI) SummaryBox(opts Options, mgmtIface string) {
	u.StepTitle(5, 5, u.t("Сводка перед установкой", "Review before install"))
	fmt.Fprintf(u.Out, "%s\n", u.dim(ruleLine))

	row := func(label, val, detail string) {
		lbl := u.muted(padRightRunes(label, 18))
		if detail != "" {
			fmt.Fprintf(u.Out, "  %s %s %s\n", lbl, val, u.dim("("+detail+")"))
			return
		}
		fmt.Fprintf(u.Out, "  %s %s\n", lbl, val)
	}

	row(u.t("Веб-панель", "Web panel"), u.white("https://"+opts.ManagementIP+":8443"), mgmtIface)
	row(u.t("Администратор", "Admin user"), "admin", u.t("язык: "+opts.Lang, "lang: "+opts.Lang))

	if opts.SkipPXE || opts.PXEInterface == "none" || opts.DHCPMode == "skip" {
		pxeText := u.t("отложено (настройка в веб-панели)", "deferred (configure in web panel)")
		if opts.PXEInterface != "" && opts.PXEInterface != "none" {
			pxeText = opts.PXEInterface
		}
		row(u.t("PXE-интерфейс", "PXE interface"), pxeText, "")
		row(u.t("Режим DHCP", "DHCP mode"), u.t("Пропустить", "Skip"), "")
		row(u.t("Samba (ombisos)", "Samba (ombisos)"), u.t("локально (отключено до настройки PXE)", "local-only (disabled until PXE setup)"), "")
		row(u.t("URL меню iPXE", "iPXE menu URL"), fmt.Sprintf("http://%s:8080/boot/menu.ipxe", opts.ManagementIP), "")
	} else {
		row(u.t("PXE-интерфейс", "PXE interface"), opts.PXEInterface, opts.PXEServerIP+", "+opts.PXESubnet)
		dhcpLabel := "Proxy DHCP"
		if opts.DHCPMode == "full" {
			dhcpLabel = fmt.Sprintf("Full DHCP (%s – %s)", opts.DHCPStart, opts.DHCPEnd)
		}
		row(u.t("Режим DHCP", "DHCP mode"), dhcpLabel, "")
		row(u.t("Samba (ombisos)", "Samba (ombisos)"), u.t("автонастройка под "+opts.PXESubnet, "auto-configured for "+opts.PXESubnet), "")
		row(u.t("URL меню iPXE", "iPXE menu URL"), fmt.Sprintf("http://%s:8080/boot/menu.ipxe", opts.PXEServerIP), "")
	}

	fmt.Fprintf(u.Out, "%s\n\n", u.dim(ruleLine))
}

// ProgressHeader prints the start of execution phase.
func (u *UI) ProgressHeader() {
	fmt.Fprintf(u.Out, "\n%s\n", u.white(u.t("Выполнение установки...", "Installing OMB...")))
}

// ProgressStep prints a localized `[✓]` progress line for an Installer step.
func (u *UI) ProgressStep(step, fallbackMsg string) {
	text := u.stepMessage(step, fallbackMsg)
	fmt.Fprintf(u.Out, "  %s %s\n", u.green("[✓]"), text)
}

func (u *UI) stepMessage(step, fallback string) string {
	switch step {
	case "postgres":
		return u.t("PostgreSQL: запуск и проверка чистоты баз", "PostgreSQL: started and verified clean state")
	case "databases":
		return u.t("PostgreSQL: созданы системные пользователи, роли и 3 БД", "PostgreSQL: created system users, roles and 3 databases")
	case "bootstrap":
		return u.t("Схемы БД: применены bootstrap SQL и реестр schema_migrations", "Database schemas: applied bootstrap SQL and schema_migrations")
	case "assets":
		return u.t("Загрузочные файлы: распакованы iPXE, shim и wimboot", "Boot assets: extracted iPXE, shim and wimboot")
	case "config":
		return u.t("Конфигурация: записаны /etc/omb/*.env", "Configuration: wrote /etc/omb/*.env")
	case "secrets":
		return u.t("Безопасность: созданы TLS-сертификат, secrets.key и пароль omb-iso", "Security: generated TLS certificate, secrets.key and omb-iso password")
	case "samba":
		return u.t("Samba: подготовлена базовая локальная конфигурация", "Samba: prepared initial local-only configuration")
	case "admin":
		return u.t("Инициализация: добавлено хранилище Local и учётная запись admin", "Initialization: registered Local storage and admin account")
	case "services":
		return u.t("Службы: запущены omb-boot, omb-agent, omb-images, omb-control", "Services: started omb-boot, omb-agent, omb-images, omb-control")
	case "pxe":
		return u.t("PXE и Samba: применены omb-dnsmasq и smbd", "PXE & Samba: applied omb-dnsmasq and smbd")
	case "state":
		return u.t("Метаданные: записан режим установки", "Metadata: recorded installation mode")
	default:
		if fallback != "" {
			return fallback
		}
		return step
	}
}

// ReadyBanner prints the final READY / ГОТОВО block after successful installation.
func (u *UI) ReadyBanner(opts Options) {
	url := fmt.Sprintf("https://%s:8443", opts.ManagementIP)
	fmt.Fprintf(u.Out, "\n%s %s\n\n",
		u.okBadge(" "+u.t("ГОТОВО", "READY")+" "),
		u.white(u.t("oh my boot_ установлен и запущен", "oh my boot_ is installed and running")),
	)
	fmt.Fprintf(u.Out, "  %s %s\n", u.muted(padRightRunes(u.t("Панель управления:", "Control panel:"), 19)), u.cyan(url))
	fmt.Fprintf(u.Out, "  %s %s\n", u.muted(padRightRunes(u.t("Логин:", "Username:"), 19)), u.white("admin"))
	if !opts.SkipPXE && opts.PXEInterface != "" && opts.PXEInterface != "none" && opts.DHCPMode != "skip" {
		fmt.Fprintf(u.Out, "  %s %s %s\n",
			u.muted(padRightRunes(u.t("PXE-интерфейс:", "PXE interface:"), 19)),
			opts.PXEInterface,
			u.dim("("+opts.DHCPMode+", "+opts.PXESubnet+")"),
		)
	}
	fmt.Fprintf(u.Out, "\n  %s\n",
		u.dim(u.t(
			"Откройте панель в браузере, загрузите ISO и опубликуйте первое меню.",
			"Open the panel in your browser, add an ISO and publish your first menu.",
		)),
	)
}

// UFWWarning prints a concise amber warning when UFW is active after installation.
func (u *UI) UFWWarning() {
	fmt.Fprintln(u.Out)
	u.Warn(u.t(
		"UFW активен: откройте порты — Management: 8443/tcp; PXE: 67,69,4011/udp, 8080,445/tcp",
		"UFW is active: allow ports — Management: 8443/tcp; PXE: 67,69,4011/udp, 8080,445/tcp",
	))
}
