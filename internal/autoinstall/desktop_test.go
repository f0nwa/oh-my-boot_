package autoinstall

import (
	"bytes"
	"go.yaml.in/yaml/v3"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestCompileDesktopSettings(t *testing.T) {
	form := validForm()
	form.RealName = "Рабочий пользователь"
	form.Keyboard, form.KeyboardVariant = "us,ru", ","
	form.KeyboardToggle = "alt_shift_toggle"
	form.Packages = []string{"vim", "curl", "vim"}
	form.Drivers, form.Codecs = true, true
	form.PostInstallScript = "printf '%s\\n' 'ready' > /etc/omb-workstation\n"
	form.Shutdown = "poweroff"
	values, data, err := Compile(form)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(values.Packages, []string{"vim", "curl"}) {
		t.Fatalf("packages: %#v", values.Packages)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	ai := doc["autoinstall"].(map[string]any)
	if ai["identity"].(map[string]any)["realname"] != form.RealName || ai["shutdown"] != "poweroff" {
		t.Fatalf("desktop configuration: %#v", ai)
	}
	command := ai["late-commands"].([]any)[0].([]any)
	want := []any{"curtin", "in-target", "--", "/bin/sh", "-eu", "-c", form.PostInstallScript}
	if !reflect.DeepEqual(command, want) {
		t.Fatalf("script argv: %#v", command)
	}
	again, err := Render(values)
	if err != nil || !bytes.Equal(data, again) {
		t.Fatalf("non-deterministic output: %v", err)
	}
}
func TestDesktopRejectsInvalidSettings(t *testing.T) {
	cases := map[string]func(*Form){
		"layouts":  func(f *Form) { f.Keyboard = "us,ru,de,fr,es" },
		"repeat":   func(f *Form) { f.Keyboard = "us,us" },
		"variants": func(f *Form) { f.Keyboard = "us,ru"; f.KeyboardVariant = "basic" },
		"toggle":   func(f *Form) { f.KeyboardToggle = "unknown" },
		"packages": func(f *Form) {
			for i := 0; i < 129; i++ {
				f.Packages = append(f.Packages, "pkg"+strings.Repeat("a", i))
			}
		},
		"argument": func(f *Form) { f.Packages = []string{"--option"} },
		"shell":    func(f *Form) { f.Packages = []string{"pkg;cmd"} },
		"version":  func(f *Form) { f.Packages = []string{"pkg=1.0"} },
		"script":   func(f *Form) { f.PostInstallScript = strings.Repeat("a", 16385) },
		"nul":      func(f *Form) { f.PostInstallScript = "echo \x00" },
		"shutdown": func(f *Form) { f.Shutdown = "sleep" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := validForm()
			mutate(&f)
			if _, _, err := Compile(f); err == nil {
				t.Fatal("accepted invalid setting")
			}
		})
	}
}
func TestDesktopScriptFailsOnError(t *testing.T) {
	f := validForm()
	f.PostInstallScript = "false\nprintf should-not-run"
	_, data, err := Compile(f)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	argv := doc["autoinstall"].(map[string]any)["late-commands"].([]any)[0].([]any)
	args := []string{}
	for _, a := range argv[4:] {
		args = append(args, a.(string))
	}
	output, err := exec.Command(argv[3].(string), args...).CombinedOutput()
	if err == nil || len(output) != 0 {
		t.Fatalf("script did not fail early: %s %v", output, err)
	}
}
