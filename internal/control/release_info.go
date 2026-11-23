package control

import (
	"encoding/json"
	"os"
	"strings"
)

const (
	DefaultReleaseInfoPath = "/usr/share/omb/release.json"
	ChannelStable          = "stable"
	ChannelDev             = "dev"
)

// ReleaseInfo describes the running OMB build loaded from package metadata.
type ReleaseInfo struct {
	Version        string `json:"version"`
	DisplayVersion string `json:"display_version"`
	Channel        string `json:"channel"`
	Commit         string `json:"commit"`
	FromCommit     string `json:"from_commit,omitempty"`
	Package        string `json:"package"`
}

// FormatDisplayVersion turns a Debian package version into a display tag (e.g. 0.2.0~dev.1 -> v0.2.0-dev.1).
func FormatDisplayVersion(debVersion string) string {
	v := strings.TrimSpace(debVersion)
	if v == "" {
		return ""
	}
	if idx := strings.IndexByte(v, ':'); idx >= 0 {
		v = v[idx+1:]
	}
	v = strings.ReplaceAll(v, "~", "-")
	if v != "" && !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}

// NormalizeReleaseInfo fills defaults for channel, display version, and package name.
func NormalizeReleaseInfo(info ReleaseInfo) ReleaseInfo {
	if info.Channel != ChannelDev && info.Channel != ChannelStable {
		if strings.Contains(info.Version, "~dev") || strings.Contains(info.Version, "-dev") || strings.Contains(info.DisplayVersion, "-dev") {
			info.Channel = ChannelDev
		} else {
			info.Channel = ChannelStable
		}
	}
	if info.DisplayVersion == "" && info.Version != "" {
		info.DisplayVersion = FormatDisplayVersion(info.Version)
	}
	if info.DisplayVersion == "" {
		info.DisplayVersion = "dev"
	}
	if info.Package == "" && info.Version != "" {
		info.Package = "omb " + info.Version
	}
	if info.Package == "" {
		info.Package = "omb"
	}
	return info
}

// LoadReleaseInfo reads release metadata JSON from path, falling back gracefully when unavailable.
func LoadReleaseInfo(path string) ReleaseInfo {
	if strings.TrimSpace(path) == "" {
		path = DefaultReleaseInfoPath
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return NormalizeReleaseInfo(ReleaseInfo{Version: "dev", DisplayVersion: "dev", Channel: ChannelStable, Package: "omb"})
	}
	var info ReleaseInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return NormalizeReleaseInfo(ReleaseInfo{Version: "dev", DisplayVersion: "dev", Channel: ChannelStable, Package: "omb"})
	}
	return NormalizeReleaseInfo(info)
}

func (r ReleaseInfo) IsDev() bool { return r.Channel == ChannelDev }

func (r ReleaseInfo) ChannelBadge() string {
	if r.IsDev() {
		return ChannelDev
	}
	return ChannelStable
}

func (r ReleaseInfo) ChannelLabelKey() string {
	if r.IsDev() {
		return "dev (разработка)"
	}
	return "stable (стабильный)"
}

func (r ReleaseInfo) ShortCommit() string {
	if len(r.Commit) >= 7 {
		return r.Commit[:7]
	}
	if r.Commit != "" {
		return r.Commit
	}
	return "—"
}
