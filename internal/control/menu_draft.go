package control

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// MenuDraftItem places an installation in the menu with its own title/folder.
// Key is a temporary bridge for callers converted by the following tasks.
type MenuDraftItem struct {
	InstallationID int64
	Key            string
	Folder         string
	Title          string
}

// MenuDraft is the boot menu being prepared; Items are in menu order.
type MenuDraft struct {
	Title, Label, Message string
	Ubuntu                bool
	UbuntuFolder          string
	// NoDiagnostics omits the built-in diagnostics item from the client menu.
	NoDiagnostics bool
	// LocalTimeout is how many seconds the main screen waits before booting
	// the local disk; 0 means the default. NoLocalTimer makes the menu wait
	// for a choice instead.
	LocalTimeout int
	NoLocalTimer bool
	Items        []MenuDraftItem
	// look names the menu design a published version was built with ("" for
	// versions before the «Пульт» look); a draft always gets the current one.
	look string
}

// defaultLocalTimeout is the wait before booting the local disk, in seconds,
// for drafts and versions that do not set one.
const defaultLocalTimeout = 10

// maxLocalTimeout is the longest wait the settings accept, in seconds.
const maxLocalTimeout = 3600

// LocalTimerOn reports whether the main screen boots the local disk by itself.
func (d MenuDraft) LocalTimerOn() bool { return !d.NoLocalTimer }

// DiagnosticsOn reports whether the built-in diagnostics item is shown.
func (d MenuDraft) DiagnosticsOn() bool { return !d.NoDiagnostics }

// LocalTimeoutSeconds is the wait before booting the local disk; with the
// timer off it is the value the settings form offers when it is turned on.
func (d MenuDraft) LocalTimeoutSeconds() int {
	if d.LocalTimeout <= 0 {
		return defaultLocalTimeout
	}
	return d.LocalTimeout
}

// localTimer is the wait the menu script gets: 0 means no timer.
func (d MenuDraft) localTimer() int {
	if d.NoLocalTimer {
		return 0
	}
	return d.LocalTimeoutSeconds()
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

// InstallationMenuSources supplies the shared profiles and installation identities.
// The optional renderer argument is a development bridge for legacy callers.
type InstallationMenuSources struct {
	Installations []Installation
	BootProfiles  []BootProfile
}

func menuProfileForInstallation(id int64, base string, sources InstallationMenuSources, profiles []WindowsProfile, answers []WindowsAnswerVariant, configs []DriverConfig) (menuProfile, string, error) {
	key := fmt.Sprintf("install_%d", id)
	for _, installation := range sources.Installations {
		if installation.ID != id || id <= 0 {
			continue
		}
		for _, profile := range sources.BootProfiles {
			if profile.ID != installation.BootProfileID {
				continue
			}
			if err := ValidateInstallation(installation, profile, answers, configs); err != nil {
				return menuProfile{}, "", err
			}
			var entry menuProfile
			switch profile.Kind {
			case BootProfileUbuntuBuiltin:
				entry = menuProfile{id: key, kind: profile.Kind, base: base}
			case BootProfileWindowsISO:
				sourceKey := fmt.Sprintf("winiso_%d", profile.WindowsProfileID)
				if installation.AnswerID > 0 {
					sourceKey = fmt.Sprintf("winanswer_%d", installation.AnswerID)
				}
				if installation.DriverConfigID > 0 {
					sourceKey = fmt.Sprintf("windriver_%d", installation.DriverConfigID)
				}
				var err error
				entry, _, err = menuProfileForKey(sourceKey, base, profiles, answers, configs)
				if err != nil {
					return menuProfile{}, "", err
				}
				entry.id, entry.kind = key, profile.Kind
			default:
				return menuProfile{}, "", fmt.Errorf("unsupported boot profile kind %q", profile.Kind)
			}
			entry.title = installation.Title
			return entry, installation.Folder, nil
		}
		return menuProfile{}, "", fmt.Errorf("boot profile for %s is unavailable", key)
	}
	return menuProfile{}, "", unknownMenuItemError(key)
}

func resolveDraftItem(item MenuDraftItem, base string, sources []InstallationMenuSources, profiles []WindowsProfile, answers []WindowsAnswerVariant, configs []DriverConfig) (menuProfile, string, error) {
	if item.InstallationID > 0 {
		var source InstallationMenuSources
		if len(sources) > 0 {
			source = sources[0]
		}
		return menuProfileForInstallation(item.InstallationID, base, source, profiles, answers, configs)
	}
	return menuProfileForKey(item.Key, base, profiles, answers, configs)
}

// BuildMenuFromDraft renders installations in draft order with their placements.
func BuildMenuFromDraft(d MenuDraft, baseURL string, profiles []WindowsProfile, answers []WindowsAnswerVariant, configs []DriverConfig, sources ...InstallationMenuSources) ([]byte, error) {
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
		return buildMenuTreeTimed(productMenuTitle, d.Label, d.Message, ubuntuBase, folders, d.localTimer(), d.DiagnosticsOn())
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
		key := menuDraftItemKey(item)
		if seen[key] {
			return nil, fmt.Errorf("menu item %s is listed twice", key)
		}
		seen[key] = true
		entry, folder, err := resolveDraftItem(item, base, sources, profiles, answers, configs)
		if err != nil {
			return nil, err
		}
		if item.Title != "" {
			if !safeMenuText(item.Title, 80) || !isASCII(item.Title) {
				return nil, fmt.Errorf("menu title for %s must be ASCII", key)
			}
			entry.title = item.Title
		}
		if item.Folder != defaultMenuFolder {
			folder = item.Folder
		}
		// Profile folders historically allowed any safe text; answer and driver
		// folders must be ASCII.
		if !strings.HasPrefix(item.Key, "winiso_") && !isASCII(folder) {
			return nil, fmt.Errorf("menu folder for %s must be ASCII", key)
		}
		entries = append(entries, entry)
		folders[key] = folder
	}
	for _, folder := range folders {
		if err := validateMenuFolder(folder); err != nil {
			return nil, err
		}
	}
	return buildMenuTreeWithProfilesTimed(productMenuTitle, d.Label, d.Message, entries, folders, d.localTimer(), d.DiagnosticsOn())
}

var menuItemKey = regexp.MustCompile(`^(install|winiso|winanswer|windriver)_[0-9]+$`)

// draftFromVersion restores saved placements/settings without parsing the script.
// The nil-Items parser is a temporary bridge for unconverted callers.
func draftFromVersion(v MenuVersion) MenuDraft {
	if v.Items != nil {
		d := MenuDraft{Title: v.Title, Label: v.Label, Message: v.Message, NoDiagnostics: v.NoDiagnostics, LocalTimeout: v.LocalTimeout, NoLocalTimer: v.NoLocalTimer, look: v.Look}
		items := append([]MenuVersionItem(nil), v.Items...)
		sort.SliceStable(items, func(i, j int) bool { return items[i].Position < items[j].Position })
		d.Items = make([]MenuDraftItem, 0, len(items))
		for _, item := range items {
			d.Items = append(d.Items, MenuDraftItem{InstallationID: item.InstallationID, Title: item.Title, Folder: item.Folder})
		}
		return d
	}
	// Temporary bridge for renderer/HTTP callers that do not yet supply Items.
	d := MenuDraft{Title: v.Title, Label: v.Label, Message: v.Message, NoDiagnostics: v.Script != "", look: scriptLook(v.Script)}
	if seconds := menuLocalTimer(v.Script); seconds == 0 {
		d.NoLocalTimer = true
	} else {
		d.LocalTimeout = seconds
	}
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
		if fields[1] == "diagnostics" {
			d.NoDiagnostics = false
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
// Label names the changed setting, while Before and After show its values.
type MenuChange struct {
	Kind, Key, Title string
	Label            string
	Before, After    string
}

func diffMenuDraft(active, draft MenuDraft) []MenuChange {
	var changes []MenuChange
	if active.look != draft.look {
		changes = append(changes, MenuChange{Kind: "look", Label: "Оформление меню", Before: "Прежнее", After: "Текущее"})
	}
	if active.Title != draft.Title {
		changes = append(changes, MenuChange{Kind: "settings", Label: "Заголовок меню", Before: active.Title, After: draft.Title})
	}
	if active.NoDiagnostics != draft.NoDiagnostics {
		changes = append(changes, MenuChange{Kind: "settings", Label: "Пункт Diagnostics", Before: menuVisibility(active.DiagnosticsOn()), After: menuVisibility(draft.DiagnosticsOn())})
	}
	if active.Label != draft.Label {
		changes = append(changes, MenuChange{Kind: "settings", Label: "Название Diagnostics", Before: active.Label, After: draft.Label})
	}
	if active.Message != draft.Message {
		changes = append(changes, MenuChange{Kind: "settings", Label: "Сообщение Diagnostics", Before: active.Message, After: draft.Message})
	}
	if active.Ubuntu != draft.Ubuntu {
		changes = append(changes, MenuChange{Kind: "settings", Label: "Пункт Ubuntu Desktop", Before: menuVisibility(active.Ubuntu), After: menuVisibility(draft.Ubuntu)})
	}
	if active.UbuntuFolder != draft.UbuntuFolder {
		changes = append(changes, MenuChange{Kind: "settings", Label: "Каталог Ubuntu Desktop", Before: menuFolderName(active.UbuntuFolder), After: menuFolderName(draft.UbuntuFolder)})
	}
	if active.localTimer() != draft.localTimer() {
		changes = append(changes, MenuChange{Kind: "settings", Label: "Автозагрузка с диска", Before: menuTimerName(active.localTimer()), After: menuTimerName(draft.localTimer())})
	}
	activeByKey := map[string]MenuDraftItem{}
	for _, item := range active.Items {
		activeByKey[menuDraftItemKey(item)] = item
	}
	draftByKey := map[string]MenuDraftItem{}
	for _, item := range draft.Items {
		draftByKey[menuDraftItemKey(item)] = item
		if _, ok := activeByKey[menuDraftItemKey(item)]; !ok {
			changes = append(changes, MenuChange{Kind: "add", Key: menuDraftItemKey(item), Title: item.Title})
		}
	}
	for _, item := range active.Items {
		if _, ok := draftByKey[menuDraftItemKey(item)]; !ok {
			changes = append(changes, MenuChange{Kind: "remove", Key: menuDraftItemKey(item), Title: item.Title})
		}
	}
	var activeOrder, draftOrder []string
	for _, item := range draft.Items {
		old, ok := activeByKey[menuDraftItemKey(item)]
		if !ok {
			continue
		}
		draftOrder = append(draftOrder, menuDraftItemKey(item))
		if old.Title != item.Title {
			changes = append(changes, MenuChange{Kind: "change", Key: menuDraftItemKey(item), Title: menuItemName(item), Label: "Название пункта", Before: menuTitleName(old.Title), After: menuTitleName(item.Title)})
		}
		if old.Folder != item.Folder {
			changes = append(changes, MenuChange{Kind: "change", Key: menuDraftItemKey(item), Title: menuItemName(item), Label: "Каталог пункта", Before: menuFolderName(old.Folder), After: menuFolderName(item.Folder)})
		}
	}
	for _, item := range active.Items {
		if _, ok := draftByKey[menuDraftItemKey(item)]; ok {
			activeOrder = append(activeOrder, menuDraftItemKey(item))
		}
	}
	if strings.Join(activeOrder, ",") != strings.Join(draftOrder, ",") {
		changes = append(changes, MenuChange{Kind: "order", Label: "Порядок пунктов", Before: menuOrderNames(activeOrder, activeByKey), After: menuOrderNames(draftOrder, draftByKey)})
	}
	return changes
}

func menuVisibility(on bool) string {
	if on {
		return "Показан"
	}
	return "Скрыт"
}

func menuTimerName(seconds int) string {
	if seconds == 0 {
		return "Выключена"
	}
	return fmt.Sprintf("%d с", seconds)
}

func menuTitleName(title string) string {
	if title == "" {
		return "По умолчанию"
	}
	return title
}

func menuFolderName(folder string) string {
	switch folder {
	case "":
		return "Главное меню"
	case defaultMenuFolder:
		return "Каталог установки"
	}
	return folder
}

// menuDraftItemKey keeps identity separate from editable titles and folders.
func menuDraftItemKey(item MenuDraftItem) string {
	if item.InstallationID > 0 {
		return "install_" + strconv.FormatInt(item.InstallationID, 10)
	}
	return item.Key
}

func menuItemName(item MenuDraftItem) string {
	if item.Title != "" {
		return item.Title
	}
	return menuDraftItemKey(item)
}

func menuOrderNames(keys []string, items map[string]MenuDraftItem) string {
	names := make([]string, 0, len(keys))
	for _, key := range keys {
		names = append(names, menuItemName(items[key]))
	}
	return strings.Join(names, " → ")
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
