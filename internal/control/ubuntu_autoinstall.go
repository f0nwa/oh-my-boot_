package control

import (
	"time"

	"omb/internal/autoinstall"
)

// UbuntuAutoinstallFile is an immutable, validated Ubuntu Autoinstall script
// (either built in the constructor or uploaded as custom YAML).
// Values contains only a password hash for constructor files; uploaded files
// store an empty JSON object.
type UbuntuAutoinstallFile struct {
	ID         int64
	Title      string
	SourceKind string
	Values     autoinstall.Values
	UserData   []byte
	SHA256     string
	State      string
	DiskMode   string
	DiskSerial string
	CreatedAt  time.Time
}

func (f UbuntuAutoinstallFile) IsUploaded() bool {
	return f.SourceKind == "uploaded"
}

func (f UbuntuAutoinstallFile) Size() int64 {
	return int64(len(f.UserData))
}

// UbuntuAutoinstallSummary omits the body and account fields when listing files
// or resolving installation references. Publication fetches the body separately.
type UbuntuAutoinstallSummary struct {
	Author     string
	ID         int64
	Title      string
	SourceKind string
	SHA256     string
	State      string
	DiskMode   string
	DiskSerial string
	CreatedAt  time.Time
}

func (f UbuntuAutoinstallSummary) IsUploaded() bool {
	return f.SourceKind == "uploaded"
}
