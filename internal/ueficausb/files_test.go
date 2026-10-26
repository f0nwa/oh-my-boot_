package ueficausb

import (
	"bytes"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func wimFile(t *testing.T, name string) string {
	t.Helper()
	for _, f := range WIMFiles() {
		if f.Name == name {
			return string(f.Data)
		}
	}
	t.Fatalf("no %s in WIMFiles", name)
	return ""
}

func TestWinpeshlStartsUEFICA(t *testing.T) {
	files := WIMFiles()
	if files[0].Name != "winpeshl.ini" {
		t.Fatalf("first file = %q", files[0].Name)
	}
	want := "[LaunchApps]\r\n%SYSTEMROOT%\\System32\\uefica.cmd\r\n"
	if string(files[0].Data) != want {
		t.Fatalf("winpeshl.ini = %q", files[0].Data)
	}
}

func TestWIMFileNames(t *testing.T) {
	var names []string
	for _, f := range WIMFiles() {
		names = append(names, f.Name)
	}
	if strings.Join(names, " ") != "winpeshl.ini uefica.cmd uefica.vbs" {
		t.Fatalf("names = %v", names)
	}
}

func TestTextFilesAreASCIIWithCRLF(t *testing.T) {
	all := append(WIMFiles(), File{Name: "README.txt", Data: ReadMe()})
	for _, f := range all {
		if len(f.Data) == 0 {
			t.Fatalf("%s is empty", f.Name)
		}
		for i, b := range f.Data {
			if b >= 0x80 {
				t.Fatalf("%s: non-ASCII byte at %d", f.Name, i)
			}
			if b == '\n' && (i == 0 || f.Data[i-1] != '\r') {
				t.Fatalf("%s: bare LF at %d", f.Name, i)
			}
		}
	}
}

func TestCmdSetsUSKeyboardBeforePrompts(t *testing.T) {
	cmd := wimFile(t, "uefica.cmd")
	init := strings.Index(cmd, "wpeinit")
	layout := strings.Index(cmd, "wpeutil SetKeyboardLayout 0409:00000409")
	prompt := strings.Index(cmd, "set /p")
	if init < 0 || layout < 0 || prompt < 0 || !(init < layout && layout < prompt) {
		t.Fatalf("order wpeinit=%d layout=%d prompt=%d", init, layout, prompt)
	}
}

func TestCmdClearsScreenAfterPassword(t *testing.T) {
	lines := strings.Split(wimFile(t, "uefica.cmd"), "\r\n")
	found := 0
	for i, line := range lines {
		if !strings.Contains(line, "/password") {
			continue
		}
		found++
		j := i + 1
		for j < len(lines) && (strings.TrimSpace(lines[j]) == "" || strings.HasPrefix(strings.TrimSpace(lines[j]), "set CODE=")) {
			j++
		}
		if j >= len(lines) || strings.TrimSpace(lines[j]) != "cls" {
			t.Fatalf("line %d %q is not followed by cls", i+1, line)
		}
	}
	if found == 0 {
		t.Fatal("no /password call")
	}
}

func TestVBSNeverPrintsSecret(t *testing.T) {
	printing := regexp.MustCompile(`(?i)\b(Echo|Write|WriteLine)\b.*\bsecret\b`)
	for n, line := range strings.Split(wimFile(t, "uefica.vbs"), "\r\n") {
		if printing.MatchString(line) {
			t.Fatalf("line %d prints the password: %s", n+1, line)
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "secret") && (strings.Contains(lower, "uefica.txt") || strings.Contains(lower, "uefica-calls.log") || strings.Contains(lower, "logcall")) {
			t.Fatalf("line %d writes the password to a file: %s", n+1, line)
		}
	}
}

func TestVBSUsesSCCMOptionNames(t *testing.T) {
	vbs := wimFile(t, "uefica.vbs")
	for _, s := range []string{"authenticationLevel=pktPrivacy", "Allow3rdPartyUEFICA", "AllowMicrosoft3rdPartyUEFICA", "MSUefiCA", "Enable MS UEFI CA key", "WmiOpcodePasswordAdmin:", ",ascii,us", "<utf-16/>"} {
		if !strings.Contains(vbs, s) {
			t.Errorf("uefica.vbs lacks %q", s)
		}
	}
}

func TestScriptsVersionIsStable(t *testing.T) {
	a, b := ScriptsVersion(), ScriptsVersion()
	if a != b || !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(a) {
		t.Fatalf("version %q %q", a, b)
	}
	if bytes.Contains(ReadMe(), []byte("\r\r")) {
		t.Fatal("README has doubled CR")
	}
}

// TestVBSScenariosUnderWine runs testdata/uefica-tests.cmd with Wine's
// cscript when Wine is installed (the Cyrillic password scenario needs
// Windows: Wine's VBScript has no ChrW/AscW).
func TestVBSScenariosUnderWine(t *testing.T) {
	wine, err := exec.LookPath("wine")
	if err != nil || testing.Short() {
		t.Skip("wine is not installed")
	}
	cmd := exec.Command(wine, "cmd", "/c", `testdata\uefica-tests.cmd`)
	cmd.Env = append(os.Environ(), "UEFICA_WINE=1", "WINEDEBUG=-all")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "all 16 passed") {
		t.Fatalf("uefica-tests.cmd: %v\n%s", err, out)
	}
}

func TestCmdRestartsOnlyOnChanged(t *testing.T) {
	cmd := wimFile(t, "uefica.cmd")
	if !strings.Contains(cmd, `if "%CODE%"=="20" goto restart`) || strings.Contains(cmd, `=="0" goto restart`) {
		t.Fatal("restart must follow only exit code 20 (changed)")
	}
	if !strings.Contains(cmd, "Unexpected result") {
		t.Fatal("unknown exit codes must be reported as errors")
	}
}

func TestCmdComparesAnswersSafely(t *testing.T) {
	cmd := wimFile(t, "uefica.cmd")
	if strings.Contains(cmd, `"%ANSWER%"`) || !strings.Contains(cmd, "EnableDelayedExpansion") || !strings.Contains(cmd, `"!ANSWER!"`) {
		t.Fatal(`a " typed at a prompt must not break the script: compare with delayed expansion`)
	}
}

func TestVBSReadsPasswordBeforeChecks(t *testing.T) {
	vbs := wimFile(t, "uefica.vbs")
	apply := vbs[strings.Index(vbs, "Function ApplyChange()"):]
	read := strings.Index(apply, "secret = ReadPassword()")
	prepare := strings.Index(apply, "Prepare(opt, vendorName, False)")
	if read < 0 || prepare < 0 || read > prepare {
		t.Fatal("the password must be read before any early exit, so it never lands at the next prompt")
	}
}
