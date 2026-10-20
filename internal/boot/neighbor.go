package boot

import (
	"net"
	"os"
	"strings"
)

// neighborMAC finds the hardware address the kernel holds for ip in a
// /proc/net/arp table. Incomplete entries give an empty string.
func neighborMAC(table, ip string) string {
	for _, line := range strings.Split(table, "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) < 6 || fields[0] != ip || fields[2] == "0x0" {
			continue
		}
		mac, err := net.ParseMAC(fields[3])
		if err != nil || mac.String() == "00:00:00:00:00:00" {
			return ""
		}
		return mac.String()
	}
	return ""
}

// readNeighborMAC looks ip up in the server's neighbor table. Clients in the
// PXE segment talk to the server directly, so their entry is there while they
// make requests; clients behind a router give an empty result.
func readNeighborMAC(ip string) string {
	data, err := os.ReadFile("/proc/net/arp")
	if err != nil {
		return ""
	}
	return neighborMAC(string(data), ip)
}

func remoteIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}
