// Package oui names the maker of a network card from the first three bytes
// of its MAC address, using the IEEE MA-L registry.
//
// oui.tsv.gz is built from the npm package oui-data 2.1.45 (BSD-2-Clause,
// see LICENSE-oui-data), which packs the IEEE registry: one line per prefix,
// the hex prefix and the organisation's name separated by a tab.
package oui

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"strings"
	"sync"
)

//go:embed oui.tsv.gz
var registry []byte

var (
	once  sync.Once
	byOUI map[string]string
)

func load() {
	byOUI = map[string]string{}
	zr, err := gzip.NewReader(bytes.NewReader(registry))
	if err != nil {
		return
	}
	defer zr.Close()
	scanner := bufio.NewScanner(zr)
	for scanner.Scan() {
		prefix, name, ok := strings.Cut(scanner.Text(), "\t")
		if ok {
			byOUI[prefix] = name
		}
	}
}

// Local is what Lookup returns for a locally administered address: one the
// machine or its hypervisor made up, which no maker registered.
const Local = "local"

// Lookup returns the registered maker of a MAC address such as
// "bc:24:11:52:02:39", Local for a locally administered address, or "".
func Lookup(mac string) string {
	hex := strings.ToUpper(strings.NewReplacer(":", "", "-", "", ".", "").Replace(mac))
	if len(hex) < 6 {
		return ""
	}
	var first byte
	for _, c := range hex[:2] {
		first <<= 4
		switch {
		case c >= '0' && c <= '9':
			first |= byte(c - '0')
		case c >= 'A' && c <= 'F':
			first |= byte(c - 'A' + 10)
		default:
			return ""
		}
	}
	once.Do(load)
	if name, ok := byOUI[hex[:6]]; ok {
		return name
	}
	if first&0x02 != 0 {
		return Local
	}
	return ""
}

// Size is how many prefixes the registry holds.
func Size() int {
	once.Do(load)
	return len(byOUI)
}
