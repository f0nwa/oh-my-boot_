package unattend

import (
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf16"
)

// The modifiers of the original that fit in a few lines, one function each,
// named as the C# classes.

func buildModifier(m *modifierContext) {
	m.must("//s:Build/s:Commit/s:Hash").SetInnerText(UpstreamCommit)
	m.must("//s:Build/s:Commit/s:GitHubUrl").SetInnerText("https://github.com/cschneegans/unattend-generator/commit/" + UpstreamCommit)
}

func accessibilityModifier(m *modifierContext) {
	if !m.config.UseNarrator {
		return
	}
	m.appendCommand(placeWindowsPE, m.cmd.shellCommand(`start X:\Windows\System32\Narrator.exe`))
	m.specialize.append("& 'C:\\Windows\\System32\\Narrator.exe';\n" +
		`reg.exe ADD "HKLM\Software\Microsoft\Windows NT\CurrentVersion\Accessibility" /v Configuration /t REG_SZ /d narrator /f;`)
	m.userOnce.append("& 'C:\\Windows\\System32\\Narrator.exe';\n" +
		`reg.exe ADD "HKCU\Software\Microsoft\Windows NT\CurrentVersion\Accessibility" /v Configuration /t REG_SZ /d narrator /f;`)
}

func computerNameModifier(m *modifierContext) {
	set := func(value string) {
		newSimpleElement("ComputerName", m.getOrCreateComponent("specialize", "Microsoft-Windows-Shell-Setup"), value)
	}
	var script string
	switch s := m.config.ComputerNameSettings.(type) {
	case CustomComputerNameSettings:
		set(s.Name)
		return
	case ScriptComputerNameSettings:
		script = s.Script
	case PromptComputerNameSettings:
		script = resourceString("ComputerNamePrompt.ps1")
	case RandomComputerNameSettings:
		return
	default:
		panic(fmt.Sprintf("unsupported %T", s))
	}
	set("TEMPNAME")
	getter := m.embedTextFile("GetComputerName.ps1", script, nil, nil)
	setter := m.embedTextFileFromResource("SetComputerName.ps1", nil, nil)
	m.specialize.append("[string] $newName = & '" + getter + "';\n" +
		"$newName > 'C:\\Windows\\Setup\\Scripts\\ComputerName.txt';\n" +
		"\"Will set the computer name to '${newName}'.\";\n" +
		"Start-Process -FilePath ( Get-Process -Id $PID ).Path -ArgumentList '-ExecutionPolicy \"Unrestricted\" -NoProfile -File \"" + setter + "\"' -WindowStyle 'Hidden';\n" +
		"Start-Sleep -Seconds 10;")
}

func bypassModifier(m *modifierContext) {
	if pe, ok := m.config.PESettings.(DefaultPESettings); ok && pe.BypassRequirementsCheck {
		for _, v := range []string{"BypassTPMCheck", "BypassSecureBootCheck", "BypassRAMCheck"} {
			m.appendCommand(placeWindowsPE, m.cmd.registryCommand(`add "HKLM\SYSTEM\Setup\LabConfig" /v `+v+` /t REG_DWORD /d 1 /f`))
		}
	}
	if m.config.BypassNetworkCheck {
		m.specialize.append(`reg.exe add "HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\OOBE" /v BypassNRO /t REG_DWORD /d 1 /f;`)
	}
}

func productKeyModifier(m *modifierContext) {
	keyElement := m.must("//u:UserData/u:ProductKey/u:Key")
	uiElement := m.must("//u:UserData/u:ProductKey/u:WillShowUI")
	set := func(key, ui string) {
		keyElement.SetInnerText(key)
		uiElement.SetInnerText(ui)
	}
	if dps, ok := m.config.PESettings.(DefaultPESettings); ok {
		switch s := dps.EditionSettings.(type) {
		case UnattendedEditionSettings:
			set(check(m.data.Edition(s.Edition)).ProductKey, "OnError")
		case CustomEditionSettings:
			set(strings.ToUpper(s.ProductKey.Value), "OnError")
		case InteractiveEditionSettings:
			set("00000-00000-00000-00000-00000", "Always")
		case FirmwareEditionSettings:
			keyElement.RemoveSelf()
			uiElement.SetInnerText("Never")
		default:
			panic(fmt.Sprintf("unsupported %T", s))
		}
	}
	var key *ProductKey
	if m.config.ActivationKey != nil {
		key = m.config.ActivationKey
	} else if dps, ok := m.config.PESettings.(DefaultPESettings); ok {
		if ces, ok := dps.EditionSettings.(CustomEditionSettings); ok {
			key = &ces.ProductKey
		}
	}
	if key != nil {
		m.getOrCreateElement("specialize", "Microsoft-Windows-Shell-Setup", "ProductKey").SetInnerText(strings.ToUpper(key.Value))
	}
}

func localesModifier(m *modifierContext) {
	pe := m.must("//u:component[@name = 'Microsoft-Windows-International-Core-WinPE']")
	oobe := m.must("//u:component[@name = 'Microsoft-Windows-International-Core']")
	switch s := m.config.LanguageSettings.(type) {
	case UnattendedLanguageSettings:
		image := check(m.data.ImageLanguage(s.ImageLanguage))
		main := check(m.data.Locale(s.LocaleAndKeyboard.Locale))
		var keyboards []string
		for _, pair := range []*LocaleAndKeyboard{&s.LocaleAndKeyboard, s.LocaleAndKeyboard2, s.LocaleAndKeyboard3} {
			if pair == nil {
				continue
			}
			locale := check(m.data.Locale(pair.Locale))
			keyboard := check(m.data.Keyboard(pair.Keyboard))
			switch {
			case keyboard.Type == "IME":
				keyboards = append(keyboards, keyboard.ID)
			case locale.LCID == "1000":
				replacement := locale
				if found, ok := m.data.UserLocales[key(image.ID)]; ok {
					replacement = found
				} else if image.ID == "zh-CN" {
					replacement = m.data.UserLocales[key("zh")]
				} else if image.ID == "zh-TW" {
					replacement = m.data.UserLocales[key("zh-Hant")]
				}
				keyboards = append(keyboards, replacement.LCID+":"+keyboard.ID)
			default:
				keyboards = append(keyboards, locale.LCID+":"+keyboard.ID)
			}
		}
		pe.MustSelectNode("u:UILanguage", namespaces).SetInnerText(image.ID)
		oobe.MustSelectNode("u:InputLocale", namespaces).SetInnerText(strings.Join(keyboards, ";"))
		oobe.MustSelectNode("u:SystemLocale", namespaces).SetInnerText(main.ID)
		oobe.MustSelectNode("u:UserLocale", namespaces).SetInnerText(main.ID)
		oobe.MustSelectNode("u:UILanguage", namespaces).SetInnerText(image.ID)
		geo := check(m.data.Geo(s.GeoLocation))
		if main.GeoLocation == nil || geo.ID != main.GeoLocation.ID {
			m.userOnce.append("Set-WinHomeLocation -GeoId " + geo.ID + ";")
		}
	case InteractiveLanguageSettings:
		pe.RemoveSelf()
		oobe.RemoveSelf()
	default:
		panic(fmt.Sprintf("unsupported %T", s))
	}
}

func usersModifier(m *modifierContext) {
	switch s := m.config.AccountSettings.(type) {
	case UnattendedAccountSettings:
		if cn, ok := m.config.ComputerNameSettings.(CustomComputerNameSettings); ok {
			for _, a := range s.Accounts {
				if strings.EqualFold(a.Name, cn.Name) {
					fail("Account name '%s' must not be the same as the computer name.", a.Name)
				}
			}
		}
		addAutoLogon(m, m.must("//u:AutoLogon"), s)
		addUserAccounts(m, m.must("//u:UserAccounts"), s)
	case InteractiveMicrosoftAccountSettings, InteractiveLocalAccountSettings:
		m.must("//u:AutoLogon").RemoveSelf()
		m.must("//u:UserAccounts").RemoveSelf()
		if _, local := s.(InteractiveLocalAccountSettings); local {
			m.must("//u:HideOnlineAccountScreens").SetInnerText("true")
		}
	default:
		panic(fmt.Sprintf("unsupported %T", s))
	}
}

func addAutoLogon(m *modifierContext, container *Node, s UnattendedAccountSettings) {
	var username, password string
	switch a := s.AutoLogonSettings.(type) {
	case NoneAutoLogonSettings:
		return
	case BuiltinAutoLogonSettings:
		username, password = "Administrator", a.Password
	case OwnAutoLogonSettings:
		for _, acc := range s.Accounts {
			if acc.Group == "Administrators" {
				username, password = acc.Name, acc.Password
				break
			}
		}
	default:
		panic(fmt.Sprintf("unsupported %T", a))
	}
	newSimpleElement("Username", container, username)
	newSimpleElement("Enabled", container, "true")
	newSimpleElement("LogonCount", container, "1")
	newPasswordElement(container, "Password", password, s.ObscurePasswords)
	m.firstLogon.append(`Set-ItemProperty -LiteralPath 'Registry::HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon' -Name 'AutoLogonCount' -Type 'DWord' -Force -Value 0;`)
}

// newPasswordElement writes a password; "obscured" passwords are base64 of
// the UTF-16LE password followed by the element name, as Windows expects.
func newPasswordElement(parent *Node, element, password string, obscure bool) {
	el := newElement(element, parent)
	if obscure {
		units := utf16.Encode([]rune(password + element))
		raw := make([]byte, 2*len(units))
		for i, u := range units {
			raw[2*i], raw[2*i+1] = byte(u), byte(u>>8)
		}
		password = base64.StdEncoding.EncodeToString(raw)
	}
	newSimpleElement("Value", el, password)
	plain := "true"
	if obscure {
		plain = "false"
	}
	newSimpleElement("PlainText", el, plain)
}

func addUserAccounts(m *modifierContext, container *Node, s UnattendedAccountSettings) {
	if b, ok := s.AutoLogonSettings.(BuiltinAutoLogonSettings); ok {
		newPasswordElement(container, "AdministratorPassword", b.Password, s.ObscurePasswords)
	}
	local := newElement("LocalAccounts", container)
	for _, a := range s.Accounts {
		el := newElement("LocalAccount", local)
		el.SetAttrNS(nsWCM, "action", "add")
		newSimpleElement("Name", el, a.Name)
		newSimpleElement("DisplayName", el, a.DisplayName)
		newSimpleElement("Group", el, a.Group)
		newPasswordElement(el, "Password", a.Password, s.ObscurePasswords)
	}
}

func expressSettingsModifier(m *modifierContext) {
	el := m.must("//u:OOBE/u:ProtectYourPC")
	switch m.config.ExpressSettings {
	case "Interactive":
		el.RemoveSelf()
	case "EnableAll":
		el.SetInnerText("1")
	case "DisableAll":
		el.SetInnerText("3")
	}
}

func emptyElementsModifier(m *modifierContext) {
	for {
		modified := false
		for _, leaf := range m.all("//*") {
			if len(leaf.Children) == 0 && len(leaf.Attrs) == 0 {
				leaf.RemoveSelf()
				modified = true
			}
		}
		if !modified {
			return
		}
	}
}

func lockoutModifier(m *modifierContext) {
	switch s := m.config.LockoutSettings.(type) {
	case DefaultLockoutSettings:
	case DisableLockoutSettings:
		m.specialize.append("net.exe accounts /lockoutthreshold:0;")
	case CustomLockoutSettings:
		m.specialize.append(fmt.Sprintf("net.exe accounts /lockoutthreshold:%d /lockoutduration:%d /lockoutwindow:%d;", *s.LockoutThreshold, *s.LockoutDuration, *s.LockoutWindow))
	default:
		panic(fmt.Sprintf("unsupported %T", s))
	}
}

func passwordExpirationModifier(m *modifierContext) {
	switch s := m.config.PasswordExpirationSettings.(type) {
	case DefaultPasswordExpirationSettings:
	case UnlimitedPasswordExpirationSettings:
		m.specialize.append("net.exe accounts /maxpwage:UNLIMITED;")
	case CustomPasswordExpirationSettings:
		age, err := inRange(s.MaxAge, 1, 999, "maxAge")
		if err != nil {
			panic(err)
		}
		m.specialize.append(fmt.Sprintf("net.exe accounts /maxpwage:%d;", age))
	default:
		panic(fmt.Sprintf("unsupported %T", s))
	}
}

// htmlColor is ColorTranslator.ToHtml of a color made by FromHtml: #RRGGBB.
func htmlColor(c string) string {
	return "#" + strings.ToUpper(strings.TrimPrefix(c, "#"))
}

func themeValue(t ColorTheme) string {
	if t == "Light" {
		return "1"
	}
	return "0"
}

func boolInt(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func personalizationModifier(m *modifierContext) {
	if s, ok := m.config.ColorSettings.(CustomColorSettings); ok {
		ps1 := m.embedTextFileFromResource("SetColorTheme.ps1", func(w *strings.Builder) {
			w.WriteString("$lightThemeSystem = " + themeValue(s.SystemTheme) + ";\n" +
				"$lightThemeApps = " + themeValue(s.AppsTheme) + ";\n" +
				"$accentColorOnStart = " + boolInt(s.AccentColorOnStart) + ";\n" +
				"$enableTransparency = " + boolInt(s.EnableTransparency) + ";\n" +
				"$htmlAccentColor = '" + htmlColor(s.AccentColor) + "';\n")
		}, nil)
		m.defaultUser.append(`reg.exe add "HKU\DefaultUser\Software\Microsoft\Windows\DWM" /v ColorPrevalence /t REG_DWORD /d ` + boolInt(s.AccentColorOnBorders) + ` /f;`)
		m.userOnce.invokeFile(ps1)
		m.userOnce.restartExplorer = true
	}
	writeWallpaper := func(after string) {
		ps1 := m.embedTextFileFromResource("SetWallpaper.ps1", nil, func(w *strings.Builder) { w.WriteString(after + "\n") })
		m.userOnce.invokeFile(ps1)
	}
	switch s := m.config.WallpaperSettings.(type) {
	case ScriptWallpaperSettings:
		image := `C:\Windows\Setup\Scripts\Wallpaper`
		getter := m.embedTextFile("GetWallpaper.ps1", s.Script, nil, nil)
		m.specialize.append("try {\n" +
			"  $bytes = & '" + getter + "';\n" +
			"  [System.IO.File]::WriteAllBytes( '" + image + "', $bytes );\n" +
			"} catch {\n" +
			"  $_;\n" +
			"}")
		writeWallpaper("Set-WallpaperImage -LiteralPath '" + image + "';")
	case SolidWallpaperSettings:
		writeWallpaper("Set-WallpaperColor -HtmlColor '" + htmlColor(s.Color) + "';")
	}
	if s, ok := m.config.LockScreenSettings.(ScriptLockScreenSettings); ok {
		image := `C:\Windows\Setup\Scripts\LockScreenImage`
		getter := m.embedTextFile("GetLockScreenImage.ps1", s.Script, nil, nil)
		m.specialize.append("try {\n" +
			"  $bytes = & '" + getter + "';\n" +
			"  [System.IO.File]::WriteAllBytes( '" + image + "', $bytes );\n" +
			"  reg.exe add \"HKLM\\Software\\Microsoft\\Windows\\CurrentVersion\\PersonalizationCSP\" /v LockScreenImagePath /t REG_SZ /d \"" + image + "\" /f;\n" +
			"} catch {\n" +
			"  $_;\n" +
			"}")
	}
}

func timeZoneModifier(m *modifierContext) {
	if s, ok := m.config.TimeZoneSettings.(ExplicitTimeZoneSettings); ok {
		tz := check(m.data.TimeOffset(s.TimeZone))
		newSimpleElement("TimeZone", m.getOrCreateComponent("specialize", "Microsoft-Windows-Shell-Setup"), tz.ID)
	}
}

func appLockerModifier(m *modifierContext) {
	s, ok := m.config.AppLockerSettings.(ConfigureAppLockerSettings)
	if !ok {
		return
	}
	policy, err := ParseXML([]byte(s.PolicyXml))
	if err != nil {
		fail("AppLocker policy XML is invalid: %s", err.Error())
	}
	file := m.embedXMLFile("AppLockerPolicy.xml", policy)
	m.specialize.append("Get-Service -Name 'AppIDSvc' | Set-Service -StartupType 'Automatic';\n" +
		"Get-Service -Name 'AppIDSvc' | Start-Service;\n" +
		"Set-AppLockerPolicy -XmlPolicy '" + file + "';")
}

func scriptModifier(m *modifierContext) {
	if m.config.ScriptSettings.RestartExplorer {
		m.userOnce.restartExplorer = true
	}
	for i, s := range m.config.ScriptSettings.Scripts {
		key := fmt.Sprintf("unattend-%02x", i+1)
		fileName := key + "." + strings.ToLower(string(s.Type))
		filePath := `C:\Windows\Setup\Scripts\` + fileName
		content := s.Content
		if s.Type == "Reg" {
			const prefix = "Windows Registry Editor Version 5.00"
			if !strings.HasPrefix(content, prefix) {
				content = prefix + "\r\n\r\n" + content
			}
		}
		m.embedTextFile(fileName, content, nil, nil)
		var command string
		switch s.Type {
		case "Cmd":
			command = m.cmd.raw(filePath)
		case "Ps1":
			command = m.cmd.invokePowerShellScript(filePath)
		case "Reg":
			command = m.cmd.registryCommand(`import "` + filePath + `"`)
		case "Vbs":
			command = m.cmd.invokeVBScript(filePath)
		default:
			panic("unsupported script type " + string(s.Type))
		}
		var seq *sequence
		switch s.Phase {
		case "System":
			seq = m.specialize
		case "FirstLogon":
			seq = m.firstLogon
		case "UserOnce":
			seq = m.userOnce
		case "DefaultUser":
			seq = m.defaultUser
		default:
			panic("unsupported script phase " + string(s.Phase))
		}
		if s.Type == "Ps1" {
			seq.invokeFile(filePath)
		} else {
			seq.append(command + ";")
		}
	}
}

func specializeModifier(m *modifierContext) {
	if m.specialize.isEmpty() {
		return
	}
	file := m.embedTextFile("Specialize.ps1", m.specialize.script(), nil, nil)
	m.appendCommand(placeSpecialize, m.cmd.invokePowerShellScript(file))
}

func userOnceModifier(m *modifierContext) {
	if m.userOnce.isEmpty() {
		return
	}
	file := m.embedTextFile("UserOnce.ps1", m.userOnce.script(), nil, nil)
	command := strings.ReplaceAll(m.cmd.invokePowerShellScript(file), `"`, `\""`)
	m.defaultUser.append(`reg.exe add "HKU\DefaultUser\Software\Microsoft\Windows\CurrentVersion\RunOnce" /v "UnattendedSetup" /t REG_SZ /d "` + command + `" /f;`)
}

func defaultUserModifier(m *modifierContext) {
	if m.defaultUser.isEmpty() {
		return
	}
	file := m.embedTextFile("DefaultUser.ps1", m.defaultUser.script(), nil, nil)
	m.appendCommand(placeSpecialize,
		m.cmd.registryCommand(`load "HKU\DefaultUser" "C:\Users\Default\NTUSER.DAT"`),
		m.cmd.invokePowerShellScript(file),
		m.cmd.registryCommand(`unload "HKU\DefaultUser"`))
}

func deleteModifier(m *modifierContext) {
	if m.config.KeepSensitiveFiles {
		return
	}
	if s, ok := m.config.AccountSettings.(UnattendedAccountSettings); ok {
		if _, none := s.AutoLogonSettings.(NoneAutoLogonSettings); none {
			fail("To delete sensitive files, you must let Windows log on to an administrator account.")
		}
	}
	m.firstLogon.append("Remove-Item -LiteralPath @(\n" +
		"  'C:\\Windows\\Panther\\unattend.xml';\n" +
		"  'C:\\Windows\\Panther\\unattend-original.xml';\n" +
		"  'C:\\Windows\\Setup\\Scripts\\Wifi.xml';\n" +
		") -Force -ErrorAction 'SilentlyContinue' -Verbose;")
}

func firstLogonModifier(m *modifierContext) {
	if m.firstLogon.isEmpty() {
		return
	}
	file := m.embedTextFile("FirstLogon.ps1", m.firstLogon.script(), nil, nil)
	m.appendCommand(placeOobe, m.cmd.invokePowerShellScript(file))
}

func componentsModifier(m *modifierContext) {
	for _, item := range m.config.Components {
		setting := m.must(fmt.Sprintf("/u:unattend/u:settings[@pass='%s']", item.Key.Pass))
		component := setting.SelectNode(fmt.Sprintf("u:component[@name='%s']", item.Key.Component), namespaces)
		if component == nil {
			component = NewElement(nsUnattend, "component")
			component.SetAttr("name", item.Key.Component)
			component.SetAttr("processorArchitecture", "x86")
			component.SetAttr("publicKeyToken", "31bf3856ad364e35")
			component.SetAttr("language", "neutral")
			component.SetAttr("versionScope", "nonSxS")
		} else {
			for _, c := range append([]*Node(nil), component.Children...) {
				c.RemoveSelf()
			}
		}
		parsed, err := ParseXML([]byte("<root xmlns='" + nsUnattend + "' xmlns:wcm='" + nsWCM + "'>" + item.Value + "</root>"))
		if err != nil {
			fail("Your XML markup '%s' is not well-formed.", item.Value)
		}
		if len(parsed.Root().SelectNodes("//*[local-name()='settings' or local-name()='component']", nil)) > 0 {
			fail("You must not include elements 'settings' or 'component' with your XML markup '%s'.", item.Value)
		}
		for _, n := range append([]*Node(nil), parsed.Root().Children...) {
			component.AppendChild(n.Clone())
		}
		setting.AppendChild(component)
	}
}

func orderModifier(m *modifierContext) {
	userSet := map[*Node]bool{}
	for _, item := range m.config.Components {
		userSet[m.must(fmt.Sprintf("/u:unattend/u:settings[@pass='%s']/u:component[@name='%s']", item.Key.Pass, item.Key.Component))] = true
	}
	for _, container := range m.all("//u:RunSynchronous | //u:RunAsynchronous | //u:FirstLogonCommands") {
		if userSet[container.Parent] {
			continue
		}
		pos := 1
		for _, child := range container.Elements() {
			if child.SelectNode("u:Order", namespaces) != nil {
				panic(fmt.Sprintf("'%s' already contains an <Order> element.", child.Local))
			}
			order := NewElement(nsUnattend, "Order")
			order.SetInnerText(fmt.Sprint(pos))
			pos++
			child.PrependChild(order)
			child.SetAttrNS(nsWCM, "action", "add")
		}
	}
}

// architectureOrder is the order an ImmutableHashSet of the enum enumerates.
var architectureOrder = []ProcessorArchitecture{"x86", "amd64", "arm64"}

func processorArchitectureModifier(m *modifierContext) {
	var archs []ProcessorArchitecture
	for _, a := range architectureOrder {
		for _, want := range m.config.ProcessorArchitectures {
			if want == a {
				archs = append(archs, a)
				break
			}
		}
	}
	for _, component := range m.all("//*[@processorArchitecture]") {
		if len(archs) == 0 {
			fail("At least one processor architecture must be selected.")
		}
		component.SetAttr("processorArchitecture", string(archs[0]))
		// Each copy goes right after the original, so the copies come out
		// in reverse, as in the original.
		for _, a := range archs[1:] {
			copy := component.Clone()
			copy.SetAttr("processorArchitecture", string(a))
			component.Parent.InsertAfter(copy, component)
		}
	}
}

func prettyModifier(m *modifierContext) {
	for _, el := range m.all("//*") {
		el.IsEmpty = false
	}
	m.doc.Normalize()
}

// Not yet ported: these accept only settings that leave the document alone.
