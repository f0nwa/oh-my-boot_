package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omb/internal/winpe"
)

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func setupWinPEWaitFixture(t *testing.T) (*Applier, *fakeRunner, string) {
	t.Helper()
	dir := t.TempDir()
	runner := &fakeRunner{}
	applier, err := NewApplier(
		filepath.Join(dir, "dnsmasq.conf"),
		filepath.Join(dir, "state.json"),
		filepath.Join(dir, "dhcp", "hosts"),
		runner,
	)
	if err != nil {
		t.Fatal(err)
	}

	rawTemplate, err := os.ReadFile("../../deploy/boot-assets/windows-profiles/omb-setup.cmd.in")
	if err != nil {
		t.Fatal(err)
	}
	fallbackTemplate := filepath.Join(dir, "share", "omb-setup.cmd.in")
	customTemplate := filepath.Join(dir, "etc", "omb-setup.cmd.in")
	if err := os.MkdirAll(filepath.Dir(fallbackTemplate), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fallbackTemplate, rawTemplate, 0o644); err != nil {
		t.Fatal(err)
	}

	profilesRoot := filepath.Join(dir, "tftp", "windows-profiles")
	driversRoot := filepath.Join(dir, "tftp", "windows-driver-configs")

	baseScript, err := winpe.RenderProfileStartup(rawTemplate, strings.Repeat("A", 32), 17, "install.wim")
	if err != nil {
		t.Fatal(err)
	}
	answerScript, err := winpe.RenderAnswerStartup(baseScript, 17)
	if err != nil {
		t.Fatal(err)
	}
	driverScript, err := winpe.RenderDriverStartup(answerScript, []string{"omb-drivers/abc/net.inf"})
	if err != nil {
		t.Fatal(err)
	}
	strelecNative := []byte("net start Netman\r\nnet use Z: \\\\demo\\share /user:demo demo\r\nZ:\\SSTR\\MInst\\Portable\\x64\\Files_11_x64.exe\r\nexit\r\n")
	strelecScript, err := winpe.RenderStrelecNetwork(strelecNative, rawTemplate, strings.Repeat("A", 32), 80)
	if err != nil {
		t.Fatal(err)
	}

	for path, data := range map[string][]byte{
		filepath.Join(profilesRoot, "17", "omb-setup.cmd"):        baseScript,
		filepath.Join(profilesRoot, "17", "omb-answer-setup.cmd"): answerScript,
		filepath.Join(profilesRoot, "80", "omb-setup.cmd"):        strelecScript,
		filepath.Join(driversRoot, "4", "omb-setup.cmd"):          driverScript,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o640); err != nil {
			t.Fatal(err)
		}
	}

	manifest := driverConfigManifest{
		Request: DriverBuildRequest{
			ConfigID:  4,
			ProfileID: 17,
			AnswerID:  31,
			PackageINFs: []SelectedINF{
				{PackageSHA256: strings.Repeat("a", 64), INFPath: "net.inf"},
			},
		},
		Result: DriverBuildResult{
			WIMKey:       strings.Repeat("b", 64),
			WIMSHA256:    strings.Repeat("c", 64),
			ScriptSHA256: sha256Hex(driverScript),
		},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(driversRoot, "4", "build.json"), manifestBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	applier.WinPE = &WinPEWaitUpdater{
		CustomTemplatePath:  customTemplate,
		DefaultTemplatePath: fallbackTemplate,
		ProfileRoot:         profilesRoot,
		DriverConfigRoot:    driversRoot,
	}
	return applier, runner, dir
}

func TestWinPEWaitUpdatesTemplateScriptsAndManifests(t *testing.T) {
	applier, _, dir := setupWinPEWaitFixture(t)
	oldDriverScript, err := os.ReadFile(filepath.Join(dir, "tftp", "windows-driver-configs", "4", "omb-setup.cmd"))
	if err != nil {
		t.Fatal(err)
	}
	oldSHA := sha256Hex(oldDriverScript)

	updates, err := applier.UpdateWinPEScripts(context.Background(), 30, 5)
	if err != nil {
		t.Fatalf("UpdateWinPEScripts failed: %v", err)
	}
	if len(updates) != 1 || updates[0].ConfigID != 4 || updates[0].OldSHA != oldSHA || updates[0].NewSHA == oldSHA {
		t.Fatalf("unexpected updates: %+v (oldSHA=%s)", updates, oldSHA)
	}

	customTmpl, err := os.ReadFile(filepath.Join(dir, "etc", "omb-setup.cmd.in"))
	if err != nil {
		t.Fatalf("custom template not written: %v", err)
	}
	if attempts, pause, ok := winpe.WaitFromScript(customTmpl); !ok || attempts != 30 || pause != 5 {
		t.Fatalf("custom template wait = %d/%d/%t, want 30/5/true", attempts, pause, ok)
	}

	for _, rel := range []string{
		"tftp/windows-profiles/17/omb-setup.cmd",
		"tftp/windows-profiles/17/omb-answer-setup.cmd",
		"tftp/windows-driver-configs/4/omb-setup.cmd",
	} {
		data, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		if attempts, pause, ok := winpe.WaitFromScript(data); !ok || attempts != 30 || pause != 5 {
			t.Fatalf("%s: wait = %d/%d/%t, want 30/5/true", rel, attempts, pause, ok)
		}
	}

	strelecData, err := os.ReadFile(filepath.Join(dir, "tftp", "windows-profiles", "80", "omb-setup.cmd"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(strelecData), "if %OMB_STRELEC_ATTEMPT% GEQ 30 goto omb_strelec_failed\r\nping -n 6 127.0.0.1 >nul\r\n") {
		t.Fatalf("Strelec script not updated:\n%s", strelecData)
	}

	newDriverScript, err := os.ReadFile(filepath.Join(dir, "tftp", "windows-driver-configs", "4", "omb-setup.cmd"))
	if err != nil {
		t.Fatal(err)
	}
	if sha256Hex(newDriverScript) != updates[0].NewSHA {
		t.Fatal("driver script hash does not match returned NewSHA")
	}
	manifestData, err := os.ReadFile(filepath.Join(dir, "tftp", "windows-driver-configs", "4", "build.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest driverConfigManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Result.ScriptSHA256 != updates[0].NewSHA {
		t.Fatalf("manifest script_sha256 = %s, want %s", manifest.Result.ScriptSHA256, updates[0].NewSHA)
	}
}

func TestWinPEWaitRollsBackOnCorruptDriverManifest(t *testing.T) {
	applier, _, dir := setupWinPEWaitFixture(t)
	profilePath := filepath.Join(dir, "tftp", "windows-profiles", "17", "omb-setup.cmd")
	beforeProfile, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tftp", "windows-driver-configs", "4", "build.json"), []byte("{corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := applier.UpdateWinPEScripts(context.Background(), 25, 3); err == nil {
		t.Fatal("expected failure on corrupt driver manifest")
	}
	afterProfile, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterProfile) != string(beforeProfile) {
		t.Fatal("profile script was modified despite failed driver manifest")
	}
	if _, err := os.Stat(filepath.Join(dir, "etc", "omb-setup.cmd.in")); !os.IsNotExist(err) {
		t.Fatalf("custom template was not rolled back: %v", err)
	}
}

func TestWinPEWaitSkipsDnsmasqRestartWhenOnlyWinPEChanges(t *testing.T) {
	applier, runner, dir := setupWinPEWaitFixture(t)
	cfg := agentConfig()
	if err := applier.Apply(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if runner.restarts != 1 {
		t.Fatalf("initial apply restarts = %d, want 1", runner.restarts)
	}

	cfg.WinPETimeoutSeconds = 120
	cfg.WinPERetrySeconds = 3
	if err := applier.Apply(context.Background(), cfg); err != nil {
		t.Fatalf("WinPE-only apply failed: %v", err)
	}
	if runner.restarts != 1 {
		t.Fatalf("WinPE-only change restarted dnsmasq: restarts = %d, want 1", runner.restarts)
	}
	profileData, err := os.ReadFile(filepath.Join(dir, "tftp", "windows-profiles", "17", "omb-setup.cmd"))
	if err != nil {
		t.Fatal(err)
	}
	if attempts, pause, ok := winpe.WaitFromScript(profileData); !ok || attempts != 40 || pause != 3 {
		t.Fatalf("profile wait after Apply = %d/%d/%t, want 40/3/true", attempts, pause, ok)
	}
}

func TestWinPEWaitRollsBackWhenDnsmasqRestartFails(t *testing.T) {
	applier, runner, dir := setupWinPEWaitFixture(t)
	runner.failOnce = true
	cfg := agentConfig()
	cfg.WinPETimeoutSeconds = 100
	cfg.WinPERetrySeconds = 5

	if err := applier.Apply(context.Background(), cfg); err == nil {
		t.Fatal("expected Apply failure when dnsmasq restart fails")
	}
	profileData, err := os.ReadFile(filepath.Join(dir, "tftp", "windows-profiles", "17", "omb-setup.cmd"))
	if err != nil {
		t.Fatal(err)
	}
	if attempts, pause, ok := winpe.WaitFromScript(profileData); !ok || attempts != 45 || pause != 2 {
		t.Fatalf("profile wait not rolled back: got %d/%d/%t, want 45/2/true", attempts, pause, ok)
	}
	if _, err := os.Stat(filepath.Join(dir, "etc", "omb-setup.cmd.in")); !os.IsNotExist(err) {
		t.Fatalf("custom template was not removed on rollback: %v", err)
	}
}
