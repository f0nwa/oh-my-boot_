package autoinstall

import (
	"strings"
	"testing"
)

func TestSourceSelectionUsesCatalog(t *testing.T) {
	options, err := ParseInstallSources([]byte("- id: ubuntu-desktop-minimal\n  name: Ubuntu Desktop\n  default: true\n- id: ubuntu-desktop\n  name: Extended\n"))
	if err != nil || len(options) != 2 {
		t.Fatalf("catalog: %#v, %v", options, err)
	}
	catalog := SourceCatalog{ISOSHA256: strings.Repeat("a", 64), Sources: options}
	if err := ValidateSource(Values{SourceID: "ubuntu-desktop"}, catalog); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSource(Values{SourceID: "ubuntu-server-minimal"}, catalog); err == nil {
		t.Fatal("accepted unrelated source")
	}
	if err := ValidateSource(Values{}, SourceCatalog{}); err != nil {
		t.Fatal("default must work without metadata")
	}
}
func TestSourceRejectsCorruptMetadata(t *testing.T) {
	for _, data := range []string{"invalid", "- id: ubuntu-desktop\n- id: ubuntu-desktop\n", "- id: ubuntu-server\n  name: Server\n"} {
		if _, err := ParseInstallSources([]byte(data)); err == nil {
			t.Fatal("invalid catalog accepted")
		}
	}
}
