package control

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAnswerWipesDiskGenerated runs the disk hint on answer files from the
// generator: a generated PE script that partitions a disk wipes it, the
// interactive variants do not.
func TestAnswerWipesDiskGenerated(t *testing.T) {
	want := map[string]bool{
		"default":                      false,
		"unattended-basic":             false,
		"pe-default-firmware-nobypass": false,
		"pe-interactive-partition":     false,
		"pe-script":                    false,
		"pe-gpt-disk0":                 true,
		"pe-mbr-norecovery":            true,
		"pe-auto-generated":            true,
		"pe-interactive-disk":          true,
		"pe-script-disk":               true,
		"pe-custom-diskpart":           true,
	}
	for name, expected := range want {
		data, err := os.ReadFile(filepath.Join("..", "unattend", "testdata", "golden", name+".xml"))
		if err != nil {
			t.Fatal(err)
		}
		wipes, known := AnswerWipesDisk(data)
		if !known || wipes != expected {
			t.Errorf("%s: wipes=%t known=%t, want wipes=%t", name, wipes, known, expected)
		}
	}
}
