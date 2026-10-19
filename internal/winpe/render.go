package winpe

import (
	"bytes"
	"fmt"
	"path"
	"strconv"
	"strings"
)

const passwordToken = "__PXE_ISO_PASSWORD__"

// RenderStartup inserts a limited downstream SMB credential into a WinPE script.
func RenderStartup(template []byte, password string) ([]byte, error) {
	if len(password) < 32 || len(password) > 64 {
		return nil, fmt.Errorf("downstream password must have 32–64 alphanumeric characters")
	}
	for _, b := range []byte(password) {
		if !((b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')) {
			return nil, fmt.Errorf("downstream password must be alphanumeric")
		}
	}
	if bytes.Count(template, []byte(passwordToken)) != 1 {
		return nil, fmt.Errorf("WinPE template must contain exactly one password token")
	}
	return bytes.Replace(template, []byte(passwordToken), []byte(password), 1), nil
}

// RenderProfileStartup binds a WinPE script to one prepared ISO profile.
func RenderProfileStartup(template []byte, password string, profileID int64, installFile string) ([]byte, error) {
	if profileID <= 0 || (installFile != "install.wim" && installFile != "install.esd") {
		return nil, fmt.Errorf("invalid Windows ISO profile")
	}
	const idToken = "__PXE_PROFILE_ID__"
	const fileToken = "__PXE_INSTALL_FILE__"
	if !bytes.Contains(template, []byte(idToken)) || !bytes.Contains(template, []byte(fileToken)) {
		return nil, fmt.Errorf("WinPE profile template is incomplete")
	}
	out, err := RenderStartup(template, password)
	if err != nil {
		return nil, err
	}
	out = bytes.ReplaceAll(out, []byte(idToken), []byte(strconv.FormatInt(profileID, 10)))
	out = bytes.ReplaceAll(out, []byte(fileToken), []byte(installFile))
	return out, nil
}

// RenderAnswerStartup turns a prepared ISO startup script into a strict
// answer-file variant without changing its network credential or ISO binding.
func RenderAnswerStartup(base []byte, profileID int64) ([]byte, error) {
	if profileID <= 0 {
		return nil, fmt.Errorf("invalid Windows ISO profile")
	}
	id := strconv.FormatInt(profileID, 10)
	for _, installFile := range []string{"install.wim", "install.esd"} {
		command := []byte("Y:\\" + id + "\\sources\\setup.exe /InstallFrom:Y:\\" + id + "\\sources\\" + installFile + "\r\n")
		if bytes.Count(base, command) != 1 {
			continue
		}
		replacement := append([]byte("if not exist X:\\Windows\\System32\\pxe-answer.xml goto failed\r\n"), command[:len(command)-2]...)
		replacement = append(replacement, []byte(" /Unattend:X:\\Windows\\System32\\pxe-answer.xml\r\n")...)
		return bytes.Replace(base, command, replacement, 1), nil
	}
	return nil, fmt.Errorf("prepared Windows Setup command is missing")
}

// RenderDriverStartup adds selected INF loads before WinPE initializes the
// network. The original SMB mapping and Setup command remain in the script.
func RenderDriverStartup(base []byte, infPaths []string) ([]byte, error) {
	needle := []byte("\r\nwpeinit\r\n")
	if len(infPaths) == 0 || len(infPaths) > 128 || bytes.Count(base, needle) != 1 || bytes.Contains(base, []byte(":driver_failed")) {
		return nil, fmt.Errorf("invalid WinPE startup script or driver selection")
	}
	var added strings.Builder
	added.WriteString("\r\nset PXE_DRIVER_LOG=X:\\pxe-driver.log\r\necho PXE Platform: loading WinPE drivers...\r\n")
	for _, name := range infPaths {
		if !safeDriverBatchPath(name) {
			return nil, fmt.Errorf("INF path cannot be used in WinPE startup")
		}
		windowsPath := strings.ReplaceAll(name, "/", `\`)
		added.WriteString(`drvload "X:\Windows\System32\` + windowsPath + "\" >>%PXE_DRIVER_LOG% 2>&1\r\n")
		added.WriteString("if errorlevel 1 goto driver_failed\r\n")
	}
	added.WriteString("wpeinit\r\n")
	out := bytes.Replace(base, needle, []byte(added.String()), 1)
	out = append(out, []byte("\r\n:driver_failed\r\necho PXE Platform: a selected WinPE driver failed to load.\r\ntype %PXE_DRIVER_LOG%\r\ncmd /k\r\n")...)
	return out, nil
}

func safeDriverBatchPath(name string) bool {
	if name == "" || path.IsAbs(name) || path.Clean(name) != name || strings.ContainsAny(name, `\"%!^&|<>:`) {
		return false
	}
	for _, r := range name {
		if r < 32 || r > 126 {
			return false
		}
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
