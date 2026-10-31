package control

import (
	"net/netip"
	"testing"

	"omb/internal/network"
)

func TestNetworkTargetRejectsManagementInterfaceAndWrongIP(t *testing.T) {
	config := network.Config{Mode: "proxy", Interface: "ens18", ServerIP: "10.30.0.10", Subnet: "10.30.0.0/24", BootURL: "http://10.30.0.10:8080/boot/menu.ipxe", TFTPRoot: "/srv/omb/tftp"}
	allowed := map[string]bool{"ens19": true}
	if err := ValidateNetworkTarget(config, allowed, []netip.Addr{netip.MustParseAddr("10.30.0.10")}); err == nil {
		t.Fatal("management interface accepted")
	}
	config.Interface = "ens19"
	config.ServerIP = "10.30.0.11"
	if err := ValidateNetworkTarget(config, allowed, []netip.Addr{netip.MustParseAddr("10.30.0.10")}); err == nil {
		t.Fatal("nonlocal IP accepted")
	}
	config.ServerIP = "10.30.0.10"
	if err := ValidateNetworkTarget(config, allowed, []netip.Addr{netip.MustParseAddr("10.30.0.10")}); err != nil {
		t.Fatal(err)
	}
}
