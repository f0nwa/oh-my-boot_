package control

import (
	"strings"
	"testing"
)

func TestParseMenuScreensReadsFoldersBackAndFixedItems(t *testing.T) {
	profiles := []menuProfile{{id: "ubuntu", title: "Ubuntu Desktop 26.04.1"}, {id: "install_1001", title: "Win11"}, {id: "install_2002", title: "Win11 auto"}}
	script, err := buildMenuTreeWithProfiles("oh my boot_", "Diagnostics", "Ready", profiles, map[string]string{"ubuntu": "Linux", "install_1001": "Windows", "install_2002": "Windows/Auto"})
	if err != nil {
		t.Fatal(err)
	}
	screens := parseMenuScreens(string(script))
	var got []string
	for _, screen := range screens {
		var items []string
		for _, item := range screen.Items {
			if item.Gap {
				continue
			}
			mark := ""
			switch {
			case item.Folder:
				mark = "/"
			case item.Back:
				mark = "<"
			case item.Fixed:
				mark = "!"
			}
			items = append(items, mark+item.Text)
		}
		got = append(got, screen.Title+": "+strings.Join(items, ", "))
	}
	want := []string{
		"oh my boot_: /Linux, /Windows, !Boot local disk, !Diagnostics",
		"oh my boot_ / Linux: <.. Back, Ubuntu Desktop 26.04.1",
		"oh my boot_ / Windows: <.. Back, /Auto, Win11",
		"oh my boot_ / Windows / Auto: <.. Back, Win11 auto",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("screens:\n%s", strings.Join(got, "\n"))
	}
	if label := screenFor(screens, "install_2002"); label != screens[3].Label || screens[3].Parent != screens[2].Label {
		t.Fatalf("screenFor=%s parent=%s", label, screens[3].Parent)
	}
	var tree []string
	for _, line := range menuTreeLines(screens) {
		tree = append(tree, strings.Repeat("-", line.Depth)+line.Text)
	}
	if strings.Join(tree, ",") != "Linux,-Ubuntu Desktop 26.04.1,Windows,-Auto,--Win11 auto,-Win11,Boot local disk,Diagnostics" {
		t.Fatalf("tree=%v", tree)
	}
}

func TestMenuPreviewShowsTheSelectedItemsScreen(t *testing.T) {
	profile := WindowsProfile{ID: 1, WindowsProfileSource: WindowsProfileSource{Title: "Win11", Folder: "Windows"}, State: "ready", InstallFile: "install.wim"}
	draft := MenuDraft{Title: "oh my boot_", Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{InstallationID: 1001, Title: "Win11", Folder: defaultMenuFolder}}}
	rows := menuDraftRows(draft, []WindowsProfile{profile}, nil, nil, testInstallationSources([]WindowsProfile{profile}, nil, nil))
	preview := buildMenuPreview(draft, []MenuChange{{Kind: "add", Key: "install_1001"}}, rows, nil, []WindowsProfile{profile}, nil, nil, "install_1001", "", testInstallationSources([]WindowsProfile{profile}, nil, nil))
	if preview.Error != "" || preview.Title != "oh my boot_ / Windows" || preview.Item == nil || preview.Position != 1 {
		t.Fatalf("preview=%+v", preview)
	}
	if len(preview.Rows) != 3 || preview.Rows[0].Text != ".. Back" || !preview.Rows[1].Gap || !preview.Rows[2].Selected || preview.Rows[2].Href != "/menu?item=install_1001" {
		t.Fatalf("rows=%+v", preview.Rows)
	}
	var added bool
	for _, line := range preview.Lines {
		if line.Key == "install_1001" {
			added = line.Added && line.Movable && line.Parent != ""
		}
	}
	if !added {
		t.Fatalf("lines=%+v", preview.Lines)
	}
	root := buildMenuPreview(draft, nil, rows, nil, []WindowsProfile{profile}, nil, nil, "", "menu_root", testInstallationSources([]WindowsProfile{profile}, nil, nil))
	if root.Title != "oh my boot_" || root.Rows[0].Text != "Windows" || root.Rows[0].Hint != ">" || root.Rows[0].Href != "/menu?screen=folder_1" || !root.Rows[1].Gap || root.Rows[2].Text != "Boot local disk" || root.Rows[2].Hint != "(10)" {
		t.Fatalf("root=%+v", root.Rows)
	}
}
