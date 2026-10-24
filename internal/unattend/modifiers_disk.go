package unattend

import (
	"fmt"
	"regexp"
	"strings"
)

// DiskModifier: the windowsPE pass, either left to setup.exe or replaced by
// a generated or custom pe.cmd.

const (
	pePEScript       = `X:\pe.cmd`
	peDiskpartScript = `X:\diskpart.txt`
	peTargetScript   = `X:\target.vbs`
	peTargetOutput   = `X:\target.out`
	letterSystem     = "S"
	letterWindows    = "W"
	letterRecovery   = "R"
)

// lineWriter is the StringWriter of the original: WriteLine adds "\n".
type lineWriter struct{ strings.Builder }

func (w *lineWriter) line(s string) { w.WriteString(s + "\n") }

func diskModifier(m *modifierContext) {
	cmdPE := false
	switch m.config.PESettings.(type) {
	case GeneratePESettings, ScriptPESetttings:
		cmdPE = true
	}
	if cmdPE {
		var forbidden []string
		for _, c := range m.config.Components {
			if c.Key.Pass == "windowsPE" {
				forbidden = append(forbidden, "\u2018"+c.Key.Component+"\u2019")
			}
		}
		if len(forbidden) > 0 {
			fail("Cannot create .cmd script with custom components (%s) for the \u2018windowsPE\u2019 pass. To load drivers in the PE stage, add them to the \u2018$WinPEDriver$\u2019 folder or use a custom script to run the \u2018drvload.exe\u2019 command.", strings.Join(forbidden, ", "))
		}
		for _, n := range m.all("/u:unattend/u:settings[@pass='windowsPE']/*") {
			n.RemoveSelf()
		}
		if m.config.UseNarrator {
			m.appendCommand(placeWindowsPE, m.cmd.shellCommand(`start X:\Windows\System32\Narrator.exe`))
		}
	}
	var lines []string
	switch s := m.config.PESettings.(type) {
	case ScriptPESetttings:
		lines = splitLines(s.Script)
	case GeneratePESettings:
		lines = peScript(m, s)
	case DefaultPESettings:
	default:
		panic(fmt.Sprintf("unsupported %T", s))
	}
	copy := m.must("//s:PEScriptCopy")
	if len(lines) == 0 {
		copy.RemoveSelf()
		return
	}
	m.appendCommand(placeWindowsPE, append(m.cmd.writeToFilePE(pePEScript, lines), m.cmd.shellCommand(pePEScript))...)
	insertTextWithIndentation(copy, joinLines(lines))
}

// targetDiskScript is DiskModifier.GetTargetDiskScript.
func targetDiskScript(g GeneratedTargetDiskSettings) []string {
	var w lineWriter
	w.line("Function Fail(message)\n" +
		"  WScript.Echo message\n" +
		"  WScript.Quit 1\n" +
		"End Function\n" +
		"\n" +
		"On Error Resume Next\n" +
		"Set wmi = GetObject(\"winmgmts:\\\\.\\root\\cimv2\")\n" +
		"Set drives = wmi.InstancesOf(\"Win32_DiskDrive\")\n" +
		"If Err.Number <> 0 Then\n" +
		"  Fail \"Could not enumerate disks: \" & Err.Description\n" +
		"End If\n" +
		"Set accepted = CreateObject(\"Scripting.Dictionary\")\n" +
		"\n" +
		"For Each drive In drives\n" +
		"  accept = True\n")
	if g.AssertInterfaceType {
		w.line("  actual = drive.InterfaceType\n" +
			"  If actual <> \"IDE\" And actual <> \"SCSI\" Then\n" +
			"    accept = False\n" +
			"  End If\n")
	}
	if g.AssertMediaType {
		w.line("  actual = drive.MediaType\n" +
			"  If actual <> \"Fixed hard disk media\" Then\n" +
			"    accept = False\n" +
			"  End If\n")
	}
	if g.MinSizeGiB != nil {
		w.line(fmt.Sprintf("  actual = CInt(drive.Size / 1024 / 1024 / 1024)\n"+
			"  expected = %d\n"+
			"  If actual < expected Then\n"+
			"    accept = False\n"+
			"  End If\n", *g.MinSizeGiB))
	}
	if g.MaxSizeGiB != nil {
		w.line(fmt.Sprintf("  actual = CInt(drive.Size / 1024 / 1024 / 1024)\n"+
			"  expected = %d\n"+
			"  If actual > expected Then\n"+
			"    accept = False\n"+
			"  End If\n", *g.MaxSizeGiB))
	}
	if g.Index != nil {
		w.line(fmt.Sprintf("  actual = drive.Index\n"+
			"  expected = %d\n"+
			"  If actual <> expected Then\n"+
			"    accept = False\n"+
			"  End If\n", *g.Index))
	}
	if g.AssertNoPartitions {
		w.line("  actual = drive.Partitions\n" +
			"  If actual > 0 Then\n" +
			"    accept = False\n" +
			"  End If\n")
	}
	w.line("  If accept Then\n" +
		"    accepted.Add drive.Index, \"\"\n" +
		"  End If\n" +
		"Next\n" +
		"\n" +
		"If accepted.Count = 0 Then\n" +
		"  Fail \"No disk satisfied the given criteria.\"\n" +
		"ElseIf accepted.Count > 1 Then\n" +
		"  Fail \"Several disks (\" & Join(accepted.Keys, \", \") & \") satisfied the given criteria.\"\n" +
		"Else\n" +
		"  WScript.Echo Join(accepted.Keys)\n" +
		"  WScript.Quit 0\n" +
		"End If")
	return splitLines(w.String())
}

// diskpartScript is DiskModifier.GetDiskpartScript.
func diskpartScript(s UnattendedPartitionSettings, layout PartitionLayout) []string {
	ifRecovery := func(line string) string {
		if s.RecoveryMode == "Partition" {
			return line
		}
		return ""
	}
	var disk string
	switch t := s.TargetDiskSettings.(type) {
	case FixedTargetDiskSettings:
		disk = fmt.Sprint(t.Index)
	case GeneratedTargetDiskSettings, ScriptTargetDiskSettings, InteractiveTargetDiskSettings:
		disk = "%TARGET_DISK%"
	default:
		panic(fmt.Sprintf("unsupported %T", t))
	}
	switch layout {
	case "MBR":
		return []string{
			"SELECT DISK=" + disk,
			"CLEAN",
			fmt.Sprintf("CREATE PARTITION PRIMARY SIZE=%d", s.SystemSize),
			`FORMAT QUICK FS=NTFS LABEL="System"`,
			"ASSIGN LETTER=" + letterSystem,
			"ACTIVE",
			"CREATE PARTITION PRIMARY",
			ifRecovery(fmt.Sprintf("SHRINK MINIMUM=%d", s.RecoverySize)),
			`FORMAT QUICK FS=NTFS LABEL="Windows"`,
			"ASSIGN LETTER=" + letterWindows,
			ifRecovery("CREATE PARTITION PRIMARY"),
			ifRecovery(`FORMAT QUICK FS=NTFS LABEL="Recovery"`),
			ifRecovery("ASSIGN LETTER=" + letterRecovery),
			ifRecovery("SET ID=27"),
		}
	case "GPT":
		return []string{
			"SELECT DISK=" + disk,
			"CLEAN",
			"CONVERT GPT",
			fmt.Sprintf("CREATE PARTITION EFI SIZE=%d", s.SystemSize),
			`FORMAT QUICK FS=FAT32 LABEL="System"`,
			"ASSIGN LETTER=" + letterSystem,
			"CREATE PARTITION MSR SIZE=16",
			"CREATE PARTITION PRIMARY",
			ifRecovery(fmt.Sprintf("SHRINK MINIMUM=%d", s.RecoverySize)),
			`FORMAT QUICK FS=NTFS LABEL="Windows"`,
			"ASSIGN LETTER=" + letterWindows,
			ifRecovery("CREATE PARTITION PRIMARY"),
			ifRecovery(`FORMAT QUICK FS=NTFS LABEL="Recovery"`),
			ifRecovery("ASSIGN LETTER=" + letterRecovery),
			ifRecovery(`SET ID="de94bba4-06d1-4d40-a16a-bfd50179d6ac"`),
			ifRecovery("GPT ATTRIBUTES=0x8000000000000001"),
		}
	}
	panic("unsupported partition layout " + string(layout))
}

// lettersExcept lists drive letters A–Z without the given ones.
func lettersExcept(skip string) string {
	var out []string
	for c := 'A'; c <= 'Z'; c++ {
		if !strings.ContainsRune(skip, c) {
			out = append(out, string(c))
		}
	}
	return strings.Join(out, " ")
}

func checkDriveLetter(lines []string, letter, purpose string) {
	re := regexp.MustCompile(`(?i)^\s*ASSIGN\s+LETTER((\s+)|(\s*=\s*))((` + letter + `)|("` + letter + `"))\s*$`)
	for _, l := range lines {
		if re.MatchString(l) {
			return
		}
	}
	fail("Your diskpart script must contain a line such as \u2018ASSIGN LETTER=%s\u2019 to assign the drive letter \u2018%s:\u2019 to the %s partition.", letter, letter, purpose)
}

// peScript is DiskModifier.GetPEScript.
func peScript(m *modifierContext, pe GeneratePESettings) []string {
	c := m.config
	var w lineWriter
	include := func(path string, lines []string, escape bool) {
		if len(lines) == 0 {
			return
		}
		w.line(">" + path + " (")
		for _, l := range echoProcess(lines, escape) {
			w.line("    " + l)
		}
		w.line(")")
		w.line("")
	}

	w.line("@echo off\n")

	if ls, ok := c.LanguageSettings.(UnattendedLanguageSettings); ok {
		locale := check(m.data.Locale(ls.LocaleAndKeyboard.Locale))
		keyboard := check(m.data.Keyboard(ls.LocaleAndKeyboard.Keyboard))
		w.line("call :print \"Setting keyboard layout for PE session\"\n" +
			"wpeutil.exe SetKeyboardLayout " + locale.LCID + ":" + keyboard.ID + "\n")
	}

	w.line("for %%d in (" + lettersExcept("AB"+letterSystem+letterWindows+letterRecovery) + ") do (\n" +
		"    if exist %%d:\\sources\\install.wim set \"IMAGE_FILE=%%d:\\sources\\install.wim\"\n" +
		"    if exist %%d:\\sources\\install.esd set \"IMAGE_FILE=%%d:\\sources\\install.esd\"\n" +
		"    if exist %%d:\\sources\\install.swm set \"IMAGE_FILE=%%d:\\sources\\install.swm\" & set \"SWM_PARAM=/SWMFile:%%d:\\sources\\install*.swm\"\n" +
		"    if exist %%d:\\autounattend.xml set \"XML_FILE=%%d:\\autounattend.xml\"\n" +
		"    if exist %%d:\\$OEM$ set \"OEM_FOLDER=%%d:\\$OEM$\"\n" +
		"    if exist %%d:\\$WinPEDriver$ set \"PEDRIVERS_FOLDER=%%d:\\$WinPEDriver$\"")
	if c.VirtIoGuestTools {
		w.line("if exist %%d:\\virtio-win-guest-tools.exe set \"VIRTIO_DRIVE=%%d:\"")
	}
	w.line(")\n" +
		"for /f \"tokens=3\" %%t in ('reg.exe query HKLM\\System\\Setup /v UnattendFile 2^>nul') do ( if exist %%t set \"XML_FILE=%%t\" )\n" +
		"if not defined IMAGE_FILE call :fail \"Could not locate install.wim, install.esd or install.swm.\"\n" +
		"if not defined XML_FILE call :fail \"Could not locate autounattend.xml.\"\n")

	w.line("set \"OS_VERSION=11\"\n" +
		"for /f \"tokens=3 delims=.\" %%v in ('ver') do (\n" +
		"    if %%v LSS 20000 set \"OS_VERSION=10\"\n" +
		")\n")

	w.line("if defined PEDRIVERS_FOLDER (\n" +
		"    call :print \"Loading drivers from %PEDRIVERS_FOLDER%\"\n" +
		"    for /R %PEDRIVERS_FOLDER% %%f IN (*.inf) do drvload.exe \"%%f\"\n" +
		")\n")

	if c.VirtIoGuestTools {
		w.line("if defined VIRTIO_DRIVE (\n" +
			"    call :print \"Loading VirtIO drivers from %VIRTIO_DRIVE%\"\n" +
			"    drvload.exe \"%VIRTIO_DRIVE%\\vioscsi\\w%OS_VERSION%\\%PROCESSOR_ARCHITECTURE%\\vioscsi.inf\"\n" +
			"    drvload.exe \"%VIRTIO_DRIVE%\\viostor\\w%OS_VERSION%\\%PROCESSOR_ARCHITECTURE%\\viostor.inf\"\n" +
			"    drvload.exe \"%VIRTIO_DRIVE%\\NetKVM\\w%OS_VERSION%\\%PROCESSOR_ARCHITECTURE%\\netkvm.inf\"\n" +
			")\n")
	}

	runTargetDiskScript := func(lines []string) {
		include(peTargetScript, lines, true)
		w.line("call :print \"Determining target disk\"\n" +
			"(cscript.exe //E:vbscript \"" + peTargetScript + "\" //Nologo >" + peTargetOutput + ") || (type " + peTargetOutput + " & call :fail \"Could not determine target disk. Windows Setup will halt to avoid potential data loss.\")\n" +
			"for /f %%t in (" + peTargetOutput + ") do set \"TARGET_DISK=%%t\"\n")
	}
	if ups, ok := pe.PartitionSettings.(UnattendedPartitionSettings); ok {
		switch t := ups.TargetDiskSettings.(type) {
		case InteractiveTargetDiskSettings:
			w.line("echo list disk | diskpart.exe\n" +
				"echo:\n" +
				":choice\n" +
				"set /p \"CHOICE=Enter index of the disk you want to install Windows to: \" || goto :choice\n" +
				"set \"TARGET_DISK=%CHOICE%\"\n")
		case ScriptTargetDiskSettings:
			runTargetDiskScript(splitLines(t.Script))
		case GeneratedTargetDiskSettings:
			runTargetDiskScript(targetDiskScript(t))
		case FixedTargetDiskSettings:
		default:
			panic(fmt.Sprintf("unsupported %T", t))
		}
	}

	includeDiskpart := func(path string, lines []string, escape bool) {
		checkDriveLetter(lines, letterWindows, "Windows")
		checkDriveLetter(lines, letterSystem, "system")
		include(path, lines, escape)
	}
	execute := func(path, message string) {
		w.line("call :print \"" + message + "\"")
		if pe.PauseBeforeFormatting {
			w.line("pause")
		}
		w.line("diskpart.exe /s " + path + " || call :fail \"diskpart.exe encountered an error.\"\n")
	}

	w.line("wpeutil.exe UpdateBootInfo\n" +
		"for /f \"tokens=3\" %%t in ('reg.exe query HKLM\\System\\CurrentControlSet\\Control /v PEFirmwareType') do (\n" +
		"    if %%t == 0x1 (\n" +
		"        set \"LAYOUT=MBR\"\n" +
		"        set \"FIRMWARE=BIOS\"\n" +
		"    ) else if %%t == 0x2 (\n" +
		"        set \"LAYOUT=GPT\"\n" +
		"        set \"FIRMWARE=UEFI\"\n" +
		"    ) else (\n" +
		"        call :fail \"Unexpected PEFirmwareType value %%t.\"\n" +
		"    )\n" +
		")\n" +
		"call :print \"The computer is booted in %FIRMWARE% mode, hence the target disk must be configured with the %LAYOUT% partition layout\"")

	switch s := pe.PartitionSettings.(type) {
	case CustomPartitionSettings:
		includeDiskpart(peDiskpartScript, splitLines(s.Script), true)
		execute(peDiskpartScript, "diskpart will now execute your script")
	case UnattendedPartitionSettings:
		message := "diskpart will now wipe, partition and format disk %TARGET_DISK%"
		if s.PartitionLayout == "Automatic" {
			for _, layout := range []PartitionLayout{"GPT", "MBR"} {
				includeDiskpart(`X:\`+string(layout)+".txt", diskpartScript(s, layout), false)
			}
			execute(`X:\%LAYOUT%.txt`, message)
		} else {
			includeDiskpart(peDiskpartScript, diskpartScript(s, s.PartitionLayout), false)
			execute(peDiskpartScript, message)
		}
	case InteractivePartitionSettings:
		w.line("call :print ^\"Press Shift+F10 to open a new console window, then use diskpart to partition and format the disk manually. Make sure to assign the drive letters " + letterWindows + " and " + letterSystem + " ^\n" +
			"to the Windows and system partitions, respectively. When finished, continue with Windows Setup in this window.^\"\n" +
			"pause")
	default:
		panic(fmt.Sprintf("unsupported %T", s))
	}

	switch s := pe.InstallFromSettings.(type) {
	case IndexInstallFromSettings:
		w.line(fmt.Sprintf("set \"IMG_PARAM=/Index:%d\"", s.Index))
	case NameInstallFromSettings:
		w.line("set \"IMG_PARAM=/Name:\"" + s.Name + "\"\"")
	case InteractiveInstallFromSettings:
		w.line("dism.exe /Get-WimInfo /WimFile:\"%IMAGE_FILE%\"\n" +
			"echo:\n" +
			":choice\n" +
			"set /p \"CHOICE=Enter index of the image you want to install: \" || goto :choice\n" +
			"set \"IMG_PARAM=/Index:%CHOICE%\"")
	case EditionInstallFromSettings:
		w.line("set \"IMG_PARAM=/Name:\"Windows %OS_VERSION% " + check(m.data.Edition(s.Edition)).DisplayName + "\"\"")
	default:
		panic(fmt.Sprintf("unsupported %T", s))
	}

	compact, integrity := "", " /CheckIntegrity /Verify"
	if pe.CompactOs {
		compact = " /Compact"
	}
	if pe.SkipIntegrityCheck {
		integrity = ""
	}
	w.line("call :print \"Applying Windows image to target disk\"\n" +
		"dism.exe /Apply-Image /ImageFile:%IMAGE_FILE% %SWM_PARAM% %IMG_PARAM% /ApplyDir:" + letterWindows + ":\\" + compact + integrity + " || call :fail \"dism.exe encountered an error.\"\n" +
		"\n" +
		"call :print \"Making system partition bootable\"\n" +
		"bcdboot.exe " + letterWindows + ":\\Windows /s " + letterSystem + ": || call :fail \"bcdboot.exe encountered an error.\"\n" +
		"if %LAYOUT% == GPT (\n" +
		"    bcdedit.exe /set {fwbootmgr} bootsequence {bootmgr} || call :fail \"bcdedit.exe encountered an error.\"\n" +
		")\n")

	deleteWinRE := func() {
		w.line("call :print \"Deleting winre.wim file to avoid creation of recovery partition\"\n" +
			"del " + letterWindows + ":\\Windows\\System32\\Recovery\\winre.wim\n")
	}
	switch s := pe.PartitionSettings.(type) {
	case UnattendedPartitionSettings:
		switch s.RecoveryMode {
		case "None":
			deleteWinRE()
		case "Partition":
		default:
			panic("unsupported recovery mode " + string(s.RecoveryMode))
		}
	case CustomPartitionSettings:
		script := strings.ToLower(s.Script)
		found := false
		for _, k := range []string{"SET ID=27", `LABEL="Recovery"`, `SET ID="de94bba4-06d1-4d40-a16a-bfd50179d6ac"`} {
			found = found || strings.Contains(script, strings.ToLower(k))
		}
		if !found {
			deleteWinRE()
		}
	}

	w.line("call :print \"Copying answer file to target disk\"\n" +
		"mkdir " + letterWindows + ":\\Windows\\Panther\n" +
		"copy %XML_FILE% " + letterWindows + ":\\Windows\\Panther\\unattend.xml\n")

	w.line("if defined PEDRIVERS_FOLDER (\n" +
		"    call :print \"Adding drivers from %PEDRIVERS_FOLDER% to new installation\"\n" +
		"    dism.exe /Add-Driver /Image:" + letterWindows + ":\\ /Driver:\"%PEDRIVERS_FOLDER%\" /Recurse\n" +
		")\n")

	if c.VirtIoGuestTools {
		w.line("if defined VIRTIO_DRIVE (\n" +
			"    call :print \"Adding VirtIO drivers from %VIRTIO_DRIVE% to new installation\"\n" +
			"    dism.exe /Add-Driver /Image:" + letterWindows + ":\\ /Driver:\"%VIRTIO_DRIVE%\\vioscsi\\w%OS_VERSION%\\%PROCESSOR_ARCHITECTURE%\\vioscsi.inf\"\n" +
			"    dism.exe /Add-Driver /Image:" + letterWindows + ":\\ /Driver:\"%VIRTIO_DRIVE%\\viostor\\w%OS_VERSION%\\%PROCESSOR_ARCHITECTURE%\\viostor.inf\"\n" +
			"    dism.exe /Add-Driver /Image:" + letterWindows + ":\\ /Driver:\"%VIRTIO_DRIVE%\\NetKVM\\w%OS_VERSION%\\%PROCESSOR_ARCHITECTURE%\\netkvm.inf\"\n" +
			")\n")
	}

	if tz, ok := c.TimeZoneSettings.(ExplicitTimeZoneSettings); ok {
		w.line("call :print \"Setting time zone\"\n" +
			"dism.exe /Image:" + letterWindows + ":\\ /Set-TimeZone:\"" + check(m.data.TimeOffset(tz.TimeZone)).ID + "\"\n")
	}

	const loadSystem = "reg.exe LOAD HKLM\\mount " + letterWindows + ":\\Windows\\System32\\config\\SYSTEM\n"
	if pe.Disable8Dot3Names {
		w.line("call :print \"Disabling 8.3 file names\"\n" +
			"fsutil.exe 8dot3name set " + letterWindows + ": 1\n" +
			"fsutil.exe 8dot3name strip /s /f " + letterWindows + ":\\\n" +
			loadSystem +
			"reg.exe ADD HKLM\\mount\\ControlSet001\\Control\\FileSystem /v NtfsDisable8dot3NameCreation /t REG_DWORD /d 1 /f\n" +
			"reg.exe UNLOAD HKLM\\mount\n")
	}
	if pe.DisableDefender {
		w.line("call :print \"Disabling Windows Defender\"\n" +
			loadSystem +
			"for %%s in (Sense WdBoot WdFilter WdNisDrv WdNisSvc WinDefend) do reg.exe ADD HKLM\\mount\\ControlSet001\\Services\\%%s /v Start /t REG_DWORD /d 4 /f\n" +
			"reg.exe UNLOAD HKLM\\mount\n")
	}
	if c.DisableWpbt {
		w.line("call :print \"Disabling WPBT\"\n" +
			loadSystem +
			"reg.exe add \"HKLM\\mount\\ControlSet001\\Control\\Session Manager\" /v DisableWpbtExecution /t REG_DWORD /d 1 /f\n" +
			"reg.exe UNLOAD HKLM\\mount\n")
	}
	pagingFile := func(data string) {
		w.line("call :print \"Configuring paging file\"\n" +
			loadSystem +
			"reg.exe add \"HKLM\\mount\\ControlSet001\\Control\\Session Manager\\Memory Management\" /v PagingFiles /t REG_MULTI_SZ /f " + data + "\n" +
			"reg.exe UNLOAD HKLM\\mount\n")
	}
	switch s := pe.PagingFileSettings.(type) {
	case AutomaticPagingFileSettings:
	case NoPagingFileSettings:
		pagingFile("")
	case CustomPagingFileSettings:
		pagingFile(fmt.Sprintf(`/d "C:\pagefile.sys %d %d"`, s.InitialSizeMiB, s.MaxSizeMiB))
	}
	if ls, ok := c.LanguageSettings.(UnattendedLanguageSettings); ok {
		geo := check(m.data.Geo(ls.GeoLocation))
		w.line("call :print \"Setting device setup region to " + geo.DisplayName + " (GeoID " + geo.ID + ")\"\n" +
			"reg.exe LOAD HKLM\\mount " + letterWindows + ":\\Windows\\System32\\config\\SOFTWARE\n" +
			"reg.exe ADD \"HKLM\\mount\\Microsoft\\Windows\\CurrentVersion\\Control Panel\\DeviceRegion\" /v DeviceRegion /t REG_DWORD /d " + geo.ID + " /f\n" +
			"reg.exe UNLOAD HKLM\\mount\n")
	}
	if c.UseConfigurationSet {
		w.line("set \"ROBOCOPY_ARGS=/E /XX /COPY:DAT /DCOPY:DAT /R:0\"\n" +
			"if defined OEM_FOLDER (\n" +
			"    call :print \"Copying contents of %OEM_FOLDER%\"\n" +
			"    if exist \"%OEM_FOLDER%\\$$\" robocopy.exe \"%OEM_FOLDER%\\$$\" " + letterWindows + ":\\Windows %ROBOCOPY_ARGS%\n" +
			"    if exist \"%OEM_FOLDER%\\$1\" robocopy.exe \"%OEM_FOLDER%\\$1\" " + letterWindows + ":\\ %ROBOCOPY_ARGS%\n" +
			"    for %%d in (" + lettersExcept("AB") + ") do (\n" +
			"        if exist \"%OEM_FOLDER%\\%%d\" robocopy.exe \"%OEM_FOLDER%\\%%d\" %%d:\\ %ROBOCOPY_ARGS%\n" +
			"    )\n" +
			")\n")
	}

	w.line("call :print \"Computer will now reboot\"")
	if pe.PauseBeforeReboot {
		w.line("pause")
	}
	w.line("wpeutil.exe reboot\n" +
		"goto :eof\n" +
		"\n" +
		":fail\n" +
		"echo:\n" +
		"echo:Fatal error: %~1\n" +
		"echo:\n" +
		"pause\n" +
		"exit 1\n" +
		"\n" +
		":print\n" +
		"echo:\n" +
		"echo:*** %~1 ***\n" +
		"echo:\n" +
		"goto :eof")
	return splitLines(w.String())
}
