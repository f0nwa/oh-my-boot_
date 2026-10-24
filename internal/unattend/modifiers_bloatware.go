package unattend

import (
	"sort"
	"strings"
)

// remover is Remover<T>: one script for all steps of one kind.
type remover struct {
	kind, getCommand, filterCommand, removeCommand string
	selectors                                      []string
}

const removeProcess = "{\n" +
	"  [CmdletBinding()]\n" +
	"  param(\n" +
	"    [Parameter( Mandatory, ValueFromPipeline )]\n" +
	"    $InputObject\n" +
	"  );\n" +
	"  process {\n" +
	"    $InputObject | %s;\n" +
	"  }\n" +
	"}"

func newRemovers() (pkg, capability, feature *remover) {
	pkg = &remover{
		kind:          "Package",
		getCommand:    "{\n  Get-AppxProvisionedPackage -Online;\n}",
		filterCommand: "{\n  $_.DisplayName -eq $selector;\n}",
		removeCommand: strings.Replace(removeProcess, "%s", "Remove-AppxProvisionedPackage -AllUsers -Online -ErrorAction 'Continue'", 1),
	}
	capability = &remover{
		kind:          "Capability",
		getCommand:    "{\n  Get-WindowsCapability -Online | Where-Object -Property 'State' -NotIn -Value @(\n    'NotPresent';\n    'Removed';\n  );\n}",
		filterCommand: "{\n  ($_.Name -split '~')[0] -eq $selector;\n}",
		removeCommand: strings.Replace(removeProcess, "%s", "Remove-WindowsCapability -Online -ErrorAction 'Continue'", 1),
	}
	feature = &remover{
		kind:          "Feature",
		getCommand:    "{\n  Get-WindowsOptionalFeature -Online | Where-Object -Property 'State' -NotIn -Value @(\n    'Disabled';\n    'DisabledWithPayloadRemoved';\n  );\n}",
		filterCommand: "{\n  $_.FeatureName -eq $selector;\n}",
		removeCommand: strings.Replace(removeProcess, "%s", "Disable-WindowsOptionalFeature -Online -Remove -NoRestart -ErrorAction 'Continue'", 1),
	}
	return
}

func (r *remover) save(m *modifierContext) {
	if len(r.selectors) == 0 {
		return
	}
	var w strings.Builder
	w.WriteString("$selectors = @(\n")
	for _, s := range r.selectors {
		w.WriteString("\t'" + s + "';\n")
	}
	w.WriteString(");\n")
	w.WriteString("$getCommand = " + r.getCommand + ";\n")
	w.WriteString("$filterCommand = " + r.filterCommand + ";\n")
	w.WriteString("$removeCommand = " + r.removeCommand + ";\n")
	w.WriteString("$type = '" + r.kind + "';\n")
	w.WriteString(resourceString("RemoveBloatware.ps1"))
	m.specialize.invokeFile(m.embedTextFile("Remove"+r.kind+".ps1", w.String(), nil, nil))
}

func bloatwareModifier(m *modifierContext) {
	// A set in the original: each bloatware once, sorted by ID.
	seen := map[string]bool{}
	var list []*Bloatware
	for _, id := range m.config.Bloatwares {
		bw := check(m.data.Bloatware(id))
		if !seen[key(bw.ID)] {
			seen[key(bw.ID)] = true
			list = append(list, bw)
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return compareCulture(list[i].ID, list[j].ID) < 0 })

	pkg, capability, feature := newRemovers()
	for _, bw := range list {
		for _, step := range bw.Steps {
			switch step.Kind {
			case "Package":
				pkg.selectors = append(pkg.selectors, step.Selector)
			case "Capability":
				capability.selectors = append(capability.selectors, step.Selector)
			case "OptionalFeature":
				feature.selectors = append(feature.selectors, step.Selector)
			case "Custom":
				customBloatwareStep(m, bw.ID)
			default:
				panic("unsupported bloatware step " + step.Kind)
			}
		}
	}
	pkg.save(m)
	capability.save(m)
	feature.save(m)
}

func customBloatwareStep(m *modifierContext, id string) {
	switch id {
	case "RemoveOneDrive":
		m.specialize.append("@(\n" +
			"  'C:\\Users\\Default\\AppData\\Roaming\\Microsoft\\Windows\\Start Menu\\Programs\\OneDrive.lnk';\n" +
			"  'C:\\Windows\\System32\\OneDriveSetup.exe';\n" +
			"  'C:\\Windows\\SysWOW64\\OneDriveSetup.exe';\n" +
			") | Where-Object -FilterScript { [System.IO.File]::Exists( $_ ); } | Remove-Item -Verbose -ErrorAction 'Continue';")
		m.defaultUser.append(`Remove-ItemProperty -LiteralPath 'Registry::HKU\DefaultUser\Software\Microsoft\Windows\CurrentVersion\Run' -Name 'OneDriveSetup' -Force -ErrorAction 'Continue';`)
	case "RemoveTeams":
		m.specialize.append(`reg.exe add "HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Communications" /v ConfigureChatAutoInstall /t REG_DWORD /d 0 /f;`)
	case "RemoveNotepad":
		m.specialize.append(`reg.exe add "HKCR\.txt\ShellNew" /v ItemName /t REG_EXPAND_SZ /d "@C:\Windows\system32\notepad.exe,-470" /f;` + "\n" +
			`reg.exe add "HKCR\.txt\ShellNew" /v NullFile /t REG_SZ /f;` + "\n" +
			`reg.exe add "HKCR\txtfilelegacy" /v FriendlyTypeName /t REG_EXPAND_SZ /d "@C:\Windows\system32\notepad.exe,-469" /f;` + "\n" +
			`reg.exe add "HKCR\txtfilelegacy" /ve /t REG_SZ /d "Text Document" /f;`)
		m.defaultUser.append(`reg.exe add "HKU\DefaultUser\Software\Microsoft\Notepad" /v ShowStoreBanner /t REG_DWORD /d 0 /f;`)
	case "RemoveOutlook":
		m.specialize.append(`Remove-Item -LiteralPath 'Registry::HKLM\Software\Microsoft\WindowsUpdate\Orchestrator\UScheduler_Oobe\OutlookUpdate' -Force -ErrorAction 'SilentlyContinue';`)
	case "RemoveDevHome":
		m.specialize.append(`Remove-Item -LiteralPath 'Registry::HKLM\Software\Microsoft\WindowsUpdate\Orchestrator\UScheduler_Oobe\DevHomeUpdate' -Force -ErrorAction 'SilentlyContinue';`)
	case "RemoveCopilot":
		m.userOnce.append(`Get-AppxPackage -Name 'Microsoft.Windows.Ai.Copilot.Provider' | Remove-AppxPackage;`)
		m.defaultUser.append(`reg.exe add "HKU\DefaultUser\Software\Policies\Microsoft\Windows\WindowsCopilot" /v TurnOffWindowsCopilot /t REG_DWORD /d 1 /f;`)
	case "RemoveXboxApps":
		m.defaultUser.append(`reg.exe add "HKU\DefaultUser\Software\Microsoft\Windows\CurrentVersion\GameDVR" /v AppCaptureEnabled /t REG_DWORD /d 0 /f;`)
	case "RemoveInternetExplorer":
		m.defaultUser.append(`reg.exe add "HKU\DefaultUser\Software\Microsoft\Internet Explorer\LowRegistry\Audio\PolicyConfig\PropertyStore" /f;`)
	default:
		panic("unsupported custom bloatware step for " + id)
	}
}
