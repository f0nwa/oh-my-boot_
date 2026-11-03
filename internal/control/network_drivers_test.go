package control

import "testing"

func TestEffectiveNetworkDriver(t *testing.T) {
	for _, tc := range []struct {
		name, own, global string
		rules             macRules
		want              EffectiveNetworkDriver
	}{
		{"machine ipxe", "ipxe", "snp", macRules{{Prefix: "a4:bb:6d:14:cb", NetworkDriver: "snp"}}, EffectiveNetworkDriver{"ipxe", "machine", ""}},
		{"machine snp", "snp", "ipxe", nil, EffectiveNetworkDriver{"snp", "machine", ""}},
		{"longest explicit", "", "snp", macRules{{Prefix: "a4:bb:6d", NetworkDriver: "snp"}, {Prefix: "a4:bb:6d:14"}, {Prefix: "a4:bb:6d:14:cb", NetworkDriver: "ipxe"}}, EffectiveNetworkDriver{"ipxe", "prefix", "a4:bb:6d:14:cb"}},
		{"skip empty", "", "ipxe", macRules{{Prefix: "a4:bb:6d:14:cb"}, {Prefix: "a4:bb:6d", NetworkDriver: "snp"}}, EffectiveNetworkDriver{"snp", "prefix", "a4:bb:6d"}},
		{"old global", "", "", nil, EffectiveNetworkDriver{"ipxe", "global", ""}},
		{"global snp", "", "snp", macRules{{Prefix: "bc:24:11", NetworkDriver: "ipxe"}}, EffectiveNetworkDriver{"snp", "global", ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := effectiveNetworkDriver(MachineInfo{MAC: "a4:bb:6d:14:cb:f7", NetworkDriver: tc.own}, tc.rules, tc.global)
			if got != tc.want {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}
