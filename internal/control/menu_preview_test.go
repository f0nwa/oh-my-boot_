package control

import (
	"strings"
	"testing"
)

func TestParseMenuScreensReadsFoldersBackAndFixedItems(t *testing.T) {
	profiles := []menuProfile{{id: "ubuntu", title: "Ubuntu Desktop 26.04.1"}, {id: "winiso_1", title: "Win11"}, {id: "winanswer_2", title: "Win11 auto"}}
	script, err := buildMenuTreeWithProfiles("PXE Platform", "Diagnostics", "Ready", profiles, map[string]string{"ubuntu": "Linux", "winiso_1": "Windows", "winanswer_2": "Windows/Auto"})
	if err != nil {
		t.Fatal(err)
	}
	screens := parseMenuScreens(string(script))
	var got []string
	for _, screen := range screens {
		var items []string
		for _, item := range screen.Items {
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
		"PXE Platform: !Diagnostics, /Linux, /Windows, !Boot local disk",
		"PXE Platform / Linux: <.. Back, Ubuntu Desktop 26.04.1",
		"PXE Platform / Windows: <.. Back, /Auto, Win11",
		"PXE Platform / Windows / Auto: <.. Back, Win11 auto",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("screens:\n%s", strings.Join(got, "\n"))
	}
	if label := screenFor(screens, "winanswer_2"); label != screens[3].Label || screens[3].Parent != screens[2].Label {
		t.Fatalf("screenFor=%s parent=%s", label, screens[3].Parent)
	}
	var tree []string
	for _, line := range menuTreeLines(screens) {
		tree = append(tree, strings.Repeat("-", line.Depth)+line.Text)
	}
	if strings.Join(tree, ",") != "Diagnostics,Linux,-Ubuntu Desktop 26.04.1,Windows,-Auto,--Win11 auto,-Win11,Boot local disk" {
		t.Fatalf("tree=%v", tree)
	}
}

func TestMenuPreviewShowsTheSelectedItemsScreen(t *testing.T) {
	profile := WindowsProfile{ID: 1, WindowsProfileSource: WindowsProfileSource{Title: "Win11", Folder: "Windows"}, State: "ready", InstallFile: "install.wim"}
	draft := MenuDraft{Title: "PXE Platform", Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{Key: "winiso_1", Title: "Win11", Folder: defaultMenuFolder}}}
	rows := menuDraftRows(draft, []WindowsProfile{profile}, nil, nil)
	preview := buildMenuPreview(draft, []MenuChange{{Kind: "add", Key: "winiso_1"}}, rows, nil, []WindowsProfile{profile}, nil, nil, "winiso_1", "")
	if preview.Error != "" || preview.Title != "PXE Platform / Windows" || preview.Item == nil || preview.Position != 1 {
		t.Fatalf("preview=%+v", preview)
	}
	if len(preview.Rows) != 2 || preview.Rows[0].Text != ".. Back" || !preview.Rows[1].Selected || preview.Rows[1].Href != "/menu?item=winiso_1" {
		t.Fatalf("rows=%+v", preview.Rows)
	}
	var added bool
	for _, line := range preview.Lines {
		if line.Key == "winiso_1" {
			added = line.Added && line.Movable && line.Parent != ""
		}
	}
	if !added {
		t.Fatalf("lines=%+v", preview.Lines)
	}
	root := buildMenuPreview(draft, nil, rows, nil, []WindowsProfile{profile}, nil, nil, "", "menu_root")
	if root.Title != "PXE Platform" || root.Rows[1].Text != "Windows >" || root.Rows[1].Href != "/menu?screen=folder_1" {
		t.Fatalf("root=%+v", root.Rows)
	}
}
