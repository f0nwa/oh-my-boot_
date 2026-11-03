package winpe

import (
	"bytes"
	"encoding/binary"
	"omb/internal/winpe/winpetest"
	"os"
	"testing"
)

func TestStrelecBCDSelectsMatchingWIMAndVirtualDiskPaths(t *testing.T) {
	for _, version := range []string{"10", "11"} {
		t.Run(version, func(t *testing.T) {
			original := winpetest.StrelecBCD()
			saved := append([]byte(nil), original...)
			b, err := RenderStrelecBCD(original, version)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(saved, original) {
				t.Fatal("modified original media BCD")
			}
			h := bcdHive(b)
			objects, err := h.child(binary.LittleEndian.Uint32(b[36:]), "Objects")
			if err != nil {
				t.Fatal(err)
			}
			guid := winpetest.Loader11
			if version == "10" {
				guid = winpetest.Loader10
			}
			loader, _ := h.child(objects, guid)
			for _, id := range []string{"11000001", "21000001"} {
				v, err := h.element(loader, id)
				if err != nil || !bytes.Contains(v.data, bcdUTF16(`\sources\boot.wim`)) || bytes.Contains(v.data, bcdUTF16(`\SSTR\strelec`+version+`x64.wim`)) {
					t.Fatalf("device path not adapted: %v", err)
				}
			}
			manager, _ := h.child(objects, "{9dea862c-5cdd-4e70-acc1-f32b344d4795}")
			for _, id := range []string{"23000003", "24000001"} {
				v, err := h.element(manager, id)
				want := bcdUTF16(guid)
				if id == "24000001" {
					want = append(want, 0, 0)
				}
				if err != nil || !bytes.Equal(v.data, want) {
					t.Fatalf("wrong selected loader: %s %v", id, err)
				}
			}
			timeout, _ := h.element(manager, "25000004")
			if binary.LittleEndian.Uint64(timeout.data) != 0 {
				t.Fatal("native menu still waits")
			}
			ramdisk, _ := h.child(objects, "{ae5534e0-a924-466c-b836-758539a3ee3a}")
			sdi, _ := h.element(ramdisk, "32000004")
			fonts, _ := h.element(ramdisk, "1200004a")
			if bcdString(fonts.data) != `\boot\fonts` {
				t.Fatal("font path not adapted")
			}
			if bcdString(sdi.data) != `\boot.sdi` {
				t.Fatal("SDI path not adapted")
			}
		})
	}
}
func TestStrelecBCDRejectsMalformedHive(t *testing.T) {
	for _, input := range [][]byte{nil, []byte("regf"), make([]byte, 4096), append([]byte("regf"), make([]byte, 4092)...)} {
		if _, err := RenderStrelecBCD(input, "11"); err == nil {
			t.Fatal("accepted broken hive")
		}
	}
	b := winpetest.StrelecBCD()
	if _, err := RenderStrelecBCD(b, "8"); err == nil {
		t.Fatal("accepted unsupported WIM")
	}
	for n := 4096; n < len(b); n += 17 {
		if _, err := RenderStrelecBCD(b[:n], "11"); err == nil {
			t.Fatalf("accepted truncated hive at %d", n)
		}
	}
}
func TestStrelecBCDRealMedia(t *testing.T) {
	path := os.Getenv("OMB_STRELEC_BCD_FIXTURE")
	if path == "" {
		t.Skip("operator media is not redistributed")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"10", "11"} {
		if _, err := RenderStrelecBCD(b, version); err != nil {
			t.Fatalf("native BCD version %s: %v", version, err)
		}
	}
}
func FuzzStrelecBCD(f *testing.F) {
	f.Add(winpetest.StrelecBCD())
	f.Add([]byte("regf"))
	f.Fuzz(func(t *testing.T, b []byte) { _, _ = RenderStrelecBCD(b, "11") })
}
