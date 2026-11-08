# Secure Boot: preparing domain laptops with SCCM

This guide is for Lenovo, Dell and HP laptops that get “Access Denied” at PXE boot. In the panel their iPXE step is red: “Firmware rejected the loader”. [secure-boot.md](secure-boot.md#if-a-machine-does-not-boot) explains why. In short, the firmware may trust only Windows keys, not the third-party Microsoft UEFI CAs 2011 or 2023 that sign our loader.

The script `scripts/windows/Enable-MicrosoftUefiCA.ps1` turns on the BIOS option “allow Microsoft's key for third-party loaders” from Windows, without entering the BIOS. Secure Boot stays on.

## What the script does

1. Checks whether Secure Boot is on and whether the trusted list (db) contains at least one third-party key: “Microsoft Corporation UEFI CA 2011” or “Microsoft UEFI CA 2023”. If Secure Boot is off or either key is already there, it changes nothing.
2. Finds the vendor's BIOS option through its built-in WMI interface. No vendor tools need to be installed.

   | Vendor | BIOS option | WMI name |
   |---|---|---|
   | Lenovo ThinkPad | Allow Microsoft 3rd Party UEFI CA | `Allow3rdPartyUEFICA` = `Enable` |
   | Lenovo ThinkCentre | Allow Microsoft 3rd Party UEFI CA | `AllowMicrosoft3rdPartyUEFICA` = `Enabled` |
   | Dell (models after 2018) | Enable Microsoft UEFI CA | `MSUefiCA` = `Enabled` |
   | HP | Enable MS UEFI CA key | `Enable MS UEFI CA key` = `Yes` |

3. Turns the option on, passing the BIOS administrator (supervisor) password if one is set. The password never reaches the log or the output.
4. Suspends BitLocker for one restart. At the next boot the firmware changes its Secure Boot keys, and without this BitLocker would ask for the recovery key.

The change takes effect after a restart. The log goes to `C:\Windows\Temp\OMB-MicrosoftUefiCA.log`.

### Exit codes

| Code | Meaning | What to do |
|---|---|---|
| 0 | Nothing needed: Secure Boot is off or either key is already there | — |
| 3010 | The option is on (now or from before); a restart is needed | Restart. SCCM handles this code itself |
| 2 | The model has no such option, or no vendor WMI interface | Older Dells: Dell Command \| Configure, option `--MSUefiCA`. Otherwise, by hand in the BIOS |
| 3 | A BIOS password is needed, or the one given was refused | Check the password for this serial number |
| 4 | Not Lenovo, Dell or HP | By hand in the BIOS |
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

After the restart, check that `db` contains at least one third-party CA:

```
[Text.Encoding]::ASCII.GetString((Get-SecureBootUEFI db).Bytes) -match 'Microsoft Corporation UEFI CA 2011|Microsoft UEFI CA 2023'
```

If the command prints `True`, `db` contains a third-party CA corresponding to one of the new shim's signatures. Verify the actual menu boot separately on the laptop.

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
5. **Results.** Monitoring → Deployments shows each machine's exit code. 3010 and 0 mean success; 2, 3 and 4 are explained in the table above. Details are on the machine in `C:\Windows\Temp\OMB-MicrosoftUefiCA.log`.

To only check without changes (for example, to survey the fleet in advance), use the same package with a separate program that adds `-CheckOnly`.

## Fleet report: `-Report`

`-Report` changes nothing and writes no log; it prints one CSV line: `serial,manufacturer,model,secureboot,option,biospassword`, for example `PF1ABC,LENOVO,20XW,on,disabled,yes`. Values: `secureboot` is `on`/`off`; `option` is `enabled`/`disabled`/`missing`/`unknown`; `biospassword` is `yes`/`no`/`unknown` (the BIOS administrator password, from the vendor's WMI). Exit code 0. Use it as a ManageEngine or SCCM script: collect the lines from all machines to see where the key is already on and where turning it on will need a password.

```
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Enable-MicrosoftUefiCA.ps1 -Report
```

## Script tests

The script's logic is covered by Pester tests with WMI replaced. They check which calls each vendor gets, with which parameters, and that the password never reaches the log:

```
Invoke-Pester ./scripts/windows
```

Setting name sources: [Lenovo CDRT — ThinkPad Secure Boot](https://docs.lenovocdrt.com/ref/bios/settings/thinkpad/secureboot/), [Lenovo CDRT — ThinkCentre Secure Boot](https://docs.lenovocdrt.com/ref/bios/settings/thinkcentre/secure_boot/), [Dell Command | Configure — --MSUefiCA](https://www.dell.com/support/manuals/en-us/command-configure/dcc_5.x_ref_guide/-msuefica?guid=guid-a6452e46-e9b6-4ca3-a88d-8048a650dc7c&lang=en-us), [Dell BIOS WMI](https://www.configjon.com/dell-bios-settings-management-wmi/), [HP — Secure Boot options in BCU](https://h30434.www3.hp.com/t5/Business-PCs-Workstations-and-Point-of-Sale-Systems/BIOS-Settings-using-BiosConfigUtility64/td-p/9688689).
