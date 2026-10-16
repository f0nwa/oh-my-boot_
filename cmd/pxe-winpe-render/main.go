package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"pxe-platform/internal/winpe"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: pxe-winpe-render TEMPLATE PASSWORD_FILE OUTPUT")
		os.Exit(2)
	}
	template, err := os.ReadFile(os.Args[1])
	if err != nil {
		fatal(err)
	}
	secret, err := os.ReadFile(os.Args[2])
	if err != nil {
		fatal(err)
	}
	content, err := winpe.RenderStartup(template, strings.TrimSpace(string(secret)))
	if err != nil {
		fatal(err)
	}
	outPath := os.Args[3]
	tmp, err := os.CreateTemp(filepath.Dir(outPath), ".pxe-winpe-*")
	if err != nil {
		fatal(err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(content); err != nil {
		fatal(err)
	}
	if err := tmp.Chmod(0644); err != nil {
		fatal(err)
	}
	if err := tmp.Close(); err != nil {
		fatal(err)
	}
	if err := os.Rename(tmp.Name(), outPath); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
