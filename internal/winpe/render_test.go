package winpe

import (
	"strings"
	"testing"
)

func TestRenderStartupReplacesOnlyDownstreamCredential(t *testing.T) {
	template := []byte("@echo off\r\nnet use Y: \\\\10.30.0.10\\windows11iso __PXE_ISO_PASSWORD__ /user:pxe-iso\r\n")
	password := strings.Repeat("aB9", 12)
	got, err := RenderStartup(template, password)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "__PXE_ISO_PASSWORD__") || !strings.Contains(string(got), password) {
		t.Fatal("rendered WinPE script did not contain the downstream credential")
	}
	if !strings.Contains(string(got), "\r\n") {
		t.Fatal("WinPE script lost Windows line endings")
	}
}

func TestRenderStartupRejectsUnsafePasswordAndTemplate(t *testing.T) {
	for _, tc := range []struct {
		template string
		password string
	}{
		{"__PXE_ISO_PASSWORD__", "bad&password"},
		{"__PXE_ISO_PASSWORD__", "short"},
		{"no token", strings.Repeat("A", 32)},
		{"__PXE_ISO_PASSWORD____PXE_ISO_PASSWORD__", strings.Repeat("A", 32)},
	} {
		if _, err := RenderStartup([]byte(tc.template), tc.password); err == nil {
			t.Fatalf("accepted unsafe render input: %#v", tc)
		}
	}
}
