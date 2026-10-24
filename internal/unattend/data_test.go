package unattend

import "testing"

func TestLoadData(t *testing.T) {
	d, err := LoadData()
	if err != nil {
		t.Fatal(err)
	}
	for name, n := range map[string]int{"bloatware": len(d.Bloatwares), "components": len(d.Components), "image languages": len(d.ImageLanguages), "keyboards": len(d.KeyboardIdentifiers), "locations": len(d.GeoLocations), "locales": len(d.UserLocales), "editions": len(d.WindowsEditions), "time zones": len(d.TimeOffsets), "desktop icons": len(d.DesktopIcons), "start folders": len(d.StartFolders)} {
		if n == 0 {
			t.Errorf("%s: none loaded", name)
		}
	}
	if x, err := d.ImageLanguage("EN-us"); err != nil || x.ID != "en-US" {
		t.Fatalf("image language: %+v %v", x, err)
	}
	if x, err := d.Keyboard("00000409"); err != nil || x.Type != "Keyboard" {
		t.Fatalf("keyboard: %+v %v", x, err)
	}
	if x, err := d.Geo("244"); err != nil || x.DisplayName == "" {
		t.Fatalf("geo: %+v %v", x, err)
	}
	if x, err := d.Bloatware("RemoveTeams"); err != nil || len(x.Steps) == 0 {
		t.Fatalf("bloatware: %+v %v", x, err)
	}
	if x, err := d.Locale("aa-DJ"); err != nil || x.KeyboardLayout == nil || x.KeyboardLayout.ID != "00000409" || x.GeoLocation.ID != "62" {
		t.Fatalf("locale links: %+v %v", x, err)
	}
	if x, err := d.StartFolder("FileExplorer"); err != nil || len(x.Bytes) != 16 {
		t.Fatalf("start folder: %+v %v", x, err)
	}
	if _, err := d.Edition("no-such-edition"); err == nil || err.Error() != "Could not find an element of type 'T' with key 'no-such-edition'." {
		t.Fatalf("missing key: %v", err)
	}
}

func TestCompareCulture(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"a", "B", -1}, {"B", "a", 1}, {"a", "A", -1}, {"Windows 10", "Windows 11", -1},
		{"9", "a", -1}, {" ", "0", -1}, {"ab", "abc", -1}, {"ThisPC", "Network", 1},
	} {
		if got := compareCulture(c.a, c.b); got != c.want {
			t.Errorf("compareCulture(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
