package control

import (
	"strings"
)

// menuScreen is one screen of the iPXE menu: the root or a folder.
type menuScreen struct {
	Label, Title string
	Parent       string
	Items        []menuScreenItem
}

// menuScreenItem is one line of a screen. Target is the iPXE label it jumps
// to: a menu item key, a folder label, a parent label, diagnostics or local.
type menuScreenItem struct {
	Target, Text string
	Folder       bool // opens another screen
	Back         bool // returns to the parent screen
	Fixed        bool // diagnostics or local disk, not a menu item of the draft
	Gap          bool // a separator line: blank, a heading or the key line
	Hint         string
}

// parseMenuScreens reads the screens of a script built by
// buildMenuTreeWithProfiles, in the order they appear.
func parseMenuScreens(script string) []menuScreen {
	var screens []menuScreen
	labels := map[string]bool{}
	var current *menuScreen
	lines := strings.Split(script, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "menu ") {
			// A flat menu without folders has no label before its menu line.
			label := "menu_root"
			if i > 0 && strings.HasPrefix(lines[i-1], ":") {
				label = strings.TrimPrefix(lines[i-1], ":")
			}
			screens = append(screens, menuScreen{Label: label, Title: strings.TrimSuffix(strings.TrimPrefix(line, "menu "), menuTitleSuffix)})
			labels[label] = true
			current = &screens[len(screens)-1]
			continue
		}
		if current == nil {
			continue
		}
		if strings.HasPrefix(line, "choose ") {
			current = nil
			continue
		}
		if !strings.HasPrefix(line, "item ") {
			continue
		}
		if rest := strings.TrimPrefix(line, "item "); rest == "--gap" || strings.HasPrefix(rest, "--gap ") {
			current.Items = append(current.Items, menuScreenItem{Gap: true, Text: strings.TrimSpace(strings.TrimPrefix(rest, "--gap"))})
			continue
		}
		fields := strings.SplitN(strings.TrimPrefix(line, "item "), " ", 2)
		if len(fields) != 2 {
			continue
		}
		text, hint := splitMenuItemText(fields[1])
		current.Items = append(current.Items, menuScreenItem{Target: fields[0], Text: text, Hint: hint})
	}
	for i := range screens {
		for j := range screens[i].Items {
			item := &screens[i].Items[j]
			switch {
			case item.Gap:
			case item.Text == ".. Back":
				item.Back, screens[i].Parent = true, item.Target
			case item.Target == "diagnostics" || item.Target == "local":
				item.Fixed = true
			case labels[item.Target]:
				item.Folder = true
			}
		}
	}
	return screens
}

// screenFor returns the screen that holds a menu item key, or the root.
func screenFor(screens []menuScreen, key string) string {
	for _, screen := range screens {
		for _, item := range screen.Items {
			if item.Target == key && !item.Folder && !item.Back && !item.Gap {
				return screen.Label
			}
		}
	}
	if len(screens) > 0 {
		return screens[0].Label
	}
	return ""
}

// menuTreeLine is one line of the structure column: a folder or an item,
// indented by depth, in menu order.
type menuTreeLine struct {
	Depth         int
	Label, Text   string
	Folder, Fixed bool
	Key           string
	Parent        string
}

// menuTreeLines walks the screens from the root, depth first.
func menuTreeLines(screens []menuScreen) []menuTreeLine {
	byLabel := map[string]menuScreen{}
	for _, screen := range screens {
		byLabel[screen.Label] = screen
	}
	var lines []menuTreeLine
	var walk func(label string, depth int)
	walk = func(label string, depth int) {
		for _, item := range byLabel[label].Items {
			switch {
			case item.Back, item.Gap:
			case item.Folder:
				lines = append(lines, menuTreeLine{Depth: depth, Label: item.Target, Text: item.Text, Folder: true, Parent: label})
				walk(item.Target, depth+1)
			default:
				lines = append(lines, menuTreeLine{Depth: depth, Text: item.Text, Fixed: item.Fixed, Key: item.Target, Parent: label})
			}
		}
	}
	if len(screens) > 0 {
		walk(screens[0].Label, 0)
	}
	return lines
}

// previewRow is one line of the screen preview.
type previewRow struct {
	Text, Href, Hint string
	Selected, Danger bool
	Gap              bool
}

// previewLine is one line of the structure column.
type previewLine struct {
	Text, Key, Href, Pad, Mark string
	// Parent is the screen the line sits on; items move only among siblings.
	Parent                  string
	Folder, Fixed           bool
	Selected, Added, Broken bool
	// Off marks "Boot local disk" when the main screen does not boot it by itself.
	Off bool
	// ShowKey adds the item key when two lines would read the same.
	ShowKey bool
	// Movable lines are draft items that can be dragged among siblings.
	Movable bool
}

// menuPreview is what the menu page shows around the screen preview.
type menuPreview struct {
	Title    string
	Rows     []previewRow
	Lines    []previewLine
	Removed  []string
	Error    string
	Key      string
	Item     *menuDraftRow
	Install  *installationRow
	Builtin  string
	Position int
	Count    int
}

const previewBaseURL = "http://preview.invalid"

// buildMenuPreview renders the draft the way iPXE will show it: the screen
// that holds the selected item (or the one asked for), the whole structure,
// and the selected item's details.
func buildMenuPreview(draft MenuDraft, changes []MenuChange, rows []menuDraftRow, installs []installationRow, profiles []WindowsProfile, answers []WindowsAnswerVariant, configs []DriverConfig, key, screenLabel string) *menuPreview {
	preview := &menuPreview{Key: key, Count: len(draft.Items)}
	for i := range rows {
		if rows[i].Item.Key == key {
			preview.Item, preview.Position = &rows[i], i+1
		}
	}
	installByKey := map[string]installationRow{}
	for _, row := range installs {
		installByKey[row.Key] = row
	}
	if install, ok := installByKey[key]; ok {
		preview.Install = &install
	}
	switch key {
	case "ubuntu":
		preview.Builtin = "Ubuntu Desktop — встроенный пункт. Он включается и переносится в другую папку в «Настройках меню»."
	case "diagnostics":
		preview.Builtin = "Диагностический пункт показывает сообщение и загружает машину с локального диска. Его подпись — в «Настройках меню»."
	case "local":
		preview.Builtin = "Загрузка с локального диска — всегда последний пункт главного экрана. Здесь задаётся, загружается ли он сам, если ничего не выбрано."
	}
	added := map[string]bool{}
	for _, change := range changes {
		switch change.Kind {
		case "add":
			added[change.Key] = true
		case "remove":
			title := change.Title
			if title == "" {
				title = change.Key
			}
			preview.Removed = append(preview.Removed, title)
		}
	}
	script, err := BuildMenuFromDraft(draft, previewBaseURL, profiles, answers, configs)
	var screens []menuScreen
	if err == nil {
		screens = parseMenuScreens(string(script))
	}
	if err != nil || len(screens) == 0 {
		// Show the draft items as a flat list so the broken one can be found
		// and removed.
		preview.Error = "Не удалось разобрать меню"
		if err != nil {
			preview.Error = err.Error()
		}
		for _, row := range rows {
			preview.Lines = append(preview.Lines, previewLine{Text: row.Title, Key: row.Item.Key, Href: "/menu?item=" + row.Item.Key, Mark: "·", Broken: !row.Available, Added: added[row.Item.Key], Selected: row.Item.Key == key, ShowKey: true})
		}
		return preview
	}
	if screenLabel == "" {
		screenLabel = screenFor(screens, key)
	}
	screen := screens[0]
	for _, candidate := range screens {
		if candidate.Label == screenLabel {
			screen = candidate
		}
	}
	preview.Title = screen.Title
	for _, item := range screen.Items {
		if item.Gap {
			// Blank lines that only push the key line down stay out of the
			// preview; the key line is its footer.
			if item.Text != "" && item.Text != menuFooter {
				preview.Rows = append(preview.Rows, previewRow{Text: item.Text, Gap: true})
			} else if item.Text == "" && len(preview.Rows) > 0 && !preview.Rows[len(preview.Rows)-1].Gap {
				preview.Rows = append(preview.Rows, previewRow{Gap: true})
			}
			continue
		}
		row := previewRow{Text: item.Text, Href: "/menu?item=" + item.Target, Hint: item.Hint}
		switch {
		case item.Folder:
			row.Href = "/menu?screen=" + item.Target
		case item.Back:
			row.Href = "/menu?screen=" + item.Target
		case item.Target == "local" && screen.Label == screens[0].Label:
			row.Hint = localDiskHint(string(script))
		case !item.Fixed:
			row.Danger = item.Hint == "!! WIPES DISK"
		}
		row.Selected = key != "" && item.Target == key
		preview.Rows = append(preview.Rows, row)
	}
	// A trailing blank line before the footer adds nothing.
	for len(preview.Rows) > 0 && preview.Rows[len(preview.Rows)-1].Gap && preview.Rows[len(preview.Rows)-1].Text == "" {
		preview.Rows = preview.Rows[:len(preview.Rows)-1]
	}
	treeLines := menuTreeLines(screens)
	titles := map[string]int{}
	for _, line := range treeLines {
		titles[line.Text]++
	}
	broken := map[string]bool{}
	for _, row := range rows {
		broken[row.Item.Key] = !row.Available
	}
	for _, line := range treeLines {
		out := previewLine{Text: line.Text, Key: line.Key, Parent: line.Parent, Pad: strings.Repeat("  ", line.Depth), Folder: line.Folder, Fixed: line.Fixed, Added: added[line.Key], Broken: broken[line.Key], Selected: key != "" && line.Key == key, ShowKey: !line.Folder && titles[line.Text] > 1}
		out.Movable = menuItemKey.MatchString(line.Key)
		out.Off = line.Key == "local" && !draft.LocalTimerOn()
		if line.Folder {
			out.Href, out.Mark = "/menu?screen="+line.Label, "▾"
		} else {
			out.Href, out.Mark = "/menu?item="+line.Key, "·"
		}
		preview.Lines = append(preview.Lines, out)
	}
	return preview
}
