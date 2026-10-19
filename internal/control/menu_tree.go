package control

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

const menuFolderMarker = "# pxe-platform:folders-v1 "
const menuWindowsMarker = "# pxe-platform:winiso-v1 "
const menuAnswerMarker = "# pxe-platform:winanswers-v1 "

type menuProfile struct {
	id           string
	title        string
	base         string
	windowsISOID int64
	answerID     int64
	answerSHA256 string
}

type menuTreeNode struct {
	name     string
	path     string
	label    string
	parent   *menuTreeNode
	children []*menuTreeNode
	profiles []menuProfile
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

func buildMenuTree(title, diagnosticLabel, diagnosticMessage, ubuntuBase, windows10Base, windows11Base string, folders map[string]string) ([]byte, error) {
	if !safeMenuText(title, 80) || !safeMenuText(diagnosticLabel, 80) || !safeMenuText(diagnosticMessage, 120) {
		return nil, fmt.Errorf("menu text contains unsupported characters or is too long")
	}
	profiles := []menuProfile{}
	if ubuntuBase != "" {
		profiles = append(profiles, menuProfile{id: "ubuntu", title: "Ubuntu Desktop 26.04.1", base: ubuntuBase})
	}
	if windows10Base != "" {
		profiles = append(profiles, menuProfile{id: "windows10", title: "Windows 10 installer - WinPE", base: windows10Base})
	}
	if windows11Base != "" {
		profiles = append(profiles, menuProfile{id: "windows11", title: "Windows 11 installer - WinPE", base: windows11Base})
	}
	return buildMenuTreeWithProfiles(title, diagnosticLabel, diagnosticMessage, profiles, folders)
}

func buildMenuTreeWithProfiles(title, diagnosticLabel, diagnosticMessage string, profiles []menuProfile, folders map[string]string) ([]byte, error) {
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
	for _, profile := range profiles {
		if profile.answerID > 0 {
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
	writeMenuNode(&out, root, title, diagnosticLabel)
	out.WriteString(":diagnostics\n" + "echo " + diagnosticMessage + "\n" + "sleep 3\n" + "goto local\n")
	for _, profile := range profiles {
		writeProfileBoot(&out, profile)
	}
	out.WriteString(":local\nexit\n")
	return []byte(out.String()), nil
}

func writeMenuNode(out *strings.Builder, node *menuTreeNode, title, diagnosticLabel string) {
	out.WriteString(":" + node.label + "\n")
	menuTitle := title
	if node.path != "" {
		menuTitle += " / " + node.path
	}
	out.WriteString("menu " + menuTitle + "\n")
	parentLabel := "local"
	if node.parent == nil {
		out.WriteString("item diagnostics " + diagnosticLabel + "\n")
	} else {
		parentLabel = node.parent.label
		out.WriteString("item " + parentLabel + " .. Back\n")
	}
	for _, child := range node.children {
		out.WriteString("item " + child.label + " " + child.name + "\n")
	}
	for _, profile := range node.profiles {
		out.WriteString("item " + profile.id + " " + profile.title + "\n")
	}
	if node.parent == nil {
		out.WriteString("item local Boot local disk\n")
	}
	out.WriteString("choose target || goto " + parentLabel + "\n")
	out.WriteString("goto ${target} || goto " + parentLabel + "\n")
	for _, child := range node.children {
		writeMenuNode(out, child, title, diagnosticLabel)
	}
}

func writeProfileBoot(out *strings.Builder, profile menuProfile) {
	base := profile.base
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
		out.WriteString(":" + profile.id + "\n" +
			"kernel " + base + "/assets/windows/wimboot\n" +
			windowsInjectedFiles(profile.id, files...) +
			"initrd " + prefix + "boot.wim boot.wim\n" +
			"boot || goto " + profile.id + "_failed\n" +
			":" + profile.id + "_failed\necho Windows boot failed\nsleep 5\ngoto local\n")
		return
	}
	switch profile.id {
	case "ubuntu":
		out.WriteString(ubuntuBootScript(base))
	case "windows10", "windows11":
		out.WriteString(":" + profile.id + "\n" +
			"kernel " + base + "/assets/windows/wimboot\n")
		if profile.id == "windows11" {
			out.WriteString(windowsInjectedFiles("windows11",
				windowsInitrdFile{name: "winpeshl.ini", url: base + "/assets/windows11/winpeshl.ini"},
				windowsInitrdFile{name: "pxe-setup.cmd", url: base + "/assets/windows11/pxe-setup.cmd"}))
		}
		out.WriteString("initrd " + base + "/assets/" + profile.id + "/boot.wim boot.wim\n" +
			"boot || goto " + profile.id + "_failed\n" +
			":" + profile.id + "_failed\necho " + profile.title + " boot failed\nsleep 5\ngoto local\n")
	}
}
