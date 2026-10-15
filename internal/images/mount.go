package images

import (
	"os"
	"path/filepath"
	"strings"
)

func IsMountpoint(path string) bool {
	_, ok := MountIdentity(path)
	return ok
}

func MountIdentity(path string) (string, bool) {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return "", false
	}
	return mountIdentityListed(string(data), path)
}

func mountpointListed(mountinfo, path string) bool {
	_, ok := mountIdentityListed(mountinfo, path)
	return ok
}

func mountIdentityListed(mountinfo, path string) (string, bool) {
	want := filepath.Clean(path)
	var identity string
	for _, line := range strings.Split(mountinfo, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		mountPath := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(fields[4])
		if filepath.Clean(mountPath) == want {
			// A newer mount can cover an older one at the same mountpoint.
			identity = fields[0]
		}
	}
	return identity, identity != ""
}
