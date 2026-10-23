<#
.SYNOPSIS
Lets a Lenovo, Dell or HP laptop trust the Microsoft UEFI CA, so it can boot
PXE Platform's signed iPXE shim with Secure Boot on.

.DESCRIPTION
Secured-core laptops ship trusting only Windows' own Secure Boot key. The
iPXE shim is signed by "Microsoft Corporation UEFI CA 2011", so such a laptop
refuses it ("Access Denied" right after the loader is downloaded).

The script:
  1. does nothing if Secure Boot is off or the firmware already trusts the CA;
  2. finds the vendor's BIOS option (Lenovo "Allow Microsoft 3rd Party UEFI
     CA", Dell "Enable Microsoft UEFI CA", HP "Enable MS UEFI CA key") through
     the vendor's built-in WMI interface, with no extra tools;
  3. turns it on with the BIOS administrator password, if one is set;
  4. suspends BitLocker for one restart, because the firmware changes its
     Secure Boot keys at the next boot and BitLocker would otherwise ask for
     the recovery key.
The change takes effect after a restart.

Passwords are never written to the log or the output.

.PARAMETER Password
The BIOS administrator (supervisor) password, when it is the same on the
machines this run targets.

.PARAMETER PasswordFile
A CSV file with columns Serial and Password: one BIOS password per machine,
looked up by the BIOS serial number. Takes priority over -Password.

.PARAMETER CheckOnly
Report what would be done, change nothing.

.PARAMETER NoBitLockerSuspend
Do not suspend BitLocker before the restart.

.PARAMETER LogPath
Where to append the log. Defaults to %WINDIR%\Temp\PXE-MicrosoftUefiCA.log.

.NOTES
Exit codes (SCCM understands 0 and 3010):
  0     nothing to do: Secure Boot off, or the CA is already trusted
  3010  the option was turned on (or already is); restart to apply
  1     unexpected error
  2     this model has no such option (or vendor WMI is missing)
  3     a BIOS password is needed, or the given one was refused
  4     not a Lenovo, Dell or HP machine
  5     the option is on but the firmware still lacks the 2011 CA (see log)
#>
[CmdletBinding()]
param(
    [string]$Password,
    [string]$PasswordFile,
    [switch]$CheckOnly,
    [switch]$NoBitLockerSuspend,
    [string]$LogPath = (Join-Path $env:WINDIR 'Temp\PXE-MicrosoftUefiCA.log')
)

$ErrorActionPreference = 'Stop'

$script:UefiCA2011 = 'Microsoft Corporation UEFI CA 2011'
$script:UefiCA2023 = 'Microsoft UEFI CA 2023'

# ---- small wrappers, so tests can replace the system calls ----------------

function Get-BiosWmi([string]$Namespace, [string]$Class) {
    Get-WmiObject -Namespace $Namespace -Class $Class -ErrorAction Stop
}

function Get-BiosCim([string]$Namespace, [string]$Class) {
    Get-CimInstance -Namespace $Namespace -ClassName $Class -ErrorAction Stop
}

function Invoke-BiosCim($InputObject, [string]$Method, [hashtable]$Arguments) {
    Invoke-CimMethod -InputObject $InputObject -MethodName $Method -Arguments $Arguments
}

function Get-SecureBootState {
    # $null: not a UEFI machine (or the cmdlet is unavailable)
    try { return [bool](Confirm-SecureBootUEFI) } catch { return $null }
}

function Get-DbText {
    $db = Get-SecureBootUEFI -Name db
    [System.Text.Encoding]::ASCII.GetString($db.Bytes)
}

function Get-Manufacturer { (Get-CimInstance -ClassName Win32_ComputerSystem).Manufacturer }

function Get-BiosSerial { ([string](Get-CimInstance -ClassName Win32_BIOS).SerialNumber).Trim() }

function Suspend-BitLockerOnce {
    $vol = Get-BitLockerVolume -MountPoint $env:SystemDrive -ErrorAction SilentlyContinue
    if ($vol -and $vol.ProtectionStatus -eq 'On') {
        Suspend-BitLocker -MountPoint $env:SystemDrive -RebootCount 1 | Out-Null
        return $true
    }
    return $false
}

function Write-UefiCALog([string]$Message) {
    $line = '{0} {1} {2}' -f (Get-Date -Format 's'), $env:COMPUTERNAME, $Message
    try { Add-Content -Path $LogPath -Value $line -Encoding UTF8 } catch { }
    Write-Verbose $line
}

# ---- what the firmware trusts ---------------------------------------------

# Test-DbTrust tells which Microsoft UEFI CAs the firmware's db holds.
function Test-DbTrust {
    $text = Get-DbText
    [pscustomobject]@{
        CA2011 = $text.Contains($script:UefiCA2011)
        CA2023 = $text.Contains($script:UefiCA2023)
    }
}

# ---- passwords -------------------------------------------------------------

function Resolve-BiosPassword([string]$Password, [string]$PasswordFile) {
    if ($PasswordFile) {
        $serial = Get-BiosSerial
        $row = Import-Csv -Path $PasswordFile | Where-Object { ([string]$_.Serial).Trim() -eq $serial } | Select-Object -First 1
        if ($row) { return [string]$row.Password }
        Write-UefiCALog "serial $serial is not in the password file"
    }
    return $Password
}

# Result of a vendor step: Status is one of Enabled (already on), Changed,
# Missing (no such option), Denied (password needed or refused), Failed.
function New-Result([string]$Status, [string]$Detail) {
    [pscustomobject]@{ Status = $Status; Detail = $Detail }
}

# ---- Lenovo ----------------------------------------------------------------

# Lenovo_BiosSetting rows look like "Allow3rdPartyUEFICA,Disable;[Optional:Disable,Enable]"
# (older models: "Name,Value"). ThinkPads call the option Allow3rdPartyUEFICA
# (Enable/Disable), ThinkCentres AllowMicrosoft3rdPartyUEFICA (Enabled/Disabled).
function ConvertFrom-LenovoSetting([string]$Line) {
    if (-not $Line) { return $null }
    $main, $rest = $Line -split ';', 2
    $name, $value = $main -split ',', 2
    $options = @()
    if ($rest -match '\[Optional:([^\]]*)\]') { $options = $Matches[1] -split ',' }
    [pscustomobject]@{ Name = $name; Value = $value; Options = $options }
}

function Set-LenovoUefiCA([string]$Pass, [bool]$CheckOnly) {
    $names = 'Allow3rdPartyUEFICA', 'AllowMicrosoft3rdPartyUEFICA'
    try {
        $rows = Get-BiosWmi 'root\wmi' 'Lenovo_BiosSetting'
    } catch {
        return New-Result 'Missing' 'Lenovo BIOS WMI interface is not available'
    }
    $setting = $null
    foreach ($row in $rows) {
        $s = ConvertFrom-LenovoSetting $row.CurrentSetting
        if ($s -and $names -contains $s.Name) { $setting = $s; break }
    }
    if (-not $setting) { return New-Result 'Missing' 'no Allow3rdPartyUEFICA option on this model' }
    if ($setting.Value -match '^Enabled?$') { return New-Result 'Enabled' "$($setting.Name)=$($setting.Value)" }

    $target = $setting.Options | Where-Object { $_ -match '^Enabled?$' } | Select-Object -First 1
    if (-not $target) { $target = if ($setting.Value -eq 'Disabled') { 'Enabled' } else { 'Enable' } }
    if ($CheckOnly) { return New-Result 'Changed' "would set $($setting.Name)=$target" }

    $set = Get-BiosWmi 'root\wmi' 'Lenovo_SetBiosSetting'
    $save = Get-BiosWmi 'root\wmi' 'Lenovo_SaveBiosSettings'
    $opcode = $null
    if ($Pass) { try { $opcode = Get-BiosWmi 'root\wmi' 'Lenovo_WmiOpcodeInterface' } catch { $opcode = $null } }

    if (-not $Pass) {
        $r = $set.SetBiosSetting("$($setting.Name),$target").return
        if ($r -eq 'Success') { $r = $save.SaveBiosSettings().return }
    } elseif ($opcode) {
        # Newer models: password through the opcode interface, between set and save.
        $r = $set.SetBiosSetting("$($setting.Name),$target").return
        if ($r -eq 'Success') { $r = $opcode.WmiOpcodeInterface("WmiOpcodePasswordAdmin:$Pass").return }
        if ($r -eq 'Success') { $r = $save.SaveBiosSettings().return }
    } else {
        # Older models: password in both calls.
        $r = $set.SetBiosSetting("$($setting.Name),$target,$Pass,ascii,us").return
        if ($r -eq 'Success') { $r = $save.SaveBiosSettings("$Pass,ascii,us").return }
    }
    switch ($r) {
        'Success'       { return New-Result 'Changed' "$($setting.Name)=$target" }
        'Access Denied' { return New-Result 'Denied' 'Lenovo: Access Denied (supervisor password needed or wrong)' }
        default         { return New-Result 'Failed' "Lenovo: $r" }
    }
}

# ---- Dell ------------------------------------------------------------------

# Dell's WMI BIOS interface exists on models released after 2018.
function Set-DellUefiCA([string]$Pass, [bool]$CheckOnly) {
    $ns = 'root\dcim\sysman\biosattributes'
    try {
        $attrs = Get-BiosCim $ns 'EnumerationAttribute'
    } catch {
        return New-Result 'Missing' 'Dell BIOS WMI interface is not available (model before 2018?); use Dell Command | Configure --MSUefiCA'
    }
    $attr = $attrs | Where-Object { $_.AttributeName -eq 'MSUefiCA' } | Select-Object -First 1
    if (-not $attr) {
        $attr = $attrs | Where-Object { $_.AttributeName -match 'UefiCA' -and $_.AttributeName -notmatch 'Legacy' } | Select-Object -First 1
    }
    if (-not $attr) { return New-Result 'Missing' 'no Microsoft UEFI CA option on this model' }
    if ($attr.CurrentValue -eq 'Enabled') { return New-Result 'Enabled' "$($attr.AttributeName)=Enabled" }
    if ($CheckOnly) { return New-Result 'Changed' "would set $($attr.AttributeName)=Enabled" }

    $iface = Get-BiosCim $ns 'BIOSAttributeInterface'
    $callArgs = @{ AttributeName = $attr.AttributeName; AttributeValue = 'Enabled'; SecType = [uint32]0; SecHndCount = [uint32]0; SecHandle = [byte[]]@() }
    if ($Pass) {
        $bytes = [System.Text.Encoding]::UTF8.GetBytes($Pass)
        $callArgs.SecType = [uint32]1
        $callArgs.SecHndCount = [uint32]$bytes.Length
        $callArgs.SecHandle = [byte[]]$bytes
    }
    $r = [int](Invoke-BiosCim $iface 'SetAttribute' $callArgs).Status
    switch ($r) {
        0       { return New-Result 'Changed' "$($attr.AttributeName)=Enabled" }
        3       { return New-Result 'Denied' 'Dell: Access Denied (admin password needed or wrong)' }
        4       { return New-Result 'Missing' 'Dell: Not Supported' }
        default { return New-Result 'Failed' "Dell: SetAttribute returned $r" }
    }
}

# ---- HP --------------------------------------------------------------------

function Set-HPUefiCA([string]$Pass, [bool]$CheckOnly) {
    $ns = 'root\HP\InstrumentedBIOS'
    try {
        $enums = Get-BiosWmi $ns 'HP_BIOSEnumeration'
    } catch {
        return New-Result 'Missing' 'HP BIOS WMI interface is not available'
    }
    $setting = $enums | Where-Object { $_.Name -eq 'Enable MS UEFI CA key' } | Select-Object -First 1
    if (-not $setting) {
        $setting = $enums | Where-Object { $_.Name -match 'MS UEFI CA' -and $_.Name -notmatch 'Ready' } | Select-Object -First 1
    }
    if (-not $setting) { return New-Result 'Missing' 'no "Enable MS UEFI CA key" option on this model' }
    if ($setting.CurrentValue -eq 'Yes') { return New-Result 'Enabled' "$($setting.Name)=Yes" }
    if ($CheckOnly) { return New-Result 'Changed' "would set $($setting.Name)=Yes" }

    $iface = Get-BiosWmi $ns 'HP_BIOSSettingInterface'
    $pw = ''
    if ($Pass) { $pw = '<utf-16/>' + $Pass }
    $r = [int]$iface.SetBIOSSetting($setting.Name, 'Yes', $pw).Return
    switch ($r) {
        0       { return New-Result 'Changed' "$($setting.Name)=Yes" }
        6       { return New-Result 'Denied' 'HP: Access Denied (setup password needed or wrong)' }
        1       { return New-Result 'Missing' 'HP: Not Supported' }
        default { return New-Result 'Failed' "HP: SetBIOSSetting returned $r" }
    }
}

# ---- main ------------------------------------------------------------------

function Invoke-Main {
    param(
        [string]$Password,
        [string]$PasswordFile,
        [switch]$CheckOnly,
        [switch]$NoBitLockerSuspend
    )
    try {
        $sb = Get-SecureBootState
        if ($null -eq $sb) { Write-UefiCALog 'not a UEFI machine: nothing to do'; return 0 }
        if (-not $sb) { Write-UefiCALog 'Secure Boot is off: nothing to do'; return 0 }

        $trust = Test-DbTrust
        if ($trust.CA2011) { Write-UefiCALog "db already has $script:UefiCA2011"; return 0 }

        $maker = [string](Get-Manufacturer)
        $pass = Resolve-BiosPassword $Password $PasswordFile
        switch -Regex ($maker) {
            '^LENOVO'                  { $result = Set-LenovoUefiCA $pass $CheckOnly.IsPresent; break }
            '^Dell'                    { $result = Set-DellUefiCA $pass $CheckOnly.IsPresent; break }
            '^(HP|Hewlett)'            { $result = Set-HPUefiCA $pass $CheckOnly.IsPresent; break }
            default {
                Write-UefiCALog "unsupported manufacturer '$maker'"
                return 4
            }
        }
        Write-UefiCALog ("{0}: {1}{2}" -f $maker, $result.Status, $(if ($result.Detail) { " - $($result.Detail)" }))

        switch ($result.Status) {
            'Changed' {
                if (-not $CheckOnly -and -not $NoBitLockerSuspend) {
                    if (Suspend-BitLockerOnce) { Write-UefiCALog 'BitLocker suspended for one restart' }
                }
                return 3010
            }
            'Enabled' {
                # On, yet the db lacks the CA: either a restart is pending, or
                # the firmware only carries the 2023 CA, which the iPXE shim
                # (signed by the 2011 CA) does not chain to.
                if ($trust.CA2023) {
                    Write-UefiCALog "option is on but db has only $script:UefiCA2023; the iPXE shim needs $script:UefiCA2011"
                    return 5
                }
                Write-UefiCALog 'option is on, db not updated yet: restart pending'
                return 3010
            }
            'Missing' { return 2 }
            'Denied'  { return 3 }
            default   { return 1 }
        }
    } catch {
        Write-UefiCALog "error: $($_.Exception.Message)"
        return 1
    }
}

if ($MyInvocation.InvocationName -ne '.') {
    $code = Invoke-Main -Password $Password -PasswordFile $PasswordFile -CheckOnly:$CheckOnly -NoBitLockerSuspend:$NoBitLockerSuspend
    Write-Output "exit $code (log: $LogPath)"
    exit $code
}
