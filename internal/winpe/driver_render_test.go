package winpe

import (
	"strings"
	"testing"
)

func TestDriverStartupLoadsBeforeNetworkAndSetup(t *testing.T) {
	base := []byte("@echo off\r\necho PXE Platform: initializing network...\r\nwpeinit\r\nnet use Y: \\server\\share\r\nY:\\17\\sources\\setup.exe\r\n:failed\r\ncmd /k\r\n")
	got, err := RenderDriverStartup(base, []string{"pxe-drivers/abcdef/net/driver.inf"})
	if err != nil {
		t.Fatal(err)
	}
	script := string(got)
	load := strings.Index(script, `drvload "X:\Windows\System32\pxe-drivers\abcdef\net\driver.inf"`)
	if load < 0 || load > strings.Index(script, "wpeinit") || strings.Index(script, "wpeinit") > strings.Index(script, "setup.exe") {
		t.Fatalf("bad startup order: %s", script)
	}
	if !strings.Contains(script, "if errorlevel 1 goto driver_failed\r\n") || !strings.Contains(script, ":driver_failed\r\n") {
		t.Fatalf("driver failure is hidden: %s", script)
	}
}

func TestDriverStartupRejectsUnsafePathAndMissingNetworkInit(t *testing.T) {
	base := []byte("@echo off\r\nwpeinit\r\n")
	for _, name := range []string{"../net.inf", `net/driver%PATH%.inf`, `net/driver".inf`, "net/драйвер.inf"} {
		if _, err := RenderDriverStartup(base, []string{name}); err == nil {
			t.Fatalf("accepted unsafe INF path %q", name)
		}
	}
	if _, err := RenderDriverStartup([]byte("@echo off\r\n"), []string{"net.inf"}); err == nil {
		t.Fatal("accepted missing wpeinit")
	}
}
