package setup

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"

	"omb/internal/network"
)

// NetIface describes an active non-loopback IPv4 interface on the host.
type NetIface struct {
	Name   string
	IP     netip.Addr
	Prefix netip.Prefix
}

// DetectInterfaces scans host network interfaces and returns active non-loopback IPv4 addresses.
func DetectInterfaces() ([]NetIface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list network interfaces: %w", err)
	}
	var out []NetIface
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, raw := range addrs {
			ipNet, ok := raw.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipNet.IP.To4()
			if ip4 == nil {
				continue
			}
			addr, ok := netip.AddrFromSlice(ip4)
			if !ok || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsUnspecified() {
				continue
			}
			ones, bits := ipNet.Mask.Size()
			if bits != 32 {
				continue
			}
			out = append(out, NetIface{
				Name:   iface.Name,
				IP:     addr,
				Prefix: netip.PrefixFrom(addr, ones).Masked(),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].IP.Less(out[j].IP)
	})
	return out, nil
}

// DefaultDHCPRange calculates a safe default DHCP lease range inside prefix that excludes serverIP.
func DefaultDHCPRange(prefix netip.Prefix, serverIP netip.Addr) (start, end netip.Addr, err error) {
	if !prefix.IsValid() || !prefix.Addr().Is4() || prefix.Bits() < 1 || prefix.Bits() > 30 {
		return netip.Addr{}, netip.Addr{}, fmt.Errorf("invalid IPv4 subnet %q", prefix)
	}
	prefix = prefix.Masked()
	if !serverIP.IsValid() || !serverIP.Is4() || !prefix.Contains(serverIP) {
		return netip.Addr{}, netip.Addr{}, fmt.Errorf("server IP %q is outside subnet %s", serverIP, prefix)
	}

	first, last := usableIPv4Bounds(prefix)
	serverN := ipv4ToUint32(serverIP)
	if serverN < first || serverN > last {
		return netip.Addr{}, netip.Addr{}, fmt.Errorf("server IP %s is a network or broadcast address", serverIP)
	}
	if last <= first {
		return netip.Addr{}, netip.Addr{}, fmt.Errorf("subnet %s has no usable hosts besides server IP", prefix)
	}

	if prefix.Bits() == 24 {
		base := ipv4ToUint32(prefix.Addr())
		prefStart, prefEnd := base+100, base+200
		if serverN < prefStart || serverN > prefEnd {
			return uint32ToIPv4(prefStart), uint32ToIPv4(prefEnd), nil
		}
		leftSize := serverN - prefStart
		rightSize := prefEnd - serverN
		if rightSize >= leftSize && rightSize > 0 {
			return uint32ToIPv4(serverN + 1), uint32ToIPv4(prefEnd), nil
		}
		return uint32ToIPv4(prefStart), uint32ToIPv4(serverN - 1), nil
	}

	count := last - first + 1
	if count == 2 {
		if serverN == first {
			return uint32ToIPv4(last), uint32ToIPv4(last), nil
		}
		return uint32ToIPv4(first), uint32ToIPv4(first), nil
	}

	mid := first + count/2
	if serverN < mid || serverN > last {
		return uint32ToIPv4(mid), uint32ToIPv4(last), nil
	}
	leftSize := serverN - first
	rightSize := last - serverN
	if rightSize >= leftSize && rightSize > 0 {
		return uint32ToIPv4(serverN + 1), uint32ToIPv4(last), nil
	}
	return uint32ToIPv4(first), uint32ToIPv4(serverN - 1), nil
}

// ResolveAllowedInterfaces computes the value for OMB_CONTROL_INTERFACES.
func ResolveAllowedInterfaces(ifaces []NetIface, mgmtIP netip.Addr, pxeIface string, nonInteractive bool) string {
	if pxeIface != "" && pxeIface != "none" {
		return pxeIface
	}
	if nonInteractive && pxeIface == "none" {
		return "omb-pxe-off"
	}

	mgmtNames := make(map[string]bool)
	for _, nic := range ifaces {
		if nic.IP == mgmtIP {
			mgmtNames[nic.Name] = true
		}
	}

	var nonMgmt, all []string
	seenNonMgmt := make(map[string]bool)
	seenAll := make(map[string]bool)
	for _, nic := range ifaces {
		if nic.Name == "" {
			continue
		}
		if !seenAll[nic.Name] {
			seenAll[nic.Name] = true
			all = append(all, nic.Name)
		}
		if !mgmtNames[nic.Name] && !seenNonMgmt[nic.Name] {
			seenNonMgmt[nic.Name] = true
			nonMgmt = append(nonMgmt, nic.Name)
		}
	}
	if len(nonMgmt) > 0 {
		return strings.Join(nonMgmt, ",")
	}
	if len(all) > 0 {
		return strings.Join(all, ",")
	}
	return "omb-pxe-off"
}

// BuildNetworkConfig constructs and validates an omb-agent network.Config from setup Options.
func BuildNetworkConfig(opts Options) (network.Config, error) {
	cfg := network.Config{
		Mode:      opts.DHCPMode,
		Interface: opts.PXEInterface,
		ServerIP:  opts.PXEServerIP,
		Subnet:    opts.PXESubnet,
		BootURL:   fmt.Sprintf("http://%s:8080/boot/menu.ipxe", opts.PXEServerIP),
		TFTPRoot:  "/srv/omb/tftp",
	}
	if opts.DHCPMode == "full" {
		cfg.DHCPStart = opts.DHCPStart
		cfg.DHCPEnd = opts.DHCPEnd
	}
	if err := network.Validate(cfg); err != nil {
		return network.Config{}, err
	}
	return cfg, nil
}

func ipv4ToUint32(addr netip.Addr) uint32 {
	a := addr.As4()
	return binary.BigEndian.Uint32(a[:])
}

func uint32ToIPv4(v uint32) netip.Addr {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	return netip.AddrFrom4(b)
}

func usableIPv4Bounds(prefix netip.Prefix) (uint32, uint32) {
	base := ipv4ToUint32(prefix.Addr())
	mask := uint32(0xffffffff) << (32 - prefix.Bits())
	return base + 1, (base | ^mask) - 1
}
