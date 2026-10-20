package control

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

type windowsInitrdFile struct {
	name string
	url  string
}

// The pinned BIOS iPXE build boots injected WinPE files using pathname arguments;
// its --name form produced "Bad CPIO magic" in SeaBIOS. Keep the tested UEFI form.
func windowsInjectedFiles(label string, files ...windowsInitrdFile) string {
	var out strings.Builder
	out.WriteString("iseq ${platform} efi && goto " + label + "_efi_initrd || goto " + label + "_bios_initrd\n")
	out.WriteString(":" + label + "_bios_initrd\n")
	for _, file := range files {
		out.WriteString("initrd " + file.url + " " + file.name + "\n")
	}
	out.WriteString("goto " + label + "_initrd_done\n")
	out.WriteString(":" + label + "_efi_initrd\n")
	for _, file := range files {
		out.WriteString("initrd -n " + file.name + " " + file.url + "\n")
	}
	out.WriteString(":" + label + "_initrd_done\n")
	return out.String()
}

func safeMenuText(value string, max int) bool {
	if value == "" || len([]rune(value)) > max || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || strings.ContainsRune(".,!?:-_()", r) {
			continue
		}
		return false
	}
	return true
}

func BuildMenu(title, label, message string) ([]byte, error) {
	return buildMenu(title, label, message, "")
}

// BuildUbuntuMenu adds the prepared Ubuntu Desktop boot profile to a menu.
func BuildUbuntuMenu(title, label, message, baseURL string) ([]byte, error) {
	base, err := validatedBootURL(baseURL)
	if err != nil {
		return nil, err
	}
	return buildMenu(title, label, message, base)
}

func validatedBootURL(baseURL string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(baseURL, " \t\r\n") {
		return "", fmt.Errorf("invalid boot URL")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func BuildMenuFromForm(form url.Values, baseURL string) ([]byte, error) {
	ubuntuBase := ""
	folders := map[string]string{}
	if form.Get("ubuntu") == "on" {
		base, err := validatedBootURL(baseURL)
		if err != nil {
			return nil, err
		}
		ubuntuBase = base
		folders["ubuntu"] = form.Get("folder_ubuntu")
	}
	for _, folder := range folders {
		if err := validateMenuFolder(folder); err != nil {
			return nil, err
		}
	}
	for _, folder := range folders {
		if folder != "" {
			return buildMenuTree(form.Get("title"), form.Get("label"), form.Get("message"), ubuntuBase, folders)
		}
	}
	return buildMenu(form.Get("title"), form.Get("label"), form.Get("message"), ubuntuBase)
}

func buildMenu(title, label, message, ubuntuBase string) ([]byte, error) {
	if !safeMenuText(title, 80) || !safeMenuText(label, 80) || !safeMenuText(message, 120) {
		return nil, fmt.Errorf("menu text contains unsupported characters or is too long")
	}
	script := "#!ipxe\n" +
		"menu " + title + "\n" +
		"item diagnostics " + label + "\n"
	if ubuntuBase != "" {
		script += "item ubuntu Ubuntu Desktop 26.04.1\n"
	}
	script += "item local Boot local disk\n" +
		"choose target || goto local\n" +
		"goto ${target}\n" +
		":diagnostics\n" +
		"echo " + message + "\n" +
		"sleep 3\n" +
		"goto local\n"
	if ubuntuBase != "" {
		script += ubuntuBootScript(ubuntuBase)
	}
	script += ":local\n" +
		"exit\n"
	return []byte(script), nil
}

func ubuntuBootScript(base string) string {
	return ":ubuntu\n" +
		"kernel " + base + "/assets/ubuntu/vmlinuz initrd=initrd boot=casper ip=dhcp iso-url=" + base + "/images/ubuntu-desktop.iso ---\n" +
		"initrd " + base + "/assets/ubuntu/initrd\n" +
		"iseq ${platform} efi && goto ubuntu_efi || goto ubuntu_boot\n" +
		":ubuntu_efi\n" +
		"shim " + base + "/assets/ubuntu/bootx64.efi || goto ubuntu_failed\n" +
		":ubuntu_boot\n" +
		"boot || goto ubuntu_failed\n" +
		":ubuntu_failed\n" +
		"echo Ubuntu boot failed\n" +
		"sleep 5\n" +
		"goto local\n"
}
