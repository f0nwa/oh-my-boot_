package ombmigration

import (
	"bytes"
	"context"
	"testing"
)

func TestCLIRejectsUnknownModeAndRequiresManifest(t *testing.T) {
	for _, args := range [][]string{{}, {"erase", "--manifest", "x"}, {"plan"}, {"apply", "--manifest", "x", "unexpected"}} {
		var out, err bytes.Buffer
		if code := RunCLI(context.Background(), args, &out, &err); code == 0 {
			t.Fatalf("accepted %v", args)
		}
		if out.Len() != 0 {
			t.Fatal("reported successful output")
		}
	}
}
func TestStoppedServicesFailsClosed(t *testing.T) {
	r := RunFunc(func(context.Context, string, []string, []byte) ([]byte, error) {
		return []byte("inactive\nactive\n"), nil
	})
	if err := CheckStopped(context.Background(), r, nil); err == nil {
		t.Fatal("accepted active service")
	}
}
