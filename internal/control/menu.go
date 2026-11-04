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

func validatedBootURL(baseURL string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(baseURL, " \t\r\n") {
		return "", fmt.Errorf("invalid boot URL")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func ubuntuBootScript(base, label, seed string) string {
	return ubuntuBootScriptAt(label, seed, base+"/assets/ubuntu", base+"/images/ubuntu-desktop.iso")
}

func ubuntuBootScriptAt(label, seed, assetsURL, isoURL string) string {
	args := ""
	if seed != "" {
		// iPXE splits whitespace and treats only a standalone ";" as a command
		// terminator. Keep this one literal token; quotes/backslashes would be
		// passed to the kernel. See src/core/exec.c split_command and
		// command_terminator: https://github.com/ipxe/ipxe/blob/v2.0.0/src/core/exec.c#L115-L198
		args = " autoinstall ds=nocloud-net;s=" + seed
	}
	return ":" + label + "\n" +
		"kernel " + assetsURL + "/vmlinuz initrd=initrd boot=casper ip=dhcp iso-url=" + isoURL + args + " ---\n" +
		"initrd " + assetsURL + "/initrd\n" +
		"iseq ${platform} efi && goto " + label + "_efi || goto " + label + "_boot\n" +
		":" + label + "_efi\n" +
		"shim " + assetsURL + "/bootx64.efi || goto " + label + "_failed\n" +
		":" + label + "_boot\n" +
		"boot || goto " + label + "_failed\n" +
		":" + label + "_failed\n" +
		"echo Ubuntu boot failed\n" +
		"sleep 5\n" +
		"goto local\n"
}
