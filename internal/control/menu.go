package control

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

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
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(baseURL, " \t\r\n") {
		return nil, fmt.Errorf("invalid Ubuntu boot URL")
	}
	return buildMenu(title, label, message, strings.TrimRight(u.String(), "/"))
}

func BuildMenuFromForm(form url.Values, baseURL string) ([]byte, error) {
	if form.Get("ubuntu") == "on" {
		return BuildUbuntuMenu(form.Get("title"), form.Get("label"), form.Get("message"), baseURL)
	}
	return BuildMenu(form.Get("title"), form.Get("label"), form.Get("message"))
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
		script += ":ubuntu\n" +
			"kernel " + ubuntuBase + "/assets/ubuntu/vmlinuz initrd=initrd boot=casper ip=dhcp iso-url=" + ubuntuBase + "/images/ubuntu-desktop.iso ---\n" +
			"initrd " + ubuntuBase + "/assets/ubuntu/initrd\n" +
			"boot || goto ubuntu_failed\n" +
			":ubuntu_failed\n" +
			"echo Ubuntu boot failed\n" +
			"sleep 5\n" +
			"goto local\n"
	}
	script += ":local\n" +
		"exit\n"
	return []byte(script), nil
}
