package control

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

const menuFolderMarker = "# pxe-platform:folders-v1 "

type menuProfile struct {
	id    string
	title string
	base  string
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
	switch profile.id {
	case "ubuntu":
		out.WriteString(":ubuntu\n" +
			"kernel " + base + "/assets/ubuntu/vmlinuz initrd=initrd boot=casper ip=dhcp iso-url=" + base + "/images/ubuntu-desktop.iso ---\n" +
			"initrd " + base + "/assets/ubuntu/initrd\n" +
			"shim " + base + "/assets/ubuntu/bootx64.efi || goto ubuntu_failed\n" +
			"boot || goto ubuntu_failed\n" +
			":ubuntu_failed\necho Ubuntu boot failed\nsleep 5\ngoto local\n")
	case "windows10", "windows11":
		out.WriteString(":" + profile.id + "\n" +
			"kernel " + base + "/assets/windows/wimboot\n")
		if profile.id == "windows11" {
			out.WriteString("initrd -n winpeshl.ini " + base + "/assets/windows11/winpeshl.ini\n" +
				"initrd -n pxe-setup.cmd " + base + "/assets/windows11/pxe-setup.cmd\n")
		}
		out.WriteString("initrd " + base + "/assets/" + profile.id + "/boot.wim boot.wim\n" +
			"boot || goto " + profile.id + "_failed\n" +
			":" + profile.id + "_failed\necho " + profile.title + " boot failed\nsleep 5\ngoto local\n")
	}
}
