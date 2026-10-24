package unattend

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"
)

// ImportReport tells the editor what became of an opened answer file.
type ImportReport struct {
	// Exact: the file came from the editor and opened from its settings.
	Exact bool
	// Modified: such a file was changed after the editor saved it; saving
	// from the editor drops those changes.
	Modified bool
	// Recognized are the groups of settings taken into the editor:
	// language, timezone, computer-name, edition, activation-key,
	// bypass-checks, configuration-set, accounts, express-settings,
	// eula, wifi, online-accounts, scripts.
	Recognized []string
	// KeptAsXML are elements kept as they are, as pass/component/path.
	KeptAsXML []string
	// Added are values the editor writes that the file did not have.
	Added []string
	// Lost are values of the file the result would not have; empty unless
	// the port has a bug.
	Lost []string
	// DroppedComments counts XML comments, which are not kept.
	DroppedComments int
}

// Open reads an answer file into editor settings. A file made by the editor
// opens from its <Editor> element; any other file is taken apart: settings
// the editor knows go into the form, everything else is kept as XML and
// merged back by Build. A setting is taken into the form only if the result
// reproduces it; otherwise it stays as XML, so nothing of the file is lost.
func Open(d *Data, data []byte) (Config, ImportReport, error) {
	data, err := toUTF8(data)
	if err != nil {
		return Config{}, ImportReport{}, err
	}
	doc, err := ParseXML(data)
	if err != nil {
		return Config{}, ImportReport{}, fmt.Errorf("unattend: the file is not well-formed XML: %w", err)
	}
	root := doc.Root()
	if root == nil || root.Space != nsUnattend || root.Local != "unattend" {
		return Config{}, ImportReport{}, errors.New("unattend: the file is not an answer file: its root element is not <unattend>")
	}
	c, err := editorSettings(doc)
	if err != nil {
		return Config{}, ImportReport{}, err
	}
	if c != nil {
		out, _, err := Build(d, *c)
		if err != nil {
			return Config{}, ImportReport{}, err
		}
		return *c, ImportReport{Exact: true, Modified: !bytes.Equal(normalizeNewlines(out), normalizeNewlines(data))}, nil
	}
	return importForeign(d, doc)
}

func normalizeNewlines(b []byte) []byte {
	return bytes.ReplaceAll(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), []byte("\r\n"), []byte("\n"))
}

// toUTF8 turns UTF-16 with a byte order mark into UTF-8.
func toUTF8(data []byte) ([]byte, error) {
	var big bool
	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}):
	case bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		big = true
	default:
		return data, nil
	}
	data = data[2:]
	if len(data)%2 != 0 {
		return nil, errors.New("unattend: the UTF-16 file has an odd length")
	}
	units := make([]uint16, len(data)/2)
	for i := range units {
		if big {
			units[i] = uint16(data[2*i])<<8 | uint16(data[2*i+1])
		} else {
			units[i] = uint16(data[2*i+1])<<8 | uint16(data[2*i])
		}
	}
	return []byte(string(utf16.Decode(units))), nil
}

// importGroup is one setting taken from a foreign file: the nodes it reads
// and how it changes the settings.
type importGroup struct {
	name  string
	nodes []*Node
	apply func(*Config)
	// verify checks the result; nil means: every value of nodes is there.
	verify func(out *Document, leaves leafSet) bool
}

type importer struct {
	d      *Data
	doc    *Document
	groups []*importGroup
}

func (im *importer) add(name string, apply func(*Config), verify func(*Document, leafSet) bool, nodes ...*Node) {
	for _, n := range nodes {
		if n == nil {
			return
		}
	}
	im.groups = append(im.groups, &importGroup{name: name, nodes: nodes, apply: apply, verify: verify})
}

// component returns the first component of the name in the pass.
func (im *importer) component(pass, name string) *Node {
	for _, s := range im.doc.Root().Elements() {
		if p, _ := s.Attr("pass"); s.Local != "settings" || p != pass {
			continue
		}
		for _, c := range s.Elements() {
			if n, _ := c.Attr("name"); c.Local == "component" && n == name {
				return c
			}
		}
	}
	return nil
}

func child(n *Node, local string) *Node {
	if n == nil {
		return nil
	}
	return childElement(n, local)
}

func text(n *Node) string {
	if n == nil {
		return ""
	}
	return strings.TrimSpace(n.InnerText())
}

// onlyChildren reports whether n has no element children but the named.
func onlyChildren(n *Node, names ...string) bool {
	for _, c := range n.Elements() {
		found := false
		for _, name := range names {
			found = found || c.Local == name
		}
		if !found || c.Space != nsUnattend {
			return false
		}
	}
	return true
}

func importForeign(d *Data, doc *Document) (Config, ImportReport, error) {
	im := &importer{d: d, doc: doc}
	base := Default()
	base.KeepSensitiveFiles = true
	base.ExpressSettings = "Interactive"
	base.PESettings = DefaultPESettings{EditionSettings: InteractiveEditionSettings{}, BypassRequirementsCheck: false}
	base.ProcessorArchitectures = sourceArchitectures(doc)

	im.recognizeLanguage()
	im.recognizeShellSetupSpecialize()
	im.recognizeSetup()
	im.recognizeAccounts()
	im.recognizeOOBE()
	im.recognizeScripts()

	var report ImportReport
	disabled := map[*importGroup]bool{}
	var cfg Config
	var out *Document
	for attempt := 0; ; attempt++ {
		if attempt > len(im.groups)+1 {
			return Config{}, ImportReport{}, errors.New("unattend: could not settle the import")
		}
		cfg = base
		consumed := map[*Node]bool{}
		var enabled []*importGroup
		for _, g := range im.groups {
			if disabled[g] {
				continue
			}
			g.apply(&cfg)
			for _, n := range g.nodes {
				consumed[n] = true
			}
			enabled = append(enabled, g)
		}
		cfg.ExtraXML = leftovers(doc, consumed)
		var err error
		out, err = Generate(d, cfg)
		if err == nil {
			_, err = mergeExtra(out, cfg.ExtraXML)
		}
		if err != nil {
			// A recognized setting the generator refuses (an account named as
			// the computer, say): keep the last one as XML and try again.
			if len(enabled) == 0 {
				return Config{}, ImportReport{}, err
			}
			disabled[enabled[len(enabled)-1]] = true
			continue
		}
		outLeaves := leavesOf(out.Root())
		failed := false
		for _, g := range enabled {
			ok := true
			if g.verify != nil {
				ok = g.verify(out, outLeaves)
			} else {
				for _, n := range g.nodes {
					ok = ok && outLeaves.containsAll(leavesOf(n))
				}
			}
			if !ok {
				disabled[g] = true
				failed = true
			}
		}
		if failed {
			continue
		}
		seen := map[string]bool{}
		for _, g := range enabled {
			if !seen[g.name] {
				report.Recognized = append(report.Recognized, g.name)
				seen[g.name] = true
			}
		}
		break
	}
	for _, x := range cfg.ExtraXML {
		where := "/" + x.Component
		if x.Pass == "" {
			where = ""
		}
		if x.Path != "" {
			where += "/" + x.Path
		}
		frag, _ := ParseXML([]byte(x.XML))
		report.KeptAsXML = append(report.KeptAsXML, strings.TrimPrefix(x.Pass+where+"/"+frag.Root().Local, "/"))
		if !leavesOf(out.Root()).containsAll(leavesOfContext(frag.Root(), x)) {
			report.Lost = append(report.Lost, strings.TrimPrefix(x.Pass+where+"/"+frag.Root().Local, "/"))
		}
	}
	src := leavesOf(doc.Root())
	for k := range leavesOf(out.Root()) {
		if !src[k] && !strings.HasPrefix(k, "root|") {
			report.Added = append(report.Added, k)
		}
	}
	sort.Strings(report.Added)
	report.DroppedComments = countComments(doc)
	return cfg, report, nil
}

func countComments(n *Node) int {
	count := 0
	for _, c := range n.Children {
		if c.kind == commentNode {
			count++
		}
		count += countComments(c)
	}
	return count
}

func sourceArchitectures(doc *Document) []ProcessorArchitecture {
	seen := map[string]bool{}
	for _, n := range doc.Root().SelectNodes("//*[@processorArchitecture]", nil) {
		a, _ := n.Attr("processorArchitecture")
		seen[a] = true
	}
	var out []ProcessorArchitecture
	for _, a := range architectureOrder {
		if seen[string(a)] {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		out = []ProcessorArchitecture{"amd64"}
	}
	return out
}

// leftovers lists what no group took, element by element: an element with
// nothing taken is kept whole, one with taken parts is kept part by part.
func leftovers(doc *Document, consumed map[*Node]bool) []ExtraXML {
	var out []ExtraXML
	taken := func(n *Node) bool {
		if consumed[n] {
			return true
		}
		for p := n.Parent; p != nil; p = p.Parent {
			if consumed[p] {
				return true
			}
		}
		return false
	}
	var partly func(n *Node) bool
	partly = func(n *Node) bool {
		for _, c := range n.Elements() {
			if consumed[c] || partly(c) {
				return true
			}
		}
		return false
	}
	var visit func(n *Node, x ExtraXML)
	visit = func(n *Node, x ExtraXML) {
		if taken(n) {
			return
		}
		if !partly(n) {
			x.XML = fragmentXML(n)
			out = append(out, x)
			return
		}
		path := n.Local
		if x.Path != "" {
			path = x.Path + "/" + n.Local
		}
		for _, c := range n.Elements() {
			visit(c, ExtraXML{Pass: x.Pass, Component: x.Component, Arch: x.Arch, Path: path})
		}
	}
	for _, n := range doc.Root().Elements() {
		switch {
		case n.Space == nsUnattend && n.Local == "settings":
			pass, _ := n.Attr("pass")
			for _, comp := range n.Elements() {
				name, _ := comp.Attr("name")
				arch, _ := comp.Attr("processorArchitecture")
				for _, c := range comp.Elements() {
					visit(c, ExtraXML{Pass: pass, Component: name, Arch: arch})
				}
			}
		case partly(n):
			// A root element with taken parts (Extensions): keep a copy with
			// the rest.
			rest := n.Clone()
			rest.Children = nil
			for _, c := range n.Elements() {
				if !consumed[c] {
					rest.AppendChild(c.Clone())
				}
			}
			if len(rest.Children) > 0 {
				out = append(out, ExtraXML{XML: fragmentXML(rest)})
			}
		case !consumed[n]:
			out = append(out, ExtraXML{XML: fragmentXML(n)})
		}
	}
	return out
}

// leafSet holds values of a document as "context|path=value" strings.
type leafSet map[string]bool

func (s leafSet) containsAll(other leafSet) bool {
	for k := range other {
		if !s[k] {
			return false
		}
	}
	return true
}

// leavesOf lists the values under n: the text of every element without
// element children and every attribute, keyed by the pass, component and
// element path. <Order> and wcm:action are left out, since merging
// renumbers lists.
func leavesOf(n *Node) leafSet {
	ctx, path := leafContext(n)
	out := leafSet{}
	collectLeaves(n, ctx, path, out)
	return out
}

func leavesOfContext(n *Node, x ExtraXML) leafSet {
	ctx := "root|"
	if x.Pass != "" {
		ctx = x.Pass + "|" + x.Component + "|" + x.Arch + "|"
	}
	path := x.Path
	if path != "" {
		path += "/"
	}
	out := leafSet{}
	collectLeaves(n, ctx, path, out)
	return out
}

func leafContext(n *Node) (ctx, path string) {
	var names []string
	for p := n.Parent; p != nil && p.kind == elementNode; p = p.Parent {
		if p.Local == "component" && p.Space == nsUnattend {
			name, _ := p.Attr("name")
			arch, _ := p.Attr("processorArchitecture")
			pass := ""
			if p.Parent != nil {
				pass, _ = p.Parent.Attr("pass")
			}
			return pass + "|" + name + "|" + arch + "|", joinPath(names)
		}
		if p.Parent != nil && p.Parent.kind == documentNode {
			break
		}
		names = append([]string{elementName(p)}, names...)
	}
	return "root|", joinPath(names)
}

func joinPath(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return strings.Join(names, "/") + "/"
}

func elementName(n *Node) string {
	if p, ok := n.Attr("path"); ok && n.Local == "File" {
		return "File[" + strings.ToLower(p) + "]"
	}
	return n.Local
}

func collectLeaves(n *Node, ctx, path string, out leafSet) {
	switch {
	case n.Space == nsUnattend && n.Local == "settings":
		for _, c := range n.Elements() {
			collectLeaves(c, "", "", out)
		}
		return
	case n.Space == nsUnattend && n.Local == "component":
		name, _ := n.Attr("name")
		arch, _ := n.Attr("processorArchitecture")
		pass := ""
		if n.Parent != nil {
			pass, _ = n.Parent.Attr("pass")
		}
		for _, c := range n.Elements() {
			collectLeaves(c, pass+"|"+name+"|"+arch+"|", "", out)
		}
		return
	case n.Space == nsUnattend && n.Local == "unattend":
		for _, c := range n.Elements() {
			collectLeaves(c, "root|", "", out)
		}
		return
	case n.Local == "Order":
		return
	}
	if ctx == "" {
		ctx = "root|"
	}
	here := path + elementName(n)
	for _, a := range n.Attrs {
		if a.Space == nsXMLNS || (a.Space == nsWCM && a.Local == "action") || (a.Local == "path" && n.Local == "File") {
			continue
		}
		out[ctx+here+"/@"+a.Local+"="+a.Value] = true
	}
	elements := n.Elements()
	if len(elements) == 0 {
		out[ctx+here+"="+strings.TrimSpace(n.InnerText())] = true
		return
	}
	for _, c := range elements {
		collectLeaves(c, ctx, here+"/", out)
	}
}

// Recognizers. Each adds groups; a group whose value the result does not
// reproduce is dropped again by importForeign.

func (im *importer) recognizeLanguage() {
	pe := im.component("windowsPE", "Microsoft-Windows-International-Core-WinPE")
	oobe := im.component("oobeSystem", "Microsoft-Windows-International-Core")
	peUI, input, system, ui, user := child(pe, "UILanguage"), child(oobe, "InputLocale"), child(oobe, "SystemLocale"), child(oobe, "UILanguage"), child(oobe, "UserLocale")
	if input == nil || system == nil || ui == nil || user == nil {
		return
	}
	nodes := []*Node{input, system, ui, user}
	if peUI != nil {
		nodes = append(nodes, peUI)
	} else if pe == nil {
		// No windowsPE language: the generator adds the same one there.
		peUI = ui
	} else {
		return
	}
	image, err := im.d.ImageLanguage(text(peUI))
	if err != nil || !strings.EqualFold(text(ui), image.ID) || !strings.EqualFold(text(system), text(user)) {
		return
	}
	main, err := im.d.Locale(text(system))
	if err != nil || main.GeoLocation == nil {
		return
	}
	var pairs []LocaleAndKeyboard
	for _, entry := range strings.Split(text(input), ";") {
		entry = strings.TrimSpace(entry)
		var pair LocaleAndKeyboard
		switch i := strings.Index(entry, ":"); {
		case strings.Contains(entry, "{"):
			k, err := im.d.Keyboard(entry)
			if err != nil {
				return
			}
			pair = LocaleAndKeyboard{Locale: main.ID, Keyboard: k.ID}
		case i > 0:
			k, err := im.d.Keyboard(entry[i+1:])
			if err != nil {
				return
			}
			locale := ""
			if strings.EqualFold(entry[:i], main.LCID) {
				locale = main.ID
			} else {
				for _, id := range im.d.LocaleList {
					if l := im.d.UserLocales[key(id)]; strings.EqualFold(l.LCID, entry[:i]) {
						locale = l.ID
						break
					}
				}
			}
			if locale == "" {
				return
			}
			pair = LocaleAndKeyboard{Locale: locale, Keyboard: k.ID}
		default:
			l, err := im.d.Locale(entry)
			if err != nil || l.KeyboardLayout == nil {
				return
			}
			pair = LocaleAndKeyboard{Locale: l.ID, Keyboard: l.KeyboardLayout.ID}
		}
		pairs = append(pairs, pair)
	}
	if len(pairs) == 0 || len(pairs) > 3 {
		return
	}
	s := UnattendedLanguageSettings{ImageLanguage: image.ID, LocaleAndKeyboard: pairs[0], GeoLocation: main.GeoLocation.ID}
	if len(pairs) > 1 {
		s.LocaleAndKeyboard2 = &pairs[1]
	}
	if len(pairs) > 2 {
		s.LocaleAndKeyboard3 = &pairs[2]
	}
	// The generator writes the main locale, not the first keyboard's.
	s.LocaleAndKeyboard.Locale = main.ID
	im.add("language", func(c *Config) { c.LanguageSettings = s }, nil, nodes...)
}

func (im *importer) recognizeShellSetupSpecialize() {
	shell := im.component("specialize", "Microsoft-Windows-Shell-Setup")
	if tz := child(shell, "TimeZone"); tz != nil {
		if t, err := im.d.TimeOffset(text(tz)); err == nil {
			im.add("timezone", func(c *Config) { c.TimeZoneSettings = ExplicitTimeZoneSettings{TimeZone: t.ID} }, nil, tz)
		}
	}
	if cn := child(shell, "ComputerName"); cn != nil {
		name := text(cn)
		switch {
		case name == "*" || name == "":
			// Windows picks a random name, as the generator's default does.
			im.add("computer-name", func(c *Config) { c.ComputerNameSettings = RandomComputerNameSettings{} },
				func(*Document, leafSet) bool { return true }, cn)
		case checkComputerName(name) == nil:
			im.add("computer-name", func(c *Config) { c.ComputerNameSettings = CustomComputerNameSettings{Name: name} }, nil, cn)
		}
	}
	if pk := child(shell, "ProductKey"); pk != nil && productKeyPattern.MatchString(text(pk)) {
		value := strings.ToUpper(text(pk))
		im.add("activation-key", func(c *Config) {
			if pe, ok := c.PESettings.(DefaultPESettings); ok {
				if ce, ok := pe.EditionSettings.(CustomEditionSettings); ok && strings.EqualFold(ce.ProductKey.Value, value) {
					return // written by the edition already
				}
			}
			c.ActivationKey = &ProductKey{Value: value}
		}, nil, pk)
	}
}

func setPE(c *Config, f func(*DefaultPESettings)) {
	pe, ok := c.PESettings.(DefaultPESettings)
	if !ok {
		return
	}
	f(&pe)
	c.PESettings = pe
}

var labConfigCommand = regexp.MustCompile(`^reg\.exe add "HKLM\\SYSTEM\\Setup\\LabConfig" /v (BypassTPMCheck|BypassSecureBootCheck|BypassRAMCheck) /t REG_DWORD /d 1 /f$`)

func (im *importer) recognizeSetup() {
	setup := im.component("windowsPE", "Microsoft-Windows-Setup")
	if setup == nil {
		return
	}
	userData := child(setup, "UserData")
	if eula := child(userData, "AcceptEula"); eula != nil && text(eula) == "true" {
		im.add("eula", func(*Config) {}, nil, eula)
	}
	if pk := child(userData, "ProductKey"); pk != nil && onlyChildren(pk, "Key", "WillShowUI") {
		k, ui := child(pk, "Key"), text(child(pk, "WillShowUI"))
		value := strings.ToUpper(text(k))
		var edition EditionSettings
		switch {
		case ui == "Always" && (value == "" || value == "00000-00000-00000-00000-00000"):
			edition = InteractiveEditionSettings{}
		case ui == "Never" && k == nil:
			edition = FirmwareEditionSettings{}
		case ui == "OnError" && productKeyPattern.MatchString(value):
			edition = CustomEditionSettings{ProductKey: ProductKey{Value: value}}
			for _, id := range im.d.EditionList {
				if e := im.d.WindowsEditions[key(id)]; strings.EqualFold(e.ProductKey, value) {
					edition = UnattendedEditionSettings{Edition: e.ID}
					break
				}
			}
		}
		if edition != nil {
			im.add("edition", func(c *Config) { setPE(c, func(pe *DefaultPESettings) { pe.EditionSettings = edition }) }, nil, pk)
		}
	}
	if set := child(setup, "UseConfigurationSet"); set != nil && (text(set) == "true" || text(set) == "false") {
		value := text(set) == "true"
		im.add("configuration-set", func(c *Config) { c.UseConfigurationSet = value }, nil, set)
	}
	var bypass []*Node
	found := map[string]bool{}
	for _, cmd := range child(setup, "RunSynchronous").elementsOrNil() {
		if m := labConfigCommand.FindStringSubmatch(text(child(cmd, "Path"))); m != nil && !found[m[1]] {
			found[m[1]] = true
			bypass = append(bypass, cmd)
		}
	}
	if len(bypass) == 3 {
		im.add("bypass-checks", func(c *Config) { setPE(c, func(pe *DefaultPESettings) { pe.BypassRequirementsCheck = true }) }, nil, bypass...)
	}
}

func (n *Node) elementsOrNil() []*Node {
	if n == nil {
		return nil
	}
	return n.Elements()
}

// readPassword reads a <Password>-like element: the value and whether it was
// obscured (base64 of UTF-16LE of the password and the element name).
func readPassword(n *Node) (password string, obscured bool, ok bool) {
	if n == nil || !onlyChildren(n, "Value", "PlainText") {
		return "", false, false
	}
	value := text(child(n, "Value"))
	switch text(child(n, "PlainText")) {
	case "true":
		return value, false, true
	case "false":
		raw, err := base64.StdEncoding.DecodeString(value)
		if err != nil || len(raw)%2 != 0 {
			return "", false, false
		}
		units := make([]uint16, len(raw)/2)
		for i := range units {
			units[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
		}
		s := string(utf16.Decode(units))
		if !strings.HasSuffix(s, n.Local) {
			return "", false, false
		}
		return strings.TrimSuffix(s, n.Local), true, true
	}
	return "", false, false
}

func (im *importer) recognizeAccounts() {
	shell := im.component("oobeSystem", "Microsoft-Windows-Shell-Setup")
	accounts, autoLogon := child(shell, "UserAccounts"), child(shell, "AutoLogon")
	if accounts == nil || !onlyChildren(accounts, "LocalAccounts", "AdministratorPassword") {
		return
	}
	var s UnattendedAccountSettings
	obscure := map[bool]bool{}
	for _, la := range child(accounts, "LocalAccounts").elementsOrNil() {
		if la.Local != "LocalAccount" || !onlyChildren(la, "Name", "DisplayName", "Group", "Password") {
			return
		}
		password, obscured, ok := readPassword(child(la, "Password"))
		if !ok {
			return
		}
		obscure[obscured] = true
		s.Accounts = append(s.Accounts, Account{Name: text(child(la, "Name")), DisplayName: text(child(la, "DisplayName")), Password: password, Group: text(child(la, "Group"))})
	}
	adminPassword, adminObscured, hasAdmin := readPassword(child(accounts, "AdministratorPassword"))
	if child(accounts, "AdministratorPassword") != nil && !hasAdmin {
		return
	}
	nodes := []*Node{accounts}
	s.AutoLogonSettings = NoneAutoLogonSettings{}
	if autoLogon != nil {
		if !onlyChildren(autoLogon, "Username", "Enabled", "LogonCount", "Password") || text(child(autoLogon, "Enabled")) != "true" || text(child(autoLogon, "LogonCount")) != "1" {
			return
		}
		password, obscured, ok := readPassword(child(autoLogon, "Password"))
		if !ok {
			return
		}
		obscure[obscured] = true
		user := text(child(autoLogon, "Username"))
		switch {
		case strings.EqualFold(user, "Administrator") && hasAdmin && password == adminPassword:
			obscure[adminObscured] = true
			s.AutoLogonSettings = BuiltinAutoLogonSettings{Password: password}
		default:
			first := -1
			for i, a := range s.Accounts {
				if a.Group == "Administrators" {
					first = i
					break
				}
			}
			if first < 0 || s.Accounts[first].Name != user || s.Accounts[first].Password != password {
				return
			}
			s.AutoLogonSettings = OwnAutoLogonSettings{}
		}
		nodes = append(nodes, autoLogon)
	} else if hasAdmin {
		return // the generator writes the administrator password only with built-in autologon
	}
	if len(obscure) > 1 {
		return
	}
	s.ObscurePasswords = obscure[true]
	if checkAccounts(s) != nil {
		return
	}
	for _, a := range s.Accounts {
		if checkUsername(a.Name) != nil {
			return
		}
	}
	im.add("accounts", func(c *Config) { c.AccountSettings = s }, nil, nodes...)
}

func (im *importer) recognizeOOBE() {
	oobe := child(im.component("oobeSystem", "Microsoft-Windows-Shell-Setup"), "OOBE")
	if oobe == nil {
		return
	}
	if n := child(oobe, "ProtectYourPC"); n != nil {
		switch text(n) {
		case "1":
			im.add("express-settings", func(c *Config) { c.ExpressSettings = "EnableAll" }, nil, n)
		case "3":
			im.add("express-settings", func(c *Config) { c.ExpressSettings = "DisableAll" }, nil, n)
		}
	}
	if n := child(oobe, "HideEULAPage"); n != nil && text(n) == "true" {
		im.add("eula", func(*Config) {}, nil, n)
	}
	if n := child(oobe, "HideWirelessSetupInOOBE"); n != nil {
		switch text(n) {
		case "true":
			im.add("wifi", func(c *Config) { c.WifiSettings = SkipWifiSettings{} }, nil, n)
		case "false":
			im.add("wifi", func(c *Config) { c.WifiSettings = InteractiveWifiSettings{} }, nil, n)
		}
	}
	if n := child(oobe, "HideOnlineAccountScreens"); n != nil {
		switch text(n) {
		case "true":
			im.add("online-accounts", func(c *Config) {
				if _, ok := c.AccountSettings.(InteractiveMicrosoftAccountSettings); ok {
					c.AccountSettings = InteractiveLocalAccountSettings{}
				}
			}, nil, n)
		case "false":
			im.add("online-accounts", func(*Config) {}, nil, n)
		}
	}
}

var (
	sequenceFiles = []struct {
		file  string
		phase ScriptPhase
	}{
		{`C:\Windows\Setup\Scripts\Specialize.ps1`, "System"},
		{`C:\Windows\Setup\Scripts\FirstLogon.ps1`, "FirstLogon"},
		{`C:\Windows\Setup\Scripts\UserOnce.ps1`, "UserOnce"},
		{`C:\Windows\Setup\Scripts\DefaultUser.ps1`, "DefaultUser"},
	}
	sequenceCommand = regexp.MustCompile(`(?i)Extensions\.ExtractScript|\\Setup\\Scripts\\(Specialize|DefaultUser|FirstLogon)\.ps1|^reg\.exe (load|unload) "HKU\\DefaultUser"`)
)

// recognizeScripts takes the script sequences of a file made by the
// Schneegans generator: Specialize.ps1, FirstLogon.ps1, UserOnce.ps1 and
// DefaultUser.ps1 run again from the same phases, from new paths; their
// helper files stay as they are.
func (im *importer) recognizeScripts() {
	ext := childElementNS(im.doc.Root(), nsMine, "Extensions")
	if ext == nil {
		return
	}
	var nodes []*Node
	var scripts []ImportedScript
	for _, seq := range sequenceFiles {
		for _, f := range ext.Elements() {
			if p, _ := f.Attr("path"); f.Local == "File" && strings.EqualFold(p, seq.file) {
				nodes = append(nodes, f)
				name := seq.file[strings.LastIndex(seq.file, `\`)+1:]
				scripts = append(scripts, ImportedScript{Phase: seq.phase, Name: name, Content: strings.TrimSpace(f.InnerText())})
			}
		}
	}
	if len(scripts) == 0 {
		return
	}
	for _, f := range ext.Elements() {
		if f.Local == "ExtractScript" || f.Local == "Build" {
			nodes = append(nodes, f)
		}
	}
	hidden := false
	for _, list := range []*Node{
		child(im.component("specialize", "Microsoft-Windows-Deployment"), "RunSynchronous"),
		child(im.component("oobeSystem", "Microsoft-Windows-Shell-Setup"), "FirstLogonCommands"),
	} {
		for _, cmd := range list.elementsOrNil() {
			line := text(child(cmd, "Path")) + text(child(cmd, "CommandLine"))
			if sequenceCommand.MatchString(line) {
				nodes = append(nodes, cmd)
				hidden = hidden || strings.Contains(line, "Hidden")
			}
		}
	}
	verify := func(out *Document, _ leafSet) bool {
		outExt := childElementNS(out.Root(), nsMine, "Extensions")
		for _, s := range scripts {
			found := false
			for _, f := range outExt.elementsOrNil() {
				p, _ := f.Attr("path")
				found = found || (strings.EqualFold(p, `C:\Windows\Setup\Scripts\Imported\`+s.Name) && strings.TrimSpace(f.InnerText()) == s.Content)
			}
			if !found {
				return false
			}
		}
		return true
	}
	im.add("scripts", func(c *Config) {
		c.ImportedScripts = append(c.ImportedScripts, scripts...)
		c.HidePowerShellWindows = c.HidePowerShellWindows || hidden
	}, verify, nodes...)
}
