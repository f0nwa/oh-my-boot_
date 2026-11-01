package autoinstall

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
)

func TestGoldenUserData(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "autoinstall")
	compiler := jsonschema.NewCompiler()
	schemaPath := filepath.Join(root, "autoinstall-schema-26.04.1.json")
	schema, err := compiler.Compile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	paths := flag.Args()
	if len(paths) == 0 {
		paths = []string{filepath.Join(root, "interactive-user-data"), filepath.Join(root, "serial-user-data"), filepath.Join(root, "desktop-user-data"), filepath.Join(root, "offline-user-data")}
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(data, []byte("#cloud-config\n")) {
				t.Fatal("missing cloud-config header")
			}
			var doc map[string]any
			if err := yaml.Unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			ai, ok := doc["autoinstall"].(map[string]any)
			if !ok || len(doc) != 1 {
				t.Fatal("expected a single autoinstall mapping")
			}
			if err := schema.Validate(ai); err != nil {
				t.Fatalf("schema: %v", err)
			}
			if len(flag.Args()) == 0 {
				identity := ai["identity"].(map[string]any)
				mode, serial := "interactive", ""
				if strings.Contains(path, "serial-user-data") {
					mode, serial = "serial", "DISK: #123"
				}
				values := Values{Locale: "en_US.UTF-8", Keyboard: "us", TimeZone: "UTC", Hostname: "lab", Username: "operator", PasswordHash: identity["password"].(string), DiskMode: mode, DiskSerial: serial}
				switch filepath.Base(path) {
				case "desktop-user-data":
					values = Values{Locale: "en_US.UTF-8", Keyboard: "us,ru", KeyboardVariant: ",", KeyboardToggle: "alt_shift_toggle", TimeZone: "Europe/Moscow", Hostname: "desktop-lab", Username: "operator", RealName: "Desktop User", PasswordHash: identity["password"].(string), DiskMode: "serial", DiskSerial: "DESKTOP-123", SourceID: "ubuntu-desktop", Drivers: true, Codecs: true, Packages: []string{"curl", "vim", "g++"}, MirrorMode: "custom", MirrorURL: "http://repo.example/ubuntu", Network: NetworkValues{Mode: "static", MAC: "52:54:00:12:34:56", Address: "192.0.2.10/24", Gateway: "192.0.2.1", DNS: []string{"192.0.2.53"}}, SSH: SSHValues{InstallServer: true, AuthorizedKeys: []string{ai["ssh"].(map[string]any)["authorized-keys"].([]any)[0].(string)}}, PostInstallScript: "printf '%s\\n' desktop > /etc/omb-workstation\n", Shutdown: "poweroff"}
				case "offline-user-data":
					values.Hostname = "offline-desktop"
					values.MirrorMode = "offline"
					values.Shutdown = "reboot"
				}
				generated, err := Render(values)
				if err != nil || !bytes.Equal(data, generated) {
					t.Fatalf("fixture differs from generated output: %v", err)
				}
			}
			ai["version"] = "invalid"
			if err := schema.Validate(ai); err == nil {
				t.Fatal("schema accepted invalid version")
			} else if !strings.Contains(fmt.Sprint(err), "version") {
				t.Fatalf("missing field diagnostic: %v", err)
			}
		})
	}
}
