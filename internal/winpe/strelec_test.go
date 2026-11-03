package winpe

import (
	"bytes"
	"strings"
	"testing"
)

func TestStrelecNetworkRetainsNativeInitializationAndStartsExternalPrograms(t *testing.T) {
	original := []byte("net start Netman\r\nnet use Z: \\\\STRELEC-PC\\H /USER:demo demo-password\r\npecmd.exe WAIT 2000\r\nZ:\\SSTR\\MInst\\Portable\\x64\\Files_11_x64.exe\r\nexit\r\n")
	base := []byte("net use Y: \\\\10.30.0.10\\ombisos __OMB_ISO_PASSWORD__ /user:omb-iso /persistent:no\r\n")
	output, err := RenderStrelecNetwork(original, base, strings.Repeat("A", 32), 17)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"net start Netman\r\n", `net use Z: \\10.30.0.10\ombisos\17 `, `/user:omb-iso /persistent:no`, `Z:\SSTR\MInst\Portable\x64\Files_11_x64.exe`, "pecmd.exe WAIT 2000\r\n", ":omb_strelec_ready\r\n"} {
		if !bytes.Contains(output, []byte(expected)) {
			t.Fatalf("missing %q", expected)
		}
	}
	for _, forbidden := range []string{"STRELEC-PC", "demo-password", "setup.exe", "/InstallFrom:"} {
		if bytes.Contains(output, []byte(forbidden)) {
			t.Fatalf("retained %q", forbidden)
		}
	}
}

func TestStrelecNetworkRejectsAmbiguousScriptsAndUnsafeCredentials(t *testing.T) {
	original := []byte("net use Z: \\\\host\\share /user:test test\r\nZ:\\SSTR\\MInst\\Portable\\x64\\Files_11_x64.exe\r\nexit\r\n")
	base := []byte("net use Y: \\\\10.30.0.10\\ombisos __OMB_ISO_PASSWORD__ /user:omb-iso\r\n")
	for _, tc := range []struct {
		script, template []byte
		password         string
		id               int64
	}{
		{[]byte("echo no mapping\r\n"), base, strings.Repeat("A", 32), 17},
		{append(append([]byte{}, original...), original...), base, strings.Repeat("A", 32), 17},
		{original, []byte("no share"), strings.Repeat("A", 32), 17},
		{original, base, "bad%PASSWORD%", 17},
		{original, base, strings.Repeat("A", 32), 0},
	} {
		if _, err := RenderStrelecNetwork(tc.script, tc.template, tc.password, tc.id); err == nil {
			t.Fatal("accepted invalid network startup")
		}
	}
}

func TestStrelecStartupInitializesMInstLinksAfterPayloadExtraction(t *testing.T) {
	for _, version := range []string{"10", "11"} {
		t.Run(version, func(t *testing.T) {
			original := []byte("net use Z: \\\\demo\\share /user:demo demo\r\nZ:\\SSTR\\MInst\\Portable\\x64\\Files_" + version + "_x64.exe\r\nexit\r\n")
			template := []byte("net use Y: \\\\10.30.0.10\\ombisos __OMB_ISO_PASSWORD__ /user:omb-iso\r\n")
			got, err := RenderStrelecNetwork(original, template, strings.Repeat("A", 32), 17)
			if err != nil {
				t.Fatal(err)
			}
			script := string(got)
			payload := strings.Index(script, "Z:\\SSTR\\MInst\\Portable\\x64\\Files_"+version+"_x64.exe")
			launch := strings.Index(script, `start "" "Z:\SSTR\MInst\MInst.exe"`)
			if launch < payload || !strings.Contains(script, `"{Patch}\profiles\strelec64Windows`+version+`.ini" /lnk /minimized`) {
				t.Fatal("SMB connection and payload extraction never initialize the MInst menu")
			}
			if !strings.Contains(script, `X:\omb-strelec.log`) {
				t.Fatal("service initialization stages are not logged")
			}
		})
	}
}
