package autoinstall

import (
	"errors"
	"net/url"
)

func needsArchive(v Values) bool {
	return len(v.Packages) > 0 || v.SSH.InstallServer || v.Drivers || v.Codecs
}
func validateRepository(v Values) error {
	switch v.MirrorMode {
	case "":
		if v.MirrorURL != "" {
			return errors.New("mirror: URL requires custom mode")
		}
	case "offline":
		if v.MirrorURL != "" || needsArchive(v) {
			return errors.New("mirror: offline mode conflicts with archive requirements")
		}
	case "custom":
		u, e := url.Parse(v.MirrorURL)
		if e != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || !validText(v.MirrorURL) {
			return errors.New("mirror: absolute HTTP(S) URL without credentials required")
		}
	default:
		return errors.New("mirror: invalid mode")
	}
	return nil
}
func repositoryConfig(v Values) map[string]any {
	switch v.MirrorMode {
	case "offline":
		return map[string]any{"geoip": false, "mirror-selection": map[string]any{"primary": []any{}}, "fallback": "offline-install"}
	case "custom":
		return map[string]any{"geoip": false, "mirror-selection": map[string]any{"primary": []map[string]string{{"uri": v.MirrorURL}}}, "security": []map[string]any{{"arches": []string{"default"}, "uri": v.MirrorURL}}, "fallback": "abort"}
	default:
		if needsArchive(v) {
			return map[string]any{"fallback": "abort"}
		}
		return nil
	}
}
