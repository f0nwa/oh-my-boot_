package control

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const menuFolderMarker = "# pxe-platform:folders-v1 "
const menuWindowsMarker = "# pxe-platform:winiso-v1 "
const menuAnswerMarker = "# pxe-platform:winanswers-v1 "
const menuDriverMarker = "# pxe-platform:windrivers-v1 "

type menuProfile struct {
	kind               string
	id                 string
	title              string
	hint               string // shown at the right edge of the item
	base               string
	windowsISOID       int64
	answerID           int64
	answerSHA256       string
	driverConfigID     int64
	driverAnswerID     int64
	driverAnswerSHA256 string
	driverWIMKey       string
	driverWIMSHA256    string
	driverScriptSHA256 string
}

type menuTreeNode struct {
	name     string
	path     string
	label    string
	parent   *menuTreeNode
	children []*menuTreeNode
	profiles []menuProfile
}

// The look of the boot menu («Пульт»): grey text on near-black, amber
// section lines, the chosen line on amber. iPXE colours are numbered 0 black,
// 1 red, 2 green, 3 yellow, 4 blue, 5 magenta, 6 cyan, 7 white, and colour
// pairs 1 normal, 2 selected, 3 separator.
//
// On UEFI the firmware's text console is 80x25 drawn in a corner of the
// screen, in the firmware's own palette (OVMF shows "yellow" as olive). So
// the menu switches to iPXE's own framebuffer console. "--x 640 --y 480" is
// a minimum, not an exact mode: iPXE takes the smallest mode the machine
// offers that is at least that big, so one setting suits every machine, and
// the screen is filled with large text and exact colours ("colour --rgb").
// The framebuffer console draws bold text by OR-ing 0x555555 into the
// colour, so the chosen line uses dark text on amber there. Where the switch
// fails (no such mode) the firmware console stays, with white on yellow. BIOS
// loaders from ipxe.org are built without the console commands; they keep
// iPXE's own colours, and every line here fails harmlessly.
//
// iPXE splits a command line on whitespace and joins menu text back with
// single spaces, and it has no quoting. It expands ${...} after splitting,
// though, so runs of spaces come from settings: sp1…sp64 hold 1…64 spaces
// as hex bytes and ${spN:string} prints them inside one word.
var menuTheme = menuLookMarker + menuLook + "\n" +
	"console --x 640 --y 480 && set pxe_fb 1 || set pxe_fb 0\n" +
	"colour --rgb 0x0E1013 0 || echo -n\n" +
	"colour --rgb 0xF2A93B 3 || echo -n\n" +
	"colour --rgb 0xC3C8CF 7 || echo -n\n" +
	"cpair --foreground 7 --background 0 1 || echo -n\n" +
	"iseq ${pxe_fb} 1 && cpair --foreground 0 --background 3 2 || cpair --foreground 7 --background 3 2 || echo -n\n" +
	"cpair --foreground 3 --background 0 3 || echo -n\n" +
	menuPadSettings() +
	"iseq ${platform} efi && set pxe_fw UEFI || set pxe_fw BIOS\n" +
	menuHello

// menuHello reports the machine's maker, model, serial and UUID from SMBIOS
// to Boot (relative to the menu's own address, so /boot/hello) and frees the
// empty answer. A failure only skips the report.
const menuHello = "imgfetch --name pxe-hello hello?manufacturer=${manufacturer:uristring}&product=${product:uristring}&serial=${serial:uristring}&uuid=${uuid}&platform=${platform} && imgfree pxe-hello || echo -n\n"

func menuPadSettings() string {
	var b strings.Builder
	for n := 1; n <= 64; n *= 2 {
		b.WriteString(fmt.Sprintf("set sp%d:hex %s\n", n, strings.TrimSuffix(strings.Repeat("20:", n), ":")))
	}
	return b.String()
}

// menuPad is n spaces as iPXE will print them.
func menuPad(n int) string {
	var b strings.Builder
	for size := 64; size >= 1; size /= 2 {
		for n >= size {
			b.WriteString(fmt.Sprintf("${sp%d:string}", size))
			n -= size
		}
	}
	return b.String()
}

var menuPadToken = regexp.MustCompile(`\$\{sp(1|2|4|8|16|32|64):string\}`)

// expandMenuPad turns menuPad tokens back into spaces.
func expandMenuPad(text string) string {
	return menuPadToken.ReplaceAllStringFunc(text, func(token string) string {
		n, _ := strconv.Atoi(menuPadToken.FindStringSubmatch(token)[1])
		return strings.Repeat(" ", n)
	})
}

// menuTitleSuffix names the machine in every screen title; iPXE fills the
// variables on the machine itself.
var menuTitleSuffix = menuPad(2) + "|" + menuPad(2) + "${netX/mac}" + menuPad(2) + "|" + menuPad(2) + "${pxe_fw}"

// menuFooter is the last line of every screen.
var menuFooter = "ip ${netX/ip}" + menuPad(4) + "Enter: boot" + menuPad(4) + "Esc: back"

// menuScreenRows is how many list rows iPXE shows on an 80x25 screen.
const menuScreenRows = 20

// menuItemWidth is how wide a menu line may be on the narrowest screen the
// menu meets. iPXE's framebuffer console draws 9-pixel character cells, so
// 640x480 gives 71 columns, and a menu line holds the width less two columns
// of frame and two of padding on each side: 71 - 2 - 4 = 65. The firmware
// text consoles (80x25 and up) are wider, so the hints there sit a little
// left of the edge rather than past it.
const menuItemWidth = 65

// menuItemText puts a hint at the right edge of a menu line, after at least
// three spaces; a hint that does not fit is left out.
func menuItemText(title, hint string) string {
	hint = menuHint(hint)
	width := utf8.RuneCountInString(title)
	if hint == "" || width+3+len(hint) > menuItemWidth {
		return title
	}
	return title + menuPad(menuItemWidth-width-len(hint)) + hint
}

// splitMenuItemText undoes menuItemText.
func splitMenuItemText(text string) (title, hint string) {
	text = expandMenuPad(text)
	if i := strings.Index(text, "   "); i > 0 {
		return text[:i], strings.TrimSpace(text[i:])
	}
	return text, ""
}

// menuHint keeps the characters a menu line may hold and at most 32 of them.
func menuHint(hint string) string {
	var b strings.Builder
	for _, r := range hint {
		if r < 0x80 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(" .,!:+-_()>", r)) {
			b.WriteRune(r)
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	if len(out) > 32 {
		out = strings.TrimSpace(out[:31]) + "."
	}
	return out
}

func validateMenuFolder(path string) error {
	if path == "" {
		return nil
	}
	if len([]rune(path)) > 240 {
		return fmt.Errorf("menu folder path is too long")
	}
	parts := strings.Split(path, "/")
	if len(parts) > 5 {
		return fmt.Errorf("menu folder nesting exceeds five levels")
	}
	for _, part := range parts {
		if part == "." || part == ".." || !safeMenuText(part, 80) {
			return fmt.Errorf("menu folder contains unsupported characters")
		}
	}
	return nil
}

func menuFolderPaths(script string) map[string]string {
	lines := strings.SplitN(script, "\n", 3)
	if len(lines) < 2 || lines[0] != "#!ipxe" || !strings.HasPrefix(lines[1], menuFolderMarker) {
		return map[string]string{}
	}
	encoded := strings.TrimPrefix(lines[1], menuFolderMarker)
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return map[string]string{}
	}
	folders := map[string]string{}
	if err := json.Unmarshal(data, &folders); err != nil {
		return map[string]string{}
	}
	return folders
}

func buildMenuTreeWithProfiles(title, diagnosticLabel, diagnosticMessage string, profiles []menuProfile, folders map[string]string) ([]byte, error) {
	return buildMenuTreeWithProfilesTimed(title, diagnosticLabel, diagnosticMessage, profiles, folders, defaultLocalTimeout, true)
}

func buildMenuTreeWithProfilesTimed(title, diagnosticLabel, diagnosticMessage string, profiles []menuProfile, folders map[string]string, localTimer int, diagnosticsOn bool) ([]byte, error) {
	if localTimer < 0 || localTimer > maxLocalTimeout {
		return nil, fmt.Errorf("invalid wait before booting the local disk")
	}
	if !safeMenuText(title, 80) || !safeMenuText(diagnosticLabel, 80) || !safeMenuText(diagnosticMessage, 120) {
		return nil, fmt.Errorf("menu text contains unsupported characters or is too long")
	}
	root := &menuTreeNode{label: "menu_root"}
	nextID := 0
	for _, profile := range profiles {
		node := root
		if folder := folders[profile.id]; folder != "" {
			for _, segment := range strings.Split(folder, "/") {
				var child *menuTreeNode
				for _, candidate := range node.children {
					if candidate.name == segment {
						child = candidate
						break
					}
				}
				if child == nil {
					nextID++
					path := segment
					if node.path != "" {
						path = node.path + " / " + segment
					}
					child = &menuTreeNode{name: segment, path: path, label: fmt.Sprintf("folder_%d", nextID), parent: node}
					node.children = append(node.children, child)
				}
				node = child
			}
		}
		node.profiles = append(node.profiles, profile)
	}
	metadata, err := json.Marshal(folders)
	if err != nil {
		return nil, err
	}
	var out strings.Builder
	out.WriteString("#!ipxe\n" + menuFolderMarker + base64.RawURLEncoding.EncodeToString(metadata) + "\n")
	var selected []int64
	var selectedAnswers []menuWindowsAnswerRef
	var selectedDrivers []menuDriverConfigRef
	for _, profile := range profiles {
		if profile.driverConfigID > 0 {
			selectedDrivers = append(selectedDrivers, menuDriverConfigRef{ID: profile.driverConfigID, ProfileID: profile.windowsISOID, AnswerID: profile.driverAnswerID, AnswerSHA256: profile.driverAnswerSHA256, WIMKey: profile.driverWIMKey, WIMSHA256: profile.driverWIMSHA256, ScriptSHA256: profile.driverScriptSHA256})
		} else if profile.answerID > 0 {
			selectedAnswers = append(selectedAnswers, menuWindowsAnswerRef{ID: profile.answerID, ProfileID: profile.windowsISOID, SHA256: profile.answerSHA256})
		} else if profile.windowsISOID > 0 {
			selected = append(selected, profile.windowsISOID)
		}
	}
	if len(selectedAnswers) > 0 {
		encoded, err := json.Marshal(selectedAnswers)
		if err != nil {
			return nil, err
		}
		out.WriteString(menuAnswerMarker + base64.RawURLEncoding.EncodeToString(encoded) + "\n")
	}
	if len(selected) > 0 {
		encoded, err := json.Marshal(selected)
		if err != nil {
			return nil, err
		}
		out.WriteString(menuWindowsMarker + base64.RawURLEncoding.EncodeToString(encoded) + "\n")
	}
	if len(selectedDrivers) > 0 {
		encoded, err := json.Marshal(selectedDrivers)
		if err != nil {
			return nil, err
		}
		out.WriteString(menuDriverMarker + base64.RawURLEncoding.EncodeToString(encoded) + "\n")
	}
	out.WriteString(menuTheme)
	writeMenuNode(&out, root, title, diagnosticLabel, localTimer, diagnosticsOn)
	if diagnosticsOn {
		out.WriteString(":diagnostics\n" + "echo " + diagnosticMessage + "\n" + "sleep 3\n" + "goto local\n")
	}
	for _, profile := range profiles {
		writeProfileBoot(&out, profile)
	}
	out.WriteString(":local\nexit\n")
	return []byte(out.String()), nil
}

func writeMenuNode(out *strings.Builder, node *menuTreeNode, title, diagnosticLabel string, localTimer int, diagnosticsOn bool) {
	out.WriteString(":" + node.label + "\n")
	menuTitle := title
	if node.path != "" {
		menuTitle += " / " + node.path
	}
	out.WriteString("menu " + menuTitle + menuTitleSuffix + "\n")
	rows := 0
	item := func(target, text string) {
		out.WriteString("item " + target + " " + text + "\n")
		rows++
	}
	gap := func(text string) {
		if text == "" {
			out.WriteString("item --gap\n")
		} else {
			out.WriteString("item --gap " + text + "\n")
		}
		rows++
	}
	parentLabel := "local"
	if node.parent != nil {
		parentLabel = node.parent.label
		item(parentLabel, ".. Back")
		gap("")
	}
	for _, child := range node.children {
		item(child.label, menuItemText(child.name, ">"))
	}
	for _, profile := range node.profiles {
		item(profile.id, menuItemText(profile.title, profile.hint))
	}
	if node.parent == nil {
		gap("")
		item("local", "Boot local disk")
		if diagnosticsOn {
			item("diagnostics", diagnosticLabel)
		}
	}
	// The key line sits on the last row of an 80x25 screen when the list is
	// short enough, under the list otherwise.
	gap("")
	for rows < menuScreenRows-1 {
		gap("")
	}
	gap(menuFooter)
	if node.parent == nil {
		// Untouched, the machine goes on to its own disk: after Windows
		// Setup reboots it must not wait in the menu.
		if localTimer > 0 {
			out.WriteString("choose --default local --timeout " + strconv.Itoa(localTimer*1000) + " target || goto local\n")
		} else {
			out.WriteString("choose --default local target || goto local\n")
		}
	} else {
		out.WriteString("choose target || goto " + parentLabel + "\n")
	}
	out.WriteString("goto ${target} || goto " + parentLabel + "\n")
	for _, child := range node.children {
		writeMenuNode(out, child, title, diagnosticLabel, localTimer, diagnosticsOn)
	}
}

func writeProfileBoot(out *strings.Builder, profile menuProfile) {
	base := profile.base
	choiceReport := ""
	if raw, ok := strings.CutPrefix(profile.id, "install_"); ok {
		// A failed telemetry request must never prevent boot file downloads.
		choiceReport = "imgfetch --name pxe-choice " + base + "/boot/choice?id=" + raw + " && imgfree pxe-choice || echo -n\n"
	}
	if profile.driverConfigID > 0 {
		configID := fmt.Sprintf("%d", profile.driverConfigID)
		profileID := fmt.Sprintf("%d", profile.windowsISOID)
		files := []windowsInitrdFile{
			{name: "winpeshl.ini", url: base + "/assets/windows-profiles/" + profileID + "/winpeshl.ini"},
			{name: "pxe-setup.cmd", url: base + "/assets/windows-driver-configs/" + configID + "/pxe-setup.cmd"},
		}
		if profile.driverAnswerID > 0 {
			files = append(files, windowsInitrdFile{name: "pxe-answer.xml", url: base + "/assets/windows-answers/" + fmt.Sprintf("%d", profile.driverAnswerID) + "/pxe-answer.xml"})
		}
		out.WriteString(":" + profile.id + "\n" + choiceReport +
			"kernel " + base + "/assets/windows/wimboot\n" +
			windowsInjectedFiles(profile.id, files...) +
			"initrd " + base + "/assets/windows-driver-wims/" + profile.driverWIMKey + "/boot.wim boot.wim\n" +
			"boot || goto " + profile.id + "_failed\n" +
			":" + profile.id + "_failed\necho Windows boot failed\nsleep 5\ngoto local\n")
		return
	}
	if profile.windowsISOID > 0 {
		id := fmt.Sprintf("%d", profile.windowsISOID)
		prefix := base + "/assets/windows-profiles/" + id + "/"
		startup := prefix + "pxe-setup.cmd"
		files := []windowsInitrdFile{{name: "winpeshl.ini", url: prefix + "winpeshl.ini"}}
		if profile.answerID > 0 {
			answerPrefix := base + "/assets/windows-answers/" + fmt.Sprintf("%d", profile.answerID) + "/"
			startup = answerPrefix + "pxe-setup.cmd"
			files = append(files, windowsInitrdFile{name: "pxe-setup.cmd", url: startup}, windowsInitrdFile{name: "pxe-answer.xml", url: answerPrefix + "pxe-answer.xml"})
		} else {
			files = append(files, windowsInitrdFile{name: "pxe-setup.cmd", url: startup})
		}
		out.WriteString(":" + profile.id + "\n" + choiceReport +
			"kernel " + base + "/assets/windows/wimboot\n" +
			windowsInjectedFiles(profile.id, files...) +
			"initrd " + prefix + "boot.wim boot.wim\n" +
			"boot || goto " + profile.id + "_failed\n" +
			":" + profile.id + "_failed\necho Windows boot failed\nsleep 5\ngoto local\n")
		return
	}
	if profile.kind == BootProfileUbuntuBuiltin {
		out.WriteString(strings.Replace(ubuntuBootScript(base, profile.id), ":"+profile.id+"\n", ":"+profile.id+"\n"+choiceReport, 1))
	}
}

var menuLocalChoose = regexp.MustCompile(`(?m)^choose --default local(?: --timeout ([0-9]+))? target `)

// menuLocalTimer reads the wait before the main screen boots the local disk
// from a menu script, in seconds; 0 means it waits for a choice. A script
// without the main screen's choose line gets the default.
func menuLocalTimer(script string) int {
	m := menuLocalChoose.FindStringSubmatch(script)
	if m == nil {
		return defaultLocalTimeout
	}
	if m[1] == "" {
		return 0
	}
	ms, err := strconv.Atoi(m[1])
	if err != nil || ms < 1000 {
		return defaultLocalTimeout
	}
	return ms / 1000
}

// localDiskHint is the countdown the preview shows next to "Boot local disk".
func localDiskHint(script string) string {
	if seconds := menuLocalTimer(script); seconds > 0 {
		return "(" + strconv.Itoa(seconds) + ")"
	}
	return ""
}
