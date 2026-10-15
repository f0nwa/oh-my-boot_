package boot

import (
	"strings"
	"testing"
)

func TestMenuHasDiagnosticsAndLocalBoot(t *testing.T) {
	menu, err := Menu("http://192.0.2.10:8080")
	if err != nil {
		t.Fatal(err)
	}
	text := string(menu)
	if !strings.HasPrefix(text, "#!ipxe\n") {
		t.Fatalf("missing iPXE header: %q", text)
	}
	for _, fragment := range []string{"menu PXE Platform", "item diagnostics Diagnostics", "item local Boot local disk", "choose target", ":diagnostics", ":local", "exit"} {
		if !strings.Contains(text, fragment) {
			t.Errorf("menu missing %q: %s", fragment, text)
		}
	}
}

func TestMenuRejectsNonHTTPBaseURL(t *testing.T) {
	if _, err := Menu("file:///etc/passwd"); err == nil {
		t.Fatal("expected non-HTTP URL to be rejected")
	}
}
