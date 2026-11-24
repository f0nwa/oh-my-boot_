package setup

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

// ReadPasswordTTY reads a line from a TTY file descriptor with terminal echo disabled
// and always restores the original termios settings before returning.
func ReadPasswordTTY(fd int) (string, error) {
	orig, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		return "", fmt.Errorf("get terminal attributes: %w", err)
	}
	noEcho := *orig
	noEcho.Lflag &^= unix.ECHO
	if err := unix.IoctlSetTermios(fd, ioctlWriteTermios, &noEcho); err != nil {
		return "", fmt.Errorf("disable terminal echo: %w", err)
	}
	defer func() {
		_ = unix.IoctlSetTermios(fd, ioctlWriteTermios, orig)
	}()

	var line []byte
	var buf [1]byte
	for {
		n, err := unix.Read(fd, buf[:])
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				return "", fmt.Errorf("password input interrupted")
			}
			return "", fmt.Errorf("read password: %w", err)
		}
		if n == 0 {
			if len(line) == 0 {
				return "", io.EOF
			}
			break
		}
		b := buf[0]
		switch b {
		case '\n', '\r':
			return string(line), nil
		case 0x03: // Ctrl+C
			return "", fmt.Errorf("password input interrupted")
		case 0x04: // Ctrl+D
			if len(line) == 0 {
				return "", io.EOF
			}
			return string(line), nil
		case 0x7f, 0x08: // Backspace / DEL
			if len(line) > 0 {
				_, size := utf8.DecodeLastRune(line)
				if size > 0 && size <= len(line) {
					line = line[:len(line)-size]
				} else {
					line = line[:len(line)-1]
				}
			}
		default:
			line = append(line, b)
		}
	}
	return string(line), nil
}

func validatePassword(pw string) error {
	if strings.ContainsAny(pw, "\r\n") {
		return fmt.Errorf("password must not contain newline characters")
	}
	if len(pw) < 12 {
		return fmt.Errorf("password must be at least 12 characters")
	}
	return nil
}

// RunWizard runs the interactive Variant 1 («Инженерный пульт») setup wizard.
// It returns the populated Options, whether the user confirmed installation, and any error.
func RunWizard(
	in io.Reader,
	out io.Writer,
	readSecret func() (string, error),
	ifaces []NetIface,
	preset Options,
	color bool,
) (Options, bool, error) {
	reader := bufio.NewReader(in)
	opts := preset

	readLine := func() (string, error) {
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		if errors.Is(err, io.EOF) && line == "" {
			return "", fmt.Errorf("unexpected end of input")
		}
		return strings.TrimSpace(line), nil
	}

	if readSecret == nil {
		readSecret = readLine
	}

	ui := NewUI(out, opts.Lang, color, "")
	ui.Header("")

	// Step 0: Language selection
	if opts.Lang != "ru" && opts.Lang != "en" {
		fmt.Fprintf(out, "%s %s\n", ui.amberBold("▸"), ui.white("Language / Язык интерфейса мастера"))
		ui.OptionLine(1, "Русский", "(также будет выбран для учётной записи admin)", false)
		ui.OptionLine(2, "English", "(will also be set for the admin account)", false)
		fmt.Fprintln(out)
		for {
			fmt.Fprintf(out, "  %s ", ui.muted("Выбор / Choice [1]:"))
			ans, err := readLine()
			if err != nil {
				return Options{}, false, err
			}
			switch ans {
			case "", "1":
				opts.Lang = "ru"
			case "2":
				opts.Lang = "en"
			default:
				ui.Error("Enter 1 or 2 / Введите 1 или 2")
				continue
			}
			break
		}
	}
	ui.Lang = opts.Lang
	ui.OK(ui.t("Язык", "Language"), ui.t("Русский", "English"), "")

	// Step 1: Management IP (Web panel HTTPS :8443)
	ui.StepTitle(1, 5, ui.t("Адрес веб-панели управления (HTTPS :8443)", "Web control panel address (HTTPS :8443)"))
	var mgmtAddr netip.Addr
	var mgmtIface string

	if len(ifaces) > 0 {
		fmt.Fprintf(out, "  %s\n", ui.t("Найдены сетевые интерфейсы:", "Detected network interfaces:"))
		for i, nic := range ifaces {
			label := fmt.Sprintf("%-7s %s/%d", ui.white(nic.Name), nic.IP.String(), nic.Prefix.Bits())
			note := ""
			if i == 0 {
				note = "(" + ui.t("по умолчанию", "default") + ")"
			}
			ui.OptionLine(i+1, label, note, false)
		}
		fmt.Fprintln(out)
		for {
			ui.Prompt(ui.t("Выберите адрес панели", "Select panel address"), "1")
			ans, err := readLine()
			if err != nil {
				return Options{}, false, err
			}
			if ans == "" {
				ans = "1"
			}
			if idx, err := strconv.Atoi(ans); err == nil && idx >= 1 && idx <= len(ifaces) {
				nic := ifaces[idx-1]
				mgmtAddr = nic.IP
				mgmtIface = nic.Name
				break
			}
			if addr, err := netip.ParseAddr(ans); err == nil && addr.Is4() {
				mgmtAddr = addr
				mgmtIface = findIfaceNameByIP(ifaces, addr)
				break
			}
			ui.Error(ui.t("Введите номер из списка или корректный IPv4-адрес.", "Enter a number from the list or a valid IPv4 address."))
		}
	} else {
		for {
			ui.Prompt(ui.t("Введите IPv4-адрес веб-панели", "Enter web panel IPv4 address"), "")
			ans, err := readLine()
			if err != nil {
				return Options{}, false, err
			}
			addr, err := netip.ParseAddr(ans)
			if err != nil || !addr.Is4() {
				ui.Error(ui.t("Некорректный IPv4-адрес.", "Invalid IPv4 address."))
				continue
			}
			mgmtAddr = addr
			break
		}
	}
	opts.ManagementIP = mgmtAddr.String()
	ui.OK(ui.t("Веб-панель", "Web panel"), "https://"+opts.ManagementIP+":8443", mgmtIface)

	// Step 2: Admin password
	ui.StepTitle(2, 5, ui.t("Пароль первого администратора (admin)", "First administrator password (admin)"))
	fmt.Fprintf(out, "  %s\n\n", ui.dim(ui.t("Минимум 12 символов. Ввод скрыт.", "At least 12 characters. Input is hidden.")))
	for {
		ui.Prompt(ui.t("Пароль", "Password"), "")
		pw1, err := readSecret()
		fmt.Fprintln(out)
		if err != nil {
			return Options{}, false, err
		}
		if err := validatePassword(pw1); err != nil {
			if strings.ContainsAny(pw1, "\r\n") {
				ui.Error(ui.t("Пароль не должен содержать переносы строк.", "Password must not contain newline characters."))
			} else {
				ui.Error(ui.t("Пароль должен содержать минимум 12 символов.", "Password must be at least 12 characters."))
			}
			continue
		}

		ui.Prompt(ui.t("Повторите пароль", "Repeat password"), "")
		pw2, err := readSecret()
		fmt.Fprintln(out)
		if err != nil {
			return Options{}, false, err
		}
		if pw1 != pw2 {
			ui.Error(ui.t("Пароли не совпадают. Попробуйте снова.", "Passwords do not match. Please try again."))
			continue
		}
		opts.AdminPassword = pw1
		break
	}
	ui.OK(ui.t("Администратор", "Administrator"), "admin", ui.t("пароль задан", "password set"))

	// Step 3: PXE interface
	ui.StepTitle(3, 5, ui.t("Интерфейс для сетевой загрузки (PXE)", "Network boot interface (PXE)"))
	ordered := orderPXECandidates(ifaces, mgmtAddr)
	singleNIC := len(ifaces) == 1
	if singleNIC {
		ui.Warn(ui.t(
			"На сервере найден 1 сетевой интерфейс. Для общей сети рекомендуется Proxy DHCP.",
			"Only 1 network interface found. Proxy DHCP is recommended on shared networks.",
		))
		fmt.Fprintln(out)
	}

	var selectedPXE *NetIface
	if len(ordered) == 0 {
		opts.PXEInterface = "none"
		opts.DHCPMode = "skip"
		opts.SkipPXE = true
		opts.AllowedInterfaces = ResolveAllowedInterfaces(ifaces, mgmtAddr, "none", false)
		ui.OK(ui.t("PXE-интерфейс", "PXE interface"), ui.t("отложено", "deferred"), ui.t("нет доступных интерфейсов", "no interfaces detected"))
	} else {
		for i, nic := range ordered {
			isMgmt := nic.IP == mgmtAddr
			label := fmt.Sprintf("%-7s %s/%d", ui.white(nic.Name), nic.IP.String(), nic.Prefix.Bits())
			note := ""
			highlight := false
			if singleNIC {
				note = "(" + ui.t("общий с веб-панелью", "shared with web panel") + ")"
			} else if !isMgmt && i == 0 {
				note = "(" + ui.t("рекомендуется — отдельный интерфейс", "recommended — dedicated NIC") + ")"
				highlight = true
			} else if isMgmt {
				note = "(" + ui.t("совпадает с веб-панелью", "same as web panel") + ")"
			}
			ui.OptionLine(i+1, label, note, highlight)
		}
		deferOption := len(ordered) + 1
		ui.OptionLine(deferOption, ui.t("Настроить позже в веб-панели", "Configure later in web panel"), "", false)
		fmt.Fprintln(out)

		for {
			ui.Prompt(ui.t("Выберите PXE-интерфейс", "Select PXE interface"), "1")
			ans, err := readLine()
			if err != nil {
				return Options{}, false, err
			}
			if ans == "" {
				ans = "1"
			}
			idx, err := strconv.Atoi(ans)
			if err != nil || idx < 1 || idx > deferOption {
				ui.Error(ui.t("Введите номер из списка.", "Enter a number from the list."))
				continue
			}
			if idx == deferOption {
				opts.PXEInterface = "none"
				opts.DHCPMode = "skip"
				opts.SkipPXE = true
				opts.AllowedInterfaces = ResolveAllowedInterfaces(ifaces, mgmtAddr, "none", false)
				ui.OK(ui.t("PXE-интерфейс", "PXE interface"), ui.t("отложено", "deferred"), ui.t("настройка в веб-панели", "configure in web panel"))
			} else {
				nic := ordered[idx-1]
				selectedPXE = &nic
				opts.PXEInterface = nic.Name
				opts.PXEServerIP = nic.IP.String()
				opts.PXESubnet = nic.Prefix.String()
				opts.AllowedInterfaces = ResolveAllowedInterfaces(ifaces, mgmtAddr, nic.Name, false)
				ui.OK(ui.t("PXE-интерфейс", "PXE interface"), nic.Name, opts.PXEServerIP+", "+opts.PXESubnet)
			}
			break
		}
	}

	// Step 4: DHCP mode & pool (only when a PXE interface was selected)
	if selectedPXE != nil {
		ui.StepTitle(4, 5, fmt.Sprintf("%s %s (%s)",
			ui.t("Режим DHCP для", "DHCP mode for"),
			selectedPXE.Name,
			opts.PXESubnet,
		))
		dedicated := selectedPXE.IP != mgmtAddr && len(ifaces) > 1
		defaultChoice := "1"
		proxySuffix := " (" + ui.t("рекомендуется", "recommended") + ")"
		if dedicated {
			defaultChoice = "2"
			proxySuffix = ""
		}

		ui.OptionLine(1, ui.white("Proxy DHCP"), "— "+ui.t("IP-адреса уже выдаёт внешний DHCP-сервер", "existing DHCP server assigns IP addresses")+proxySuffix, false)
		ui.OptionLine(2, ui.white("Full DHCP "), "— "+ui.t("OMB сам выдаёт IP и опции PXE (изолированная сеть)", "OMB assigns IP addresses and PXE options (isolated VLAN)"), false)
		ui.OptionLine(3, ui.t("Пропустить", "Skip"), "— "+ui.t("включить DHCP позже в веб-панели", "enable DHCP later in web panel"), false)
		fmt.Fprintln(out)

		for {
			ui.Prompt(ui.t("Режим DHCP", "DHCP mode"), defaultChoice)
			ans, err := readLine()
			if err != nil {
				return Options{}, false, err
			}
			if ans == "" {
				ans = defaultChoice
			}
			switch ans {
			case "1":
				opts.DHCPMode = "proxy"
				opts.SkipPXE = false
				ui.OK("DHCP", "Proxy DHCP", "")
			case "2":
				opts.DHCPMode = "full"
				opts.SkipPXE = false
				defStart, defEnd, err := DefaultDHCPRange(selectedPXE.Prefix, selectedPXE.IP)
				defStartStr, defEndStr := "", ""
				if err == nil {
					defStartStr = defStart.String()
					defEndStr = defEnd.String()
				}
				for {
					ui.Prompt(ui.t("Начало пула DHCP", "DHCP pool start"), defStartStr)
					s, err := readLine()
					if err != nil {
						return Options{}, false, err
					}
					if s == "" {
						s = defStartStr
					}

					ui.Prompt(ui.t("Конец пула DHCP", "DHCP pool end"), defEndStr)
					e, err := readLine()
					if err != nil {
						return Options{}, false, err
					}
					if e == "" {
						e = defEndStr
					}

					opts.DHCPStart = s
					opts.DHCPEnd = e
					if _, err := BuildNetworkConfig(opts); err != nil {
						ui.Error(fmt.Sprintf("%s: %v", ui.t("Некорректный пул DHCP", "Invalid DHCP pool"), err))
						continue
					}
					break
				}
				ui.OK("DHCP", fmt.Sprintf("Full DHCP (%s – %s)", opts.DHCPStart, opts.DHCPEnd), "")
			case "3":
				opts.DHCPMode = "skip"
				opts.SkipPXE = true
				ui.OK("DHCP", ui.t("Пропустить", "Skip"), "")
			default:
				ui.Error(ui.t("Введите 1, 2 или 3.", "Enter 1, 2 or 3."))
				continue
			}
			break
		}
	}

	// Step 5: Summary & confirm
	ui.SummaryBox(opts, mgmtIface)
	for {
		ui.Prompt(ui.t("Применить настройки и установить OMB?", "Apply settings and install OMB?"), "Y/n")
		ans, err := readLine()
		if err != nil {
			return Options{}, false, err
		}
		switch strings.ToLower(ans) {
		case "", "y", "yes", "д", "да":
			return opts, true, nil
		case "n", "no", "н", "нет":
			return opts, false, nil
		default:
			ui.Error(ui.t("Введите Y или n.", "Enter Y or n."))
		}
	}
}

// ResolveNonInteractive validates and normalizes CLI options for --non-interactive execution.
func ResolveNonInteractive(opts Options, ifaces []NetIface, passwordStdin io.Reader, readPasswordStdin bool) (Options, error) {
	if opts.Lang == "" {
		opts.Lang = "ru"
	}
	if opts.Lang != "ru" && opts.Lang != "en" {
		return Options{}, fmt.Errorf("unsupported --lang %q (expected ru or en)", opts.Lang)
	}

	if opts.ManagementIP == "" {
		return Options{}, fmt.Errorf("--management-ip is required in --non-interactive mode")
	}
	mgmtAddr, err := netip.ParseAddr(opts.ManagementIP)
	if err != nil || !mgmtAddr.Is4() {
		return Options{}, fmt.Errorf("invalid --management-ip %q: must be an IPv4 address", opts.ManagementIP)
	}

	if readPasswordStdin {
		if passwordStdin == nil {
			return Options{}, fmt.Errorf("--admin-password-stdin requires stdin input")
		}
		raw, err := io.ReadAll(io.LimitReader(passwordStdin, 4096))
		if err != nil {
			return Options{}, fmt.Errorf("read admin password from stdin: %w", err)
		}
		pw := strings.TrimSuffix(string(raw), "\n")
		pw = strings.TrimSuffix(pw, "\r")
		opts.AdminPassword = pw
	}
	if opts.AdminPassword == "" {
		return Options{}, fmt.Errorf("admin password is required (pass --admin-password-stdin)")
	}
	if err := validatePassword(opts.AdminPassword); err != nil {
		return Options{}, fmt.Errorf("invalid admin password: %w", err)
	}

	if opts.InstallMode == "" {
		opts.InstallMode = "online"
	}
	if opts.InstallMode != "online" && opts.InstallMode != "offline" {
		return Options{}, fmt.Errorf("unsupported --install-mode %q (expected online or offline)", opts.InstallMode)
	}

	if opts.SkipPXE || opts.DHCPMode == "skip" || opts.PXEInterface == "none" {
		opts.SkipPXE = true
		opts.DHCPMode = "skip"
		if opts.PXEInterface == "" {
			opts.PXEInterface = "none"
		}
		opts.AllowedInterfaces = ResolveAllowedInterfaces(ifaces, mgmtAddr, opts.PXEInterface, true)
		return opts, nil
	}

	if opts.PXEInterface == "" {
		return Options{}, fmt.Errorf("--pxe-interface (or --skip-pxe) is required in --non-interactive mode")
	}
	if opts.DHCPMode == "" {
		return Options{}, fmt.Errorf("--dhcp-mode (proxy, full, or skip) is required in --non-interactive mode")
	}
	if opts.DHCPMode != "proxy" && opts.DHCPMode != "full" {
		return Options{}, fmt.Errorf("unsupported --dhcp-mode %q (expected proxy, full, or skip)", opts.DHCPMode)
	}

	nic, found := findIfaceByName(ifaces, opts.PXEInterface)
	if !found && (opts.PXEServerIP == "" || opts.PXESubnet == "") {
		return Options{}, fmt.Errorf("PXE interface %q was not found among active IPv4 interfaces", opts.PXEInterface)
	}
	if found {
		if opts.PXEServerIP == "" {
			opts.PXEServerIP = nic.IP.String()
		}
		if opts.PXESubnet == "" {
			opts.PXESubnet = nic.Prefix.String()
		}
	}

	if opts.DHCPMode == "full" && opts.DHCPStart == "" && opts.DHCPEnd == "" && found {
		start, end, err := DefaultDHCPRange(nic.Prefix, nic.IP)
		if err != nil {
			return Options{}, fmt.Errorf("calculate default DHCP range for %s: %w", opts.PXEInterface, err)
		}
		opts.DHCPStart = start.String()
		opts.DHCPEnd = end.String()
	}

	if _, err := BuildNetworkConfig(opts); err != nil {
		return Options{}, fmt.Errorf("invalid PXE configuration: %w", err)
	}

	opts.AllowedInterfaces = ResolveAllowedInterfaces(ifaces, mgmtAddr, opts.PXEInterface, true)
	return opts, nil
}

func orderPXECandidates(ifaces []NetIface, mgmtAddr netip.Addr) []NetIface {
	var nonMgmt, mgmt []NetIface
	for _, nic := range ifaces {
		if nic.IP == mgmtAddr {
			mgmt = append(mgmt, nic)
		} else {
			nonMgmt = append(nonMgmt, nic)
		}
	}
	return append(nonMgmt, mgmt...)
}

func findIfaceNameByIP(ifaces []NetIface, addr netip.Addr) string {
	for _, nic := range ifaces {
		if nic.IP == addr {
			return nic.Name
		}
	}
	return ""
}

func findIfaceByName(ifaces []NetIface, name string) (NetIface, bool) {
	for _, nic := range ifaces {
		if nic.Name == name {
			return nic, true
		}
	}
	return NetIface{}, false
}
