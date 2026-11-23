package network

import (
	"encoding/json"
	"strings"
	"testing"
)

// Decode the public setting as Agent would, including on an existing config.
func withFirmwareNetwork(t *testing.T, c Config, enabled bool) Config {
	t.Helper()
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	fields["reuse_firmware_network"] = enabled
	data, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestFirmwareNetworkOptionsFollowEffectiveSNPDriver(t *testing.T) {
	for _, defaultDriver := range []string{"ipxe", "snp"} {
		c := fullConfig()
		c.DefaultDriver = defaultDriver
		c = withFirmwareNetwork(t, c, true)
		config, err := RenderDnsmasq(c)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			tags []string
			want bool
		}{
			{nil, defaultDriver == "snp"},
			{[]string{"omb-driver-machine-snp", "omb-driver-p5-ipxe"}, true},
			{[]string{"omb-driver-machine-ipxe", "omb-driver-p5-snp"}, false},
			{[]string{"omb-driver-p5-snp", "omb-driver-p4-ipxe", "omb-driver-p3-ipxe"}, true},
			{[]string{"omb-driver-p4-ipxe", "omb-driver-p3-snp"}, false},
			{[]string{"omb-driver-p3-snp"}, true},
		} {
			for _, stage := range []string{"uefi", "bios", "ipxe"} {
				tags := map[string]bool{"efi64": stage != "bios", "ipxe": stage == "ipxe"}
				for _, tag := range tc.tags {
					tags[tag] = true
				}
				options := matchingFirmwareOptions(config, tags)
				want := 0
				if tc.want && stage == "uefi" {
					want = 1
				}
				if len(options) != want {
					t.Fatalf("default=%s tags=%v stage=%s options=%v; want %d", defaultDriver, tc.tags, stage, options, want)
				}
				if want == 1 && !strings.HasSuffix(options[0], `encap:175,81,"chain http://192.0.2.10:8080/boot/menu.ipxe || autoboot"`) {
					t.Fatalf("wrong menu or missing normal DHCP fallback: %s", options[0])
				}
			}
		}
	}
}

func matchingFirmwareOptions(config string, tags map[string]bool) []string {
	var found []string
	for _, line := range strings.Split(config, "\n") {
		if !strings.HasPrefix(line, "dhcp-option-force=") {
			continue
		}
		matches := true
		for _, part := range strings.Split(strings.TrimPrefix(line, "dhcp-option-force="), ",") {
			if !strings.HasPrefix(part, "tag:") {
				continue
			}
			tag := strings.TrimPrefix(part, "tag:")
			negative := strings.HasPrefix(tag, "!")
			tag = strings.TrimPrefix(tag, "!")
			if tags[tag] == negative {
				matches = false
			}
		}
		if matches {
			found = append(found, line)
		}
	}
	return found
}

func TestFirmwareNetworkInactiveModesKeepExistingBootFlow(t *testing.T) {
	for _, mode := range []string{"disabled", "proxy"} {
		c := fullConfig()
		c.DefaultDriver = "snp"
		if mode == "proxy" {
			c.Mode, c.DHCPStart, c.DHCPEnd = "proxy", "", ""
		}
		c = withFirmwareNetwork(t, c, mode != "disabled")
		got, err := RenderDnsmasq(c)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(got, "encap:175,81") {
			t.Fatalf("startup override in %s: %s", mode, got)
		}
	}
}

func TestFirmwareNetworkRejectsUnsafeOrOversizedScriptURL(t *testing.T) {
	for _, suffix := range []string{"a ; reboot", "${net0/mac}", `a"b`, `a\b`, "?a=\tb", strings.Repeat("a", 240)} {
		c := withFirmwareNetwork(t, fullConfig(), true)
		c.BootURL = "http://192.0.2.10/" + suffix
		if _, err := RenderDnsmasq(c); err == nil {
			t.Errorf("unsafe/oversized startup URL accepted: %q", c.BootURL)
		}
	}
	c := withFirmwareNetwork(t, fullConfig(), true)
	c.BootURL = "http://192.0.2.10/menu.ipxe?a=1&b=%24value"
	if _, err := RenderDnsmasq(c); err != nil {
		t.Fatal(err)
	}
}
