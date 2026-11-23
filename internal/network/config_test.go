package network

import "testing"

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
