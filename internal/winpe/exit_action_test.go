package winpe

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"omb/internal/network"
)

func TestRenderSetupExitBlock(t *testing.T) {
	for _, tc := range []struct {
		action  string
		contain string
	}{
		{"", "cmd /k"},
		{network.WinPEExitActionCMD, "cmd /k"},
		{network.WinPEExitActionReboot, "wpeutil reboot"},
		{network.WinPEExitActionRebootTimer, "omb-exit.vbs"},
	} {
		b, err := RenderSetupExitBlock(tc.action)
		if err != nil {
			t.Fatalf("action %q error: %v", tc.action, err)
		}
		if !strings.Contains(string(b), tc.contain) {
			t.Errorf("action %q output %q does not contain %q", tc.action, string(b), tc.contain)
		}
	}

	if _, err := RenderSetupExitBlock("unknown_action"); err == nil {
		t.Fatal("expected error on unknown action")
	}
}

func TestUpdateStartupExitActionOnLegacyScript(t *testing.T) {
	legacy := []byte("@echo off\r\n" +
		"set OMB_ISO_PASSWORD=test\r\n" +
		"echo OMB: initializing network...\r\n" +
		"wpeinit\r\n" +
		":connect_media\r\n" +
		"ping -n 3 127.0.0.1 >nul\r\n" +
		":media_ready\r\n" +
		"if not exist Y:\\__OMB_PROFILE_ID__\\sources\\__OMB_INSTALL_FILE__ goto failed\r\n" +
		"echo OMB: starting Windows Setup...\r\n" +
		"Y:\\__OMB_PROFILE_ID__\\sources\\setup.exe /InstallFrom:Y:\\__OMB_PROFILE_ID__\\sources\\__OMB_INSTALL_FILE__\r\n" +
		"if errorlevel 1 goto failed\r\n" +
		"exit /b 0\r\n\r\n" +
		":failed\r\n" +
		"echo OMB: installation media is unavailable or Setup failed.\r\n" +
		"echo Check network connectivity and the ISO mount on the PXE server.\r\n" +
		"cmd /k\r\n")

	// 1. Upgrade legacy script to reboot_timer
	updated, changed, err := UpdateStartupExitAction(legacy, network.WinPEExitActionRebootTimer)
	if err != nil {
		t.Fatalf("upgrade legacy script error: %v", err)
	}
	if !changed {
		t.Fatal("expected script to be changed")
	}
	if !bytes.Contains(updated, []byte("omb-exit.vbs")) {
		t.Fatal("expected omb-exit.vbs in updated script")
	}
	if !bytes.Contains(updated, []byte(":failed\r\necho OMB: installation media is unavailable or Setup failed.")) {
		t.Fatal("expected :failed block to remain intact")
	}
	action, ok := ExitActionFromScript(updated)
	if !ok || action != network.WinPEExitActionRebootTimer {
		t.Fatalf("ExitActionFromScript = %q, %t; want reboot_timer, true", action, ok)
	}

	// 2. Transition reboot_timer -> reboot
	updatedReboot, changed, err := UpdateStartupExitAction(updated, network.WinPEExitActionReboot)
	if err != nil {
		t.Fatalf("transition to reboot error: %v", err)
	}
	if !changed {
		t.Fatal("expected script to be changed")
	}
	if bytes.Contains(updatedReboot, []byte("omb-exit.vbs")) {
		t.Fatal("reboot script must not contain omb-exit.vbs")
	}
	if !bytes.Contains(updatedReboot, []byte("wpeutil reboot")) {
		t.Fatal("expected wpeutil reboot")
	}
	action, ok = ExitActionFromScript(updatedReboot)
	if !ok || action != network.WinPEExitActionReboot {
		t.Fatalf("ExitActionFromScript = %q, %t; want reboot, true", action, ok)
	}

	// 3. Transition reboot -> cmd
	updatedCMD, changed, err := UpdateStartupExitAction(updatedReboot, network.WinPEExitActionCMD)
	if err != nil {
		t.Fatalf("transition to cmd error: %v", err)
	}
	if !changed {
		t.Fatal("expected script to be changed")
	}
	if bytes.Contains(updatedCMD, []byte("wpeutil reboot")) {
		t.Fatal("cmd script must not contain wpeutil reboot")
	}
	if !bytes.Contains(updatedCMD, []byte("OMB: Windows Setup closed (exit code %OMB_SETUP_EXIT%).\r\ncmd /k")) {
		t.Fatal("expected cmd exit block")
	}
	action, ok = ExitActionFromScript(updatedCMD)
	if !ok || action != network.WinPEExitActionCMD {
		t.Fatalf("ExitActionFromScript = %q, %t; want cmd, true", action, ok)
	}

	// 4. Same action produces changed == false
	same, changed, err := UpdateStartupExitAction(updatedCMD, network.WinPEExitActionCMD)
	if err != nil || changed || !bytes.Equal(same, updatedCMD) {
		t.Fatalf("re-applying same action produced err=%v changed=%t", err, changed)
	}
}

func TestUpdateStartupExitActionStrelecIgnored(t *testing.T) {
	strelec := []byte(`@echo off
:omb_strelec_connect
net use Z: \\10.30.0.10\ombisos\6
exit /b 0
`)
	out, changed, err := UpdateStartupExitAction(strelec, network.WinPEExitActionRebootTimer)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed || !bytes.Equal(out, strelec) {
		t.Fatal("expected Strelec script to remain completely unchanged")
	}
	action, ok := ExitActionFromScript(strelec)
	if ok || action != "" {
		t.Fatalf("ExitActionFromScript on Strelec returned %q, %t; want empty, false", action, ok)
	}
}

func TestUpdateStartupExitActionWithDriversAndAnswer(t *testing.T) {
	raw, err := os.ReadFile("../../deploy/boot-assets/windows-profiles/omb-setup.cmd.in")
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	profileScript, err := RenderProfileStartup(raw, "abcdef1234567890abcdef1234567890", 2, "install.wim")
	if err != nil {
		t.Fatalf("RenderProfileStartup: %v", err)
	}

	// Add driver
	driverScript, err := RenderDriverStartup(profileScript, []string{"net/driver.inf"})
	if err != nil {
		t.Fatalf("RenderDriverStartup: %v", err)
	}

	// Turn into answer script
	answerScript, err := RenderAnswerStartup(driverScript, 2)
	if err != nil {
		t.Fatalf("RenderAnswerStartup: %v", err)
	}

	// Now apply exit action reboot_timer
	updated, changed, err := UpdateStartupExitAction(answerScript, network.WinPEExitActionRebootTimer)
	if err != nil {
		t.Fatalf("UpdateStartupExitAction: %v", err)
	}
	if !changed {
		t.Fatal("expected changed to be true")
	}

	// Verify driver block is still at the end
	if !bytes.Contains(updated, []byte(":driver_failed")) {
		t.Fatal("driver_failed block was lost")
	}
	// Verify answer parameter is still present
	if !bytes.Contains(updated, []byte("/Unattend:X:\\Windows\\System32\\omb-answer.xml")) {
		t.Fatal("unattend parameter was lost")
	}
	// Verify exit block is present
	if !bytes.Contains(updated, []byte("omb-exit.vbs")) {
		t.Fatal("omb-exit.vbs was not found")
	}

	// Verify wait updater still works on this updated script!
	waitUpdated, waitChanged, err := UpdateStartupWait(updated, 50, 4)
	if err != nil {
		t.Fatalf("UpdateStartupWait: %v", err)
	}
	if !waitChanged {
		t.Fatal("expected wait to change")
	}
	attempts, pause, ok := WaitFromScript(waitUpdated)
	if !ok || attempts != 50 || pause != 4 {
		t.Fatalf("WaitFromScript = %d, %d, %t; want 50, 4, true", attempts, pause, ok)
	}
}
