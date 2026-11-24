package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestReorderArgs(t *testing.T) {
	got := reorderArgs([]string{"dev", "--no-update", "--repo", "org/omb"}, map[string]bool{"repo": true, "lang": true})
	want := []string{"--no-update", "--repo", "org/omb", "dev"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reorderArgs = %v, want %v", got, want)
	}
}

func TestRunVersionHelpAndUnknown(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"version"}, strings.NewReader(""), &out, &errOut, func(string) string { return "" })
	if code != 0 || !strings.Contains(out.String(), "omb ") {
		t.Fatalf("version: code=%d out=%q err=%q", code, out.String(), errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = run([]string{"help", "--lang", "ru"}, strings.NewReader(""), &out, &errOut, func(string) string { return "C" })
	if code != 0 || !strings.Contains(out.String(), "oh my boot_") || !strings.Contains(out.String(), "reset-password") {
		t.Fatalf("help ru: code=%d out=%q err=%q", code, out.String(), errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = run([]string{"nonexistent"}, strings.NewReader(""), &out, &errOut, func(string) string { return "en_US.UTF-8" })
	if code != 2 || !strings.Contains(errOut.String(), "unknown command") {
		t.Fatalf("unknown: code=%d out=%q err=%q", code, out.String(), errOut.String())
	}
}
