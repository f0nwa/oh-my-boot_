package autoinstall

import (
	"bytes"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// MaxUploadBytes matches boot.MaxAutoinstallUserDataSize (64 KiB).
const MaxUploadBytes = 64 << 10

var (
	ErrUploadEmpty           = errors.New("autoinstall file is empty")
	ErrUploadTooLarge        = errors.New("autoinstall file exceeds 64 KiB")
	ErrUploadEncoding        = errors.New("autoinstall file must be valid UTF-8 text")
	ErrUploadCloudConfig     = errors.New("autoinstall file must start with #cloud-config")
	ErrUploadYAML            = errors.New("autoinstall file is not valid YAML")
	ErrUploadMissingSection  = errors.New("autoinstall file must contain an autoinstall section")
	ErrUploadVersion         = errors.New("autoinstall.version must be 1")
)

// UploadedMeta describes disk-handling metadata extracted from an uploaded
// Ubuntu Autoinstall YAML file.
type UploadedMeta struct {
	DiskMode   string
	DiskSerial string
}

// ValidateUploadedUserData validates a custom Ubuntu Autoinstall user-data file
// and returns the normalized bytes (with any UTF-8 BOM removed) and extracted
// disk metadata.
func ValidateUploadedUserData(raw []byte) ([]byte, UploadedMeta, error) {
	if len(raw) == 0 {
		return nil, UploadedMeta{}, ErrUploadEmpty
	}
	if len(raw) > MaxUploadBytes {
		return nil, UploadedMeta{}, ErrUploadTooLarge
	}
	if !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return nil, UploadedMeta{}, ErrUploadEncoding
	}
	clean := bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})
	if len(clean) == 0 {
		return nil, UploadedMeta{}, ErrUploadEmpty
	}
	if !hasCloudConfigHeader(clean) {
		return nil, UploadedMeta{}, ErrUploadCloudConfig
	}

	dec := yaml.NewDecoder(bytes.NewReader(clean))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		return nil, UploadedMeta{}, ErrUploadYAML
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, UploadedMeta{}, ErrUploadYAML
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, UploadedMeta{}, ErrUploadYAML
	}
	root := doc.Content[0]
	aiNode := yamlMapValue(root, "autoinstall")
	if aiNode == nil || aiNode.Kind != yaml.MappingNode {
		return nil, UploadedMeta{}, ErrUploadMissingSection
	}
	verNode := yamlMapValue(aiNode, "version")
	if verNode == nil || verNode.Kind != yaml.ScalarNode {
		return nil, UploadedMeta{}, ErrUploadVersion
	}
	ver, err := strconv.Atoi(strings.TrimSpace(verNode.Value))
	if err != nil || ver != 1 {
		return nil, UploadedMeta{}, ErrUploadVersion
	}

	meta := analyzeUploadedDisk(aiNode)
	out := append([]byte(nil), clean...)
	return out, meta, nil
}

func hasCloudConfigHeader(data []byte) bool {
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		s := strings.TrimSpace(strings.TrimSuffix(string(line), "\r"))
		if s == "" {
			continue
		}
		return s == "#cloud-config" || strings.HasPrefix(s, "#cloud-config ") || strings.HasPrefix(s, "#cloud-config\t")
	}
	return false
}

func yamlMapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func analyzeUploadedDisk(ai *yaml.Node) UploadedMeta {
	if interactive := yamlMapValue(ai, "interactive-sections"); interactive != nil && interactive.Kind == yaml.SequenceNode {
		for _, item := range interactive.Content {
			v := strings.TrimSpace(item.Value)
			if v == "storage" || v == "*" {
				return UploadedMeta{DiskMode: "interactive"}
			}
		}
	}
	storage := yamlMapValue(ai, "storage")
	if storage == nil || storage.Kind != yaml.MappingNode {
		return UploadedMeta{DiskMode: "unknown"}
	}
	if layout := yamlMapValue(storage, "layout"); layout != nil {
		if layout.Kind == yaml.MappingNode {
			if match := yamlMapValue(layout, "match"); match != nil && match.Kind == yaml.MappingNode {
				if serialNode := yamlMapValue(match, "serial"); serialNode != nil && serialNode.Kind == yaml.ScalarNode {
					serial := strings.TrimSpace(serialNode.Value)
					if len(serial) > 0 && len(serial) <= 128 && validText(serial) {
						return UploadedMeta{DiskMode: "serial", DiskSerial: serial}
					}
				}
			}
		}
		return UploadedMeta{DiskMode: "default"}
	}
	if cfg := yamlMapValue(storage, "config"); cfg != nil && cfg.Kind == yaml.SequenceNode && len(cfg.Content) > 0 {
		return UploadedMeta{DiskMode: "custom"}
	}
	return UploadedMeta{DiskMode: "unknown"}
}
