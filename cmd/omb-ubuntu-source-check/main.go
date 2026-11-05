// omb-ubuntu-source-check verifies a prepared source before a legacy migration.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"omb/internal/boot"
)

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func verifySource(iso, sourceDir, digest string) error {
	if !filepath.IsAbs(iso) || !filepath.IsAbs(sourceDir) || !digestPattern.MatchString(digest) {
		return fmt.Errorf("invalid source arguments")
	}
	catalog, err := boot.NewUbuntuSourceAt(iso, sourceDir).Catalog()
	if err != nil || catalog.ISOSHA256 != digest || len(catalog.Sources) == 0 {
		return fmt.Errorf("source manifest, catalog, or ISO does not match")
	}
	return nil
}

func main() {
	iso := flag.String("iso", "", "absolute ISO path")
	source := flag.String("source", "", "absolute prepared source directory")
	digest := flag.String("sha256", "", "expected ISO SHA-256")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected argument")
		os.Exit(2)
	}
	if err := verifySource(*iso, *source, *digest); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Ubuntu source manifest and catalog verified")
}
