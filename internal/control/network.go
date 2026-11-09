package control

import (
	"fmt"
	"net"
	"net/netip"

	"omb/internal/network"
)

func ValidateNetworkTarget(c network.Config, allowed map[string]bool, interfaceIPs []netip.Addr) error {
	if !allowed[c.Interface] {
		return fmt.Errorf("PXE interface is not allowed")
	}
	if err := network.Validate(c); err != nil {
		return err
	}
	wanted, err := netip.ParseAddr(c.ServerIP)
	if err != nil {
		return err
	}
	for _, ip := range interfaceIPs {
		if ip == wanted {
			return nil
		}
	}
	return fmt.Errorf("server IP is not assigned to selected interface")
}

var InterfaceIPs = func(name string) ([]netip.Addr, error) {
	nic, err := net.InterfaceByName(name)
	if err != nil {
		return nil, err
	}
	addresses, err := nic.Addrs()
	if err != nil {
		return nil, err
	}
	out := []netip.Addr{}
	for _, address := range addresses {
		prefix, err := netip.ParsePrefix(address.String())
		if err == nil {
			out = append(out, prefix.Addr())
		}
	}
	return out, nil
}
