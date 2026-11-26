package winpe

import (
	"strings"
	"testing"
)

func TestDriverStartupLoadsBeforeNetworkAndSetup(t *testing.T) {
	base := []byte("@echo off\r\necho OMB: initializing network...\r\nwpeinit\r\nnet use Y: \\server\\share\r\nY:\\17\\sources\\setup.exe\r\n:failed\r\ncmd /k\r\n")
	got, err := RenderDriverStartup(base, []string{"omb-drivers/abcdef/net/driver.inf"})
	if err != nil {
		t.Fatal(err)
	}
	script := string(got)
	load := strings.Index(script, `drvload "X:\Windows\System32\omb-drivers\abcdef\net\driver.inf"`)
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

func TestStrelecDriverStartupLoadsBeforeNetworkAndPreservesNativePrograms(t *testing.T) {
	native := []byte("net start Netman\r\nnet use Z: \\\\demo\\share /user:demo demo\r\nZ:\\SSTR\\MInst\\Portable\\x64\\Files_11_x64.exe\r\nexit\r\n")
	template := []byte("net use Y: \\\\10.30.0.10\\ombisos __OMB_ISO_PASSWORD__ /user:omb-iso\r\n")
	base, err := RenderStrelecNetwork(native, template, strings.Repeat("A", 32), 17)
	if err != nil {
		t.Fatal(err)
	}
	got, err := RenderDriverStartup(base, []string{"omb-drivers/abcdef/NetKVM/netkvm.inf"})
	if err != nil {
		t.Fatal(err)
	}
	script := string(got)
	load := strings.Index(script, `drvload "X:\Windows\System32\omb-drivers\abcdef\NetKVM\netkvm.inf"`)
	init := strings.Index(script, "wpeutil InitializeNetwork")
	wait := strings.Index(script, "wpeutil WaitForNetwork")
	share := strings.Index(script, "net use Z:")
	if load < 0 || !(load < init && init < wait && wait < share) {
		t.Fatalf("wrong driver/network order: %s", script)
	}
	if !strings.Contains(script, `Files_11_x64.exe`) || !strings.Contains(script, ":omb_strelec_connect") || strings.Contains(script, "setup.exe") {
		t.Fatal("native service startup replaced")
	}
	if _, err := RenderDriverStartup(got, []string{"another.inf"}); err == nil {
		t.Fatal("accepted repeated driver injection")
	}
}

func TestServiceDriverFailureDoesNotSkipOtherDriversOrNetwork(t *testing.T) {
	native := []byte("net use Z: \\\\demo\\share /user:demo demo\r\nZ:\\SSTR\\MInst\\Portable\\x64\\Files_11_x64.exe\r\nexit\r\n")
	template := []byte("net use Y: \\\\10.30.0.10\\ombisos __OMB_ISO_PASSWORD__ /user:omb-iso\r\n")
	base, err := RenderStrelecNetwork(native, template, strings.Repeat("A", 32), 17)
	if err != nil {
		t.Fatal(err)
	}
	got, err := RenderDriverStartup(base, []string{"omb-drivers/storage/vioscsi.inf", "omb-drivers/network/netkvm.inf"})
	if err != nil {
		t.Fatal(err)
	}
	script := string(got)
	first := strings.Index(script, `drvload "X:\Windows\System32\omb-drivers\storage\vioscsi.inf"`)
	second := strings.Index(script, `drvload "X:\Windows\System32\omb-drivers\network\netkvm.inf"`)
	init := strings.Index(script, "wpeutil InitializeNetwork")
	if first < 0 || second < first || init < second {
		t.Fatal("not every selected INF is attempted before network")
	}
	if strings.Contains(script[first:init], "goto driver_failed") || strings.Contains(script[first:init], "exit /b") {
		t.Fatal("a nonzero vioscsi result still skips NetKVM and the working native NIC")
	}
	if strings.Count(script[first:init], "echo Drvload exit code: %OMB_DRIVER_EXIT%") != 2 {
		t.Fatal("per-INF result was not recorded")
	}
}

func TestDriverStartupWorksWithSplitInstallModes(t *testing.T) {
	template := []byte("@echo off\r\nset OMB_ISO_PASSWORD=__OMB_ISO_PASSWORD__\r\nwpeinit\r\nnet use Y: \\\\10.30.0.10\\ombisos %OMB_ISO_PASSWORD% /user:omb-iso /persistent:no >nul 2>&1\r\n:media_ready\r\nif not exist Y:\\__OMB_PROFILE_ID__\\sources\\__OMB_INSTALL_FILE__ goto failed\r\nY:\\__OMB_PROFILE_ID__\\sources\\setup.exe /InstallFrom:Y:\\__OMB_PROFILE_ID__\\sources\\__OMB_INSTALL_FILE__\r\n:failed\r\ncmd /k\r\n")
	for _, setupSource := range []string{`X:\sources\setup.exe`, `Y:\17\sources\setup.exe`} {
		base, err := RenderSplitInstallStartup(template, strings.Repeat("A", 32), setupSource, "install-5", "install.wim")
		if err != nil {
			t.Fatal(err)
		}
		answer, err := RenderAnswerStartupSubdir(base, "install-5")
		if err != nil {
			t.Fatal(err)
		}
		for _, script := range [][]byte{base, answer} {
			got, err := RenderDriverStartup(script, []string{"omb-drivers/abcdef/net/driver.inf"})
			if err != nil {
				t.Fatalf("RenderDriverStartup(%s): %v", setupSource, err)
			}
			text := string(got)
			load := strings.Index(text, `drvload "X:\Windows\System32\omb-drivers\abcdef\net\driver.inf"`)
			if load < 0 || load > strings.Index(text, "wpeinit") || strings.Index(text, "wpeinit") > strings.Index(text, setupSource) {
				t.Fatalf("bad startup order for %s:\n%s", setupSource, text)
			}
		}
	}
}
