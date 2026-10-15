package network

import (
	"strings"
	"testing"
)

func TestRenderDnsmasqFullOffersLeasesAndArchitectureBootFiles(t *testing.T) {
	text, err := RenderDnsmasq(fullConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"port=0",
		"interface=enp2s0",
		"enable-tftp",
		"tftp-root=/srv/pxe/tftp",
		"dhcp-range=192.0.2.100,192.0.2.150,12h",
		"dhcp-match=set:ipxe,175",
		"dhcp-match=set:efi64,option:client-arch,7",
		"dhcp-boot=tag:!ipxe,tag:efi64,ipxe-shim.efi,,192.0.2.10",
		"dhcp-boot=tag:!ipxe,tag:!efi64,undionly.kpxe,,192.0.2.10",
		"dhcp-boot=tag:ipxe,http://192.0.2.10:8080/boot/menu.ipxe,,192.0.2.10",
	} {
		if !strings.Contains(text, line+"\n") {
			t.Errorf("missing config line %q:\n%s", line, text)
		}
	}
}

func TestRenderDnsmasqProxyNeverAllocatesLeases(t *testing.T) {
	c := fullConfig()
	c.Mode, c.DHCPStart, c.DHCPEnd = "proxy", "", ""
	text, err := RenderDnsmasq(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"dhcp-range=192.0.2.0,proxy",
		"pxe-service=tag:!ipxe,X86PC,PXE,undionly.kpxe,192.0.2.10",
		"pxe-service=tag:!ipxe,X86-64_EFI,PXE,ipxe-shim.efi,192.0.2.10",
		"dhcp-boot=tag:ipxe,http://192.0.2.10:8080/boot/menu.ipxe,,192.0.2.10",
	} {
		if !strings.Contains(text, line+"\n") {
			t.Errorf("missing proxy line %q:\n%s", line, text)
		}
	}
	if strings.Contains(text, "dhcp-range=192.0.2.100") {
		t.Fatalf("proxy mode allocated leases:\n%s", text)
	}
}
