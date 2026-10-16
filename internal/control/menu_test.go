package control

import (
	"net/url"
	"strings"
	"testing"
)

func TestMenuBuildRejectsCommandInjection(t *testing.T) {
	for _, field := range []string{"Bad\ngoto local", "Bad${target}", "Bad\\nnext"} {
		if _, err := BuildMenu("PXE", field, "ready"); err == nil {
			t.Fatalf("accepted unsafe title %q", field)
		}
		if _, err := BuildMenu("PXE", "Diagnostics", field); err == nil {
			t.Fatalf("accepted unsafe message %q", field)
		}
	}
}

func TestMenuFormAddsUbuntuOnlyWhenSelected(t *testing.T) {
	form := url.Values{"title": {"PXE Platform"}, "label": {"Diagnostics"}, "message": {"Ready"}, "ubuntu": {"on"}}
	withUbuntu, err := BuildMenuFromForm(form, "http://10.30.0.10:8080")
	if err != nil || !strings.Contains(string(withUbuntu), "item ubuntu Ubuntu Desktop") {
		t.Fatalf("Ubuntu was not added: %v %q", err, withUbuntu)
	}
	form.Del("ubuntu")
	withoutUbuntu, err := BuildMenuFromForm(form, "http://10.30.0.10:8080")
	if err != nil || strings.Contains(string(withoutUbuntu), "item ubuntu") {
		t.Fatalf("Ubuntu was not removed: %v %q", err, withoutUbuntu)
	}
}

func TestMenuFormAddsWindows10OnlyWhenSelected(t *testing.T) {
	form := url.Values{"title": {"PXE Platform"}, "label": {"Diagnostics"}, "message": {"Ready"}, "ubuntu": {"on"}, "windows10": {"on"}}
	script, err := BuildMenuFromForm(form, "http://10.30.0.10:8080")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"item ubuntu Ubuntu Desktop 26.04.1\n",
		"item windows10 Windows 10 installer - WinPE\n",
		":windows10\n",
		"kernel http://10.30.0.10:8080/assets/windows/wimboot\n",
		"initrd http://10.30.0.10:8080/assets/windows10/boot.wim boot.wim\n",
		"boot || goto windows10_failed\n",
		":windows10_failed\n",
	} {
		if !strings.Contains(string(script), want) {
			t.Fatalf("missing %q in %q", want, script)
		}
	}
	form.Del("windows10")
	script, err = BuildMenuFromForm(form, "http://10.30.0.10:8080")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(script), "item windows10") || !strings.Contains(string(script), "item ubuntu") {
		t.Fatalf("Windows 10 selection leaked or Ubuntu disappeared: %q", script)
	}
}

func TestMenuBuildKeepsStructuredText(t *testing.T) {
	script, err := BuildMenu("PXE Лаборатория", "Диагностика", "Сеть готова")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"#!ipxe\n", "menu PXE Лаборатория\n", "item diagnostics Диагностика\n", "echo Сеть готова\n", "item local Boot local disk\n"} {
		if !strings.Contains(string(script), want) {
			t.Fatalf("missing %q in %q", want, script)
		}
	}
}

func TestUbuntuMenuLoadsKernelInitrdAndISOOverHTTP(t *testing.T) {
	script, err := BuildUbuntuMenu("PXE Platform", "Diagnostics", "Ready", "http://10.30.0.10:8080")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"item ubuntu Ubuntu Desktop 26.04.1\n",
		"kernel http://10.30.0.10:8080/assets/ubuntu/vmlinuz initrd=initrd boot=casper ip=dhcp iso-url=http://10.30.0.10:8080/images/ubuntu-desktop.iso ---\n",
		"initrd http://10.30.0.10:8080/assets/ubuntu/initrd\n",
		"shim http://10.30.0.10:8080/assets/ubuntu/bootx64.efi || goto ubuntu_failed\n",
		"boot || goto ubuntu_failed\n",
	} {
		if !strings.Contains(string(script), want) {
			t.Fatalf("missing %q in %q", want, script)
		}
	}
	if _, err := BuildUbuntuMenu("PXE", "Diagnostics", "Ready", "http://example.com/;exit"); err == nil {
		t.Fatal("accepted unsafe boot URL")
	}
}
