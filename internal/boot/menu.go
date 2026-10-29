package boot

import (
	"fmt"
	"net/url"
)

// Menu returns the diagnostic menu served to an already chainloaded iPXE client.
func Menu(baseURL string) ([]byte, error) {
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("invalid boot base URL %q", baseURL)
	}
	script := "#!ipxe\n" +
		"set base " + u.String() + "\n" +
		"menu oh my boot_\n" +
		"item diagnostics Diagnostics\n" +
		"item local Boot local disk\n" +
		"choose target || goto local\n" +
		"goto ${target}\n" +
		":diagnostics\n" +
		"echo Oh My Boot network boot is ready\n" +
		"sleep 3\n" +
		"goto local\n" +
		":local\n" +
		"exit\n"
	return []byte(script), nil
}
