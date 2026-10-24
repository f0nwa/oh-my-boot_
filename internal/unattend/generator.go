package unattend

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// UpstreamCommit is the unattend-generator commit this package ports; the
// generated document names it in <Build>, as the original does.
const UpstreamCommit = "27237f82e9a8498d9c5bab7d398dc34b7682a1e8"

// namespaces are the XPath prefixes of the generator.
var namespaces = map[string]string{"u": nsUnattend, "wcm": nsWCM, "s": nsMine}

// Generate builds autounattend.xml from settings, as
// UnattendGenerator.GenerateXml does.
func Generate(d *Data, c Config) (doc *Document, err error) {
	// The original reports bad settings by throwing ConfigurationException
	// from deep inside a modifier; panicking with *ConfigurationError does
	// the same here.
	defer func() {
		if r := recover(); r != nil {
			var ce *ConfigurationError
			if e, ok := r.(error); ok && errors.As(e, &ce) {
				doc, err = nil, ce
				return
			}
			// Anything else is a bug of the port; report it rather than
			// take the panel down.
			doc, err = nil, fmt.Errorf("unattend: internal error: %v", r)
		}
	}()
	if err := validateConfig(d, c); err != nil {
		return nil, err
	}
	ctx := &modifierContext{
		doc:         resourceXML("autounattend.xml"),
		config:      c,
		data:        d,
		specialize:  newSequence("Running scripts to customize your Windows installation.", `C:\Windows\Setup\Scripts\Specialize.log`),
		firstLogon:  newSequence("Running scripts to finalize your Windows installation.", `C:\Windows\Setup\Scripts\FirstLogon.log`),
		userOnce:    newSequence("Running scripts to configure this user account.", `$env:TEMP\UserOnce.log`),
		defaultUser: newSequence("Running scripts to modify the default user\u2019\u2019s registry hive.", `C:\Windows\Setup\Scripts\DefaultUser.log`),
		cmd:         commandBuilder{hidePowerShellWindows: c.HidePowerShellWindows},
	}
	for _, m := range modifiers {
		m(ctx)
	}
	return ctx.doc, nil
}

// GenerateBytes is Generate followed by Serialize.
func GenerateBytes(d *Data, c Config) ([]byte, error) {
	doc, err := Generate(d, c)
	if err != nil {
		return nil, err
	}
	return Serialize(doc), nil
}

// modifiers run in the order of UnattendGenerator.GenerateXml.
var modifiers = []func(*modifierContext){
	buildModifier,
	accessibilityModifier,
	computerNameModifier,
	bypassModifier,
	productKeyModifier,
	localesModifier,
	diskModifier,
	usersModifier,
	bloatwareModifier,
	expressSettingsModifier,
	wifiModifier,
	emptyElementsModifier,
	lockoutModifier,
	passwordExpirationModifier,
	optimizationsModifier,
	personalizationModifier,
	timeZoneModifier,
	appLockerModifier,
	scriptModifier,
	importedScriptsModifier, // PXE Platform extension
	specializeModifier,
	userOnceModifier,
	defaultUserModifier,
	deleteModifier,
	firstLogonModifier,
	componentsModifier,
	orderModifier,
	processorArchitectureModifier,
	prettyModifier,
}

// modifierContext is ModifierContext with the helpers of Modifier.
type modifierContext struct {
	doc                                           *Document
	config                                        Config
	data                                          *Data
	specialize, firstLogon, userOnce, defaultUser *sequence
	cmd                                           commandBuilder
}

func (m *modifierContext) must(expr string) *Node { return m.doc.MustSelectNode(expr, namespaces) }
func (m *modifierContext) find(expr string) *Node { return m.doc.SelectNode(expr, namespaces) }
func (m *modifierContext) all(expr string) []*Node {
	return m.doc.SelectNodes(expr, namespaces)
}

// fail aborts generation with a configuration error.
func fail(format string, args ...any) {
	panic(configError(format, args...))
}

// check turns a lookup error into a configuration error.
func check[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// getOrCreateComponent is Util.GetOrCreateElement(pass, component).
func (m *modifierContext) getOrCreateComponent(pass, component string) *Node {
	setting := m.must(fmt.Sprintf("/u:unattend/u:settings[@pass='%s']", pass))
	if el := setting.SelectNode(fmt.Sprintf("u:component[@name='%s']", component), namespaces); el != nil {
		return el
	}
	el := NewElement(nsUnattend, "component")
	el.SetAttr("name", component)
	el.SetAttr("processorArchitecture", "x86")
	el.SetAttr("publicKeyToken", "31bf3856ad364e35")
	el.SetAttr("language", "neutral")
	el.SetAttr("versionScope", "nonSxS")
	setting.AppendChild(el)
	return el
}

// getOrCreateElement is Util.GetOrCreateElement(pass, component, element).
func (m *modifierContext) getOrCreateElement(pass, component, element string) *Node {
	comp := m.getOrCreateComponent(pass, component)
	if el := comp.SelectNode("u:"+element, namespaces); el != nil {
		return el
	}
	return comp.AppendChild(NewElement(nsUnattend, element))
}

// newSimpleElement is Util.NewSimpleElement.
func newSimpleElement(name string, parent *Node, text string) *Node {
	el := NewElement(nsUnattend, name)
	el.SetInnerText(text)
	return parent.AppendChild(el)
}

// newElement is Util.NewElement.
func newElement(name string, parent *Node) *Node {
	return parent.AppendChild(NewElement(nsUnattend, name))
}

// Commands go to one of three places, as CommandConfig.
type commandPlace int

const (
	placeWindowsPE commandPlace = iota
	placeSpecialize
	placeOobe
)

// appendCommand is CommandAppender.Append.
func (m *modifierContext) appendCommand(place commandPlace, values ...string) {
	for _, v := range values {
		var outer *Node
		switch place {
		case placeWindowsPE:
			outer = newElement("RunSynchronousCommand", m.getOrCreateElement("windowsPE", "Microsoft-Windows-Setup", "RunSynchronous"))
			newElement("Path", outer).SetInnerText(v)
		case placeSpecialize:
			outer = newElement("RunSynchronousCommand", m.getOrCreateElement("specialize", "Microsoft-Windows-Deployment", "RunSynchronous"))
			newElement("Path", outer).SetInnerText(v)
		case placeOobe:
			outer = newElement("SynchronousCommand", m.getOrCreateElement("oobeSystem", "Microsoft-Windows-Shell-Setup", "FirstLogonCommands"))
			newElement("CommandLine", outer).SetInnerText(v)
		}
	}
}

// insertTextWithIndentation is the Extensions method of the same name.
func insertTextWithIndentation(el *Node, text string) {
	level := el.Depth()
	el.AppendChild(NewText("\r\n" + strings.TrimSpace(text) + "\r\n" + strings.Repeat("\t", level)))
}

// embedFile stores a file in <Extensions>; ExtractScript writes it to disk
// during the specialize pass. It returns the file's path.
func (m *modifierContext) embedFile(name, content string) string {
	path := name
	if !strings.Contains(name, `\`) {
		path = `C:\Windows\Setup\Scripts\` + name
	}
	extensions := m.must("/u:unattend/s:Extensions")
	extract := extensions.SelectNode("s:ExtractScript", namespaces)
	if extract == nil {
		extract = extensions.AppendChild(NewElement(nsMine, "ExtractScript"))
		insertTextWithIndentation(extract, resourceString("ExtractScripts.ps1"))
		m.appendCommand(placeSpecialize, m.cmd.powerShellCommand(`$xml = [xml]::new(); $xml.Load('C:\Windows\Panther\unattend.xml'); $sb = [scriptblock]::Create( $xml.unattend.Extensions.ExtractScript ); Invoke-Command -ScriptBlock $sb -ArgumentList $xml;`))
	}
	file := extensions.AppendChild(NewElement(nsMine, "File"))
	file.SetAttr("path", path)
	insertTextWithIndentation(file, content)
	return path
}

// embedTextFile is Modifier.EmbedTextFile: StringWriter.WriteLine adds a
// line break after the content.
func (m *modifierContext) embedTextFile(name, content string, before, after func(*strings.Builder)) string {
	var w strings.Builder
	if before != nil {
		before(&w)
	}
	w.WriteString(content)
	w.WriteString("\n")
	if after != nil {
		after(&w)
	}
	return m.embedFile(name, w.String())
}

func (m *modifierContext) embedTextFileFromResource(name string, before, after func(*strings.Builder)) string {
	return m.embedTextFile(name, resourceString(name), before, after)
}

// embedXMLFile is Modifier.EmbedXmlFile.
func (m *modifierContext) embedXMLFile(name string, doc *Document) string {
	return m.embedFile(name, prettyString(doc))
}

// commandBuilder is CommandBuilder.
type commandBuilder struct{ hidePowerShellWindows bool }

func (b commandBuilder) raw(command string) string { return command }

func (b commandBuilder) shellCommand(command string) string { return `cmd.exe /c "` + command + `"` }

func (b commandBuilder) registryCommand(value string) string { return "reg.exe " + value }

func (b commandBuilder) windowStyle() string {
	if b.hidePowerShellWindows {
		return "Hidden"
	}
	return "Normal"
}

func (b commandBuilder) powerShellCommand(value string) string {
	if strings.Contains(value, `"`) {
		panic(fmt.Sprintf("PowerShell command '%s' must not contain '\"'.", value))
	}
	if !strings.HasSuffix(value, ";") && !strings.HasSuffix(value, "}") {
		panic(fmt.Sprintf("PowerShell command '%s' must end with either ';' or '}'.", value))
	}
	return `powershell.exe -WindowStyle "` + b.windowStyle() + `" -NoProfile -Command "` + value + `"`
}

func (b commandBuilder) invokePowerShellScript(path string) string {
	return `powershell.exe -WindowStyle "` + b.windowStyle() + `" -ExecutionPolicy "Unrestricted" -NoProfile -File "` + path + `"`
}

func (b commandBuilder) invokeVBScript(path string) string {
	return `cscript.exe //E:vbscript "` + path + `"`
}

// writeToFilePE is CommandBuilder.WriteToFilePE: echo commands appending the
// lines to a file, each command at most 255 characters.
func (b commandBuilder) writeToFilePE(path string, lines []string) []string {
	if strings.IndexFunc(path, isSpace) >= 0 {
		panic(fmt.Sprintf("Path '%s' must not contain whitespace characters.", path))
	}
	const maxLineLength = 255
	segments := echoProcess(lines, true)
	var result []string
	for len(segments) > 0 {
		prev, current := "", ""
		havePrev, haveCurrent := false, false
		for take := 1; take <= len(segments); take++ {
			current = "cmd.exe /c >>" + path + " (" + strings.Join(segments[:take], "&") + ")"
			haveCurrent = true
			if len([]rune(current)) > maxLineLength {
				if !havePrev {
					fail("Line '%s' is too long. You need to add line breaks to your input to make it shorter.", current)
				}
				result = append(result, prev)
				segments = segments[take-1:]
				haveCurrent = false
				break
			}
			prev, havePrev = current, true
		}
		if haveCurrent {
			result = append(result, current)
			break
		}
	}
	return result
}

func isSpace(r rune) bool {
	return strings.ContainsRune(" \t\n\r\v\f\u0085\u00a0", r) || r == '\u2028' || r == '\u2029'
}

// echoProcess is EchoProcessor.Process: trimmed, non-empty lines turned into
// echo statements, with cmd metacharacters escaped when asked.
func echoProcess(lines []string, escape bool) []string {
	var out []string
	for _, line := range lines {
		line = strings.TrimFunc(line, isSpace)
		if line == "" {
			continue
		}
		if escape {
			var b strings.Builder
			for _, r := range line {
				if strings.ContainsRune(`^&<>|%)"`, r) {
					b.WriteByte('^')
				}
				b.WriteRune(r)
			}
			line = b.String()
		}
		out = append(out, "echo:"+line)
	}
	return out
}

// sequence is PowerShellSequence.
type sequence struct {
	activity, logFile string
	commands          []string
	restartExplorer   bool
}

func newSequence(activity, logFile string) *sequence {
	return &sequence{activity: activity, logFile: logFile}
}

func (s *sequence) append(command string)  { s.commands = append(s.commands, command) }
func (s *sequence) invokeFile(file string) { s.append("& '" + file + "';") }
func (s *sequence) isEmpty() bool          { return len(s.commands) == 0 }

func (s *sequence) script() string {
	var w strings.Builder
	block := func(command string) {
		w.WriteString("\t{\n")
		for _, line := range splitLines(command) {
			w.WriteString("\t\t" + line + "\n")
		}
		w.WriteString("\t};\n")
	}
	w.WriteString("$scripts = @(\n")
	for _, c := range s.commands {
		block(c)
	}
	if s.restartExplorer {
		block(resourceString("RestartExplorer.ps1"))
	}
	w.WriteString(");\n\n")
	w.WriteString("& {\n" +
		"  [float] $complete = 0;\n" +
		"  [float] $increment = 100 / $scripts.Count;\n" +
		"  foreach( $script in $scripts ) {\n" +
		"    Write-Progress -Id 0 -Activity '" + s.activity + " Do not close this window.' -PercentComplete $complete;\n" +
		"    '*** Will now execute command \u00ab{0}\u00bb.' -f $(\n" +
		"      $script.ToString().Trim() -replace '\\s+', ' ' -replace '^(.{99})(.+)$', '$1\u2026';\n" +
		"    );\n" +
		"    $start = [datetime]::Now;\n" +
		"    & $script;\n" +
		"    '*** Finished executing command after {0:0} ms.' -f [datetime]::Now.Subtract( $start ).TotalMilliseconds;\n" +
		"    \"`r`n\" * 3;\n" +
		"    $complete += $increment;\n" +
		"  }\n" +
		"} *>&1 | Out-String -Width 1KB -Stream >> \"" + s.logFile + "\";\n")
	return w.String()
}

// splitLines is Util.SplitLines: TextReader.ReadLine over the string.
func splitLines(s string) []string {
	var out []string
	for len(s) > 0 {
		i := strings.IndexAny(s, "\r\n")
		if i < 0 {
			out = append(out, s)
			break
		}
		out = append(out, s[:i])
		if s[i] == '\r' && i+1 < len(s) && s[i+1] == '\n' {
			i++
		}
		s = s[i+1:]
	}
	return out
}

func joinLines(lines []string) string { return strings.Join(lines, "\r\n") }

var productKeyPattern = regexp.MustCompile(`(?i)^([A-Z0-9]{5}-){4}[A-Z0-9]{5}$`)
