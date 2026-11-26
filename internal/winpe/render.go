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

var (
	setupSourcePattern = regexp.MustCompile(`^(?:X:\\sources\\setup\.exe|Y:\\[1-9][0-9]*\\sources\\setup\.exe)$`)
	shareSubdirPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
)

// RenderSplitInstallStartup binds a WinPE script to a setup.exe source
// ("X:\sources\setup.exe" for bare install.wim or "Y:\<iso_id>\sources\setup.exe"
// for ISO with overridden install.wim) and an installation image located under
// Y:\<installShareSubdir>\sources\<installFile>.
func RenderSplitInstallStartup(template []byte, password string, setupSource string, installShareSubdir string, installFile string) ([]byte, error) {
	if !setupSourcePattern.MatchString(setupSource) || !shareSubdirPattern.MatchString(installShareSubdir) || (installFile != "install.wim" && installFile != "install.esd") {
		return nil, fmt.Errorf("invalid Windows split install configuration")
	}
	const setupToken = `Y:\__OMB_PROFILE_ID__\sources\setup.exe`
	const idToken = "__OMB_PROFILE_ID__"
	const fileToken = "__OMB_INSTALL_FILE__"
	if bytes.Count(template, []byte(setupToken)) != 1 || !bytes.Contains(template, []byte(fileToken)) {
		return nil, fmt.Errorf("WinPE profile template is incomplete")
	}
	out, err := renderPassword(template, password)
	if err != nil {
		return nil, err
	}
	out = bytes.Replace(out, []byte(setupToken), []byte(setupSource), 1)
	if !bytes.Contains(out, []byte(idToken)) {
		return nil, fmt.Errorf("WinPE profile template is incomplete")
	}
	out = bytes.ReplaceAll(out, []byte(idToken), []byte(installShareSubdir))
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
	if profileID <= 0 {
		return nil, fmt.Errorf("invalid Windows ISO profile")
	}
	return RenderAnswerStartupSubdir(base, strconv.FormatInt(profileID, 10))
}

// RenderAnswerStartupSubdir turns a prepared Windows Setup startup script
// (ISO, ISO with overridden install.wim, or bare install.wim) into a strict
// answer-file variant without changing its network credential or media binding.
// shareSubdir names the folder under \\<IP>\ombisos containing sources\install.{wim,esd}.
func RenderAnswerStartupSubdir(base []byte, shareSubdir string) ([]byte, error) {
	return renderAnswerStartup(base, shareSubdir, true)
}

// RenderAnswerStartupLegacy is the variant script made before the I: mapping;
// answer variants prepared then keep it.
func RenderAnswerStartupLegacy(base []byte, profileID int64) ([]byte, error) {
	if profileID <= 0 {
		return nil, fmt.Errorf("invalid Windows ISO profile")
	}
	return renderAnswerStartup(base, strconv.FormatInt(profileID, 10), false)
}

func renderAnswerStartup(base []byte, shareSubdir string, peImage bool) ([]byte, error) {
	if !shareSubdirPattern.MatchString(shareSubdir) {
		return nil, fmt.Errorf("invalid Windows share subdir")
	}
	pattern := regexp.MustCompile(`(?m)^((?:Y:\\[1-9][0-9]*|X:)\\sources\\setup\.exe /InstallFrom:Y:\\` + regexp.QuoteMeta(shareSubdir) + `\\sources\\install\.(?:wim|esd))\r\n`)
	matches := pattern.FindAllSubmatchIndex(base, -1)
	if len(matches) != 1 {
		return nil, fmt.Errorf("prepared Windows Setup command is missing")
	}
	match := matches[0]
	command := base[match[0]:match[1]]
	var replacement []byte
	if peImage {
		if m := mediaMapping.FindSubmatch(base); m != nil {
			// A failed mapping only matters to pe.cmd; Setup goes on.
			replacement = append(replacement, []byte("net use "+answerImageLetter+" "+string(m[1])+"\\"+shareSubdir+" "+strings.TrimSpace(string(m[2]))+"\r\n")...)
		}
		replacement = append(replacement, []byte("copy /y X:\\Windows\\System32\\omb-answer.xml X:\\autounattend.xml >nul\r\n")...)
	}
	replacement = append(replacement, []byte("if not exist X:\\Windows\\System32\\omb-answer.xml goto failed\r\n")...)
	replacement = append(replacement, command[:len(command)-2]...)
	replacement = append(replacement, []byte(" /Unattend:X:\\Windows\\System32\\omb-answer.xml\r\n")...)
	out := make([]byte, 0, len(base)-len(command)+len(replacement))
	out = append(out, base[:match[0]]...)
	out = append(out, replacement...)
	out = append(out, base[match[1]:]...)
	return out, nil
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
