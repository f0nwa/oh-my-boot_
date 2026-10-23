# Secure Boot: preparing domain laptops with SCCM

This guide is for Lenovo, Dell and HP laptops that get “Access Denied” at PXE boot. In the panel their iPXE step is red: “Firmware rejected the loader”. [secure-boot.md](secure-boot.md#if-a-machine-does-not-boot) explains why. In short, the firmware trusts only Windows' key and not Microsoft UEFI CA 2011, the key our loader is signed with.

The script `scripts/windows/Enable-MicrosoftUefiCA.ps1` turns on the BIOS option “allow Microsoft's key for third-party loaders” from Windows, without entering the BIOS. Secure Boot stays on.

## What the script does

1. Checks whether Secure Boot is on and whether “Microsoft Corporation UEFI CA 2011” is in the trusted list (db). If Secure Boot is off or the key is already there, it changes nothing.
2. Finds the vendor's BIOS option through its built-in WMI interface. No vendor tools need to be installed.

   | Vendor | BIOS option | WMI name |
   |---|---|---|
   | Lenovo ThinkPad | Allow Microsoft 3rd Party UEFI CA | `Allow3rdPartyUEFICA` = `Enable` |
   | Lenovo ThinkCentre | Allow Microsoft 3rd Party UEFI CA | `AllowMicrosoft3rdPartyUEFICA` = `Enabled` |
   | Dell (models after 2018) | Enable Microsoft UEFI CA | `MSUefiCA` = `Enabled` |
   | HP | Enable MS UEFI CA key | `Enable MS UEFI CA key` = `Yes` |

3. Turns the option on, passing the BIOS administrator (supervisor) password if one is set. The password never reaches the log or the output.
4. Suspends BitLocker for one restart. At the next boot the firmware changes its Secure Boot keys, and without this BitLocker would ask for the recovery key.

The change takes effect after a restart. The log goes to `C:\Windows\Temp\PXE-MicrosoftUefiCA.log`.

### Exit codes

| Code | Meaning | What to do |
|---|---|---|
| 0 | Nothing needed: Secure Boot is off or the key is already there | — |
| 3010 | The option is on (now or from before); a restart is needed | Restart. SCCM handles this code itself |
| 2 | The model has no such option, or no vendor WMI interface | Older Dells: Dell Command \| Configure, option `--MSUefiCA`. Otherwise, by hand in the BIOS |
| 3 | A BIOS password is needed, or the one given was refused | Check the password for this serial number |
| 4 | Not Lenovo, Dell or HP | By hand in the BIOS |
| 5 | The option is on, but the firmware carries only the new UEFI CA 2023, not 2011 | Our loader is signed with the 2011 key. For now the only option is to turn Secure Boot off on this machine |
| 1 | Unexpected error | See the log |

## BIOS passwords

The machines have different passwords, so the script reads them from a CSV file by BIOS serial number:

```
Serial,Password
PF3ABC12,password-of-machine-1
5CG1234XYZ,password-of-machine-2
```

A machine's serial number is `(Get-CimInstance Win32_BIOS).SerialNumber`. SCCM keeps it in inventory (Hardware Inventory → PC BIOS → Serial Number), so the list can be exported with a report.

If a serial number is not in the file, the script uses `-Password` if it is given, and otherwise tries without a password. Each password is tried once: after several wrong attempts in a row a BIOS may lock input until the next restart.

**Be careful with the password file.** SCCM caches package content on the client in `C:\Windows\ccmcache`, where local administrators can read it. So:
- make a separate package for each rollout wave, with only that wave's machines in the CSV;
- remove the package from the distribution points after the rollout; the client cache then clears over time.

## Test on one machine

Before the rollout, test on one machine of each model. The setting names come from the vendors' documentation; the script has not yet been run on real hardware.

From an elevated PowerShell:

```
# what would be done, no changes
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Enable-MicrosoftUefiCA.ps1 -CheckOnly -Password '<password>'
# do it
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Enable-MicrosoftUefiCA.ps1 -Password '<password>'
```

After the restart, check that the key is there:

```
[Text.Encoding]::ASCII.GetString((Get-SecureBootUEFI db).Bytes) -match 'Microsoft Corporation UEFI CA 2011'
```

It should print `True`. The machine should then boot from the network into our menu with Secure Boot on.

## Rolling out with SCCM

1. **Package.** Put `Enable-MicrosoftUefiCA.ps1` and `bios-passwords.csv` into a folder on the source server. Create a package (Software Library → Packages) with that folder as its source and distribute it to the distribution points.
2. **Package program:**
   - Command line:
     ```
     powershell.exe -NoProfile -ExecutionPolicy Bypass -File Enable-MicrosoftUefiCA.ps1 -PasswordFile bios-passwords.csv
     ```
   - Run: Hidden. Program can run: Whether or not a user is logged on. Run mode: Run with administrative rights.
   - After running: Configuration Manager restarts computer. You can choose “No action required” to leave the restart to the user; the change then applies at their next restart.
3. **Collection.** Put only the laptops that do not boot from the network in it: Secured-core models, or the machines the panel marked “Firmware rejected the loader”.
4. **Deployment.** Make it Required, with a maintenance window. The user gets the usual SCCM restart notice.
5. **Results.** Monitoring → Deployments shows each machine's exit code. 3010 and 0 mean success; 2, 3, 4 and 5 are explained in the table above. Details are on the machine in `C:\Windows\Temp\PXE-MicrosoftUefiCA.log`.

To only check without changes (for example, to survey the fleet in advance), use the same package with a separate program that adds `-CheckOnly`.

## Script tests

The script's logic is covered by Pester tests with WMI replaced. They check which calls each vendor gets, with which parameters, and that the password never reaches the log:

```
Invoke-Pester ./scripts/windows
```

Setting name sources: [Lenovo CDRT — ThinkPad Secure Boot](https://docs.lenovocdrt.com/ref/bios/settings/thinkpad/secureboot/), [Lenovo CDRT — ThinkCentre Secure Boot](https://docs.lenovocdrt.com/ref/bios/settings/thinkcentre/secure_boot/), [Dell Command | Configure — --MSUefiCA](https://www.dell.com/support/manuals/en-us/command-configure/dcc_5.x_ref_guide/-msuefica?guid=guid-a6452e46-e9b6-4ca3-a88d-8048a650dc7c&lang=en-us), [Dell BIOS WMI](https://www.configjon.com/dell-bios-settings-management-wmi/), [HP — Secure Boot options in BCU](https://h30434.www3.hp.com/t5/Business-PCs-Workstations-and-Point-of-Sale-Systems/BIOS-Settings-using-BiosConfigUtility64/td-p/9688689).
