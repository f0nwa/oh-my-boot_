package images

import "testing"

func TestMountIdentityListed(t *testing.T) {
	info := "10 1 8:1 / /mnt/iso rw - ext4 /dev/sda1 rw\n11 1 8:2 / /mnt/iso rw - cifs //nas/images rw\n12 1 8:3 / /mnt/other\\040files rw - cifs //nas/other rw\n"
	if got, ok := mountIdentityListed(info, "/mnt/iso"); !ok || got != "11" {
		t.Fatalf("topmost mount identity = %q, %v", got, ok)
	}
	if got, ok := mountIdentityListed(info, "/mnt/other files"); !ok || got != "12" {
		t.Fatalf("escaped mount path identity = %q, %v", got, ok)
	}
	if _, ok := mountIdentityListed(info, "/mnt/missing"); ok {
		t.Fatal("missing path accepted as a mountpoint")
	}
}
