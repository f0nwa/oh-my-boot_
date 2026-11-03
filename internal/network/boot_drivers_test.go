package network

import (
	"strings"
	"testing"
)

func TestRenderDHCPHosts(t *testing.T) {
	rules := []BootDriverRule{{"A4:BB:6D:14:CB:F7", "ipxe"}, {"a4:bb:6d:14:cb:*", "snp"}, {"a4:bb:6d:*:*:*", "snp"}, {"a4:bb:6d:14", "ipxe"}}
	want := "a4:bb:6d:*:*:*,set:omb-driver-p3-snp\na4:bb:6d:14:*:*,set:omb-driver-p4-ipxe\na4:bb:6d:14:cb:*,set:omb-driver-p5-snp\na4:bb:6d:14:cb:f7,set:omb-driver-machine-ipxe\n"
	got, err := RenderDHCPHosts(rules)
	if err != nil || string(got) != want {
		t.Fatalf("hosts=%q err=%v; want %q", got, err, want)
	}
	for i, j := 0, len(rules)-1; i < j; i, j = i+1, j-1 {
		rules[i], rules[j] = rules[j], rules[i]
	}
	again, err := RenderDHCPHosts(rules)
	if err != nil || string(again) != want {
		t.Fatalf("order changed result: %q %v", again, err)
	}
	empty, err := RenderDHCPHosts(nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty=%q %v", empty, err)
	}
}

func TestValidateBootDriverRulesRejectsInvalidInput(t *testing.T) {
	for _, rule := range []BootDriverRule{
		{"not-a-mac", "snp"}, {"a4:bb:6d:14:cb:f7", ""}, {"a4:bb:6d:14:cb:f7", "other"},
		{"a4:bb:6d:14:*", "snp"}, {"a5:bb:6d:14:cb:f7", "snp"}, {"00:00:00:00:00:00", "snp"},
		{"00:00:00", "snp"}, {"a4:bb:*:14:*:*", "snp"}, {"a4:bb:6d\n", "snp"}, {"a4:bb:6d,set:ipxe", "snp"},
	} {
		if err := ValidateBootDriverRules([]BootDriverRule{rule}); err == nil {
			t.Errorf("accepted %+v", rule)
		}
	}
	for _, duplicate := range []string{"A4:BB:6D", "a4:bb:6d:*:*:*"} {
		if err := ValidateBootDriverRules([]BootDriverRule{{"a4:bb:6d", "snp"}, {duplicate, "ipxe"}}); err == nil {
			t.Errorf("accepted duplicate %s", duplicate)
		}
	}
}

func TestRenderDnsmasqDriverPriority(t *testing.T) {
	cases := []struct {
		tags []string
		want string
	}{
		{[]string{"omb-driver-machine-ipxe", "omb-driver-p5-snp"}, "ipxe-shim.efi"},
		{[]string{"omb-driver-machine-snp", "omb-driver-p3-ipxe"}, "snponly-shim.efi"},
		{[]string{"omb-driver-p3-snp", "omb-driver-p4-ipxe", "omb-driver-p5-snp"}, "snponly-shim.efi"},
		{[]string{"omb-driver-p3-snp", "omb-driver-p4-ipxe"}, "ipxe-shim.efi"},
		{[]string{"omb-driver-p3-snp"}, "snponly-shim.efi"},
	}
	for _, mode := range []string{"full", "proxy"} {
		for _, driver := range []string{"", "ipxe", "snp"} {
			c := fullConfig()
			c.Mode = mode
			c.DefaultDriver = driver
			if mode == "proxy" {
				c.DHCPStart = ""
				c.DHCPEnd = ""
			}
			got, err := RenderDnsmasq(c)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(got, "dhcp-hostsfile=/var/lib/omb-dhcp/hosts\n") {
				t.Fatal("missing hostsfile")
			}
			defaultFile := "ipxe-shim.efi"
			if driver == "snp" {
				defaultFile = "snponly-shim.efi"
			}
			scenarios := append(append([]struct {
				tags []string
				want string
			}{}, cases...), struct {
				tags []string
				want string
			}{nil, defaultFile})
			for _, scenario := range scenarios {
				for _, arch := range []string{"BC_EFI", "X86-64_EFI"} {
					tags := map[string]bool{"efi64": true}
					for _, tag := range scenario.tags {
						tags[tag] = true
					}
					offers := matchingDriverOffers(got, mode, arch, tags)
					if len(offers) != 1 || !strings.Contains(offers[0], scenario.want) {
						t.Errorf("mode=%s default=%s tags=%v arch=%s offers=%v want=%s", mode, driver, tags, arch, offers, scenario.want)
					}
					tags["ipxe"] = true
					if offers := matchingDriverOffers(got, mode, arch, tags); len(offers) != 0 {
						t.Errorf("first-stage offer to iPXE: %v", offers)
					}
				}
			}
		}
	}
}

// Interpret dnsmasq tag predicates independently of the renderer.
func matchingDriverOffers(config, mode, arch string, tags map[string]bool) []string {
	var offers []string
	for _, line := range strings.Split(config, "\n") {
		if mode == "full" {
			if !strings.HasPrefix(line, "dhcp-boot=") || !strings.Contains(line, "tag:efi64,") {
				continue
			}
		} else {
			if !strings.HasPrefix(line, "pxe-service=") || !strings.Contains(line, ","+arch+",") {
				continue
			}
		}
		matches := true
		for _, part := range strings.Split(strings.SplitN(line, "=", 2)[1], ",") {
			if strings.HasPrefix(part, "tag:") {
				tag := strings.TrimPrefix(part, "tag:")
				negative := strings.HasPrefix(tag, "!")
				tag = strings.TrimPrefix(tag, "!")
				if tags[tag] == negative {
					matches = false
				}
			}
		}
		if matches {
			offers = append(offers, line)
		}
	}
	return offers
}

func TestValidateDefaultDriver(t *testing.T) {
	for _, driver := range []string{"", "ipxe", "snp", "other"} {
		c := fullConfig()
		c.DefaultDriver = driver
		err := Validate(c)
		if (err != nil) != (driver == "other") {
			t.Errorf("driver=%q err=%v", driver, err)
		}
	}
}

func TestRenderDnsmasqHostsPath(t *testing.T) {
	got, err := RenderDnsmasqWithHosts(fullConfig(), "/tmp/omb-test-dhcp/hosts")
	if err != nil || !strings.Contains(got, "dhcp-hostsfile=/tmp/omb-test-dhcp/hosts\n") {
		t.Fatalf("custom hosts=%s %v", got, err)
	}
	for _, path := range []string{"relative", "/tmp/hosts\ndhcp-boot=bad", "/tmp/hosts,extra"} {
		if _, err := RenderDnsmasqWithHosts(fullConfig(), path); err == nil {
			t.Errorf("accepted hosts path %q", path)
		}
	}
}
