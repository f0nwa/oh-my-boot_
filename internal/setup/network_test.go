package setup

import (
	"net/netip"
	"testing"

	"omb/internal/network"
)

func TestDefaultDHCPRange(t *testing.T) {
	tests := []struct {
		name      string
		subnet    string
		serverIP  string
		wantStart string // optional exact check when non-empty
		wantEnd   string // optional exact check when non-empty
	}{
		{
			name:      "/24 with .10",
			subnet:    "10.30.0.0/24",
			serverIP:  "10.30.0.10",
			wantStart: "10.30.0.100",
			wantEnd:   "10.30.0.200",
		},
		{
			name:     "/24 with .150 inside .100-.200",
			subnet:   "10.30.0.0/24",
			serverIP: "10.30.0.150",
		},
		{
			name:     "/28 small subnet",
			subnet:   "192.168.50.0/28",
			serverIP: "192.168.50.1",
		},
		{
			name:     "/30 single usable host besides server",
			subnet:   "172.16.0.0/30",
			serverIP: "172.16.0.1",
			wantStart: "172.16.0.2",
			wantEnd:   "172.16.0.2",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prefix := netip.MustParsePrefix(tc.subnet)
			server := netip.MustParseAddr(tc.serverIP)

			start, end, err := DefaultDHCPRange(prefix, server)
			if err != nil {
				t.Fatalf("DefaultDHCPRange(%s, %s) unexpected error: %v", tc.subnet, tc.serverIP, err)
			}

			if tc.wantStart != "" && start.String() != tc.wantStart {
				t.Errorf("start = %s, want %s", start, tc.wantStart)
			}
			if tc.wantEnd != "" && end.String() != tc.wantEnd {
				t.Errorf("end = %s, want %s", end, tc.wantEnd)
			}

			cfg := network.Config{
				Mode:      "full",
				Interface: "eth1",
				ServerIP:  tc.serverIP,
				Subnet:    tc.subnet,
				DHCPStart: start.String(),
				DHCPEnd:   end.String(),
				BootURL:   "http://" + tc.serverIP + ":8080/boot/menu.ipxe",
				TFTPRoot:  "/srv/omb/tftp",
			}
			if err := network.Validate(cfg); err != nil {
				t.Errorf("returned range %s-%s failed network.Validate: %v", start, end, err)
			}
		})
	}
}

func TestResolveAllowedInterfaces(t *testing.T) {
	nic0 := NetIface{
		Name:   "ens18",
		IP:     netip.MustParseAddr("192.168.10.135"),
		Prefix: netip.MustParsePrefix("192.168.10.0/24"),
	}
	nic1 := NetIface{
		Name:   "ens19",
		IP:     netip.MustParseAddr("10.30.0.10"),
		Prefix: netip.MustParsePrefix("10.30.0.0/24"),
	}
	nic2 := NetIface{
		Name:   "ens20",
		IP:     netip.MustParseAddr("10.40.0.10"),
		Prefix: netip.MustParsePrefix("10.40.0.0/24"),
	}

	t.Run("explicit PXE interface", func(t *testing.T) {
		got := ResolveAllowedInterfaces([]NetIface{nic0, nic1}, nic0.IP, "ens19", false)
		if got != "ens19" {
			t.Fatalf("got %q, want %q", got, "ens19")
		}
	})

	t.Run("deferred PXE on 2-NIC host returns non-management interfaces joined by comma", func(t *testing.T) {
		got := ResolveAllowedInterfaces([]NetIface{nic0, nic1, nic2}, nic0.IP, "none", false)
		if got != "ens19,ens20" {
			t.Fatalf("got %q, want %q", got, "ens19,ens20")
		}
	})

	t.Run("deferred PXE on 1-NIC host returns that single interface", func(t *testing.T) {
		got := ResolveAllowedInterfaces([]NetIface{nic0}, nic0.IP, "none", false)
		if got != "ens18" {
			t.Fatalf("got %q, want %q", got, "ens18")
		}
	})

	t.Run("non-interactive explicit --pxe-interface none with no detected interfaces", func(t *testing.T) {
		got := ResolveAllowedInterfaces(nil, nic0.IP, "none", true)
		if got != "omb-pxe-off" {
			t.Fatalf("got %q, want %q", got, "omb-pxe-off")
		}
	})
}
