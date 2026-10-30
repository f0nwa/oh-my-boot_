package control

import (
	"time"

	"pxe-platform/internal/autoinstall"
)

// UbuntuAutoinstallFile is an immutable, validated constructor result. Values
// contains only a password hash; the transient constructor Form is never stored.
type UbuntuAutoinstallFile struct {
	ID        int64
	Title     string
	Values    autoinstall.Values
	UserData  []byte
	SHA256    string
	State     string
	CreatedAt time.Time
}

// UbuntuAutoinstallSummary omits the body and account fields when listing files
// or resolving installation references. Publication fetches the body separately.
type UbuntuAutoinstallSummary struct {
	ID         int64
	Title      string
	SHA256     string
	State      string
	DiskMode   string
	DiskSerial string
	CreatedAt  time.Time
}
