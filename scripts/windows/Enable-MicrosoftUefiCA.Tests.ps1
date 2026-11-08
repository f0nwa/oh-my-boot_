# Pester 5 tests: Invoke-Pester ./scripts/windows
BeforeAll {
    $script:LogPath = Join-Path ([IO.Path]::GetTempPath()) "uefica-test-$PID.log"
    . (Join-Path $PSScriptRoot 'Enable-MicrosoftUefiCA.ps1') -LogPath $script:LogPath

    # A WMI object whose methods record their calls and answer from a queue.
    function New-FakeWmi([string[]]$Methods, [hashtable]$Answers) {
        $o = [pscustomobject]@{ Calls = [System.Collections.Generic.List[string]]::new() }
        foreach ($m in $Methods) {
            $body = [scriptblock]::Create(@"
`$this.Calls.Add('$m(' + (`$args -join '|') + ')')
`$a = `$script:answers['$m']
if (`$a -is [System.Collections.Queue]) { `$a = `$a.Dequeue() }
`$a
"@)
            $o | Add-Member -MemberType ScriptMethod -Name $m -Value $body
        }
        $script:answers = $Answers
        $o
    }
}

Describe 'Invoke-Main' {
    BeforeEach {
        Mock Get-SecureBootState { $true }
        Mock Test-DbTrust { [pscustomobject]@{ CA2011 = $false; CA2023 = $false } }
        Mock Suspend-BitLockerOnce { $true }
        Mock Get-BiosSerial { 'PF3ABC12' }
    }

    It 'does nothing with Secure Boot off' {
        Mock Get-SecureBootState { $false }
        Invoke-Main | Should -Be 0
    }

    It 'does nothing when db already trusts the 2011 CA' {
        Mock Test-DbTrust { [pscustomobject]@{ CA2011 = $true; CA2023 = $true } }
        Invoke-Main | Should -Be 0
    }

    It 'refuses other vendors' {
        Mock Get-Manufacturer { 'ASUSTeK COMPUTER INC.' }
        Invoke-Main | Should -Be 4
    }

    Context 'Lenovo' {
        BeforeEach {
            Mock Get-Manufacturer { 'LENOVO' }
            $script:set = $null; $script:save = $null; $script:opcode = $null
            Mock Get-BiosWmi -ParameterFilter { $Class -eq 'Lenovo_BiosSetting' } {
                @([pscustomobject]@{ CurrentSetting = 'WakeOnLAN,Enable' },
                  [pscustomobject]@{ CurrentSetting = 'Allow3rdPartyUEFICA,Disable;[Optional:Disable,Enable]' })
            }
            Mock Get-BiosWmi -ParameterFilter { $Class -eq 'Lenovo_SetBiosSetting' } {
                $script:set = New-FakeWmi 'SetBiosSetting' @{ SetBiosSetting = [pscustomobject]@{ return = 'Success' } }; $script:set
            }
            Mock Get-BiosWmi -ParameterFilter { $Class -eq 'Lenovo_SaveBiosSettings' } {
                $script:save = [pscustomobject]@{ Calls = [System.Collections.Generic.List[string]]::new() }
                $script:save | Add-Member ScriptMethod SaveBiosSettings { $this.Calls.Add('Save(' + ($args -join '|') + ')'); [pscustomobject]@{ return = 'Success' } }
                $script:save
            }
            Mock Get-BiosWmi -ParameterFilter { $Class -eq 'Lenovo_WmiOpcodeInterface' } {
                $script:opcode = [pscustomobject]@{ Calls = [System.Collections.Generic.List[string]]::new() }
                $script:opcode | Add-Member ScriptMethod WmiOpcodeInterface { $this.Calls.Add('Op(' + ($args -join '|') + ')'); [pscustomobject]@{ return = 'Success' } }
                $script:opcode
            }
        }

        It 'turns the option on without a password and asks for a restart' {
            Invoke-Main | Should -Be 3010
            $script:set.Calls | Should -Be @('SetBiosSetting(Allow3rdPartyUEFICA,Enable)')
            $script:save.Calls | Should -Be @('Save()')
            Should -Invoke Suspend-BitLockerOnce -Times 1
        }

        It 'passes the password through the opcode interface on newer models' {
            Invoke-Main -Password 'S3cret' | Should -Be 3010
            $script:set.Calls | Should -Be @('SetBiosSetting(Allow3rdPartyUEFICA,Enable)')
            $script:opcode.Calls | Should -Be @('Op(WmiOpcodePasswordAdmin:S3cret)')
            $script:save.Calls | Should -Be @('Save()')
        }

        It 'passes the password in both calls on older models' {
            Mock Get-BiosWmi -ParameterFilter { $Class -eq 'Lenovo_WmiOpcodeInterface' } { throw 'Invalid class' }
            Invoke-Main -Password 'S3cret' | Should -Be 3010
            $script:set.Calls | Should -Be @('SetBiosSetting(Allow3rdPartyUEFICA,Enable,S3cret,ascii,us)')
            $script:save.Calls | Should -Be @('Save(S3cret,ascii,us)')
        }

        It 'uses the ThinkCentre name and value spelling' {
            Mock Get-BiosWmi -ParameterFilter { $Class -eq 'Lenovo_BiosSetting' } {
                [pscustomobject]@{ CurrentSetting = 'AllowMicrosoft3rdPartyUEFICA,Disabled' }
            }
            Invoke-Main | Should -Be 3010
            $script:set.Calls | Should -Be @('SetBiosSetting(AllowMicrosoft3rdPartyUEFICA,Enabled)')
        }

        It 'reports a refused password' {
            Mock Get-BiosWmi -ParameterFilter { $Class -eq 'Lenovo_SetBiosSetting' } {
                $script:set = New-FakeWmi 'SetBiosSetting' @{ SetBiosSetting = [pscustomobject]@{ return = 'Access Denied' } }; $script:set
            }
            Invoke-Main | Should -Be 3
            Should -Invoke Suspend-BitLockerOnce -Times 0
        }

        It 'changes nothing with -CheckOnly' {
            Invoke-Main -CheckOnly | Should -Be 3010
            $script:set | Should -BeNullOrEmpty
            Should -Invoke Suspend-BitLockerOnce -Times 0
        }

        It 'reports a model without the option' {
            Mock Get-BiosWmi -ParameterFilter { $Class -eq 'Lenovo_BiosSetting' } { [pscustomobject]@{ CurrentSetting = 'WakeOnLAN,Enable' } }
            Invoke-Main | Should -Be 2
        }

        It 'waits for a restart when the option is already on' {
            Mock Get-BiosWmi -ParameterFilter { $Class -eq 'Lenovo_BiosSetting' } { [pscustomobject]@{ CurrentSetting = 'Allow3rdPartyUEFICA,Enable' } }
            Mock Test-DbTrust { [pscustomobject]@{ CA2011 = $false; CA2023 = $false } }
            Invoke-Main | Should -Be 3010
        }

        It 'reports without changing anything' {
            Mock Get-BiosSerial { 'PF1ABC' }
            Mock Get-Model { '20XW' }
            Mock Get-BiosWmi -ParameterFilter { $Class -eq 'Lenovo_BiosPasswordSettings' } { [pscustomobject]@{ PasswordState = 2 } }
            Invoke-Main -Report | Should -Be 'PF1ABC,LENOVO,20XW,on,disabled,yes'
            $script:set | Should -BeNullOrEmpty
            Should -Invoke Suspend-BitLockerOnce -Times 0
        }

        It 'reports unknown password state when the class is missing' {
            Mock Get-BiosSerial { 'PF1ABC' }
            Mock Get-Model { 'ThinkPad, X1' }
            Mock Get-BiosWmi -ParameterFilter { $Class -eq 'Lenovo_BiosPasswordSettings' } { throw 'Invalid class' }
            Invoke-Main -Report | Should -Be 'PF1ABC,LENOVO,"ThinkPad, X1",on,disabled,unknown'
        }

        It 'does nothing when db already trusts only the 2023 CA' {
            Mock Test-DbTrust { [pscustomobject]@{ CA2011 = $false; CA2023 = $true } }
            Invoke-Main | Should -Be 0
            Should -Invoke Get-BiosWmi -Times 0 -ParameterFilter { $Class -eq 'Lenovo_BiosSetting' }
        }
    }

    Context 'Dell' {
        BeforeEach {
            Mock Get-Manufacturer { 'Dell Inc.' }
            Mock Get-BiosCim -ParameterFilter { $Class -eq 'EnumerationAttribute' } {
                @([pscustomobject]@{ AttributeName = 'LegacyMSUefiCA'; CurrentValue = 'Disabled' },
                  [pscustomobject]@{ AttributeName = 'MSUefiCA'; CurrentValue = 'Disabled' })
            }
            Mock Get-BiosCim -ParameterFilter { $Class -eq 'BIOSAttributeInterface' } { 'iface' }
            Mock Invoke-BiosCim { [pscustomobject]@{ Status = 0 } }
        }

        It 'sets MSUefiCA without a password' {
            Invoke-Main | Should -Be 3010
            Should -Invoke Invoke-BiosCim -Times 1 -ParameterFilter {
                $Method -eq 'SetAttribute' -and $Arguments.AttributeName -eq 'MSUefiCA' -and $Arguments.AttributeValue -eq 'Enabled' -and $Arguments.SecType -eq 0
            }
        }

        It 'sends the password from the file, looked up by serial' {
            $file = Join-Path ([IO.Path]::GetTempPath()) "pw-$PID.csv"
            "Serial,Password`nOTHER1,nope`nPF3ABC12,DellPw" | Set-Content $file
            Invoke-Main -PasswordFile $file -Password 'fallback' | Should -Be 3010
            Should -Invoke Invoke-BiosCim -Times 1 -ParameterFilter {
                $Arguments.SecType -eq 1 -and [Text.Encoding]::UTF8.GetString($Arguments.SecHandle) -eq 'DellPw' -and $Arguments.SecHndCount -eq 6
            }
            Remove-Item $file
        }

        It 'reports access denied' {
            Mock Invoke-BiosCim { [pscustomobject]@{ Status = 3 } }
            Invoke-Main | Should -Be 3
        }

        It 'reports older models without the WMI interface' {
            Mock Get-BiosCim -ParameterFilter { $Class -eq 'EnumerationAttribute' } { throw 'Invalid namespace' }
            Invoke-Main | Should -Be 2
        }
    }

    Context 'HP' {
        BeforeEach {
            Mock Get-Manufacturer { 'HP' }
            Mock Get-BiosWmi -ParameterFilter { $Class -eq 'HP_BIOSEnumeration' } {
                @([pscustomobject]@{ Name = 'Ready to disable MS UEFI CA Key'; CurrentValue = 'Ready' },
                  [pscustomobject]@{ Name = 'Enable MS UEFI CA key'; CurrentValue = 'No' })
            }
            $script:hp = $null
            Mock Get-BiosWmi -ParameterFilter { $Class -eq 'HP_BIOSSettingInterface' } {
                $script:hp = New-FakeWmi 'SetBIOSSetting' @{ SetBIOSSetting = [pscustomobject]@{ Return = 0 } }; $script:hp
            }
        }

        It 'sets the key with a utf-16 password' {
            Invoke-Main -Password 'HpPw' | Should -Be 3010
            $script:hp.Calls | Should -Be @('SetBIOSSetting(Enable MS UEFI CA key|Yes|<utf-16/>HpPw)')
        }

        It 'reports access denied' {
            Mock Get-BiosWmi -ParameterFilter { $Class -eq 'HP_BIOSSettingInterface' } {
                New-FakeWmi 'SetBIOSSetting' @{ SetBIOSSetting = [pscustomobject]@{ Return = 6 } }
            }
            Invoke-Main | Should -Be 3
        }
    }

    It 'never writes the password to the log' {
        Mock Get-Manufacturer { 'HP' }
        Mock Get-BiosWmi -ParameterFilter { $Class -eq 'HP_BIOSEnumeration' } { [pscustomobject]@{ Name = 'Enable MS UEFI CA key'; CurrentValue = 'No' } }
        Mock Get-BiosWmi -ParameterFilter { $Class -eq 'HP_BIOSSettingInterface' } { New-FakeWmi 'SetBIOSSetting' @{ SetBIOSSetting = [pscustomobject]@{ Return = 6 } } }
        Invoke-Main -Password 'TopSecretPw' | Out-Null
        Should -Invoke Get-BiosWmi -ParameterFilter { $Class -eq 'HP_BIOSSettingInterface' } -Times 1
        Get-Content $script:LogPath -Raw | Should -Not -Match 'TopSecretPw'
    }
}

AfterAll { Remove-Item $script:LogPath -ErrorAction SilentlyContinue }
