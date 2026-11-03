package autoinstall

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func validForm() Form {
	return Form{Locale: "en_US.UTF-8", Keyboard: "us", TimeZone: "UTC", Hostname: "lab", Username: "operator", Password: "unique-plaintext-password", DiskMode: "interactive"}
}

func TestCompileDiskModes(t *testing.T) {
	for _, mode := range []string{"interactive", "serial"} {
		t.Run(mode, func(t *testing.T) {
			form := validForm()
			form.DiskMode = mode
			if mode == "serial" {
				form.DiskSerial = "DISK: #123"
			}
			values, data, err := Compile(form)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(data, []byte("#cloud-config\n")) || len(data) >= 64*1024 {
				t.Fatal("invalid cloud-config envelope")
			}
			encoded, err := json.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(data, []byte(form.Password)) || bytes.Contains(encoded, []byte(form.Password)) {
				t.Fatal("plaintext password escaped into output")
			}
			if values.PasswordHash == "" {
				t.Fatal("missing password hash")
			}
			var doc map[string]any
			if err := yaml.Unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			if len(doc) != 1 {
				t.Fatalf("unexpected root keys: %v", doc)
			}
			ai := doc["autoinstall"].(map[string]any)
			if ai["version"] != 1 || ai["locale"] != form.Locale || ai["timezone"] != form.TimeZone {
				t.Fatalf("unexpected configuration: %v", ai)
			}
			identity := ai["identity"].(map[string]any)
			if identity["hostname"] != form.Hostname || identity["username"] != form.Username || identity["password"] != values.PasswordHash {
				t.Fatal("identity did not round-trip")
			}
			if ai["keyboard"].(map[string]any)["layout"] != form.Keyboard {
				t.Fatal("keyboard did not round-trip")
			}
			if mode == "interactive" {
				sections := ai["interactive-sections"].([]any)
				if len(sections) != 1 || sections[0] != "storage" || ai["storage"] != nil {
					t.Fatal("interactive mode selected a disk")
				}
			} else {
				layout := ai["storage"].(map[string]any)["layout"].(map[string]any)
				if layout["name"] != "direct" || layout["match"].(map[string]any)["serial"] != form.DiskSerial || ai["interactive-sections"] != nil {
					t.Fatal("serial mode did not preserve exact disk match")
				}
			}
			again, err := Render(values)
			if err != nil || !bytes.Equal(data, again) {
				t.Fatal("Render changed compiled bytes")
			}
		})
	}
}

func TestCompileRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Form)
	}{
		{"missing locale", func(f *Form) { f.Locale = "" }},
		{"bad locale", func(f *Form) { f.Locale = "en_US\nUTF-8" }},
		{"long locale", func(f *Form) { f.Locale = strings.Repeat("a", 65) }},
		{"missing keyboard", func(f *Form) { f.Keyboard = "" }},
		{"bad keyboard", func(f *Form) { f.Keyboard = "us: bad" }},
		{"long keyboard", func(f *Form) { f.Keyboard = strings.Repeat("a", 33) }},
		{"missing timezone", func(f *Form) { f.TimeZone = "" }},
		{"unknown timezone", func(f *Form) { f.TimeZone = "Mars/Olympus" }},
		{"long timezone", func(f *Form) { f.TimeZone = strings.Repeat("a", 65) }},
		{"missing hostname", func(f *Form) { f.Hostname = "" }},
		{"bad hostname", func(f *Form) { f.Hostname = "-lab" }},
		{"long hostname", func(f *Form) { f.Hostname = strings.Repeat("a", 64) }},
		{"missing username", func(f *Form) { f.Username = "" }},
		{"bad username", func(f *Form) { f.Username = "user: admin" }},
		{"long username", func(f *Form) { f.Username = strings.Repeat("a", 33) }},
		{"missing password", func(f *Form) { f.Password = "" }},
		{"long password", func(f *Form) { f.Password = strings.Repeat("a", 1025) }},
		{"control password", func(f *Form) { f.Password = "hidden\npassword" }},
		{"unknown mode", func(f *Form) { f.DiskMode = "largest" }},
		{"missing serial", func(f *Form) { f.DiskMode = "serial" }},
		{"long serial", func(f *Form) { f.DiskMode = "serial"; f.DiskSerial = strings.Repeat("a", 129) }},
		{"stale interactive serial", func(f *Form) { f.DiskSerial = "DISK123" }},
	}
	for _, bad := range []string{"*", "?", "[", "]", "disk\x00", "disk\n", " disk", "disk ", "disk\x7f"} {
		serial := bad
		cases = append(cases, struct {
			name   string
			change func(*Form)
		}{"invalid serial " + serial, func(f *Form) { f.DiskMode = "serial"; f.DiskSerial = serial }})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			form := validForm()
			tc.change(&form)
			_, data, err := Compile(form)
			if err == nil || len(data) != 0 {
				t.Fatal("invalid form accepted")
			}
			if strings.Contains(err.Error(), form.Password) && form.Password != "" {
				t.Fatal("error contains plaintext password")
			}
		})
	}
}

func TestValidateRejectsInvalidStoredValues(t *testing.T) {
	values, _, err := Compile(validForm())
	if err != nil {
		t.Fatal(err)
	}
	for _, hash := range []string{"", "plaintext", "$6$salt$hash", "$y$j9T$bad"} {
		invalid := values
		invalid.PasswordHash = hash
		if Validate(invalid) == nil {
			t.Fatal("invalid password hash accepted")
		}
		if data, err := Render(invalid); err == nil || len(data) != 0 {
			t.Fatal("Render accepted invalid values")
		}
	}
}

func TestCompileRejectsControlCharactersBeforeNormalization(t *testing.T) {
	for _, field := range []string{"locale", "keyboard", "timezone", "hostname", "username", "disk mode"} {
		t.Run(field, func(t *testing.T) {
			form := validForm()
			switch field {
			case "locale":
				form.Locale += "\n"
			case "keyboard":
				form.Keyboard += "\t"
			case "timezone":
				form.TimeZone += "\r"
			case "hostname":
				form.Hostname += "\n"
			case "username":
				form.Username += "\t"
			case "disk mode":
				form.DiskMode += "\n"
			}
			if _, _, err := Compile(form); err == nil {
				t.Fatal("control character silently normalized")
			}
		})
	}
}

func TestCompileNormalizesNonsecretFields(t *testing.T) {
	form := validForm()
	form.Locale = " en_US.UTF-8 "
	form.Hostname = " lab "
	form.TimeZone = " Europe/Moscow "
	values, _, err := Compile(form)
	if err != nil {
		t.Fatal(err)
	}
	if values.Locale != "en_US.UTF-8" || values.Hostname != "lab" || values.TimeZone != "Europe/Moscow" {
		t.Fatal("fields not normalized")
	}
	if err := Validate(values); err != nil {
		t.Fatal(err)
	}
}

func TestCompileRejectsRootUsername(t *testing.T) {
	form := validForm()
	form.Username = "root"
	if _, _, err := Compile(form); err == nil || !strings.HasPrefix(err.Error(), "username:") {
		t.Fatalf("root username accepted or misreported: %v", err)
	}
}

func TestCompileDefaultDiskUsesInstallerChoice(t *testing.T) {
	form := validForm()
	form.DiskMode = "default"
	_, data, err := Compile(form)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	ai := doc["autoinstall"].(map[string]any)
	layout := ai["storage"].(map[string]any)["layout"].(map[string]any)
	if layout["name"] != "direct" {
		t.Fatalf("unexpected default disk layout: %#v", layout)
	}
	if _, exists := layout["match"]; exists {
		t.Fatalf("default disk constrained by a match: %#v", layout)
	}
}

func TestCompileDefaultDiskRejectsStaleSerial(t *testing.T) {
	form := validForm()
	form.DiskMode = "default"
	form.DiskSerial = "DISK123"
	if _, _, err := Compile(form); err == nil {
		t.Fatal("default disk accepted a stale serial")
	}
}
