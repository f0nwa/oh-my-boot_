package winpe

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func waitTemplate(t *testing.T) []byte {
	t.Helper()
	template, err := os.ReadFile("../../deploy/boot-assets/windows-profiles/omb-setup.cmd.in")
	if err != nil {
		t.Fatal(err)
	}
	return template
}

func waitVariants(t *testing.T) map[string][]byte {
	t.Helper()
	base, err := RenderProfileStartup(waitTemplate(t), strings.Repeat("A", 32), 17, "install.wim")
	if err != nil {
		t.Fatal(err)
	}
	answer, err := RenderAnswerStartup(base, 17)
	if err != nil {
		t.Fatal(err)
	}
	driver, err := RenderDriverStartup(base, []string{"omb-drivers/abc/net.inf"})
	if err != nil {
		t.Fatal(err)
	}
	combined, err := RenderDriverStartup(answer, []string{"omb-drivers/abc/net.inf"})
	if err != nil {
		t.Fatal(err)
	}
	return map[string][]byte{"template": waitTemplate(t), "profile": base, "answer": answer, "driver": driver, "answer+driver": combined}
}

func strelecWaitScript(t *testing.T, drivers bool) []byte {
	t.Helper()
	native := []byte("net start Netman\r\nping -n 9 127.0.0.1 >nul\r\nnet use Z: \\\\demo\\share /user:demo demo\r\nZ:\\SSTR\\MInst\\Portable\\x64\\Files_11_x64.exe\r\nexit\r\n")
	script, err := RenderStrelecNetwork(native, waitTemplate(t), strings.Repeat("A", 32), 17)
	if err != nil {
		t.Fatal(err)
	}
	if drivers {
		script, err = RenderDriverStartup(script, []string{"omb-drivers/abc/net.inf"})
		if err != nil {
			t.Fatal(err)
		}
	}
	return script
}

func TestWaitUpdatesWindowsSetupVariants(t *testing.T) {
	for name, script := range waitVariants(t) {
		got, changed, err := UpdateStartupWait(script, 20, 5)
		if err != nil || !changed {
			t.Fatalf("%s: changed=%t err=%v", name, changed, err)
		}
		text := string(got)
		for _, want := range []string{
			"\r\nif %OMB_ATTEMPT% GEQ 20 goto connect_final\r\n",
			"retrying %OMB_ATTEMPT%/20...\r\n",
			"\r\nping -n 6 127.0.0.1 >nul\r\ngoto connect_media\r\n",
			"if not errorlevel 1 goto media_ready\r\n",
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s: missing %q in\n%s", name, want, text)
			}
		}
		if strings.Contains(text, "GEQ 45") || strings.Contains(text, "/45...") || strings.Contains(text, "ping -n 3 ") {
			t.Fatalf("%s: old wait values remain", name)
		}
		again, changed, err := UpdateStartupWait(got, 20, 5)
		if err != nil || changed || !bytes.Equal(again, got) {
			t.Fatalf("%s: update is not idempotent (changed=%t err=%v)", name, changed, err)
		}
		attempts, pause, ok := WaitFromScript(got)
		if !ok || attempts != 20 || pause != 5 {
			t.Fatalf("%s: WaitFromScript = %d/%d/%t", name, attempts, pause, ok)
		}
	}
}

func TestWaitDefaultsMatchCurrentTemplate(t *testing.T) {
	attempts, pause, ok := WaitFromScript(waitTemplate(t))
	if !ok || attempts != 45 || pause != 2 {
		t.Fatalf("template wait = %d/%d/%t, want 45/2", attempts, pause, ok)
	}
	got, changed, err := UpdateStartupWait(waitTemplate(t), 45, 2)
	if err != nil || changed || !bytes.Equal(got, waitTemplate(t)) {
		t.Fatalf("default values changed the template: changed=%t err=%v", changed, err)
	}
}

func TestStrelecInheritsWaitFromCustomizedTemplate(t *testing.T) {
	customTmpl, _, err := UpdateStartupWait(waitTemplate(t), 30, 4)
	if err != nil {
		t.Fatal(err)
	}
	native := []byte("net start Netman\r\nnet use Z: \\\\demo\\share /user:demo demo\r\nZ:\\SSTR\\MInst\\Portable\\x64\\Files_11_x64.exe\r\nexit\r\n")
	script, err := RenderStrelecNetwork(native, customTmpl, strings.Repeat("A", 32), 17)
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	if !strings.Contains(text, "if %OMB_STRELEC_ATTEMPT% GEQ 30 goto omb_strelec_failed\r\nping -n 5 127.0.0.1 >nul\r\n") {
		t.Fatalf("Strelec did not inherit template wait values:\n%s", text)
	}
}

func TestWaitUpdatesStrelecLoopOnly(t *testing.T) {
	for _, drivers := range []bool{false, true} {
		script := strelecWaitScript(t, drivers)
		got, changed, err := UpdateStartupWait(script, 7, 10)
		if err != nil || !changed {
			t.Fatalf("drivers=%t: changed=%t err=%v", drivers, changed, err)
		}
		text := string(got)
		if !strings.Contains(text, "\r\nif %OMB_STRELEC_ATTEMPT% GEQ 7 goto omb_strelec_failed\r\nping -n 11 127.0.0.1 >nul\r\ngoto omb_strelec_connect\r\n") {
			t.Fatalf("drivers=%t: Strelec loop not updated:\n%s", drivers, text)
		}
		if !strings.Contains(text, "ping -n 9 127.0.0.1 >nul\r\nnet use Z:") && !strings.Contains(text, "ping -n 9 127.0.0.1 >nul") {
			t.Fatalf("drivers=%t: native ping changed", drivers)
		}
		if !strings.Contains(text, "if not errorlevel 1 goto omb_strelec_ready\r\n") || !strings.Contains(text, "Files_11_x64.exe") {
			t.Fatalf("drivers=%t: Strelec flow changed", drivers)
		}
		if _, changed, _ := UpdateStartupWait(got, 7, 10); changed {
			t.Fatalf("drivers=%t: Strelec update is not idempotent", drivers)
		}
	}
}

func TestWaitLeavesScriptsWithoutLoopUnchanged(t *testing.T) {
	script := []byte("@echo off\r\nwpeinit\r\nping -n 3 127.0.0.1 >nul\r\n")
	got, changed, err := UpdateStartupWait(script, 10, 1)
	if err != nil || changed || !bytes.Equal(got, script) {
		t.Fatalf("changed=%t err=%v", changed, err)
	}
	if _, _, ok := WaitFromScript(script); ok {
		t.Fatal("found wait values in a script without the loop")
	}
}

func TestWaitRejectsAmbiguousOrInvalidInput(t *testing.T) {
	base := waitVariants(t)["profile"]
	broken := bytes.Replace(base, []byte("goto connect_media\r\n\r\n:connect_final"), []byte("goto elsewhere\r\n\r\n:connect_final"), 1)
	if _, _, err := UpdateStartupWait(broken, 10, 1); err == nil {
		t.Fatal("accepted a loop without its pause line")
	}
	doubled := append(append([]byte{}, base...), base...)
	if _, _, err := UpdateStartupWait(doubled, 10, 1); err == nil {
		t.Fatal("accepted two wait loops")
	}
	for _, values := range [][2]int{{0, 2}, {601, 2}, {10, 0}, {10, 31}} {
		if _, _, err := UpdateStartupWait(base, values[0], values[1]); err == nil {
			t.Fatalf("accepted attempts=%d pause=%d", values[0], values[1])
		}
	}
}
