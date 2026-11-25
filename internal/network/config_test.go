package network

import (
	"reflect"
	"testing"
)

func fullConfig() Config {
	return Config{
		Mode:      "full",
		Interface: "enp2s0",
		ServerIP:  "192.0.2.10",
		Subnet:    "192.0.2.0/24",
		DHCPStart: "192.0.2.100",
		DHCPEnd:   "192.0.2.150",
		BootURL:   "http://192.0.2.10:8080/boot/menu.ipxe",
		TFTPRoot:  "/srv/omb/tftp",
	}
}

func TestValidateAcceptsFullAndProxy(t *testing.T) {
	if err := Validate(fullConfig()); err != nil {
		t.Fatalf("valid full config rejected: %v", err)
	}
	proxy := fullConfig()
	proxy.Mode = "proxy"
	proxy.DHCPStart = ""
	proxy.DHCPEnd = ""
	if err := Validate(proxy); err != nil {
		t.Fatalf("valid proxy config rejected: %v", err)
	}
}

func TestValidateRejectsUnsafeNetworkConfiguration(t *testing.T) {
	cases := map[string]func(*Config){
		"no interface":        func(c *Config) { c.Interface = "" },
		"interface injection": func(c *Config) { c.Interface = "eth0\ninterface=eth1" },
		"bad boot URL":        func(c *Config) { c.BootURL = "file:///tmp/menu" },
		"range outside subnet": func(c *Config) {
			c.DHCPStart = "198.51.100.10"
		},
		"reversed range":     func(c *Config) { c.DHCPStart, c.DHCPEnd = c.DHCPEnd, c.DHCPStart },
		"server in range":    func(c *Config) { c.DHCPStart = "192.0.2.5" },
		"relative TFTP path": func(c *Config) { c.TFTPRoot = "tftp" },
		"unknown mode":       func(c *Config) { c.Mode = "auto" },
		"unknown client access": func(c *Config) {
			c.ClientAccess = "custom"
		},
		"subnets mode without list": func(c *Config) {
			c.ClientAccess = ClientAccessSubnets
		},
		"subnets mode with unmasked CIDR": func(c *Config) {
			c.ClientAccess = ClientAccessSubnets
			c.AllowedClientSubnets = []string{"10.20.0.1/24"}
		},
		"subnets mode with ipv6": func(c *Config) {
			c.ClientAccess = ClientAccessSubnets
			c.AllowedClientSubnets = []string{"fd00::/64"}
		},
		"pxe_subnet mode with extra list": func(c *Config) {
			c.ClientAccess = ClientAccessPXESubnet
			c.AllowedClientSubnets = []string{"10.20.0.0/16"}
		},
		"any mode with extra list": func(c *Config) {
			c.ClientAccess = ClientAccessAny
			c.AllowedClientSubnets = []string{"10.20.0.0/16"}
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			config := fullConfig()
			change(&config)
			if err := Validate(config); err == nil {
				t.Fatalf("invalid config accepted: %+v", config)
			}
		})
	}
}

func TestClientAccessPoliciesAndParsing(t *testing.T) {
	cfg := fullConfig()
	if cfg.EffectiveClientAccess() != ClientAccessPXESubnet {
		t.Fatalf("default client access = %q, want pxe_subnet", cfg.EffectiveClientAccess())
	}
	if got := cfg.EffectiveAllowedSubnets(); !reflect.DeepEqual(got, []string{"192.0.2.0/24"}) {
		t.Fatalf("default allowed subnets = %v", got)
	}

	parsed, err := ParseAllowedSubnets("10.20.0.0/16, 172.16.5.0/24\n10.20.0.0/16; 192.0.2.0/24")
	if err != nil {
		t.Fatalf("ParseAllowedSubnets: %v", err)
	}
	if !reflect.DeepEqual(parsed, []string{"10.20.0.0/16", "172.16.5.0/24", "192.0.2.0/24"}) {
		t.Fatalf("parsed = %v", parsed)
	}

	cfg.ClientAccess = ClientAccessSubnets
	cfg.AllowedClientSubnets = parsed
	if err := Validate(cfg); err != nil {
		t.Fatalf("valid subnets config rejected: %v", err)
	}
	if got := cfg.EffectiveAllowedSubnets(); !reflect.DeepEqual(got, []string{"192.0.2.0/24", "10.20.0.0/16", "172.16.5.0/24"}) {
		t.Fatalf("effective allowed subnets = %v", got)
	}

	cfg.ClientAccess = ClientAccessAny
	cfg.AllowedClientSubnets = nil
	if err := Validate(cfg); err != nil {
		t.Fatalf("valid any config rejected: %v", err)
	}
	if got := cfg.EffectiveAllowedSubnets(); got != nil {
		t.Fatalf("any allowed subnets = %v, want nil", got)
	}
}
