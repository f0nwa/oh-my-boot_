package control

import (
	"strings"
	"testing"
)

func TestMenuItemTextPutsHintsAtTheRightEdge(t *testing.T) {
	line := expandMenuPad(menuItemText("Windows 11 26H2 auto", "!! WIPES DISK"))
	if len(line) != menuItemWidth || !strings.HasSuffix(line, "!! WIPES DISK") {
		t.Fatalf("line=%q", line)
	}
	if title, hint := splitMenuItemText(menuItemText("Windows 11 26H2 auto", "!! WIPES DISK")); title != "Windows 11 26H2 auto" || hint != "!! WIPES DISK" {
		t.Fatalf("split=%q %q", title, hint)
	}
	// Hints lose characters iPXE would read as variables or commands, and do
	// not squeeze a long title.
	if got := expandMenuPad(menuItemText("Win", "${evil}; x")); got != "Win"+strings.Repeat(" ", menuItemWidth-3-len("evil x"))+"evil x" {
		t.Fatalf("sanitised=%q", got)
	}
	long := strings.Repeat("W", 70)
	if menuItemText(long, "hint") != long {
		t.Fatal("hint squeezed into a long title")
	}
	if title, hint := splitMenuItemText("Windows 11"); title != "Windows 11" || hint != "" {
		t.Fatalf("plain split=%q %q", title, hint)
	}
}

func TestMenuThemeScreensKeepTheKeyLineAtTheBottom(t *testing.T) {
	profiles := []menuProfile{{id: "install_1001", title: "Win11"}, {id: "install_2002", title: "Win11 auto", hint: "!! WIPES DISK"}}
	script, err := buildMenuTreeWithProfiles("oh my boot_", "Diagnostics", "Ready", profiles, map[string]string{"install_2002": "Windows"})
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, want := range []string{menuTheme, "menu oh my boot_" + menuTitleSuffix + "\n", "item install_2002 " + menuItemText("Win11 auto", "!! WIPES DISK") + "\n"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in\n%s", want, text)
		}
	}
	for _, screen := range parseMenuScreens(text) {
		if n := len(screen.Items); n != menuScreenRows || !screen.Items[n-1].Gap || screen.Items[n-1].Text != menuFooter {
			t.Fatalf("%s: %d rows, last %+v", screen.Label, n, screen.Items[n-1])
		}
	}
}

func TestPublishedThemedMenuReadsBackWithoutChanges(t *testing.T) {
	profiles, answers, configs := draftFixture()
	d := MenuDraft{Title: "oh my boot_", Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{InstallationID: 3002, Title: "Win11 key", Folder: ""}, {InstallationID: 1017, Title: "Windows 11", Folder: ""}}}
	script, err := buildTestMenuFromDraft(d, testBootURL, profiles, answers, configs)
	if err != nil {
		t.Fatal(err)
	}
	d.look = menuLook
	back := draftFromVersion(MenuVersion{Title: d.Title, Label: d.Label, Message: d.Message, Script: string(script), Look: menuLook, Items: versionItems(d)})
	if changes := diffMenuDraft(back, d); len(changes) != 0 {
		t.Fatalf("changes after a round trip: %+v (items %+v)", changes, back.Items)
	}
	old := d
	old.look = "previous-look"
	if changes := diffMenuDraft(old, d); len(changes) != 1 || changes[0].Kind != "look" {
		t.Fatalf("old look: %+v", changes)
	}
}

// iPXE splits on whitespace and joins menu text with single spaces, so a
// menu line may not rely on runs of spaces: they come from ${spN:string}.
func TestMenuLinesNeverHoldRunsOfSpaces(t *testing.T) {
	profiles := []menuProfile{{id: "install_2002", title: "Win11 auto", hint: "!! WIPES DISK"}}
	script, err := buildMenuTreeWithProfiles("oh my boot_", "Diagnostics", "Ready", profiles, map[string]string{"install_2002": "Windows"})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(script), "\n") {
		if (strings.HasPrefix(line, "item ") || strings.HasPrefix(line, "menu ")) && strings.Contains(line, "  ") {
			t.Fatalf("double space in %q", line)
		}
	}
	if got := expandMenuPad(menuPad(73)); got != strings.Repeat(" ", 73) {
		t.Fatalf("pad=%q", got)
	}
	if !strings.Contains(menuTheme, "set sp64:hex 20:20:") || !strings.Contains(menuTheme, "set sp1:hex 20\n") {
		t.Fatal("pad settings missing")
	}
}

// The narrowest screen is iPXE's framebuffer console at 640x480: 71 columns
// of 9-pixel cells, 65 of them for item text.
func TestMenuLinesFitTheNarrowestScreen(t *testing.T) {
	line := expandMenuPad(menuItemText("Windows 11 key no-disk test", "auto install"))
	if len(line) > 640/9-2-4 || !strings.HasSuffix(line, "auto install") {
		t.Fatalf("%d columns: %q", len(line), line)
	}
}
