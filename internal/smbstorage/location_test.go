package smbstorage

import "testing"

func TestParsePath(t *testing.T) {
	for _, input := range []string{"//192.168.10.10/Downloads/iso", `\\192.168.10.10\Downloads\iso`, "192.168.10.10/Downloads/iso"} {
		server, share, subpath, err := ParsePath(input)
		if err != nil || server != "192.168.10.10" || share != "Downloads" || subpath != "iso" {
			t.Fatalf("input=%q got=%q/%q/%q err=%v", input, server, share, subpath, err)
		}
	}
	for _, input := range []string{"", "//192.168.10.10", "//192.168.10.10/Downloads/../iso", "//192.168.10.10/Downloads//iso", "//192.168.10.10/Downloads/iso,ro"} {
		if _, _, _, err := ParsePath(input); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
}

func TestValidateLocationAndRoot(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef"
	if err := Validate(key, "192.168.10.10", "Downloads", "iso"); err != nil {
		t.Fatal(err)
	}
	if got := Root(key); got != "/mnt/pxe-platform-smb/"+key {
		t.Fatalf("root=%q", got)
	}
	for _, test := range []struct{ key, server, share, subpath string }{
		{"bad", "192.168.10.10", "Downloads", "iso"},
		{key, "192.168.10.10,foo", "Downloads", "iso"},
		{key, "192.168.10.10", "Downloads/other", "iso"},
		{key, "192.168.10.10", "Downloads", "../iso"},
		{key, "192.168.10.10", "Downloads", "iso,,other"},
	} {
		if err := Validate(test.key, test.server, test.share, test.subpath); err == nil {
			t.Fatalf("accepted invalid location: %+v", test)
		}
	}
}
