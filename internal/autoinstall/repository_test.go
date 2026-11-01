package autoinstall

import (
	"go.yaml.in/yaml/v3"
	"testing"
)

func TestOfflineRejectsArchiveRequirements(t *testing.T) {
	for _, feature := range []string{"package", "ssh", "drivers", "codecs"} {
		t.Run(feature, func(t *testing.T) {
			f := validForm()
			f.MirrorMode = "offline"
			switch feature {
			case "package":
				f.Packages = []string{"curl"}
			case "ssh":
				f.SSH.InstallServer = true
			case "drivers":
				f.Drivers = true
			case "codecs":
				f.Codecs = true
			}
			if _, _, err := Compile(f); err == nil {
				t.Fatal("archive requirement accepted")
			}
		})
	}
}
func TestOfflineAndCustomRepository(t *testing.T) {
	f := validForm()
	f.MirrorMode = "offline"
	_, data, err := Compile(f)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	yaml.Unmarshal(data, &doc)
	a := doc["autoinstall"].(map[string]any)
	apt := a["apt"].(map[string]any)
	if apt["fallback"] != "offline-install" || apt["geoip"] != false || len(apt["mirror-selection"].(map[string]any)["primary"].([]any)) != 0 || a["refresh-installer"].(map[string]any)["update"] != false {
		t.Fatalf("offline: %#v", a)
	}
	f.MirrorMode = "custom"
	f.MirrorURL = "http://repo.example/ubuntu"
	_, data, err = Compile(f)
	if err != nil {
		t.Fatal(err)
	}
	yaml.Unmarshal(data, &doc)
	apt = doc["autoinstall"].(map[string]any)["apt"].(map[string]any)
	if apt["fallback"] != "abort" {
		t.Fatal("custom must abort")
	}
	for _, u := range []string{"ftp://repo/ubuntu", "https://user:pass@repo/", "http://repo/?token=secret", "not-a-url"} {
		f.MirrorURL = u
		if _, _, err := Compile(f); err == nil {
			t.Fatal("bad mirror accepted")
		}
	}
}
