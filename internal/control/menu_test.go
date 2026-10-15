package control

import (
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
