package winpe

import (
	"bytes"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

const passwordToken = "__OMB_ISO_PASSWORD__"

// renderPassword inserts a limited downstream SMB credential into a WinPE script.
func renderPassword(template []byte, password string) ([]byte, error) {
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
	const idToken = "__OMB_PROFILE_ID__"
	const fileToken = "__OMB_INSTALL_FILE__"
	if !bytes.Contains(template, []byte(idToken)) || !bytes.Contains(template, []byte(fileToken)) {
		return nil, fmt.Errorf("WinPE profile template is incomplete")
	}
	out, err := renderPassword(template, password)
	if err != nil {
		return nil, err
	}
	out = bytes.ReplaceAll(out, []byte(idToken), []byte(strconv.FormatInt(profileID, 10)))
	out = bytes.ReplaceAll(out, []byte(fileToken), []byte(installFile))
	return out, nil
}

// answerImageLetter is the drive letter under which an answer variant maps
// its profile folder, so that the profile's files lie at the drive root.
const answerImageLetter = "I:"

var mediaMapping = regexp.MustCompile(`(?m)^net use Y: (\\\\[^ \r\n]+) ([^\r\n]*)\r$`)

// RenderAnswerStartup turns a prepared ISO startup script into a strict
// answer-file variant without changing its network credential or ISO binding.
//
// Besides Setup's /Unattend option it prepares for answer files whose
// windowsPE pass runs its own pe.cmd (the editor's «wipe and partition»):
// pe.cmd looks for <drive>:\sources\install.wim and <drive>:\autounattend.xml,
// so the profile folder is mapped once more as I: and the answer file is
// copied to X:\autounattend.xml. Setup itself does not use either.
func RenderAnswerStartup(base []byte, profileID int64) ([]byte, error) {
	return renderAnswerStartup(base, profileID, true)
}

// RenderAnswerStartupLegacy is the variant script made before the I: mapping;
// answer variants prepared then keep it.
func RenderAnswerStartupLegacy(base []byte, profileID int64) ([]byte, error) {
	return renderAnswerStartup(base, profileID, false)
}

func renderAnswerStartup(base []byte, profileID int64, peImage bool) ([]byte, error) {
	if profileID <= 0 {
		return nil, fmt.Errorf("invalid Windows ISO profile")
	}
	id := strconv.FormatInt(profileID, 10)
	for _, installFile := range []string{"install.wim", "install.esd"} {
		command := []byte("Y:\\" + id + "\\sources\\setup.exe /InstallFrom:Y:\\" + id + "\\sources\\" + installFile + "\r\n")
		if bytes.Count(base, command) != 1 {
			continue
		}
		var replacement []byte
		if peImage {
			if m := mediaMapping.FindSubmatch(base); m != nil {
				// A failed mapping only matters to pe.cmd; Setup goes on.
				replacement = append(replacement, []byte("net use "+answerImageLetter+" "+string(m[1])+"\\"+id+" "+strings.TrimSpace(string(m[2]))+"\r\n")...)
			}
			replacement = append(replacement, []byte("copy /y X:\\Windows\\System32\\omb-answer.xml X:\\autounattend.xml >nul\r\n")...)
		}
		replacement = append(replacement, []byte("if not exist X:\\Windows\\System32\\omb-answer.xml goto failed\r\n")...)
		replacement = append(replacement, command[:len(command)-2]...)
		replacement = append(replacement, []byte(" /Unattend:X:\\Windows\\System32\\omb-answer.xml\r\n")...)
		return bytes.Replace(base, command, replacement, 1), nil
	}
	return nil, fmt.Errorf("prepared Windows Setup command is missing")
}

// RenderDriverStartup adds selected INF loads before the prepared environment
// connects its media. Both Windows Setup and native Strelec startup are supported.
func RenderDriverStartup(base []byte, infPaths []string) ([]byte, error) {
	needle := []byte("\r\nwpeinit\r\n")
	initialize := "wpeinit\r\n"
	service := bytes.Contains(base, []byte("\r\n:omb_strelec_connect\r\n"))
	if service {
		// Strelec has already started its native driver/service setup. Refresh
		// networking after loading our additional drivers, before the SMB retry.
		needle = []byte("\r\nwpeutil WaitForNetwork >nul 2>&1\r\n")
		initialize = "wpeutil InitializeNetwork\r\nwpeutil WaitForNetwork >nul 2>&1\r\n"
	}
	if len(infPaths) == 0 || len(infPaths) > 128 || bytes.Count(base, needle) != 1 || bytes.Contains(base, []byte(":driver_failed")) || bytes.Contains(base, []byte("set OMB_DRIVER_LOG=")) {
		return nil, fmt.Errorf("invalid WinPE startup script or driver selection")
	}
	if service {
		var err error
		base, err = renderStrelecApplications(base)
		if err != nil {
			return nil, err
		}
	}
	var added strings.Builder
	added.WriteString("\r\nset OMB_DRIVER_LOG=X:\\omb-driver.log\r\necho OMB: loading WinPE drivers...\r\n")
	if service {
		added.WriteString("set OMB_DRIVER_ERRORS=0\r\necho OMB: selected WinPE drivers >%OMB_DRIVER_LOG%\r\n")
	}
	for _, name := range infPaths {
		if !safeDriverBatchPath(name) {
			return nil, fmt.Errorf("INF path cannot be used in WinPE startup")
		}
		windowsPath := strings.ReplaceAll(name, "/", `\`)
		if service {
			added.WriteString("echo INF: " + windowsPath + " >>%OMB_DRIVER_LOG%\r\n")
		}
		added.WriteString(`drvload "X:\Windows\System32\` + windowsPath + "\" >>%OMB_DRIVER_LOG% 2>&1\r\n")
		if service {
			// A shared recovery entry can contain drivers for several machines.
			// A failed/non-applicable storage driver must not skip the NIC driver
			// or prevent an already working native NIC from connecting media.
			added.WriteString("set OMB_DRIVER_EXIT=%errorlevel%\r\necho Drvload exit code: %OMB_DRIVER_EXIT% >>%OMB_DRIVER_LOG%\r\nif not \"%OMB_DRIVER_EXIT%\"==\"0\" set OMB_DRIVER_ERRORS=1\r\n")
		} else {
			added.WriteString("if errorlevel 1 goto driver_failed\r\n")
		}
	}
	if service {
		added.WriteString("if \"%OMB_DRIVER_ERRORS%\"==\"1\" echo OMB: some selected drivers were not loaded. See X:\\omb-driver.log. Continuing network initialization.\r\n")
	}
	added.WriteString(initialize)
	out := bytes.Replace(base, needle, []byte(added.String()), 1)
	if !service {
		out = append(out, []byte("\r\n:driver_failed\r\necho OMB: a selected WinPE driver failed to load.\r\ntype %OMB_DRIVER_LOG%\r\ncmd /k\r\n")...)
	}
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
