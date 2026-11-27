package control

import (
	"encoding/json"
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

	fullPath := filepath.Join(dir, "release-full.json")
	fullPayload := `{"channel":"stable","commit":"b7f1cabfee6b7defbd7b132d70de42c537c20530","display_version":"v0.2.0","package":"omb 0.2.0","version":"0.2.0"}`
	if err := os.WriteFile(fullPath, []byte(fullPayload), 0o600); err != nil {
		t.Fatal(err)
	}
	fullLoaded := LoadReleaseInfo(fullPath)
	if fullLoaded.Channel != ChannelStable || fullLoaded.DisplayVersion != "v0.2.0" || fullLoaded.Package != "omb 0.2.0" || fullLoaded.Version != "0.2.0" || fullLoaded.ShortCommit() != "b7f1cab" {
		t.Fatalf("unexpected full release info: %+v", fullLoaded)
	}
	marshaled, err := json.Marshal(fullLoaded)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(marshaled), "from_commit") {
		t.Fatalf("release metadata must not contain from_commit: %s", marshaled)
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
	for _, want := range []string{`id="server-release-card"`, "Выпуск OMB", "v0.2.0-dev.1", "<dt>Канал</dt>", "dev (разработка)", "Все выпуски"} {
		if !strings.Contains(bodyRU, want) {
			t.Fatalf("RU /system missing %q", want)
		}
	}

	sEN := &Server{
		Store:   authOnlyStore{user: User{ID: 2, Username: "obs", Role: "observer", Enabled: true, Language: langEN}},
		Release: &ReleaseInfo{Version: "0.2.0", DisplayVersion: "v0.2.0", Channel: ChannelStable, Commit: "b7f1cab", Package: "omb 0.2.0"},
	}
	bodyEN := libraryGet(t, sEN, "/system")
	for _, want := range []string{`id="server-release-card"`, "OMB release", "v0.2.0", "<dt>Channel</dt>", "stable (stable)", "All releases"} {
		if !strings.Contains(bodyEN, want) {
			t.Fatalf("EN /system missing %q", want)
		}
	}
	for _, test := range []struct {
		body, version string
	}{
		{bodyRU, "v0.2.0-dev.1"},
		{bodyEN, "v0.2.0"},
	} {
		_, rest, _ := strings.Cut(test.body, `id="server-release-card"`)
		card, _, _ := strings.Cut(rest, "</section>")
		if count := strings.Count(card, test.version); count != 1 {
			t.Errorf("release card shows version %s %d times", test.version, count)
		}
		for _, unwanted := range []string{"<dt>Коммит</dt>", "<dt>Пакет</dt>", "<dt>Commit</dt>", "<dt>Package</dt>", "sudo omb update"} {
			if strings.Contains(card, unwanted) {
				t.Errorf("release card still contains %q", unwanted)
			}
		}
	}
}

func TestReleaseInfoGitHubLinks(t *testing.T) {
	info := NormalizeReleaseInfo(ReleaseInfo{Version: "0.2.5~dev.4", SourceRepo: "example-org/omb.fork"})
	if got := info.RepoURL(); got != "https://github.com/example-org/omb.fork" {
		t.Fatalf("RepoURL = %q", got)
	}
	if got := info.ReleasesURL(); got != "https://github.com/example-org/omb.fork/releases" {
		t.Fatalf("ReleasesURL = %q", got)
	}
	if got := info.ReleaseURL(); got != "https://github.com/example-org/omb.fork/releases/tag/v0.2.5-dev.4" {
		t.Fatalf("ReleaseURL = %q", got)
	}

	// Missing or unsafe source_repo falls back to the default repository.
	for _, bad := range []string{"", "no-slash", "a/b/c", "https://evil.example/x", "owner/../x", "own er/repo"} {
		got := NormalizeReleaseInfo(ReleaseInfo{SourceRepo: bad})
		if got.SourceRepo != DefaultSourceRepo || got.RepoURL() != "https://github.com/"+DefaultSourceRepo {
			t.Fatalf("source_repo %q: got %q / %q", bad, got.SourceRepo, got.RepoURL())
		}
	}

	// Local builds without a release tag link to the releases list.
	local := NormalizeReleaseInfo(ReleaseInfo{})
	if got := local.ReleaseURL(); got != "https://github.com/"+DefaultSourceRepo+"/releases" {
		t.Fatalf("local ReleaseURL = %q", got)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "release.json")
	raw, _ := json.Marshal(map[string]string{"version": "0.2.0", "channel": "stable", "source_repo": "acme/omb"})
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LoadReleaseInfo(path).RepoURL(); got != "https://github.com/acme/omb" {
		t.Fatalf("LoadReleaseInfo RepoURL = %q", got)
	}
}

func TestTopbarAndSystemCardRenderGitHubLinks(t *testing.T) {
	s := &Server{
		Store:   authOnlyStore{user: User{ID: 1, Username: "admin", Role: "admin", Enabled: true, Language: langRU}},
		Release: &ReleaseInfo{Version: "0.2.5~dev.4", DisplayVersion: "v0.2.5-dev.4", Channel: ChannelDev, SourceRepo: "acme/omb"},
	}
	body := libraryGet(t, s, "/system")
	for _, want := range []string{
		`class="github-top-btn" href="https://github.com/acme/omb" target="_blank" rel="noopener noreferrer"`,
		`aria-label="Репозиторий OMB на GitHub (откроется в новой вкладке)"`,
		`class="release-repo-link" href="https://github.com/acme/omb"`,
		`>acme/omb</a>`,
		`href="https://github.com/acme/omb/releases"`,
		"Все выпуски",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("/system missing %q", want)
		}
	}
	if strings.Contains(body, "Открыть на GitHub") || strings.Contains(body, `href="https://github.com/acme/omb/releases/tag/v0.2.5-dev.4"`) {
		t.Fatal("/system should link to all releases without a separate GitHub release button")
	}

	sEN := &Server{
		Store:   authOnlyStore{user: User{ID: 2, Username: "obs", Role: "observer", Enabled: true, Language: langEN}},
		Release: &ReleaseInfo{Version: "0.2.0", DisplayVersion: "v0.2.0", Channel: ChannelStable},
	}
	bodyEN := libraryGet(t, sEN, "/")
	for _, want := range []string{`class="github-top-btn" href="https://github.com/` + DefaultSourceRepo + `"`, "OMB repository on GitHub (opens in a new tab)"} {
		if !strings.Contains(bodyEN, want) {
			t.Fatalf("EN / missing %q", want)
		}
	}
}
