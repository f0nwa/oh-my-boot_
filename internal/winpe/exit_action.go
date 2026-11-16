package winpe

import (
	"bytes"
	"fmt"
	"regexp"

	"omb/internal/network"
)

var setupExitPattern = regexp.MustCompile(`(?s)(\r?\n[^\r\n]*setup\.exe[^\r\n]*\r?\n)(?:if errorlevel 1 goto failed\r?\nexit /b 0|set OMB_SETUP_EXIT=%errorlevel%\r?\nif "%OMB_SETUP_EXIT%"=="0" exit /b 0\r?\n.*?)(?:\r?\n)+(:failed\r?\n)`)

// RenderSetupExitBlock returns the commands executed when Windows Setup exits.
func RenderSetupExitBlock(action string) ([]byte, error) {
	return RenderSetupExitBlockWithEOL(action, "\r\n")
}

// RenderSetupExitBlockWithEOL returns the commands executed when Windows Setup exits, formatted with eol.
func RenderSetupExitBlockWithEOL(action, eol string) ([]byte, error) {
	switch action {
	case "", network.WinPEExitActionCMD:
		return []byte("echo OMB: Windows Setup closed (exit code %OMB_SETUP_EXIT%)." + eol + "cmd /k"), nil
	case network.WinPEExitActionReboot:
		return []byte("echo OMB: Windows Setup closed (exit code %OMB_SETUP_EXIT%). Restarting system..." + eol + "wpeutil reboot" + eol + "exit /b 0"), nil
	case network.WinPEExitActionRebootTimer:
		return []byte("echo OMB: Windows Setup closed (exit code %OMB_SETUP_EXIT%)." + eol +
			"echo Restarting system in 30 seconds..." + eol +
			"echo Set s=CreateObject(\"WScript.Shell\") >\"%TEMP%\\omb-exit.vbs\"" + eol +
			"echo WScript.Quit s.Popup(\"Setup closed (exit code \" ^& \"%OMB_SETUP_EXIT%\" ^& \"). Restart in 30s?\", 30, \"OMB Windows Setup\", 1+48) >>\"%TEMP%\\omb-exit.vbs\"" + eol +
			"cscript //nologo \"%TEMP%\\omb-exit.vbs\"" + eol +
			"if errorlevel 2 goto setup_cmd" + eol +
			"wpeutil reboot" + eol +
			"exit /b 0" + eol +
			":setup_cmd" + eol +
			"cmd /k"), nil
	default:
		return nil, fmt.Errorf("invalid WinPE exit action: %q", action)
	}
}

// UpdateStartupExitAction updates the Setup exit block in a prepared Windows
// startup script. Scripts without a setup.exe invocation (such as Strelec) are
// returned unchanged without error.
func UpdateStartupExitAction(script []byte, action string) ([]byte, bool, error) {
	if !bytes.Contains(script, []byte("setup.exe")) || !bytes.Contains(script, []byte(":media_ready")) {
		return script, false, nil
	}
	eol := "\r\n"
	if !bytes.Contains(script, []byte("\r\n")) && bytes.Contains(script, []byte("\n")) {
		eol = "\n"
	}
	body, err := RenderSetupExitBlockWithEOL(action, eol)
	if err != nil {
		return nil, false, err
	}
	m := setupExitPattern.FindSubmatchIndex(script)
	if len(m) != 6 {
		return nil, false, fmt.Errorf("WinPE Setup exit block is missing or ambiguous")
	}
	var out bytes.Buffer
	out.Grow(len(script) + len(body) + 64)
	out.Write(script[:m[3]])
	out.WriteString("set OMB_SETUP_EXIT=%errorlevel%" + eol + "if \"%OMB_SETUP_EXIT%\"==\"0\" exit /b 0" + eol)
	out.Write(body)
	out.WriteString(eol + eol)
	out.Write(script[m[4]:])
	result := out.Bytes()
	return result, !bytes.Equal(result, script), nil
}

// ExitActionFromScript inspects a Windows Setup startup script and returns the
// configured exit action.
func ExitActionFromScript(script []byte) (string, bool) {
	if !bytes.Contains(script, []byte("setup.exe")) || !bytes.Contains(script, []byte(":media_ready")) {
		return "", false
	}
	if bytes.Contains(script, []byte("omb-exit.vbs")) && bytes.Contains(script, []byte("Popup(")) {
		return network.WinPEExitActionRebootTimer, true
	}
	if bytes.Contains(script, []byte("wpeutil reboot")) {
		return network.WinPEExitActionReboot, true
	}
	return network.WinPEExitActionCMD, true
}
