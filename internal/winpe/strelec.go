package winpe

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var strelecMapping = regexp.MustCompile(`(?mi)^net[\t ]+use[\t ]+Z:[^\r\n]*(?:\r?\n|$)`)
var strelecPayload = regexp.MustCompile(`(?mi)^Z:\\SSTR\\MInst\\Portable\\x64\\Files_(10|11)_x64\.exe(?:\r?\n|$)`)

// RenderStrelecNetwork keeps the image author's native network services and
// program launcher, replacing only the example Z: mapping with this ISO.
func RenderStrelecNetwork(original, installationTemplate []byte, password string, profileID int64) ([]byte, error) {
	if profileID <= 0 || len(original) > 256*1024 || bytes.ContainsRune(original, 0) {
		return nil, fmt.Errorf("invalid Strelec startup")
	}
	mappings := strelecMapping.FindAllIndex(original, -1)
	share := mediaMapping.FindSubmatch(installationTemplate)
	if len(mappings) != 1 || len(share) < 2 || bytes.Contains(original, []byte("omb_strelec_")) {
		return nil, fmt.Errorf("Strelec network mapping is missing or ambiguous")
	}
	replacement := []byte("set OMB_STRELEC_PASSWORD=" + passwordToken + "\r\n" +
		"wpeutil WaitForNetwork >nul 2>&1\r\n" +
		"echo WaitForNetwork exit code: %errorlevel% >>\"%OMB_STRELEC_LOG%\"\r\n" +
		"set /a OMB_STRELEC_ATTEMPT=0 >nul\r\n" +
		":omb_strelec_connect\r\n" +
		"net use Z: " + string(share[1]) + "\\" + strconv.FormatInt(profileID, 10) + " %OMB_STRELEC_PASSWORD% /user:omb-iso /persistent:no >>\"%OMB_STRELEC_LOG%\" 2>&1\r\n" +
		"if not errorlevel 1 goto omb_strelec_ready\r\n" +
		"set /a OMB_STRELEC_ATTEMPT+=1 >nul\r\n" +
		"if %OMB_STRELEC_ATTEMPT% GEQ 45 goto omb_strelec_failed\r\n" +
		"ping -n 3 127.0.0.1 >nul\r\n" +
		"goto omb_strelec_connect\r\n" +
		":omb_strelec_failed\r\n" +
		"set OMB_STRELEC_PASSWORD=\r\n" +
		"ipconfig /all >>\"%OMB_STRELEC_LOG%\" 2>&1\r\n" +
		"start \"OMB: Strelec\" cmd.exe /k \"echo OMB: service media is unavailable. See X:\\omb-strelec.log & type X:\\omb-strelec.log\"\r\n" +
		"exit /b 1\r\n" +
		":omb_strelec_ready\r\n" +
		"set OMB_STRELEC_PASSWORD=\r\n" +
		"if not exist Z:\\SSTR\\MInst\\MInst.exe goto omb_strelec_failed\r\n")
	output := append([]byte("@echo off\r\nsetlocal EnableExtensions\r\nset OMB_STRELEC_LOG=X:\\omb-strelec.log\r\necho OMB: service startup >\"%OMB_STRELEC_LOG%\"\r\n"), original[:mappings[0][0]]...)
	output = append(output, replacement...)
	output = append(output, original[mappings[0][1]:]...)
	output, err := renderStrelecApplications(output)
	if err != nil {
		return nil, err
	}
	return renderPassword(output, password)
}

// The native PXE recipe extracts additional DLLs but leaves MInst's /lnk
// startup commented out. Initialize the same profile as MInstLink.cmd after
// extraction so the network payload also populates the recovery start menu.
func renderStrelecApplications(base []byte) ([]byte, error) {
	const marker = "rem OMB_STRELEC_APPLICATIONS"
	if bytes.Count(base, []byte(marker)) == 1 {
		return base, nil
	}
	matches := strelecPayload.FindAllSubmatch(base, -1)
	if len(matches) != 1 {
		return nil, fmt.Errorf("Strelec application payload is missing or ambiguous")
	}
	version := string(matches[0][1])
	profile := "strelec64Windows" + version + ".ini"
	command := strings.TrimRight(string(matches[0][0]), "\r\n")
	startup := marker + "\r\nset OMB_STRELEC_LOG=X:\\omb-strelec.log\r\n" +
		"echo OMB: extracting service components >>\"%OMB_STRELEC_LOG%\"\r\n" +
		command + " >>\"%OMB_STRELEC_LOG%\" 2>&1\r\n" +
		"echo Payload exit code: %errorlevel% >>\"%OMB_STRELEC_LOG%\"\r\n" +
		"if not exist Z:\\SSTR\\MInst\\profiles\\" + profile + " goto omb_strelec_failed\r\n" +
		"echo OMB: initializing MInst and start-menu shortcuts >>\"%OMB_STRELEC_LOG%\"\r\n" +
		"set drive=Z\r\n" +
		"start \"\" \"Z:\\SSTR\\MInst\\MInst.exe\" \"{Patch}\\profiles\\" + profile + "\" /lnk /minimized\r\n" +
		"echo MInst launch exit code: %errorlevel% >>\"%OMB_STRELEC_LOG%\"\r\n"
	return strelecPayload.ReplaceAll(base, []byte(startup)), nil
}
