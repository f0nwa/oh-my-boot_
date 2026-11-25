package winpe

import (
	"bytes"
	"fmt"
	"net/netip"
	"regexp"
)

var ombisosUNC = regexp.MustCompile(`\\\\[0-9]{1,3}(?:\.[0-9]{1,3}){3}\\ombisos\b`)

// UpdateStartupServerIP rewrites any \\<ipv4>\ombisos UNC host in a WinPE
// template or prepared startup script to the configured PXE server IP.
func UpdateStartupServerIP(script []byte, serverIP string) ([]byte, bool, error) {
	if serverIP == "" {
		return append([]byte(nil), script...), false, nil
	}
	addr, err := netip.ParseAddr(serverIP)
	if err != nil || !addr.Is4() {
		return nil, false, fmt.Errorf("invalid WinPE server IP %q", serverIP)
	}
	target := []byte(`\\` + addr.String() + `\ombisos`)
	if !ombisosUNC.Match(script) {
		return append([]byte(nil), script...), false, nil
	}
	updated := ombisosUNC.ReplaceAll(script, target)
	return updated, !bytes.Equal(updated, script), nil
}
