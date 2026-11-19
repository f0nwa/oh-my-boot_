package control

import (
	"strings"
	"testing"

	"omb/internal/network"
)

func sampleNetworkConfig() network.Config {
	return network.Config{
		Mode:                 "full",
		Interface:            "ens19",
		ServerIP:             "10.30.0.10",
		Subnet:               "10.30.0.0/24",
		DHCPStart:            "10.30.0.100",
		DHCPEnd:              "10.30.0.120",
		UEFILoader:           "ipxe",
		DefaultDriver:        "snp",
		ReuseFirmwareNetwork: true,
		WinPETimeoutSeconds:  90,
		WinPERetrySeconds:    2,
		WinPEExitAction:      "cmd",
		BootURL:              "http://10.30.0.10:8080/boot/menu.ipxe",
		TFTPRoot:             "/var/lib/omb-tftp",
	}
}

func TestNetworkChangedMethodsWhenIdentical(t *testing.T) {
	c := sampleNetworkConfig()
	data := pageData{
		OldNetwork: c,
		Network:    c,
	}

	if data.NetworkChangedMode() {
		t.Errorf("Mode should not be marked changed")
	}
	if data.NetworkChangedInterface() {
		t.Errorf("Interface should not be marked changed")
	}
	if data.NetworkChangedServerIP() {
		t.Errorf("ServerIP should not be marked changed")
	}
	if data.NetworkChangedSubnet() {
		t.Errorf("Subnet should not be marked changed")
	}
	if data.NetworkChangedUEFILoader() {
		t.Errorf("UEFILoader should not be marked changed")
	}
	if data.NetworkChangedDefaultDriver() {
		t.Errorf("DefaultDriver should not be marked changed")
	}
	if data.NetworkChangedReuseFirmware() {
		t.Errorf("ReuseFirmware should not be marked changed")
	}
	if data.NetworkChangedWinPETimeout() {
		t.Errorf("WinPETimeout should not be marked changed")
	}
	if data.NetworkChangedWinPERetry() {
		t.Errorf("WinPERetry should not be marked changed")
	}
	if data.NetworkChangedWinPEExitAction() {
		t.Errorf("WinPEExitAction should not be marked changed")
	}
	if data.NetworkChangedDHCPPool() {
		t.Errorf("DHCPPool should not be marked changed")
	}
	if data.NetworkChangedBootURL() {
		t.Errorf("BootURL should not be marked changed")
	}
	if data.NetworkChangedTFTPRoot() {
		t.Errorf("TFTPRoot should not be marked changed")
	}
	if count := data.NetworkChangesCount(); count != 0 {
		t.Errorf("NetworkChangesCount should be 0, got %d", count)
	}
}

func TestNetworkChangedSingleWinPEExitAction(t *testing.T) {
	oldCfg := sampleNetworkConfig()
	newCfg := sampleNetworkConfig()
	newCfg.WinPEExitAction = "reboot_timer"

	data := pageData{
		OldNetwork: oldCfg,
		Network:    newCfg,
	}

	if !data.NetworkChangedWinPEExitAction() {
		t.Errorf("WinPEExitAction should be marked changed")
	}
	if data.NetworkChangedMode() || data.NetworkChangedInterface() || data.NetworkChangedServerIP() ||
		data.NetworkChangedSubnet() || data.NetworkChangedUEFILoader() || data.NetworkChangedDefaultDriver() ||
		data.NetworkChangedReuseFirmware() || data.NetworkChangedWinPETimeout() || data.NetworkChangedWinPERetry() ||
		data.NetworkChangedDHCPPool() || data.NetworkChangedBootURL() || data.NetworkChangedTFTPRoot() {
		t.Errorf("Other fields must not be marked changed")
	}
	if count := data.NetworkChangesCount(); count != 1 {
		t.Errorf("NetworkChangesCount should be 1, got %d", count)
	}
}

func TestNetworkChangedSubnetAndPool(t *testing.T) {
	oldCfg := sampleNetworkConfig()
	newCfg := sampleNetworkConfig()
	newCfg.Subnet = "10.40.0.0/24"
	newCfg.DHCPStart = "10.40.0.100"
	newCfg.DHCPEnd = "10.40.0.150"

	data := pageData{
		OldNetwork: oldCfg,
		Network:    newCfg,
	}

	if !data.NetworkChangedSubnet() {
		t.Errorf("Subnet should be marked changed")
	}
	if !data.NetworkChangedDHCPPool() {
		t.Errorf("DHCPPool should be marked changed")
	}
	if count := data.NetworkChangesCount(); count != 2 {
		t.Errorf("NetworkChangesCount should be 2, got %d", count)
	}
}

func TestNetworkChangedUEFILoaderAndDriverEffects(t *testing.T) {
	// 1. Switching from iPXE to GRUB
	oldCfg := sampleNetworkConfig()
	newCfg := sampleNetworkConfig()
	newCfg.UEFILoader = "grub-test"

	data := pageData{
		OldNetwork: oldCfg,
		Network:    newCfg,
	}

	if !data.NetworkChangedUEFILoader() {
		t.Errorf("UEFILoader should be marked changed")
	}
	// Driver and reuse firmware become "not used (GRUB)", which is a changed state from active
	if !data.NetworkChangedDefaultDriver() {
		t.Errorf("DefaultDriver should be marked changed when transitioning to GRUB")
	}
	if !data.NetworkChangedReuseFirmware() {
		t.Errorf("ReuseFirmware should be marked changed when transitioning to GRUB")
	}

	// 2. Both already GRUB: driver and reuse firmware are not shown and not changed
	oldGrub := sampleNetworkConfig()
	oldGrub.UEFILoader = "grub-test"
	newGrub := sampleNetworkConfig()
	newGrub.UEFILoader = "grub-test"

	dataGrub := pageData{
		OldNetwork: oldGrub,
		Network:    newGrub,
	}
	if dataGrub.NetworkChangedUEFILoader() {
		t.Errorf("UEFILoader should not be marked changed if both are GRUB")
	}
	if dataGrub.NetworkChangedDefaultDriver() {
		t.Errorf("DefaultDriver should not be marked changed if both are GRUB")
	}
	if dataGrub.NetworkChangedReuseFirmware() {
		t.Errorf("ReuseFirmware should not be marked changed if both are GRUB")
	}
}

func TestNetworkPreviewDiffHTMLRendering(t *testing.T) {
	oldCfg := sampleNetworkConfig()
	newCfg := sampleNetworkConfig()
	newCfg.WinPEExitAction = "reboot_timer"

	dataRU := samplePageData(langRU)
	dataRU.OldNetwork = oldCfg
	dataRU.Network = newCfg

	bodyRU := renderPage("network_preview", dataRU)

	// Check that the table has row-changed for winpe_exit_action
	if !strings.Contains(bodyRU, `class="row-changed"`) {
		t.Fatalf("expected row-changed class in rendered RU HTML:\n%s", bodyRU)
	}
	if !strings.Contains(bodyRU, `<span class="row-change-badge">изменено</span>`) {
		t.Fatalf("expected 'изменено' badge in rendered RU HTML:\n%s", bodyRU)
	}
	if !strings.Contains(bodyRU, "Действие при закрытии установщика") {
		t.Fatalf("expected 'Действие при закрытии установщика' in rendered RU HTML")
	}

	// Check English rendering
	dataEN := samplePageData(langEN)
	dataEN.OldNetwork = oldCfg
	dataEN.Network = newCfg

	bodyEN := renderPage("network_preview", dataEN)
	if !strings.Contains(bodyEN, `class="row-changed"`) {
		t.Fatalf("expected row-changed class in rendered EN HTML:\n%s", bodyEN)
	}
	if !strings.Contains(bodyEN, `<span class="row-change-badge">changed</span>`) {
		t.Fatalf("expected 'changed' badge in rendered EN HTML:\n%s", bodyEN)
	}
}
