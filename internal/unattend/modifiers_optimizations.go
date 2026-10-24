package unattend

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// OptimizationsModifier, in the order of the original. Script texts are the
// raw string literals of the original with their indentation removed.

// effectOrder is the order of the Effect enum, which is also the order an
// ImmutableDictionary keyed by it enumerates.
var effectOrder = []Effect{
	"ControlAnimations", "AnimateMinMax", "TaskbarAnimations", "DWMAeroPeekEnabled", "MenuAnimation",
	"TooltipAnimation", "SelectionFade", "DWMSaveThumbnailEnabled", "CursorShadow", "ListviewShadow",
	"ThumbnailsOrIcon", "ListviewAlphaSelect", "DragFullWindows", "ComboBoxAnimation", "FontSmoothing",
	"ListBoxSmoothScrolling", "DropShadow",
}

var stickyKeyValues = map[StickyKeys]int{
	"HotKeyActive":    0x04,
	"Indicator":       0x20,
	"TriState":        0x80,
	"TwoKeysOff":      0x100,
	"AudibleFeedback": 0x40,
	"HotKeySound":     0x10,
}

var taskbarSearchValues = map[TaskbarSearchMode]int{"Hide": 0, "Icon": 1, "Box": 2, "Label": 3}

const emptyTaskbarLayout = `<LayoutModificationTemplate xmlns="http://schemas.microsoft.com/Start/2014/LayoutModification" xmlns:defaultlayout="http://schemas.microsoft.com/Start/2014/FullDefaultLayout" xmlns:start="http://schemas.microsoft.com/Start/2014/StartLayout" xmlns:taskbar="http://schemas.microsoft.com/Start/2014/TaskbarLayout" Version="1">
  <CustomTaskbarLayoutCollection PinListPlacement="Replace">
    <defaultlayout:TaskbarLayout>
      <taskbar:TaskbarPinList>
        <taskbar:DesktopApp DesktopApplicationLinkPath="#leaveempty" />
      </taskbar:TaskbarPinList>
    </defaultlayout:TaskbarLayout>
  </CustomTaskbarLayoutCollection>
</LayoutModificationTemplate>`

const emptyStartTiles = `<LayoutModificationTemplate Version='1' xmlns='http://schemas.microsoft.com/Start/2014/LayoutModification'>
  <LayoutOptions StartTileGroupCellWidth='6' />
  <DefaultLayoutOverride>
    <StartLayoutCollection>
      <StartLayout GroupCellWidth='6' xmlns='http://schemas.microsoft.com/Start/2014/FullDefaultLayout' />
    </StartLayoutCollection>
  </DefaultLayoutOverride>
</LayoutModificationTemplate>`

// jsonReadable is how the original checks the JSON: it reads all tokens
// with Newtonsoft's JsonTextReader, which stops quietly at a premature end.
// Newtonsoft's leniencies (comments, single quotes) are not repeated; see
// UPSTREAM.md.
func jsonReadable(s string) bool {
	dec := json.NewDecoder(strings.NewReader(s))
	for {
		_, err := dec.Token()
		switch {
		case err == nil:
		case err == io.EOF || errors.Is(err, io.ErrUnexpectedEOF):
			return true
		default:
			return false
		}
	}
}

const advancedKey = `HKU\DefaultUser\Software\Microsoft\Windows\CurrentVersion\Explorer\Advanced`

func optimizationsModifier(m *modifierContext) {
	c := m.config
	_, cmdPE := c.PESettings.(GeneratePESettings)
	if _, ok := c.PESettings.(ScriptPESetttings); ok {
		cmdPE = true
	}

	setTaskbarIcons := func(xml string) {
		doc, err := ParseXML([]byte(xml))
		if err != nil {
			fail("Taskbar XML is not well-formed: %s", err.Error())
		}
		path := m.embedXMLFile("TaskbarLayoutModification.xml", doc)
		m.specialize.append(`reg.exe add "HKLM\Software\Policies\Microsoft\Windows\CloudContent" /v "DisableCloudOptimizedContent" /t REG_DWORD /d 1 /f;` + "\n" +
			`[System.Diagnostics.EventLog]::CreateEventSource( 'UnattendGenerator', 'Application' );`)
		m.defaultUser.append(`reg.exe add "HKU\DefaultUser\Software\Policies\Microsoft\Windows\Explorer" /v "StartLayoutFile" /t REG_SZ /d "` + path + `" /f;` + "\n" +
			`reg.exe add "HKU\DefaultUser\Software\Policies\Microsoft\Windows\Explorer" /v "LockedStartLayout" /t REG_DWORD /d 1 /f;`)
		m.embedTextFileFromResource("UnlockStartLayout.vbs", nil, nil)
		unlock := m.embedXMLFile("UnlockStartLayout.xml", resourceXML("UnlockStartLayout.xml"))
		m.specialize.append(`Register-ScheduledTask -TaskName 'UnlockStartLayout' -Xml $( Get-Content -LiteralPath '` + unlock + `' -Raw );`)
		m.userOnce.append(`[System.Diagnostics.EventLog]::WriteEntry( 'UnattendGenerator', "User '$env:USERNAME' has requested to unlock the Start menu layout.", [System.Diagnostics.EventLogEntryType]::Information, 1 );`)
	}
	switch s := c.TaskbarIcons.(type) {
	case DefaultTaskbarIcons:
	case EmptyTaskbarIcons:
		setTaskbarIcons(emptyTaskbarLayout)
	case CustomTaskbarIcons:
		setTaskbarIcons(s.Xml)
	default:
		panic(fmt.Sprintf("unsupported %T", s))
	}

	if c.ShowFileExtensions {
		m.defaultUser.append(`reg.exe add "` + advancedKey + `" /v "HideFileExt" /t REG_DWORD /d 0 /f;`)
	}
	switch c.HideFiles {
	case "None":
		m.defaultUser.append(`reg.exe add "` + advancedKey + `" /v "Hidden" /t REG_DWORD /d 1 /f;`)
		m.defaultUser.append(`reg.exe add "` + advancedKey + `" /v "ShowSuperHidden" /t REG_DWORD /d 1 /f;`)
	case "HiddenSystem":
		m.defaultUser.append(`reg.exe add "` + advancedKey + `" /v "Hidden" /t REG_DWORD /d 1 /f;`)
	}
	if c.DisableWindowsUpdate {
		file := m.embedXMLFile("PauseWindowsUpdate.xml", resourceXML("PauseWindowsUpdate.xml"))
		m.specialize.append(`Register-ScheduledTask -TaskName 'PauseWindowsUpdate' -Xml $( Get-Content -LiteralPath '` + file + `' -Raw );`)
	}
	if c.ShowAllTrayIcons {
		m.defaultUser.append(resourceString("ShowAllTrayIcons.ps1"))
		m.embedXMLFile("ShowAllTrayIcons.xml", resourceXML("ShowAllTrayIcons.xml"))
	}
	if c.HideTaskViewButton {
		m.defaultUser.append(`reg.exe add "` + advancedKey + `" /v ShowTaskViewButton /t REG_DWORD /d 0 /f;`)
	}
	if c.IsDefenderDisabled() {
		m.specialize.append(`reg.exe add "HKLM\SOFTWARE\Policies\Microsoft\Windows Defender Security Center\Notifications" /v DisableNotifications /t REG_DWORD /d 1 /f;`)
	}
	if c.UseConfigurationSet && !cmdPE {
		m.must("//u:UseConfigurationSet").SetInnerText("true")
	}
	if c.DisableSac {
		m.specialize.append(`reg.exe add "HKLM\SYSTEM\CurrentControlSet\Control\CI\Policy" /v VerifiedAndReputablePolicyState /t REG_DWORD /d 0 /f;`)
	}
	if c.DisableSmartScreen {
		m.specialize.append(`reg.exe add "HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer" /v SmartScreenEnabled /t REG_SZ /d "Off" /f;
reg.exe add "HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\WTDS\Components" /v ServiceEnabled /t REG_DWORD /d 0 /f;
reg.exe add "HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\WTDS\Components" /v NotifyMalicious /t REG_DWORD /d 0 /f;
reg.exe add "HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\WTDS\Components" /v NotifyPasswordReuse /t REG_DWORD /d 0 /f;
reg.exe add "HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\WTDS\Components" /v NotifyUnsafeApp /t REG_DWORD /d 0 /f;
reg.exe add "HKLM\SOFTWARE\Policies\Microsoft\Windows Defender Security Center\Systray" /v HideSystray /t REG_DWORD /d 1 /f;`)
		m.defaultUser.append(`reg.exe add "HKU\DefaultUser\Software\Microsoft\Edge\SmartScreenEnabled" /ve /t REG_DWORD /d 0 /f;
reg.exe add "HKU\DefaultUser\Software\Microsoft\Edge\SmartScreenPuaEnabled" /ve /t REG_DWORD /d 0 /f;
reg.exe add "HKU\DefaultUser\Software\Microsoft\Windows\CurrentVersion\AppHost" /v EnableWebContentEvaluation /t REG_DWORD /d 0 /f;
reg.exe add "HKU\DefaultUser\Software\Microsoft\Windows\CurrentVersion\AppHost" /v PreventOverride /t REG_DWORD /d 0 /f;`)
	}
	if c.DisableUac {
		m.specialize.append(`reg.exe add "HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System" /v EnableLUA /t REG_DWORD /d 0 /f`)
	}
	if c.EnableLongPaths {
		m.specialize.append(`reg.exe add "HKLM\SYSTEM\CurrentControlSet\Control\FileSystem" /v LongPathsEnabled /t REG_DWORD /d 1 /f`)
	}
	if c.EnableRemoteDesktop {
		m.specialize.append(`netsh.exe advfirewall firewall set rule group="@FirewallAPI.dll,-28752" new enable=Yes;
reg.exe add "HKLM\SYSTEM\CurrentControlSet\Control\Terminal Server" /v fDenyTSConnections /t REG_DWORD /d 0 /f;`)
	}
	if c.HardenSystemDriveAcl {
		m.specialize.append(`icacls.exe C:\ /remove:g "*S-1-5-11"`)
	}
	if c.DeleteJunctions {
		m.firstLogon.append("@(\n" +
			"\tGet-ChildItem -LiteralPath 'C:\\' -Force;\n" +
			"\tGet-ChildItem -LiteralPath 'C:\\Users' -Force;\n" +
			"\tGet-ChildItem -LiteralPath 'C:\\Users\\Default' -Force -Recurse -Depth 2;\n" +
			"\tGet-ChildItem -LiteralPath 'C:\\Users\\Public' -Force -Recurse -Depth 2;\n" +
			"\tGet-ChildItem -LiteralPath 'C:\\ProgramData' -Force;\n" +
			") | Where-Object -FilterScript {\n" +
			"\t$_.Attributes.HasFlag( [System.IO.FileAttributes]::ReparsePoint );\n" +
			"} | Remove-Item -Force -Recurse -Verbose;")
		m.userOnce.append("@(\n" +
			"  Get-ChildItem -LiteralPath $env:USERPROFILE -Force -Recurse -Depth 2;\n" +
			") | Where-Object -FilterScript {\n" +
			"\t$_.Attributes.HasFlag( [System.IO.FileAttributes]::ReparsePoint );\n" +
			"} | Remove-Item -Force -Recurse -Verbose;")
	}
	if c.DeleteEdgeDesktopIcon {
		m.specialize.append(`Remove-Item -LiteralPath 'C:\Users\Public\Desktop\Microsoft Edge.lnk' -ErrorAction 'SilentlyContinue' -Verbose;`)
		m.userOnce.append(`Remove-Item -LiteralPath "${env:USERPROFILE}\Desktop\Microsoft Edge.lnk" -ErrorAction 'SilentlyContinue' -Verbose;`)
	}
	if s, ok := c.ProcessAuditSettings.(EnabledProcessAuditSettings); ok {
		m.specialize.append(`auditpol.exe /set /subcategory:"{0CCE922B-69AE-11D9-BED3-505054503030}" /success:enable /failure:enable;`)
		if s.IncludeCommandLine {
			m.specialize.append(`reg.exe add "HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System\Audit" /v ProcessCreationIncludeCmdLine_Enabled /t REG_DWORD /d 1 /f;`)
		}
	}
	if c.AllowPowerShellScripts {
		m.specialize.append("Set-ExecutionPolicy -Scope 'LocalMachine' -ExecutionPolicy 'RemoteSigned' -Force;")
	}
	if c.DisableLastAccess {
		m.specialize.append("fsutil.exe behavior set disableLastAccess 1;")
	}
	if c.PreventAutomaticReboot {
		m.specialize.append(`reg.exe add "HKLM\SOFTWARE\Policies\Microsoft\Windows\WindowsUpdate\AU" /v AUOptions /t REG_DWORD /d 4 /f;
reg.exe add "HKLM\SOFTWARE\Policies\Microsoft\Windows\WindowsUpdate\AU" /v NoAutoRebootWithLoggedOnUsers /t REG_DWORD /d 1 /f;`)
		file := m.embedXMLFile("MoveActiveHours.xml", resourceXML("MoveActiveHours.xml"))
		m.specialize.append(`Register-ScheduledTask -TaskName 'MoveActiveHours' -Xml $( Get-Content -LiteralPath '` + file + `' -Raw );`)
	}
	if c.DisableFastStartup {
		m.specialize.append(`reg.exe add "HKLM\SYSTEM\CurrentControlSet\Control\Session Manager\Power" /v HiberbootEnabled /t REG_DWORD /d 0 /f;`)
	}
	if c.DisableSystemRestore {
		m.firstLogon.append(`Disable-ComputerRestore -Drive 'C:\';`)
	}
	if c.DisableWidgets {
		m.specialize.append(`reg.exe add "HKLM\SOFTWARE\Policies\Microsoft\Dsh" /v AllowNewsAndInterests /t REG_DWORD /d 0 /f;`)
	}
	if c.TurnOffSystemSounds {
		m.defaultUser.invokeFile(m.embedTextFileFromResource("TurnOffSystemSounds.ps1", nil, nil))
		m.userOnce.append(`Set-ItemProperty -LiteralPath 'Registry::HKCU\AppEvents\Schemes' -Name '(Default)' -Type 'String' -Value '.None';`)
		m.specialize.append(`reg.exe add "HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Authentication\LogonUI\BootAnimation" /v DisableStartupSound /t REG_DWORD /d 1 /f;
reg.exe add "HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\EditionOverrides" /v UserSetting_DisableStartupSound /t REG_DWORD /d 1 /f;`)
	}
	if c.DisableAppSuggestions {
		m.defaultUser.append(`$names = @(
  'ContentDeliveryAllowed';
  'FeatureManagementEnabled';
  'OEMPreInstalledAppsEnabled';
  'PreInstalledAppsEnabled';
  'PreInstalledAppsEverEnabled';
  'SilentInstalledAppsEnabled';
  'SoftLandingEnabled';
  'SubscribedContentEnabled';
  'SubscribedContent-310093Enabled';
  'SubscribedContent-338387Enabled';
  'SubscribedContent-338388Enabled';
  'SubscribedContent-338389Enabled';
  'SubscribedContent-338393Enabled';
  'SubscribedContent-353694Enabled';
  'SubscribedContent-353696Enabled';
  'SubscribedContent-353698Enabled';
  'SystemPaneSuggestionsEnabled';
);

foreach( $name in $names ) {
  reg.exe add "HKU\DefaultUser\Software\Microsoft\Windows\CurrentVersion\ContentDeliveryManager" /v $name /t REG_DWORD /d 0 /f;
}`)
		m.specialize.append(`reg.exe add "HKLM\Software\Policies\Microsoft\Windows\CloudContent" /v "DisableWindowsConsumerFeatures" /t REG_DWORD /d 1 /f;`)
	}

	installVM := func(resource string, alwaysAtFirstLogon bool) {
		target := m.firstLogon
		if !alwaysAtFirstLogon && c.IsDefenderDisabled() {
			target = m.specialize
		}
		target.invokeFile(m.embedTextFileFromResource(resource, nil, nil))
	}
	if c.VBoxGuestAdditions {
		installVM("VBoxGuestAdditions.ps1", false)
	}
	if c.VMwareTools {
		installVM("VMwareTools.ps1", false)
	}
	if c.VirtIoGuestTools {
		installVM("VirtIoGuestTools.ps1", true)
	}
	if c.ParallelsTools {
		installVM("ParallelsTools.ps1", false)
	}

	if c.PreventDeviceEncryption {
		m.specialize.append(`reg.exe add "HKLM\SYSTEM\CurrentControlSet\Control\BitLocker" /v "PreventDeviceEncryption" /t REG_DWORD /d 1 /f;`)
	}
	if c.ClassicContextMenu {
		m.userOnce.append(`reg.exe add "HKCU\Software\Classes\CLSID\{86ca1aa0-34aa-4e8b-a509-50c905bae2a2}\InprocServer32" /ve /f;`)
		m.userOnce.restartExplorer = true
	}
	if c.LeftTaskbar {
		m.defaultUser.append(`reg.exe add "` + advancedKey + `" /v TaskbarAl /t REG_DWORD /d 0 /f;`)
	}
	if c.HideEdgeFre {
		m.specialize.append(`reg.exe add "HKLM\Software\Policies\Microsoft\Edge" /v HideFirstRunExperience /t REG_DWORD /d 1 /f;`)
	}
	if c.DisableEdgeStartupBoost {
		m.specialize.append(`reg.exe add "HKLM\Software\Policies\Microsoft\Edge\Recommended" /v BackgroundModeEnabled /t REG_DWORD /d 0 /f;
reg.exe add "HKLM\Software\Policies\Microsoft\Edge\Recommended" /v StartupBoostEnabled /t REG_DWORD /d 0 /f;`)
	}

	if s, ok := c.LockKeySettings.(ConfigureLockKeySettings); ok {
		indicators := 0
		if s.CapsLock.Initial == "On" {
			indicators |= 1
		}
		if s.NumLock.Initial == "On" {
			indicators |= 2
		}
		if s.ScrollLock.Initial == "On" {
			indicators |= 4
		}
		m.defaultUser.append(fmt.Sprintf(`foreach( $root in 'Registry::HKU\.DEFAULT', 'Registry::HKU\DefaultUser' ) {
  Set-ItemProperty -LiteralPath "$root\Control Panel\Keyboard" -Name 'InitialKeyboardIndicators' -Type 'String' -Value %d -Force;
}`, indicators))
		var codes [][]byte
		if s.CapsLock.Behavior == "Ignore" {
			codes = append(codes, []byte{0, 0, 0x3A, 0})
		}
		if s.NumLock.Behavior == "Ignore" {
			codes = append(codes, []byte{0, 0, 0x45, 0})
		}
		if s.ScrollLock.Behavior == "Ignore" {
			codes = append(codes, []byte{0, 0, 0x46, 0})
		}
		if len(codes) > 0 {
			raw := make([]byte, 8, 16+4*len(codes))
			raw = binary.LittleEndian.AppendUint32(raw, uint32(len(codes)+1))
			for _, code := range codes {
				raw = append(raw, code...)
			}
			raw = append(raw, 0, 0, 0, 0)
			m.specialize.append(`Set-ItemProperty -LiteralPath 'Registry::HKLM\SYSTEM\CurrentControlSet\Control\Keyboard Layout' -Name 'Scancode Map' -Type 'Binary' -Value([convert]::FromBase64String('` + base64.StdEncoding.EncodeToString(raw) + `'));`)
		}
	}
	if c.MakeEdgeUninstallable {
		m.specialize.invokeFile(m.embedTextFileFromResource("MakeEdgeUninstallable.ps1", nil, nil))
	}
	if c.LaunchToThisPC {
		m.userOnce.append(`Set-ItemProperty -LiteralPath 'Registry::HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer\Advanced' -Name 'LaunchTo' -Type 'DWord' -Value 1;`)
	}
	if c.TaskbarSearch != "Box" {
		m.userOnce.append(fmt.Sprintf(`Set-ItemProperty -LiteralPath 'Registry::HKCU\Software\Microsoft\Windows\CurrentVersion\Search' -Name 'SearchboxTaskbarMode' -Type 'DWord' -Value %d;`, taskbarSearchValues[c.TaskbarSearch]))
		m.userOnce.restartExplorer = true
	}

	setStartPins := func(js string) {
		file := m.embedTextFileFromResource("SetStartPins.ps1", func(w *strings.Builder) {
			w.WriteString("$json = '" + strings.ReplaceAll(js, "'", "''") + "';\n")
		}, nil)
		m.specialize.invokeFile(file)
	}
	switch s := c.StartPinsSettings.(type) {
	case DefaultStartPinsSettings:
	case EmptyStartPinsSettings:
		setStartPins(`{"pinnedList":[]}`)
	case CustomStartPinsSettings:
		if !jsonReadable(s.Json) {
			fail("The string '%s' is not valid JSON.", s.Json)
		}
		setStartPins(strings.TrimFunc(s.Json, isSpace))
	default:
		panic(fmt.Sprintf("unsupported %T", s))
	}

	setStartTiles := func(xml string) {
		doc, err := ParseXML([]byte(xml))
		if err != nil {
			fail("Start menu XML is not well-formed: %s", err.Error())
		}
		m.embedXMLFile(`C:\Users\Default\AppData\Local\Microsoft\Windows\Shell\LayoutModification.xml`, doc)
	}
	switch s := c.StartTilesSettings.(type) {
	case DefaultStartTilesSettings:
	case EmptyStartTilesSettings:
		setStartTiles(emptyStartTiles)
	case CustomStartTilesSettings:
		setStartTiles(s.Xml)
	default:
		panic(fmt.Sprintf("unsupported %T", s))
	}

	if c.DeleteWindowsOld {
		m.firstLogon.append(m.cmd.shellCommand(`rmdir C:\Windows.old`) + ";")
	}
	if c.DisablePointerPrecision {
		m.defaultUser.append(`$params = @{
  LiteralPath = 'Registry::HKU\DefaultUser\Control Panel\Mouse';
  Type = 'String';
  Value = 0;
  Force = $true;
};
Set-ItemProperty @params -Name 'MouseSpeed';
Set-ItemProperty @params -Name 'MouseThreshold1';
Set-ItemProperty @params -Name 'MouseThreshold2';`)
	}
	if c.DisableBingResults {
		m.defaultUser.append(`reg.exe add "HKU\DefaultUser\Software\Policies\Microsoft\Windows\Explorer" /v DisableSearchBoxSuggestions /t REG_DWORD /d 1 /f;`)
	}

	setEffects := func(values map[Effect]bool, setting int) {
		var b strings.Builder
		for _, e := range effectOrder {
			v, ok := values[e]
			if !ok {
				continue
			}
			b.WriteString(fmt.Sprintf(`Set-ItemProperty -LiteralPath "Registry::HKEY_LOCAL_MACHINE\SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer\VisualEffects\%s" -Name 'DefaultValue' -Value %s -Type 'DWord' -Force;`, e, boolInt(v)) + "\n")
		}
		m.specialize.append(b.String())
		m.userOnce.append(fmt.Sprintf(`Set-ItemProperty -LiteralPath 'Registry::HKEY_CURRENT_USER\Software\Microsoft\Windows\CurrentVersion\Explorer\VisualEffects' -Name 'VisualFXSetting' -Type 'DWord' -Value %d -Force;`, setting))
	}
	all := func(v bool) map[Effect]bool {
		out := map[Effect]bool{}
		for _, e := range effectOrder {
			out[e] = v
		}
		return out
	}
	switch s := c.Effects.(type) {
	case DefaultEffects:
	case BestAppearanceEffects:
		setEffects(all(true), 1)
	case BestPerformanceEffects:
		setEffects(all(false), 2)
	case CustomEffects:
		values := map[Effect]bool{}
		for _, e := range s.Settings {
			values[e.Key] = e.Value
		}
		setEffects(values, 3)
	default:
		panic(fmt.Sprintf("unsupported %T", s))
	}

	switch s := c.DesktopIcons.(type) {
	case DefaultDesktopIconSettings:
	case CustomDesktopIconSettings:
		// An ImmutableSortedDictionary by icon ID; a later duplicate wins.
		values := map[string]bool{}
		icons := map[string]*DesktopIcon{}
		var ids []string
		for _, e := range s.Settings {
			icon := check(m.data.DesktopIcon(e.Key))
			if _, seen := icons[icon.ID]; !seen {
				ids = append(ids, icon.ID)
			}
			icons[icon.ID], values[icon.ID] = icon, e.Value
		}
		sort.SliceStable(ids, func(i, j int) bool { return compareCulture(ids[i], ids[j]) < 0 })
		var b strings.Builder
		for _, key := range []string{"ClassicStartMenu", "NewStartPanel"} {
			path := `Registry::HKEY_CURRENT_USER\Software\Microsoft\Windows\CurrentVersion\Explorer\HideDesktopIcons\` + key
			b.WriteString("New-Item -Path '" + path + "' -Force;\n")
			for _, id := range ids {
				b.WriteString("Set-ItemProperty -Path '" + path + "' -Name '" + icons[id].GUID + "' -Value " + boolInt(!values[id]) + " -Type 'DWord';\n")
			}
		}
		m.userOnce.append(b.String())
		m.userOnce.restartExplorer = true
	default:
		panic(fmt.Sprintf("unsupported %T", s))
	}

	switch s := c.StartFolderSettings.(type) {
	case DefaultStartFolderSettings:
	case CustomStartFolderSettings:
		values := map[string]bool{}
		folders := map[string]*StartFolder{}
		var ids []string
		for _, e := range s.Settings {
			folder := check(m.data.StartFolder(e.Key))
			if _, seen := folders[folder.ID]; !seen {
				ids = append(ids, folder.ID)
			}
			folders[folder.ID], values[folder.ID] = folder, e.Value
		}
		sort.SliceStable(ids, func(i, j int) bool { return compareCulture(ids[i], ids[j]) < 0 })
		var raw []byte
		for _, id := range ids {
			if values[id] {
				raw = append(raw, folders[id].Bytes...)
			}
		}
		m.userOnce.append(`Set-ItemProperty -Path 'Registry::HKEY_CURRENT_USER\Software\Microsoft\Windows\CurrentVersion\Start' -Name 'VisiblePlaces' -Value $( [convert]::FromBase64String('` + base64.StdEncoding.EncodeToString(raw) + `') ) -Type 'Binary';`)
	default:
		panic(fmt.Sprintf("unsupported %T", s))
	}

	if c.ShowEndTask {
		m.defaultUser.append(`reg.exe add "HKU\DefaultUser\Software\Microsoft\Windows\CurrentVersion\Explorer\Advanced\TaskbarDeveloperSettings" /v TaskbarEndTask /t REG_DWORD /d 1 /f;`)
	}

	setStickyKeys := func(flags []StickyKeys) {
		result := 0x02 | 0x08 // SKF_AVAILABLE | SKF_CONFIRMHOTKEY
		for _, f := range flags {
			result |= stickyKeyValues[f]
		}
		m.defaultUser.append(fmt.Sprintf(`reg.exe add "HKU\DefaultUser\Control Panel\Accessibility\StickyKeys" /v Flags /t REG_SZ /d %d /f;`, result))
		m.specialize.append(fmt.Sprintf(`reg.exe add "HKU\.DEFAULT\Control Panel\Accessibility\StickyKeys" /v Flags /t REG_SZ /d %d /f;`, result))
	}
	switch s := c.StickyKeysSettings.(type) {
	case DefaultStickyKeysSettings:
	case DisabledStickyKeysSettings:
		setStickyKeys(nil)
	case CustomStickyKeysSettings:
		setStickyKeys(s.Flags)
	default:
		panic(fmt.Sprintf("unsupported %T", s))
	}

	if c.DisableCoreIsolation {
		// This literal of the original keeps two spaces of indentation.
		m.specialize.append(`  reg.exe add "HKLM\System\CurrentControlSet\Control\DeviceGuard" /v "EnableVirtualizationBasedSecurity" /t REG_DWORD /d 0 /f;
  reg.exe add "HKLM\System\CurrentControlSet\Control\DeviceGuard\Scenarios\HypervisorEnforcedCodeIntegrity" /v "Enabled" /t REG_DWORD /d 0 /f;
  reg.exe add "HKLM\System\CurrentControlSet\Control\DeviceGuard\Scenarios\HypervisorEnforcedCodeIntegrity" /v "EnabledBootId" /t REG_DWORD /d 0 /f;
  reg.exe add "HKLM\System\CurrentControlSet\Control\DeviceGuard\Scenarios\HypervisorEnforcedCodeIntegrity" /v "WasEnabledBy" /t REG_DWORD /d 0 /f;`)
	}
	if c.DisableAutomaticRestartSignOn {
		m.specialize.append(`  reg.exe add "HKLM\Software\Microsoft\Windows\CurrentVersion\Policies\System" /v "DisableAutomaticRestartSignOn" /t REG_DWORD /d 1 /f;`)
	}
	if c.HideInfoTip {
		m.defaultUser.append(`reg.exe add "HKU\DefaultUser\Software\Microsoft\Windows\CurrentVersion\Explorer\Advanced" /v "ShowInfoTip" /d 0 /t REG_DWORD /f;`)
	}
	if c.DisableWpbt {
		m.specialize.append(`reg.exe add "HKLM\System\CurrentControlSet\Control\Session Manager" /v "DisableWpbtExecution" /t REG_DWORD /d 1 /f;`)
	}
	if c.PreventDeviceApps {
		m.specialize.append(`reg.exe add "HKLM\Software\Microsoft\Windows\CurrentVersion\Device Metadata" /v "PreventDeviceMetadataFromNetwork" /t REG_DWORD /d 1 /f;
reg.exe add "HKLM\Software\Microsoft\Windows\CurrentVersion\Device Installer" /v "DisableCoInstallers" /t REG_DWORD /d 1 /f;`)
	}
}
