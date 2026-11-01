package autoinstall

import (
	"errors"
	"net/url"
	"strings"
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
func baseRepositoryConfig(v Values) map[string]any {
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

// APT normally treats a missing dotted/plus name as an implicit regexp.
// Pattern-Only keeps valid package names but disables that fallback.
func repositoryConfig(v Values) map[string]any {
	result := baseRepositoryConfig(v)
	for _, name := range v.Packages {
		if strings.ContainsAny(name, ".+") {
			if result == nil {
				result = map[string]any{}
			}
			result["conf"] = "APT::Cmd::Pattern-Only \"true\";\n"
			break
		}
	}
	return result
}
