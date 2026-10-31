// Package ombmigration contains the one-time legacy data reader. Runtime
// services deliberately do not import it.
package ombmigration

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"path"
	"strconv"
	"strings"
)

var pathMap = [][2]string{
	{"/mnt/pxe-platform-smb-upload", "/mnt/omb-smb-upload"},
	{"/mnt/pxe-platform-smb", "/mnt/omb-smb"},
	{"/mnt/pxe-platform-iso", "/mnt/omb-iso"},
	{"/var/lib/pxe-platform-boot", "/var/lib/omb-boot"},
	{"/var/lib/pxe-platform-agent", "/var/lib/omb-agent"},
	{"/var/lib/pxe-platform-control", "/var/lib/omb-control"},
	{"/var/lib/pxe-platform-images", "/var/lib/omb-images"},
	{"/etc/pxe-platform", "/etc/omb"}, {"/run/pxe-platform-boot", "/run/omb-boot"},
	{"/run/pxe-platform-images", "/run/omb-images"}, {"/run/pxe-platform", "/run/omb"},
	{"/usr/share/pxe-platform", "/usr/share/omb"}, {"/srv/pxe", "/srv/omb"},
}

// MapPath changes only a complete product-owned prefix. Remote share names and
// relative ISO filenames are not product paths.
func MapPath(s string) string {
	for _, p := range pathMap {
		if s == p[0] || strings.HasPrefix(s, p[0]+"/") {
			return p[1] + strings.TrimPrefix(s, p[0])
		}
	}
	return s
}

var ambiguousXML = []string{"pxe-setup.cmd", "pxe-answer.xml", "pxe-driver.log", "pxeisos"}

// ConvertXML validates XML but edits only the byte spans of namespace
// declarations, preserving foreign XML, whitespace, comments and user values.
// References embedded in scripts have no provable ownership and are blockers.
func ConvertXML(in []byte) ([]byte, error) {
	d := xml.NewDecoder(bytes.NewReader(in))
	depth, roots := 0, 0
	var out bytes.Buffer
	last := 0
	for {
		start := int(d.InputOffset())
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid answer XML")
		}
		end := int(d.InputOffset())
		switch t := tok.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
			}
			depth++
			segment := in[start:end]
			converted := convertNamespaceAttributes(segment, t)
			for _, a := range t.Attr {
				for _, name := range ambiguousXML {
					if strings.Contains(strings.ToLower(a.Value), name) {
						return nil, fmt.Errorf("ambiguous legacy reference in XML attribute")
					}
				}
			}
			out.Write(in[last:start])
			out.Write(converted)
			last = end
		case xml.EndElement:
			depth--
		case xml.CharData:
			for _, name := range ambiguousXML {
				if strings.Contains(strings.ToLower(string(t)), name) {
					return nil, fmt.Errorf("ambiguous legacy reference in XML text")
				}
			}
		case xml.Directive:
			return nil, fmt.Errorf("unsupported XML directive")
		}
	}
	if roots != 1 || depth != 0 {
		return nil, fmt.Errorf("answer XML needs one root")
	}
	out.Write(in[last:])
	return out.Bytes(), nil
}

type AnswerChange struct {
	ProfileID        int64
	OldHash, NewHash string
}
type DriverChange struct {
	ProfileID, AnswerID                                  int64
	OldKey, NewKey, OldWIM, NewWIM, OldScript, NewScript string
}
type Replacements struct {
	Answers  map[int64]AnswerChange
	Drivers  map[int64]DriverChange
	Profiles map[int64]bool
}

func decodeMarker(s string, v any) error {
	b, e := base64.RawURLEncoding.DecodeString(s)
	if e != nil {
		return fmt.Errorf("invalid menu metadata encoding")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(v); e != nil {
		return fmt.Errorf("invalid menu metadata JSON")
	}
	if d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("trailing menu metadata JSON")
	}
	return nil
}
func encodeMarker(v any) string {
	b, _ := json.Marshal(v)
	return base64.RawURLEncoding.EncodeToString(b)
}

// ConvertMenu parses all five marker types, changes dependency hashes by object
// ID, and changes only command tokens owned by OMB. Display text is preserved.
func ConvertMenu(in []byte, r Replacements) ([]byte, error) {
	if !bytes.HasPrefix(in, []byte("#!ipxe\n")) {
		return nil, fmt.Errorf("unsupported menu header")
	}
	lines := strings.Split(string(in), "\n")
	seen := map[string]bool{}
	for i, line := range lines {
		if strings.HasPrefix(line, "# pxe-platform:") || strings.HasPrefix(line, "# omb:") {
			old := strings.HasPrefix(line, "# pxe-platform:")
			prefix := "# omb:"
			if old {
				prefix = "# pxe-platform:"
			}
			rest := strings.TrimPrefix(line, prefix)
			kind, payload, ok := strings.Cut(rest, " ")
			if !ok || seen[kind] {
				return nil, fmt.Errorf("conflicting menu marker")
			}
			seen[kind] = true
			switch kind {
			case "folders-v1":
				var folders map[string]string
				if e := decodeMarker(payload, &folders); e != nil {
					return nil, e
				}
			case "look":
				if payload != "pult-4" {
					return nil, fmt.Errorf("unsupported menu look")
				}
			case "winiso-v1":
				var ids []int64
				if e := decodeMarker(payload, &ids); e != nil {
					return nil, e
				}
				for _, id := range ids {
					if id <= 0 || r.Profiles != nil && !r.Profiles[id] {
						return nil, fmt.Errorf("unresolved menu profile %d", id)
					}
				}
			case "winanswers-v1":
				var refs []struct {
					ID        int64  `json:"id"`
					ProfileID int64  `json:"profile_id"`
					SHA256    string `json:"sha256"`
				}
				if e := decodeMarker(payload, &refs); e != nil {
					return nil, e
				}
				for n := range refs {
					ref := &refs[n]
					v, ok := r.Answers[ref.ID]
					if !ok || ref.ProfileID != v.ProfileID || ref.SHA256 != v.OldHash && ref.SHA256 != v.NewHash {
						return nil, fmt.Errorf("unresolved menu answer %d", ref.ID)
					}
					ref.SHA256 = v.NewHash
				}
				payload = encodeMarker(refs)
			case "windrivers-v1":
				var refs []struct {
					ID           int64  `json:"id"`
					ProfileID    int64  `json:"profile_id"`
					AnswerID     int64  `json:"answer_id"`
					AnswerSHA256 string `json:"answer_sha256"`
					WIMKey       string `json:"wim_key"`
					WIMSHA256    string `json:"wim_sha256"`
					ScriptSHA256 string `json:"script_sha256"`
				}
				if e := decodeMarker(payload, &refs); e != nil {
					return nil, e
				}
				for n := range refs {
					ref := &refs[n]
					v, ok := r.Drivers[ref.ID]
					if !ok || v.ProfileID != ref.ProfileID || v.AnswerID != ref.AnswerID || ref.WIMKey != v.OldKey && ref.WIMKey != v.NewKey || ref.WIMSHA256 != v.OldWIM && ref.WIMSHA256 != v.NewWIM || ref.ScriptSHA256 != v.OldScript && ref.ScriptSHA256 != v.NewScript {
						return nil, fmt.Errorf("unresolved menu driver %d", ref.ID)
					}
					ref.WIMKey = v.NewKey
					ref.WIMSHA256 = v.NewWIM
					ref.ScriptSHA256 = v.NewScript
					if ref.AnswerID > 0 {
						a, ok := r.Answers[ref.AnswerID]
						if !ok || ref.AnswerSHA256 != a.OldHash && ref.AnswerSHA256 != a.NewHash {
							return nil, fmt.Errorf("unresolved driver answer %d", ref.AnswerID)
						}
						ref.AnswerSHA256 = a.NewHash
					}
				}
				payload = encodeMarker(refs)
			default:
				return nil, fmt.Errorf("unsupported menu marker %s", kind)
			}
			lines[i] = "# omb:" + kind + " " + payload
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ":") {
			continue
		}
		words := strings.Fields(line)
		if len(words) == 0 {
			continue
		}
		// Text-bearing commands can contain the same filenames intentionally.
		if words[0] == "echo" || words[0] == "item" || words[0] == "menu" {
			lines[i] = strings.NewReplacer("${pxe_fw}", "${omb_fw}", "${pxe_fb}", "${omb_fb}").Replace(line)
			continue
		}
		if words[0] != "initrd" && words[0] != "imgfetch" && words[0] != "kernel" {
			for _, name := range ambiguousXML {
				if strings.Contains(line, name) {
					return nil, fmt.Errorf("ambiguous legacy reference in menu command")
				}
			}
		}
		for j, w := range words {
			w = strings.NewReplacer("${pxe_fb}", "${omb_fb}", "${pxe_fw}", "${omb_fw}").Replace(w)
			if w == "pxe_fb" {
				w = "omb_fb"
			}
			if w == "pxe_fw" {
				w = "omb_fw"
			}
			if w == "pxe-hello" {
				w = "omb-hello"
			}
			if w == "pxe-choice" {
				w = "omb-choice"
			}
			if words[0] == "initrd" || words[0] == "imgfetch" || words[0] == "kernel" {
				var e error
				w, e = convertMenuAsset(w, r)
				if e != nil {
					return nil, e
				}
			}
			words[j] = w
		}
		// Preserve original whitespace by editing tokens in their original spans.
		pos := 0
		var b strings.Builder
		for j, old := range strings.Fields(line) {
			idx := strings.Index(line[pos:], old) + pos
			b.WriteString(line[pos:idx])
			b.WriteString(words[j])
			pos = idx + len(old)
		}
		b.WriteString(line[pos:])
		lines[i] = b.String()
	}
	return []byte(strings.Join(lines, "\n")), nil
}
func convertMenuAsset(s string, r Replacements) (string, error) {
	if s == "pxe-setup.cmd" {
		return "omb-setup.cmd", nil
	}
	if s == "pxe-answer.xml" {
		return "omb-answer.xml", nil
	}
	if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") && !strings.HasPrefix(s, "/assets/") {
		for _, old := range ambiguousXML {
			if strings.Contains(s, old) {
				return "", fmt.Errorf("ambiguous embedded menu asset reference")
			}
		}
		// These commands also contain flags, operators and kernel arguments
		// such as url=http://... and ds=nocloud-net;s=http://... . They are not URLs.
		return s, nil
	}
	u, e := url.Parse(s)
	if e != nil {
		return "", fmt.Errorf("invalid menu URL")
	}
	p := u.Path
	for _, part := range []string{"windows-profiles", "windows-answers", "windows-driver-configs"} {
		prefix := "/assets/" + part + "/"
		if strings.HasPrefix(p, prefix) {
			bits := strings.Split(strings.TrimPrefix(p, prefix), "/")
			if len(bits) != 2 {
				return "", fmt.Errorf("ambiguous menu asset URL")
			}
			id, e := strconv.ParseInt(bits[0], 10, 64)
			if e != nil || id <= 0 {
				return "", fmt.Errorf("invalid menu asset ID")
			}
			switch part {
			case "windows-profiles":
				if r.Profiles != nil && !r.Profiles[id] {
					return "", fmt.Errorf("unresolved menu asset profile %d", id)
				}
			case "windows-answers":
				if _, ok := r.Answers[id]; !ok {
					return "", fmt.Errorf("unresolved menu asset answer %d", id)
				}
			case "windows-driver-configs":
				if _, ok := r.Drivers[id]; !ok {
					return "", fmt.Errorf("unresolved menu asset driver %d", id)
				}
			}
			switch bits[1] {
			case "pxe-setup.cmd":
				bits[1] = "omb-setup.cmd"
			case "pxe-answer.xml":
				bits[1] = "omb-answer.xml"
			}
			u.Path = prefix + strings.Join(bits, "/")
			return u.String(), nil
		}
	}
	const wim = "/assets/windows-driver-wims/"
	if strings.HasPrefix(p, wim) {
		key := strings.TrimSuffix(strings.TrimPrefix(p, wim), "/boot.wim")
		found := false
		for _, d := range r.Drivers {
			if key == d.OldKey || key == d.NewKey {
				u.Path = wim + path.Join(d.NewKey, "boot.wim")
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("unresolved menu WIM key")
		}
		return u.String(), nil
	}
	return s, nil
}

// Attribute spans are scanned outside quoted values. A regex over a complete
// start tag would also rewrite namespace-looking text in a foreign attribute.
func convertNamespaceAttributes(segment []byte, element xml.StartElement) []byte {
	own := map[string]bool{}
	for _, a := range element.Attr {
		if a.Value != "urn:pxe-platform:unattend-editor" {
			continue
		}
		if a.Name.Space == "xmlns" {
			own["xmlns:"+a.Name.Local] = true
		} else if a.Name.Space == "" && a.Name.Local == "xmlns" {
			own["xmlns"] = true
		}
	}
	if len(own) == 0 {
		return segment
	}
	space := func(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }
	pos := 1
	for pos < len(segment) && !space(segment[pos]) && segment[pos] != '>' && segment[pos] != '/' {
		pos++
	}
	var out bytes.Buffer
	last := 0
	for pos < len(segment) {
		for pos < len(segment) && space(segment[pos]) {
			pos++
		}
		if pos >= len(segment) || segment[pos] == '>' || segment[pos] == '/' {
			break
		}
		start := pos
		for pos < len(segment) && !space(segment[pos]) && segment[pos] != '=' {
			pos++
		}
		name := string(segment[start:pos])
		for pos < len(segment) && segment[pos] != '=' {
			pos++
		}
		pos++
		for pos < len(segment) && space(segment[pos]) {
			pos++
		}
		if pos >= len(segment) {
			break
		}
		quote := segment[pos]
		pos++
		valueStart := pos
		for pos < len(segment) && segment[pos] != quote {
			pos++
		}
		if pos >= len(segment) {
			break
		}
		if own[name] {
			out.Write(segment[last:valueStart])
			out.WriteString("urn:omb:unattend-editor")
			last = pos
		}
		pos++
	}
	out.Write(segment[last:])
	return out.Bytes()
}
