package control

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadReleaseInfoAndFormatting(t *testing.T) {
	if got := FormatDisplayVersion("0.2.0~dev.1"); got != "v0.2.0-dev.1" {
		t.Fatalf("FormatDisplayVersion dev = %q", got)
	}
	if got := FormatDisplayVersion("0.1.4"); got != "v0.1.4" {
		t.Fatalf("FormatDisplayVersion stable = %q", got)
	}
	if got := FormatDisplayVersion("1:0.2.0~dev.2"); got != "v0.2.0-dev.2" {
		t.Fatalf("FormatDisplayVersion epoch = %q", got)
	}

	missing := LoadReleaseInfo(filepath.Join(t.TempDir(), "missing.json"))
	if missing.Channel != ChannelStable || missing.DisplayVersion != "dev" || missing.Package != "omb" {
		t.Fatalf("unexpected fallback release info: %+v", missing)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "release.json")
	if err := os.WriteFile(path, []byte(`{"commit":"b7f1cabfee6b7defbd7b132d70de42c537c20530","version":"0.2.0~dev.1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded := LoadReleaseInfo(path)
	if loaded.Channel != ChannelDev || loaded.DisplayVersion != "v0.2.0-dev.1" || loaded.Package != "omb 0.2.0~dev.1" || loaded.ShortCommit() != "b7f1cab" {
		t.Fatalf("unexpected loaded release info: %+v", loaded)
	}
}

func TestTopbarShowsDevPillOnlyOnDevChannel(t *testing.T) {
	sDev := &Server{
		Store:   authOnlyStore{user: User{ID: 1, Username: "admin", Role: "admin", Enabled: true}},
		Release: &ReleaseInfo{Version: "0.2.0~dev.1", DisplayVersion: "v0.2.0-dev.1", Channel: ChannelDev, Commit: "b7f1cab"},
	}
	bodyDev := libraryGet(t, sDev, "/system")
	if !strings.Contains(bodyDev, `class="chan-pill is-dev topbar-chan-pill" href="/system"`) {
		t.Fatalf("dev channel should render topbar dev badge: %s", bodyDev)
	}

	sStable := &Server{
		Store:   authOnlyStore{user: User{ID: 1, Username: "admin", Role: "admin", Enabled: true}},
		Release: &ReleaseInfo{Version: "0.2.0", DisplayVersion: "v0.2.0", Channel: ChannelStable, Commit: "b7f1cab"},
	}
	bodyStable := libraryGet(t, sStable, "/system")
	if strings.Contains(bodyStable, "topbar-chan-pill") {
		t.Fatalf("stable channel must not render topbar dev badge: %s", bodyStable)
	}
}

func TestSystemPageRendersReleaseCardRUAndEN(t *testing.T) {
	rel := &ReleaseInfo{
		Version:        "0.2.0~dev.1",
		DisplayVersion: "v0.2.0-dev.1",
		Channel:        ChannelDev,
		Commit:         "b7f1cabfee6b7defbd7b132d70de42c537c20530",
		Package:        "omb 0.2.0~dev.1",
	}
	sRU := &Server{
		Store:   authOnlyStore{user: User{ID: 1, Username: "admin", Role: "admin", Enabled: true, Language: langRU}},
		Release: rel,
	}
	bodyRU := libraryGet(t, sRU, "/system")
	for _, want := range []string{`id="server-release-card"`, "Выпуск OMB", "v0.2.0-dev.1", "dev (разработка)", "b7f1cab", "omb 0.2.0~dev.1"} {
		if !strings.Contains(bodyRU, want) {
			t.Fatalf("RU /system missing %q", want)
		}
	}

	sEN := &Server{
		Store:   authOnlyStore{user: User{ID: 2, Username: "obs", Role: "observer", Enabled: true, Language: langEN}},
		Release: &ReleaseInfo{Version: "0.2.0", DisplayVersion: "v0.2.0", Channel: ChannelStable, Commit: "b7f1cab", Package: "omb 0.2.0"},
	}
	bodyEN := libraryGet(t, sEN, "/system")
	for _, want := range []string{`id="server-release-card"`, "OMB release", "v0.2.0", "stable (stable)", "b7f1cab", "omb 0.2.0"} {
		if !strings.Contains(bodyEN, want) {
			t.Fatalf("EN /system missing %q", want)
		}
	}
}
