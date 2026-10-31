package unattend

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Oh My Boot extensions around the port: the result keeps its settings in
// an element of our own namespace, so the editor opens its files exactly,
// and elements of foreign files are merged back in.

// EditorNamespace is the namespace of the <Editor> element. Windows Setup
// ignores elements of foreign namespaces, as it does with <Extensions>.
const EditorNamespace = "urn:omb:unattend-editor"

// EditorVersion is the format version of the <Editor> element.
const EditorVersion = "1"

// Build generates the answer file for the editor: Generate, then the kept
// elements of a foreign file, then the settings in <Editor>. Warnings name
// kept elements that could not go in.
func Build(d *Data, c Config) (xml []byte, warnings []string, err error) {
	doc, err := Generate(d, c)
	if err != nil {
		return nil, nil, err
	}
	warnings, err = mergeExtra(doc, c.ExtraXML)
	if err != nil {
		return nil, nil, err
	}
	if err := embedSettings(doc, c); err != nil {
		return nil, nil, err
	}
	return Serialize(doc), warnings, nil
}

// asciiJSON is JSON with every non-ASCII character escaped, so the text
// survives the ASCII output and needs no XML escaping inside CDATA (json
// escapes <, > and & as well).
func asciiJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	for _, r := range string(raw) {
		switch {
		case r < utf8.RuneSelf:
			b.WriteRune(r)
		case r > 0xFFFF:
			r -= 0x10000
			fmt.Fprintf(&b, `\u%04x\u%04x`, 0xD800+(r>>10), 0xDC00+(r&0x3FF))
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
	}
	return []byte(b.String()), nil
}

func embedSettings(doc *Document, c Config) error {
	raw, err := asciiJSON(c)
	if err != nil {
		return err
	}
	el := NewElement(EditorNamespace, "Editor")
	el.SetAttr("version", EditorVersion)
	el.SetAttr("upstream", UpstreamCommit)
	text := string(raw)
	// Obscured passwords are only base64 in the result; keep the settings
	// no more readable than that.
	if a, ok := c.AccountSettings.(UnattendedAccountSettings); ok && a.ObscurePasswords {
		el.SetAttr("encoding", "base64")
		text = base64.StdEncoding.EncodeToString(raw)
	}
	el.AppendChild(&Node{kind: cdataNode, Data: text})
	el.IsEmpty = false
	doc.Root().AppendChild(el)
	return nil
}

// editorSettings reads the <Editor> element, if the document has one.
func editorSettings(doc *Document) (*Config, error) {
	var el *Node
	for _, n := range doc.Root().Elements() {
		if n.Space == EditorNamespace && n.Local == "Editor" {
			el = n
		}
	}
	if el == nil {
		return nil, nil
	}
	if v, _ := el.Attr("version"); v != EditorVersion {
		return nil, fmt.Errorf("unattend: editor settings of version %q are not supported", v)
	}
	text := strings.TrimSpace(el.InnerText())
	if e, _ := el.Attr("encoding"); e == "base64" {
		raw, err := base64.StdEncoding.DecodeString(text)
		if err != nil {
			return nil, fmt.Errorf("unattend: editor settings: %w", err)
		}
		text = string(raw)
	}
	var c Config
	if err := json.Unmarshal([]byte(text), &c); err != nil {
		return nil, fmt.Errorf("unattend: editor settings: %w", err)
	}
	return &c, nil
}

// importedScriptsModifier runs the imported script sequences of a foreign
// file from the same phases (Oh My Boot extension).
func importedScriptsModifier(m *modifierContext) {
	for _, s := range m.config.ImportedScripts {
		path := m.embedTextFile(`C:\Windows\Setup\Scripts\Imported\`+s.Name, s.Content, nil, nil)
		switch s.Phase {
		case "System":
			m.specialize.invokeFile(path)
		case "FirstLogon":
			m.firstLogon.invokeFile(path)
		case "UserOnce":
			m.userOnce.invokeFile(path)
		case "DefaultUser":
			m.defaultUser.invokeFile(path)
		default:
			fail("Unknown phase '%s' of imported script '%s'.", s.Phase, s.Name)
		}
	}
}

// listContainers hold items that add up rather than replace each other.
var listContainers = map[string]bool{
	"RunSynchronous": true, "RunAsynchronous": true, "FirstLogonCommands": true, "LogonCommands": true,
	"LocalAccounts": true, "DomainAccounts": true, "DriverPaths": true, "Interfaces": true, "Routes": true,
	"Disks": true, "CreatePartitions": true, "ModifyPartitions": true, "InstallFrom": true, "MetaData": true,
}

// mergeExtra puts kept elements into the generated document: into the same
// component (made if missing) and element path. An item of a list is
// added unless the same item is there; any other element replaces one of
// the same name, since it is a setting the editor did not recognize.
func mergeExtra(doc *Document, extra []ExtraXML) ([]string, error) {
	var warnings []string
	root := doc.Root()
	touched := map[*Node]bool{}
	for _, x := range extra {
		frag, err := ParseXML([]byte(x.XML))
		if err != nil {
			return nil, fmt.Errorf("unattend: kept XML of %s/%s is not well-formed: %w", x.Component, x.Path, err)
		}
		el := frag.Root().Clone()
		el.Parent = nil
		stripNamespaceDeclarations(el)
		if x.Pass == "" {
			if w := mergeRoot(root, el); w != "" {
				warnings = append(warnings, w)
			}
			continue
		}
		component := findComponent(root, x.Pass, x.Component, x.Arch, true)
		parent := component
		if x.Path != "" {
			for _, name := range strings.Split(x.Path, "/") {
				next := childElement(parent, name)
				if next == nil {
					next = parent.AppendChild(NewElement(nsUnattend, name))
				}
				parent = next
			}
		}
		_, item := el.AttrNS(nsWCM, "action")
		if item || listContainers[parent.Local] {
			if !containsSame(parent, el) {
				parent.AppendChild(el)
				touched[parent] = true
			}
			continue
		}
		if old := childElement(parent, el.Local); old != nil && listContainers[el.Local] {
			for _, c := range el.Elements() {
				if !containsSame(old, c) {
					old.AppendChild(c.Clone())
				}
			}
			touched[old] = true
			continue
		} else if old != nil {
			parent.InsertBefore(el, old)
			old.RemoveSelf()
			continue
		}
		parent.AppendChild(el)
	}
	for list := range touched {
		renumber(list)
	}
	for _, n := range root.SelectNodes("//*", nil) {
		n.IsEmpty = false
	}
	return warnings, nil
}

func mergeRoot(root, el *Node) string {
	if el.Space == nsMine && el.Local == "Extensions" {
		ext := childElementNS(root, nsMine, "Extensions")
		if ext == nil {
			root.AppendChild(el)
			return ""
		}
		var skipped []string
		for _, c := range el.Elements() {
			switch {
			case c.Local == "ExtractScript" && childElementNS(ext, nsMine, "ExtractScript") != nil:
			case c.Local == "Build":
			case c.Local == "File" && hasFile(ext, c):
				p, _ := c.Attr("path")
				skipped = append(skipped, p)
			default:
				ext.AppendChild(c.Clone())
			}
		}
		if len(skipped) > 0 {
			return "kept files replaced by generated ones: " + strings.Join(skipped, ", ")
		}
		return ""
	}
	root.AppendChild(el)
	return ""
}

func hasFile(ext, file *Node) bool {
	want, _ := file.Attr("path")
	for _, f := range ext.Elements() {
		if p, ok := f.Attr("path"); ok && f.Local == "File" && strings.EqualFold(p, want) {
			return true
		}
	}
	return false
}

func childElement(n *Node, local string) *Node { return childElementNS(n, nsUnattend, local) }

func childElementNS(n *Node, space, local string) *Node {
	for _, c := range n.Elements() {
		if c.Space == space && c.Local == local {
			return c
		}
	}
	return nil
}

// findComponent finds the component of a pass, or makes it.
func findComponent(root *Node, pass, name, arch string, create bool) *Node {
	var settings *Node
	for _, s := range root.Elements() {
		if p, _ := s.Attr("pass"); s.Local == "settings" && p == pass {
			settings = s
		}
	}
	if settings == nil {
		if !create {
			return nil
		}
		settings = NewElement(nsUnattend, "settings")
		settings.SetAttr("pass", pass)
		if ext := childElementNS(root, nsMine, "Extensions"); ext != nil {
			root.InsertBefore(settings, ext)
		} else {
			root.AppendChild(settings)
		}
	}
	for _, c := range settings.Elements() {
		n, _ := c.Attr("name")
		a, _ := c.Attr("processorArchitecture")
		if c.Local == "component" && n == name && a == arch {
			return c
		}
	}
	if !create {
		return nil
	}
	c := NewElement(nsUnattend, "component")
	c.SetAttr("name", name)
	c.SetAttr("processorArchitecture", arch)
	c.SetAttr("publicKeyToken", "31bf3856ad364e35")
	c.SetAttr("language", "neutral")
	c.SetAttr("versionScope", "nonSxS")
	return settings.AppendChild(c)
}

// canonical is an element's content without <Order>, to compare list items.
func canonical(n *Node) string {
	var b strings.Builder
	var walk func(*Node)
	walk = func(n *Node) {
		for _, c := range n.Children {
			switch {
			case c.IsElement() && c.Local == "Order":
			case c.IsElement():
				b.WriteString("<" + c.Local + ">")
				walk(c)
				b.WriteString("</>")
			case c.IsText():
				b.WriteString(strings.TrimSpace(c.Data))
			}
		}
	}
	walk(n)
	return n.Local + ":" + b.String()
}

func containsSame(list, item *Node) bool {
	want := canonical(item)
	for _, c := range list.Elements() {
		if canonical(c) == want {
			return true
		}
	}
	return false
}

// renumber gives the items of a list consecutive <Order> values.
func renumber(list *Node) {
	pos := 1
	for _, item := range list.Elements() {
		if order := childElement(item, "Order"); order != nil {
			order.SetInnerText(fmt.Sprint(pos))
			pos++
		}
	}
}

// stripNamespaceDeclarations drops xmlns attributes; the writer declares
// the namespaces an element needs where they are not in scope.
func stripNamespaceDeclarations(n *Node) {
	attrs := n.Attrs[:0]
	for _, a := range n.Attrs {
		if a.Space != nsXMLNS {
			attrs = append(attrs, a)
		}
	}
	n.Attrs = attrs
	for _, c := range n.Elements() {
		stripNamespaceDeclarations(c)
	}
}

// fragmentXML writes one element with the namespace declarations it needs.
func fragmentXML(n *Node) string {
	doc := NewDocument()
	c := n.Clone()
	c.Parent = nil
	stripNamespaceDeclarations(c)
	doc.AppendChild(c)
	return prettyString(doc)
}
