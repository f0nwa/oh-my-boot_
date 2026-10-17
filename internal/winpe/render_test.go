package winpe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeployTemplateRendersWithOneCredential(t *testing.T) {
	templatePath := filepath.Join("..", "..", "deploy", "boot-assets", "windows11", "pxe-setup.cmd.in")
	template, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RenderStartup(template, strings.Repeat("A", 32)); err != nil {
		t.Fatalf("deployed WinPE template cannot be rendered: %v", err)
	}
}

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

func TestRenderProfileStartupTargetsOnlySelectedISO(t *testing.T) {
	template := []byte("@echo off\r\nnet use Y: \\\\10.30.0.10\\pxeisos __PXE_ISO_PASSWORD__ /user:pxe-iso\r\nif not exist Y:\\__PXE_PROFILE_ID__\\sources\\__PXE_INSTALL_FILE__ goto failed\r\nY:\\__PXE_PROFILE_ID__\\sources\\setup.exe /InstallFrom:Y:\\__PXE_PROFILE_ID__\\sources\\__PXE_INSTALL_FILE__\r\n")
	got, err := RenderProfileStartup(template, strings.Repeat("A", 32), 17, "install.esd")
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if !strings.Contains(text, `Y:\17\sources\install.esd`) || !strings.Contains(text, `\\10.30.0.10\pxeisos`) || strings.Contains(text, "__PXE_") {
		t.Fatalf("profile was not rendered safely: %s", text)
	}
	for _, bad := range []struct {
		id   int64
		file string
	}{{0, "install.wim"}, {17, "../install.wim"}, {17, "install.swm"}} {
		if _, err := RenderProfileStartup(template, strings.Repeat("A", 32), bad.id, bad.file); err == nil {
			t.Fatalf("accepted unsafe profile render: %+v", bad)
		}
	}
}

func TestRenderAnswerStartupRequiresXMLAndPassesItToSetup(t *testing.T) {
	base := []byte("@echo off\r\nY:\\17\\sources\\setup.exe /InstallFrom:Y:\\17\\sources\\install.wim\r\n")
	got, err := RenderAnswerStartup(base, 17)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "if not exist X:\\Windows\\System32\\pxe-answer.xml goto failed\r\n") || !strings.Contains(string(got), " /Unattend:X:\\Windows\\System32\\pxe-answer.xml\r\n") {
		t.Fatalf("answer option or required-file check missing: %q", got)
	}
	if _, err := RenderAnswerStartup([]byte("@echo off\r\n"), 17); err == nil {
		t.Fatal("accepted missing Setup command")
	}
}
