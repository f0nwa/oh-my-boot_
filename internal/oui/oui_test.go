package oui

import "testing"

func TestLookup(t *testing.T) {
	cases := map[string]string{
		"bc:24:11:52:02:39": "Proxmox Server Solutions GmbH",
		"00-0C-29-AA-BB-CC": "VMware, Inc.",
		"52:54:00:12:34:56": Local, // QEMU's locally administered default
		"zz:zz:zz:00:00:00": "",
		"12":                "",
	}
	for mac, want := range cases {
		if got := Lookup(mac); got != want {
			t.Errorf("%s: %q, want %q", mac, got, want)
		}
	}
	if Size() < 30000 {
		t.Fatalf("registry has %d prefixes", Size())
	}
}
