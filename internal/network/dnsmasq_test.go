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
		"tftp-root=/srv/omb/tftp",
		"dhcp-range=192.0.2.100,192.0.2.150,12h",
		"dhcp-match=set:ipxe,175",
		"dhcp-match=set:efi64,option:client-arch,7",
		"dhcp-boot=tag:!ipxe,tag:efi64,tag:!omb-driver-machine-snp,tag:!omb-driver-machine-ipxe,tag:!omb-driver-p5-snp,tag:!omb-driver-p5-ipxe,tag:!omb-driver-p4-snp,tag:!omb-driver-p4-ipxe,tag:!omb-driver-p3-snp,tag:!omb-driver-p3-ipxe,ipxeboot/x86_64-sb/ipxe-shim.efi,,192.0.2.10",
		"dhcp-boot=tag:!ipxe,tag:!efi64,ipxeboot/x86_64/undionly.kpxe,,192.0.2.10",
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
		"pxe-service=tag:!ipxe,X86PC,PXE,ipxeboot/x86_64/undionly.kpxe,192.0.2.10",
		"pxe-service=tag:!ipxe,tag:!omb-driver-machine-snp,tag:!omb-driver-machine-ipxe,tag:!omb-driver-p5-snp,tag:!omb-driver-p5-ipxe,tag:!omb-driver-p4-snp,tag:!omb-driver-p4-ipxe,tag:!omb-driver-p3-snp,tag:!omb-driver-p3-ipxe,X86-64_EFI,PXE,ipxeboot/x86_64-sb/ipxe-shim.efi,192.0.2.10",
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

func TestRenderDnsmasqGRUBOffersDebianShimToUEFIOnly(t *testing.T) {
	for _, mode := range []string{"full", "proxy"} {
		t.Run(mode, func(t *testing.T) {
			c := fullConfig()
			c.UEFILoader = "grub-test"
			c.Mode = mode
			if mode == "proxy" {
				c.DHCPStart, c.DHCPEnd = "", ""
			}
			got, err := RenderDnsmasq(c)
			if err != nil {
				t.Fatal(err)
			}
			wantUEFI := "dhcp-boot=tag:!ipxe,tag:efi64,debian-installer/amd64/bootnetx64.efi,,192.0.2.10"
			wantBIOS := "dhcp-boot=tag:!ipxe,tag:!efi64,ipxeboot/x86_64/undionly.kpxe,,192.0.2.10"
			if mode == "proxy" {
				wantUEFI = "pxe-service=tag:!ipxe,X86-64_EFI,PXE,debian-installer/amd64/bootnetx64.efi,192.0.2.10"
				wantBIOS = "pxe-service=tag:!ipxe,X86PC,PXE,ipxeboot/x86_64/undionly.kpxe,192.0.2.10"
				if !strings.Contains(got, "pxe-service=tag:!ipxe,BC_EFI,PXE,debian-installer/amd64/bootnetx64.efi,192.0.2.10\n") {
					t.Fatalf("BC_EFI does not get Debian shim:\n%s", got)
				}
			}
			for _, line := range []string{wantUEFI, wantBIOS, "dhcp-boot=tag:ipxe,http://192.0.2.10:8080/boot/menu.ipxe,,192.0.2.10"} {
				if !strings.Contains(got, line+"\n") {
					t.Errorf("missing %q:\n%s", line, got)
				}
			}
			if strings.Contains(got, "ipxeboot/x86_64-sb/ipxe-shim.efi") {
				t.Fatalf("UEFI still gets iPXE shim:\n%s", got)
			}
		})
	}
}
