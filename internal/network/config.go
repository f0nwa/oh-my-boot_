package network

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var interfaceName = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

type Config struct {
	ReuseFirmwareNetwork bool   `json:"reuse_firmware_network,omitempty"`
	DefaultDriver        string `json:"default_driver,omitempty"`
	Mode                 string `json:"mode"`
	Interface            string `json:"interface"`
	ServerIP             string `json:"server_ip"`
	Subnet               string `json:"subnet"`
	DHCPStart            string `json:"dhcp_start"`
	DHCPEnd              string `json:"dhcp_end"`
	BootURL              string `json:"boot_url"`
	TFTPRoot             string `json:"tftp_root"`
	UEFILoader           string `json:"uefi_loader,omitempty"`
}

// Validate rejects settings that could enable DHCP on the wrong network.
func Validate(c Config) error {
	if c.DefaultDriver != "" && c.DefaultDriver != "ipxe" && c.DefaultDriver != "snp" {
		return fmt.Errorf("default driver must be ipxe or snp")
	}
	if c.UEFILoader != "" && c.UEFILoader != "ipxe" && c.UEFILoader != "grub-test" {
		return fmt.Errorf("UEFI loader must be ipxe or grub-test")
	}
	if c.Mode != "full" && c.Mode != "proxy" {
		return fmt.Errorf("mode must be full or proxy")
	}
	if !interfaceName.MatchString(c.Interface) {
		return fmt.Errorf("invalid network interface")
	}
	prefix, err := netip.ParsePrefix(c.Subnet)
	if err != nil || !prefix.Addr().Is4() || prefix.Bits() < 1 || prefix.Bits() > 30 || prefix != prefix.Masked() {
		return fmt.Errorf("invalid IPv4 subnet")
	}
	server, err := netip.ParseAddr(c.ServerIP)
	if err != nil || !server.Is4() || !prefix.Contains(server) {
		return fmt.Errorf("server IP must be inside subnet")
	}
	first, last := usableBounds(prefix)
	if asUint32(server) < first || asUint32(server) > last {
		return fmt.Errorf("server IP is a network or broadcast address")
	}
	u, err := url.Parse(c.BootURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" || strings.ContainsAny(c.BootURL, "\n\r") {
		return fmt.Errorf("invalid HTTP boot URL")
	}
	if c.ReuseFirmwareNetwork {
		// DHCP suboptions have an 8-bit length; the enclosing option also
		// needs two bytes for the suboption header. The URL must stay one
		// literal iPXE token inside a quoted dnsmasq string.
		if len(firmwareNetworkScript(c.BootURL)) > 253 || strings.ContainsAny(c.BootURL, "\"\\$") || strings.ContainsFunc(c.BootURL, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
			return fmt.Errorf("boot URL is too long or contains characters unsafe for firmware network startup; use a shorter, percent-encoded URL")
		}
	}
	if !filepath.IsAbs(c.TFTPRoot) || strings.ContainsAny(c.TFTPRoot, "\n\r,") {
		return fmt.Errorf("TFTP root must be an absolute path without separators")
	}
	if c.Mode == "proxy" {
		if c.DHCPStart != "" || c.DHCPEnd != "" {
			return fmt.Errorf("proxy mode cannot allocate DHCP leases")
		}
		return nil
	}
	start, startErr := netip.ParseAddr(c.DHCPStart)
	end, endErr := netip.ParseAddr(c.DHCPEnd)
	if startErr != nil || endErr != nil || !start.Is4() || !end.Is4() || !prefix.Contains(start) || !prefix.Contains(end) {
		return fmt.Errorf("DHCP range must be inside subnet")
	}
	startN, endN, serverN := asUint32(start), asUint32(end), asUint32(server)
	if startN > endN || startN < first || endN > last {
		return fmt.Errorf("invalid DHCP range")
	}
	if startN <= serverN && serverN <= endN {
		return fmt.Errorf("DHCP range contains server IP")
	}
	return nil
}

func asUint32(addr netip.Addr) uint32 {
	a := addr.As4()
	return binary.BigEndian.Uint32(a[:])
}

func usableBounds(prefix netip.Prefix) (uint32, uint32) {
	base := asUint32(prefix.Addr())
	mask := uint32(0xffffffff) << (32 - prefix.Bits())
	return base + 1, base | ^mask - 1
}
