package winpe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeployTemplateRendersWithOneCredential(t *testing.T) {
	templatePath := filepath.Join("..", "..", "deploy", "boot-assets", "windows-profiles", "omb-setup.cmd.in")
	template, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RenderProfileStartup(template, strings.Repeat("A", 32), 1, "install.wim"); err != nil {
		t.Fatalf("deployed WinPE template cannot be rendered: %v", err)
	}
}

func TestRenderPasswordReplacesOnlyDownstreamCredential(t *testing.T) {
	template := []byte("@echo off\r\nnet use Y: \\\\10.30.0.10\\ombisos __OMB_ISO_PASSWORD__ /user:omb-iso\r\n")
	password := strings.Repeat("aB9", 12)
	got, err := renderPassword(template, password)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "__OMB_ISO_PASSWORD__") || !strings.Contains(string(got), password) {
		t.Fatal("rendered WinPE script did not contain the downstream credential")
	}
	if !strings.Contains(string(got), "\r\n") {
		t.Fatal("WinPE script lost Windows line endings")
	}
}

func TestRenderPasswordRejectsUnsafePasswordAndTemplate(t *testing.T) {
	for _, tc := range []struct {
		template string
		password string
	}{
		{"__OMB_ISO_PASSWORD__", "bad&password"},
		{"__OMB_ISO_PASSWORD__", "short"},
		{"no token", strings.Repeat("A", 32)},
		{"__OMB_ISO_PASSWORD____OMB_ISO_PASSWORD__", strings.Repeat("A", 32)},
	} {
		if _, err := renderPassword([]byte(tc.template), tc.password); err == nil {
			t.Fatalf("accepted unsafe render input: %#v", tc)
		}
	}
}

func TestRenderProfileStartupTargetsOnlySelectedISO(t *testing.T) {
	template := []byte("@echo off\r\nnet use Y: \\\\10.30.0.10\\ombisos __OMB_ISO_PASSWORD__ /user:omb-iso\r\nif not exist Y:\\__OMB_PROFILE_ID__\\sources\\__OMB_INSTALL_FILE__ goto failed\r\nY:\\__OMB_PROFILE_ID__\\sources\\setup.exe /InstallFrom:Y:\\__OMB_PROFILE_ID__\\sources\\__OMB_INSTALL_FILE__\r\n")
	got, err := RenderProfileStartup(template, strings.Repeat("A", 32), 17, "install.esd")
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if !strings.Contains(text, `Y:\17\sources\install.esd`) || !strings.Contains(text, `\\10.30.0.10\ombisos`) || strings.Contains(text, "__OMB_") {
		t.Fatalf("profile was not rendered safely: %s", text)
	}
	for _, bad := range []struct {
		id   int64
		file string
	}{{0, "install.wim"}, {17, "../install.wim"}, {17, "install.swm"}} {
		if _, err := RenderProfileStartup(template, strings.Repeat("A", 32), bad.id, bad.file); err == nil {
			t.Fatalf("accepted unsafe profile render: %+v", bad)
		}
	}
}

func TestRenderAnswerStartupRequiresXMLAndPassesItToSetup(t *testing.T) {
	base := []byte("@echo off\r\nY:\\17\\sources\\setup.exe /InstallFrom:Y:\\17\\sources\\install.wim\r\n")
	got, err := RenderAnswerStartup(base, 17)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "if not exist X:\\Windows\\System32\\omb-answer.xml goto failed\r\n") || !strings.Contains(string(got), " /Unattend:X:\\Windows\\System32\\omb-answer.xml\r\n") {
		t.Fatalf("answer option or required-file check missing: %q", got)
	}
	if _, err := RenderAnswerStartup([]byte("@echo off\r\n"), 17); err == nil {
		t.Fatal("accepted missing Setup command")
	}
}

// TestRenderAnswerStartupMapsProfileForPECmd: the answer variant maps the
// profile folder as I: (pe.cmd looks for I:\sources\install.wim) and copies
// the answer file to X:\autounattend.xml, after the share is connected and
// before Setup starts. The legacy variant does neither.
func TestRenderAnswerStartupMapsProfileForPECmd(t *testing.T) {
	template, err := os.ReadFile("../../deploy/boot-assets/windows-profiles/omb-setup.cmd.in")
	if err != nil {
		t.Fatal(err)
	}
	base, err := RenderProfileStartup(template, strings.Repeat("A", 32), 17, "install.wim")
	if err != nil {
		t.Fatal(err)
	}
	got, err := RenderAnswerStartup(base, 17)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	mapping := `net use I: \\10.30.0.10\ombisos\17 %OMB_ISO_PASSWORD% /user:omb-iso /persistent:no >nul 2>&1` + "\r\n"
	copyLine := `copy /y X:\Windows\System32\omb-answer.xml X:\autounattend.xml >nul` + "\r\n"
	ready, m, c, setup := strings.Index(text, ":media_ready"), strings.Index(text, mapping), strings.Index(text, copyLine), strings.Index(text, `Y:\17\sources\setup.exe`)
	if ready < 0 || m < ready || c < m || setup < c || strings.Count(text, "net use I:") != 1 {
		t.Fatalf("mapping not between the share and Setup:\n%s", text)
	}
	if !strings.Contains(text, `/Unattend:X:\Windows\System32\omb-answer.xml`) || strings.Contains(text, "AAAAAAAA\\") {
		t.Fatalf("bad Setup line or credential:\n%s", text)
	}
	legacy, err := RenderAnswerStartupLegacy(base, 17)
	if err != nil || strings.Contains(string(legacy), "net use I:") || strings.Contains(string(legacy), "autounattend.xml") {
		t.Fatalf("legacy variant changed: %v\n%s", err, legacy)
	}
}

func TestRenderSplitInstallStartupBareWIM(t *testing.T) {
	template, err := os.ReadFile("../../deploy/boot-assets/windows-profiles/omb-setup.cmd.in")
	if err != nil {
		t.Fatal(err)
	}
	got, err := RenderSplitInstallStartup(template, strings.Repeat("A", 32), `X:\sources\setup.exe`, "install-5", "install.wim")
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if !strings.Contains(text, "if not exist Y:\\install-5\\sources\\install.wim goto failed\r\n") {
		t.Fatalf("missing install.wim existence check:\n%s", text)
	}
	if !strings.Contains(text, "X:\\sources\\setup.exe /InstallFrom:Y:\\install-5\\sources\\install.wim\r\n") {
		t.Fatalf("missing bare WIM setup command:\n%s", text)
	}
	if strings.Contains(text, "__OMB_") {
		t.Fatalf("unreplaced template token remains:\n%s", text)
	}
	for _, bad := range []struct {
		setupSource string
		subdir      string
		file        string
	}{
		{`C:\sources\setup.exe`, "install-5", "install.wim"},
		{`Y:\0\sources\setup.exe`, "install-5", "install.wim"},
		{`X:\sources\setup.exe & calc`, "install-5", "install.wim"},
		{`X:\sources\setup.exe`, "", "install.wim"},
		{`X:\sources\setup.exe`, "../install-5", "install.wim"},
		{`X:\sources\setup.exe`, "install 5", "install.wim"},
		{`X:\sources\setup.exe`, "install-5", "install.swm"},
	} {
		if _, err := RenderSplitInstallStartup(template, strings.Repeat("A", 32), bad.setupSource, bad.subdir, bad.file); err == nil {
			t.Fatalf("accepted unsafe split install render: %+v", bad)
		}
	}
}

func TestRenderSplitInstallStartupISOOverride(t *testing.T) {
	template, err := os.ReadFile("../../deploy/boot-assets/windows-profiles/omb-setup.cmd.in")
	if err != nil {
		t.Fatal(err)
	}
	got, err := RenderSplitInstallStartup(template, strings.Repeat("A", 32), `Y:\17\sources\setup.exe`, "install-5", "install.esd")
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if !strings.Contains(text, "if not exist Y:\\install-5\\sources\\install.esd goto failed\r\n") {
		t.Fatalf("missing install.esd existence check:\n%s", text)
	}
	if !strings.Contains(text, "Y:\\17\\sources\\setup.exe /InstallFrom:Y:\\install-5\\sources\\install.esd\r\n") {
		t.Fatalf("missing ISO override setup command:\n%s", text)
	}
	if strings.Contains(text, "__OMB_") {
		t.Fatalf("unreplaced template token remains:\n%s", text)
	}
}

func TestRenderAnswerStartupWorksWithBareWIMAndPECmd(t *testing.T) {
	template, err := os.ReadFile("../../deploy/boot-assets/windows-profiles/omb-setup.cmd.in")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name        string
		setupSource string
		installFile string
	}{
		{"bare-wim", `X:\sources\setup.exe`, "install.wim"},
		{"iso-override", `Y:\17\sources\setup.exe`, "install.esd"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base, err := RenderSplitInstallStartup(template, strings.Repeat("A", 32), tc.setupSource, "install-5", tc.installFile)
			if err != nil {
				t.Fatal(err)
			}
			got, err := RenderAnswerStartupSubdir(base, "install-5")
			if err != nil {
				t.Fatal(err)
			}
			text := string(got)
			mapping := `net use I: \\10.30.0.10\ombisos\install-5 %OMB_ISO_PASSWORD% /user:omb-iso /persistent:no >nul 2>&1` + "\r\n"
			copyLine := `copy /y X:\Windows\System32\omb-answer.xml X:\autounattend.xml >nul` + "\r\n"
			setupCmd := tc.setupSource + ` /InstallFrom:Y:\install-5\sources\` + tc.installFile + ` /Unattend:X:\Windows\System32\omb-answer.xml` + "\r\n"
			ready, m, c, setup := strings.Index(text, ":media_ready"), strings.Index(text, mapping), strings.Index(text, copyLine), strings.Index(text, setupCmd)
			if ready < 0 || m < ready || c < m || setup < c || strings.Count(text, "net use I:") != 1 {
				t.Fatalf("unexpected answer startup layout:\n%s", text)
			}
			if !strings.Contains(text, "if not exist X:\\Windows\\System32\\omb-answer.xml goto failed\r\n") {
				t.Fatalf("missing answer XML check:\n%s", text)
			}
			if _, err := RenderAnswerStartupSubdir(got, "install-5"); err == nil {
				t.Fatal("accepted double answer startup injection")
			}
			if _, err := RenderAnswerStartupSubdir(base, "install-9"); err == nil {
				t.Fatal("accepted mismatched share subdir")
			}
			if _, err := RenderAnswerStartupSubdir(base, "../install-5"); err == nil {
				t.Fatal("accepted unsafe share subdir")
			}
		})
	}
}

func TestSplitInstallScriptsWorkWithWaitExitAndServerIP(t *testing.T) {
	template, err := os.ReadFile("../../deploy/boot-assets/windows-profiles/omb-setup.cmd.in")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		setupSource string
		installFile string
	}{
		{"bare-wim", `X:\sources\setup.exe`, "install.wim"},
		{"iso-override", `Y:\17\sources\setup.exe`, "install.esd"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, err := RenderSplitInstallStartup(template, strings.Repeat("A", 32), tc.setupSource, "install-5", tc.installFile)
			if err != nil {
				t.Fatal(err)
			}
			answer, err := RenderAnswerStartupSubdir(base, "install-5")
			if err != nil {
				t.Fatal(err)
			}
			for _, script := range [][]byte{base, answer} {
				updated, changed, err := UpdateStartupWait(script, 20, 5)
				if err != nil || !changed {
					t.Fatalf("UpdateStartupWait failed: changed=%v err=%v", changed, err)
				}
				updated, changed, err = UpdateStartupExitAction(updated, "reboot")
				if err != nil || !changed {
					t.Fatalf("UpdateStartupExitAction failed: changed=%v err=%v", changed, err)
				}
				if action, ok := ExitActionFromScript(updated); !ok || action != "reboot" {
					t.Fatalf("ExitActionFromScript = (%q, %v), want (reboot, true)", action, ok)
				}
				updated, changed, err = UpdateStartupServerIP(updated, "192.168.10.135")
				if err != nil || !changed {
					t.Fatalf("UpdateStartupServerIP failed: changed=%v err=%v", changed, err)
				}
				if strings.Contains(string(updated), "10.30.0.10") {
					t.Fatalf("old server IP still present:\n%s", updated)
				}
				if !strings.Contains(string(updated), `\\192.168.10.135\ombisos`) {
					t.Fatalf("new server IP missing:\n%s", updated)
				}
			}
		})
	}
}
