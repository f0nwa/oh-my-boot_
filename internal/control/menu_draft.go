package control

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// MenuDraftItem is one installation placed in the boot menu. Key names the
// underlying object: winiso_N (profile), winanswer_N (profile + answer file)
// or windriver_N (driver configuration). An empty Title uses the object's.
type MenuDraftItem struct {
	Key    string
	Folder string
	Title  string
}

// MenuDraft is the boot menu being prepared; Items are in menu order.
type MenuDraft struct {
	Title, Label, Message string
	Ubuntu                bool
	UbuntuFolder          string
	Items                 []MenuDraftItem
	// look names the menu design a published version was built with ("" for
	// versions before the «Пульт» look); a draft always gets the current one.
	look string
}

// menuLook is the design buildMenuTreeWithProfiles draws; the script names
// it in a comment line. A new value makes every published menu show a "new
// look" change until it is published again.
const menuLook = "pult-4"

const menuLookMarker = "# pxe-platform:look "

// scriptLook tells which design a published script has. Versions from before
// the marker are told apart by their commands.
func scriptLook(script string) string {
	for _, line := range strings.Split(script, "\n") {
		if look, ok := strings.CutPrefix(line, menuLookMarker); ok {
			return look
		}
	}
	switch {
	case strings.Contains(script, "console --x 640 --y 480"):
		return "pult-2"
	case strings.Contains(script, "cpair --foreground 7 --background 3 2"):
		return "pult-1"
	}
	return ""
}

// defaultMenuFolder marks an item whose folder should come from its object.
const defaultMenuFolder = "\x00default"

func unknownMenuItemError(key string) error {
	return fmt.Errorf("unknown menu item %s", key)
}

func profileBootable(p WindowsProfile) bool {
	return p.State == "ready" && (p.InstallFile == "install.wim" || p.InstallFile == "install.esd")
}

// menuProfileForKey checks that the object behind key can boot and returns its
// menu entry and default folder.
func menuProfileForKey(key, base string, profiles []WindowsProfile, answers []WindowsAnswerVariant, configs []DriverConfig) (menuProfile, string, error) {
	prefix, rawID, ok := strings.Cut(key, "_")
	id, err := strconv.ParseInt(rawID, 10, 64)
	if !ok || err != nil || id <= 0 {
		return menuProfile{}, "", unknownMenuItemError(key)
	}
	byID := map[int64]WindowsProfile{}
	for _, p := range profiles {
		byID[p.ID] = p
	}
	switch prefix {
	case "winiso":
		p, ok := byID[id]
		if !ok {
			return menuProfile{}, "", unknownMenuItemError(key)
		}
		if !profileBootable(p) || !safeMenuText(p.Title, 80) || !isASCII(p.Title) {
			return menuProfile{}, "", fmt.Errorf("Windows profile %d is not ready for PXE", p.ID)
		}
		return menuProfile{id: key, title: p.Title, base: base, windowsISOID: p.ID}, p.Folder, nil
	case "winanswer":
		for _, answer := range answers {
			if answer.ID != id {
				continue
			}
			parent, ok := byID[answer.ProfileID]
			if !ok || !profileBootable(parent) || answer.State != "ready" || answer.Size <= 0 || !safeMenuText(answer.Title, 80) || !isASCII(answer.Title) || !validAnswerHash(answer.SHA256) {
				return menuProfile{}, "", fmt.Errorf("Windows answer %d is not ready for PXE", answer.ID)
			}
			hint := "auto install"
			if answer.WipesDisk != nil && *answer.WipesDisk {
				hint = "!! WIPES DISK"
			}
			return menuProfile{id: key, title: answer.Title, hint: hint, base: base, windowsISOID: answer.ProfileID, answerID: answer.ID, answerSHA256: answer.SHA256}, answer.Folder, nil
		}
	case "windriver":
		for _, config := range configs {
			if config.ID != id {
				continue
			}
			parent, ok := byID[config.ProfileID]
			if !ok || !profileBootable(parent) || config.State != "ready" || len(config.PackageINFs) == 0 || !safeMenuText(config.Title, 80) || !isASCII(config.Title) || !validAnswerHash(config.Build.WIMKey) || !validAnswerHash(config.Build.WIMSHA256) || !validAnswerHash(config.Build.ScriptSHA256) {
				return menuProfile{}, "", fmt.Errorf("WinPE driver configuration %d is not ready", config.ID)
			}
			answerHash := ""
			hint := driverSummary(config)
			if config.AnswerID > 0 {
				found := false
				for _, answer := range answers {
					if answer.ID == config.AnswerID && answer.ProfileID == config.ProfileID && answer.State == "ready" && answer.Size > 0 && validAnswerHash(answer.SHA256) {
						answerHash, found = answer.SHA256, true
						if answer.WipesDisk != nil && *answer.WipesDisk {
							hint = "!! WIPES DISK"
						}
						break
					}
				}
				if !found {
					return menuProfile{}, "", fmt.Errorf("WinPE driver configuration answer is unavailable")
				}
			}
			return menuProfile{id: key, title: config.Title, hint: hint, base: base, windowsISOID: config.ProfileID, driverConfigID: config.ID, driverAnswerID: config.AnswerID, driverAnswerSHA256: answerHash, driverWIMKey: config.Build.WIMKey, driverWIMSHA256: config.Build.WIMSHA256, driverScriptSHA256: config.Build.ScriptSHA256}, config.Folder, nil
		}
	}
	return menuProfile{}, "", unknownMenuItemError(key)
}

// BuildMenuFromDraft renders the iPXE script for a draft: Ubuntu first when
// enabled, then the draft's items in order, with their titles and folders.
func BuildMenuFromDraft(d MenuDraft, baseURL string, profiles []WindowsProfile, answers []WindowsAnswerVariant, configs []DriverConfig) ([]byte, error) {
	if len(d.Items) == 0 {
		// Even an empty menu is drawn with today's look, so that publishing it
		// clears the "new look" change.
		ubuntuBase, folders := "", map[string]string{}
		if d.Ubuntu {
			base, err := validatedBootURL(baseURL)
			if err != nil {
				return nil, err
			}
			if err := validateMenuFolder(d.UbuntuFolder); err != nil {
				return nil, err
			}
			ubuntuBase, folders["ubuntu"] = base, d.UbuntuFolder
		}
		return buildMenuTree(productMenuTitle, d.Label, d.Message, ubuntuBase, folders)
	}
	base, err := validatedBootURL(baseURL)
	if err != nil {
		return nil, err
	}
	var entries []menuProfile
	folders := map[string]string{}
	if d.Ubuntu {
		entries = append(entries, menuProfile{id: "ubuntu", title: "Ubuntu Desktop 26.04.1", base: base})
		folders["ubuntu"] = d.UbuntuFolder
	}
	seen := map[string]bool{}
	for _, item := range d.Items {
		if seen[item.Key] {
			return nil, fmt.Errorf("menu item %s is listed twice", item.Key)
		}
		seen[item.Key] = true
		entry, folder, err := menuProfileForKey(item.Key, base, profiles, answers, configs)
		if err != nil {
			return nil, err
		}
		if item.Title != "" {
			if !safeMenuText(item.Title, 80) || !isASCII(item.Title) {
				return nil, fmt.Errorf("menu title for %s must be ASCII", item.Key)
			}
			entry.title = item.Title
		}
		if item.Folder != defaultMenuFolder {
			folder = item.Folder
		}
		// Profile folders historically allowed any safe text; answer and driver
		// folders must be ASCII.
		if !strings.HasPrefix(item.Key, "winiso_") && !isASCII(folder) {
			return nil, fmt.Errorf("menu folder for %s must be ASCII", item.Key)
		}
		entries = append(entries, entry)
		folders[item.Key] = folder
	}
	for _, folder := range folders {
		if err := validateMenuFolder(folder); err != nil {
			return nil, err
		}
	}
	return buildMenuTreeWithProfiles(productMenuTitle, d.Label, d.Message, entries, folders)
}

var menuItemKey = regexp.MustCompile(`^win(iso|answer|driver)_[0-9]+$`)

// draftFromVersion reads a published version back into a draft: its texts,
// Ubuntu entry, and the Windows items in the order they appear.
func draftFromVersion(v MenuVersion) MenuDraft {
	d := MenuDraft{Title: v.Title, Label: v.Label, Message: v.Message, look: scriptLook(v.Script)}
	folders := menuFolderPaths(v.Script)
	for _, line := range strings.Split(v.Script, "\n") {
		fields := strings.SplitN(line, " ", 3)
		if len(fields) < 2 || fields[0] != "item" {
			continue
		}
		if fields[1] == "ubuntu" {
			d.Ubuntu, d.UbuntuFolder = true, folders["ubuntu"]
			continue
		}
		if !menuItemKey.MatchString(fields[1]) {
			continue
		}
		title := ""
		if len(fields) == 3 {
			title, _ = splitMenuItemText(fields[2])
		}
		d.Items = append(d.Items, MenuDraftItem{Key: fields[1], Folder: folders[fields[1]], Title: title})
	}
	return d
}

// MenuChange is one difference between the published menu and the draft.
// Kind is add, remove, change (title or folder), order or settings.
type MenuChange struct {
	Kind  string
	Key   string
	Title string
}

func diffMenuDraft(active, draft MenuDraft) []MenuChange {
	var changes []MenuChange
	if active.look != draft.look {
		changes = append(changes, MenuChange{Kind: "look"})
	}
	if active.Title != draft.Title || active.Label != draft.Label || active.Message != draft.Message || active.Ubuntu != draft.Ubuntu || active.UbuntuFolder != draft.UbuntuFolder {
		changes = append(changes, MenuChange{Kind: "settings"})
	}
	activeByKey := map[string]MenuDraftItem{}
	for _, item := range active.Items {
		activeByKey[item.Key] = item
	}
	draftByKey := map[string]MenuDraftItem{}
	for _, item := range draft.Items {
		draftByKey[item.Key] = item
		if _, ok := activeByKey[item.Key]; !ok {
			changes = append(changes, MenuChange{Kind: "add", Key: item.Key, Title: item.Title})
		}
	}
	for _, item := range active.Items {
		if _, ok := draftByKey[item.Key]; !ok {
			changes = append(changes, MenuChange{Kind: "remove", Key: item.Key, Title: item.Title})
		}
	}
	var activeOrder, draftOrder []string
	for _, item := range draft.Items {
		old, ok := activeByKey[item.Key]
		if !ok {
			continue
		}
		draftOrder = append(draftOrder, item.Key)
		if old.Title != item.Title || old.Folder != item.Folder {
			changes = append(changes, MenuChange{Kind: "change", Key: item.Key, Title: item.Title})
		}
	}
	for _, item := range active.Items {
		if _, ok := draftByKey[item.Key]; ok {
			activeOrder = append(activeOrder, item.Key)
		}
	}
	if strings.Join(activeOrder, ",") != strings.Join(draftOrder, ",") {
		changes = append(changes, MenuChange{Kind: "order"})
	}
	return changes
}

// currentDraft returns the stored draft and the active version read as a
// draft. Without a stored draft the active version is the draft, so nothing
// shows as pending after an upgrade.
func (s *Server) currentDraft(ctx context.Context) (MenuDraft, MenuDraft, error) {
	active := MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: defaultMenuMessage}
	versions, err := s.Store.MenuVersions(ctx)
	if err != nil {
		return MenuDraft{}, MenuDraft{}, err
	}
	for _, version := range versions {
		if version.Active {
			active = draftFromVersion(version)
			break
		}
	}
	draft, ok, err := s.Store.MenuDraft(ctx)
	if err != nil {
		return MenuDraft{}, MenuDraft{}, err
	}
	if !ok {
		draft = active
		draft.Items = append([]MenuDraftItem(nil), active.Items...)
	}
	// Publishing builds the draft with today's design and the product name,
	// so a version drawn with an older one has something to publish.
	draft.look = menuLook
	draft.Title = productMenuTitle
	if draft.Message == legacyMenuMessage {
		draft.Message = defaultMenuMessage
	}
	return draft, active, nil
}

// draftSummary is what the header's draft bar shows on every page: the
// version clients see and how many items the unpublished draft adds,
// removes or otherwise changes.
type draftSummary struct {
	Active             int64
	Add, Remove, Other int
}

// Pending reports whether the draft differs from the active menu.
func (d draftSummary) Pending() bool { return d.Add+d.Remove+d.Other > 0 }

// draftStatus summarizes the menu draft for the header. It is best effort:
// any failure, including a store that does not provide menus, hides the bar
// instead of breaking the page.
func (s *Server) draftStatus(ctx context.Context) (summary draftSummary) {
	defer func() {
		if recover() != nil {
			summary = draftSummary{}
		}
	}()
	if s.Store == nil {
		return draftSummary{}
	}
	versions, err := s.Store.MenuVersions(ctx)
	if err != nil {
		return draftSummary{}
	}
	for _, version := range versions {
		if version.Active {
			summary.Active = version.ID
		}
	}
	draft, active, err := s.currentDraft(ctx)
	if err != nil {
		return draftSummary{}
	}
	counted := summarizeChanges(diffMenuDraft(active, draft))
	counted.Active = summary.Active
	return counted
}

// summarizeChanges counts draft changes for the draft bar.
func summarizeChanges(changes []MenuChange) draftSummary {
	var summary draftSummary
	for _, change := range changes {
		switch change.Kind {
		case "add":
			summary.Add++
		case "remove":
			summary.Remove++
		default:
			summary.Other++
		}
	}
	return summary
}
