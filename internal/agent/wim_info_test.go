package agent

import "testing"

const sampleWIMInfoOutput = `WIM Information:
----------------
Path:           /mnt/omb-iso/profiles/3/sources/install.wim
GUID:           0x6c2d1f44b2c43a818f4048a28df2e711
Version:        68864
Image Count:    2
Compression:    LZX
Chunk Size:     32768 bytes
Part Number:    1/1
Boot Index:     0
Size:           5084209123 bytes
Attributes:     Integrity info, Relative path junction

Available Images:
-----------------
Index:                  1
Name:                   Windows 11 Home
Description:            Windows 11 Home
Display Name:           Windows 11 Домашняя
Display Description:    Windows 11 Домашняя
Directory Count:        25000
File Count:             110000
Total Bytes:            18123456789
Hard Link Bytes:        1234567890
Creation Time:          Sat Apr 12 06:00:00 2025 UTC
Last Modification Time: Sat Apr 12 06:05:00 2025 UTC
Architecture:           x86_64
Product Name:           Microsoft® Windows® Operating System
Edition ID:             Core
Service Pack Build:     3775
Service Pack Level:     0
Build:                  26300
Minor Version:          0
Major Version:          10
WIMBoot compatible:     no
Languages:              ru-RU en-US
Default Language:       ru-RU

Index:                  2
Name:                   Windows 11 Pro
Description:            Windows 11 Pro
Display Name:           Windows 11 Pro
Display Description:    Windows 11 Pro
Directory Count:        25500
File Count:             112000
Total Bytes:            18423456789
Architecture:           ARM64
Edition ID:             Professional
Build:                  26100
Languages:              en-US
Default Language:       en-US
`

func TestParseWIMInfoExtractsImagesAndNormalizesArch(t *testing.T) {
	info, err := ParseWIMInfo([]byte(sampleWIMInfoOutput))
	if err != nil {
		t.Fatalf("ParseWIMInfo failed: %v", err)
	}
	if !info.Known || len(info.Images) != 2 {
		t.Fatalf("unexpected info: %+v", info)
	}
	first := info.Images[0]
	if first.Index != 1 || first.Name != "Windows 11 Home" || first.DisplayName != "Windows 11 Домашняя" || first.Architecture != "amd64" || first.EditionID != "Core" || first.Build != 26300 || first.DefaultLanguage != "ru-RU" {
		t.Fatalf("unexpected image[0]: %+v", first)
	}
	if len(first.Languages) != 2 || first.Languages[0] != "ru-RU" || first.Languages[1] != "en-US" {
		t.Fatalf("unexpected languages[0]: %+v", first.Languages)
	}
	second := info.Images[1]
	if second.Index != 2 || second.Architecture != "arm64" || second.EditionID != "Professional" || second.Build != 26100 {
		t.Fatalf("unexpected image[1]: %+v", second)
	}
}

func TestParseWIMInfoRejectsEmptyImages(t *testing.T) {
	if _, err := ParseWIMInfo([]byte("Image Count: 0\n")); err == nil {
		t.Fatal("accepted WIM info with no images")
	}
}
