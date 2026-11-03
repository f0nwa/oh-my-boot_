package winpe

import (
	"os"
	"strings"
	"testing"
)

// An advisory wait failure must still reach the connection retries. The script
// is generated through each supported composition, including answer + drivers.
func TestStartupNetworkWaitFailureReachesMediaRetries(t *testing.T) {
	template, err := os.ReadFile("../../deploy/boot-assets/windows-profiles/omb-setup.cmd.in")
	if err != nil {
		t.Fatal(err)
	}
	base, err := RenderProfileStartup(template, strings.Repeat("A", 32), 17, "install.wim")
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
	for name, script := range map[string][]byte{"profile": base, "answer": answer, "driver": driver, "answer_driver": combined} {
		t.Run(name, func(t *testing.T) {
			text := string(script)
			wait := strings.Index(text, "wpeutil WaitForNetwork")
			connect := strings.Index(text, "echo OMB: connecting installation media...\r\n")
			if wait < 0 || connect < wait {
				t.Fatal("network wait/connection stages missing")
			}
			waitLineEnd := wait + strings.Index(text[wait:], "\r\n")
			waitLine := text[wait:waitLineEnd]
			if !strings.Contains(waitLine, `>"%SystemRoot%\System32\omb-network-wait.log" 2>&1`) {
				t.Fatal("recoverable Windows wait error still prints to the operator console")
			}
			afterWait := text[waitLineEnd+2 : connect]
			// A goto failed here reproduces the photographed premature startup exit.
			if strings.Contains(strings.ToLower(afterWait), "goto failed") {
				t.Fatal("network wait failure exits before media retries")
			}
			if !strings.Contains(afterWait, "%errorlevel%") {
				t.Fatal("network wait result is discarded without a diagnostic")
			}
			if !strings.Contains(afterWait, `echo WaitForNetwork exit code: %OMB_WAIT_ERROR%`) || !strings.Contains(afterWait, "echo OMB: network is not ready yet; waiting for installation media...") {
				t.Fatal("technical wait status or operator waiting message is missing")
			}
			retryStart := strings.Index(text, ":connect_media\r\n")
			retryEnd := strings.Index(text, ":media_ready\r\n")
			if retryStart < connect || retryEnd < retryStart {
				t.Fatal("media retry path missing")
			}
			retry := text[retryStart:retryEnd]
			for _, required := range []string{"if %OMB_ATTEMPT% GEQ 45 goto connect_final\r\n", "if not errorlevel 1 goto media_ready\r\n", "goto connect_media\r\n", ":connect_final\r\n", "goto failed\r\n"} {
				if !strings.Contains(retry, required) {
					t.Fatalf("bounded connection/failure path missing %q", required)
				}
			}
			if !strings.Contains(text, "if not exist Y:\\17\\sources\\install.wim goto failed\r\n") {
				t.Fatal("Setup no longer requires the installation image")
			}
		})
	}
}
