package autoinstall

import (
	"errors"
	"net"
	"net/netip"
)

type NetworkValues struct {
	Mode, MAC, Address, Gateway string
	DNS                         []string
}

func normalizeNetwork(n NetworkValues) NetworkValues {
	n.DNS = uniqueStrings(n.DNS)
	if mac, e := net.ParseMAC(n.MAC); e == nil {
		n.MAC = mac.String()
	}
	return n
}
func validateNetwork(n NetworkValues) error {
	if n.Mode == "" {
		if n.MAC != "" || n.Address != "" || n.Gateway != "" || len(n.DNS) > 0 {
			return errors.New("network: static fields require static mode")
		}
		return nil
	}
	if n.Mode != "static" {
		return errors.New("network: invalid mode")
	}
	mac, e := net.ParseMAC(n.MAC)
	if e != nil || len(mac) != 6 || mac[0]&1 != 0 || n.MAC == "00:00:00:00:00:00" {
		return errors.New("network mac: exact unicast MAC required")
	}
	p, e := netip.ParsePrefix(n.Address)
	if e != nil || !p.Addr().Is4() || p.Bits() < 1 || p.Addr().IsUnspecified() || p.Addr().IsMulticast() {
		return errors.New("network address: IPv4 prefix required")
	}
	if p.Bits() <= 30 {
		host := p.Addr().As4()
		base := p.Masked().Addr().As4()
		h := uint32(host[0])<<24 | uint32(host[1])<<16 | uint32(host[2])<<8 | uint32(host[3])
		b := uint32(base[0])<<24 | uint32(base[1])<<16 | uint32(base[2])<<8 | uint32(base[3])
		if h == b || h == b|(^uint32(0)>>p.Bits()) {
			return errors.New("network address: host address required")
		}
	}
	if n.Gateway != "" {
		g, e := netip.ParseAddr(n.Gateway)
		if e != nil || !g.Is4() || g.IsUnspecified() || g.IsMulticast() || !p.Contains(g) || g == p.Addr() {
			return errors.New("network gateway: gateway in the same subnet required")
		}
	}
	if len(n.DNS) > 4 {
		return errors.New("network dns: at most four addresses")
	}
	for _, s := range n.DNS {
		a, e := netip.ParseAddr(s)
		if e != nil || a.IsUnspecified() || a.IsMulticast() || a.Zone() != "" {
			return errors.New("network dns: invalid address")
		}
	}
	return nil
}
func networkConfig(n NetworkValues) map[string]any {
	if n.Mode == "" {
		return nil
	}
	e := map[string]any{"match": map[string]string{"macaddress": n.MAC}, "dhcp4": false, "addresses": []string{n.Address}}
	if n.Gateway != "" {
		e["routes"] = []map[string]string{{"to": "default", "via": n.Gateway}}
	}
	if len(n.DNS) > 0 {
		e["nameservers"] = map[string]any{"addresses": n.DNS}
	}
	return map[string]any{"version": 2, "renderer": "NetworkManager", "ethernets": map[string]any{"omb0": e}}
}
