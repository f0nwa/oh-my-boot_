# WinPE driver catalog check — 19.10.2026

[Русская версия](../winpe-driver-test-2026-10-19.md)

## Done

- Automated tests of all Go packages and `go vet ./...` passed; Agent and Control builds for Linux amd64 were created.
- On the PXE server, `wimlib-imagex` from the `wimtools` 1.14.5 package was installed, the driver catalog migration was applied, and Agent and Control were installed. Four services — Agent, Control, Boot and dnsmasq — are active.
- The menu checksum before and after installation matched: `43459a54e0d3dc7a8361d731062653a94293ffcadc90fce0fe3ff35f52ddabf9`. The menu items did not change.
- `wimlib-imagex info` found boot index 2 in the source Windows WIM. On a temporary copy of the WIM, `update` added a test file and `verify` confirmed integrity. The test file and the temporary copy were deleted.
- Automated tests check that a folder and a ZIP get the same identity, import errors, preparing a separate WIM, the `drvload` script before `wpeinit`, independent configurations for one ISO, publishing only checked files and rejecting a changed WIM/XML.
- The NetKVM package for Windows 11 x64 from VirtIO 0.1.302 was imported. Configuration 1, “Windows 11 VirtIO Net test”, for the Windows 11 26H2 ISO was prepared separately from the source profile and published in the `Windows/Tests` folder. `netkvm.inf` was found in the prepared WIM; the script loads it before network initialisation.
- VM 113 with a VirtIO network adapter, a 70 GB SATA disk and OVMF Secure Boot Enabled booted this item over PXE without manual commands. The installer opened; `ipconfig` showed a `10.30.0.x` address, and the 70 GB disk was visible on the selection screen. Nothing was written to the disk.
- With OVMF Secure Boot Disabled, the same item also opened the installer without manual commands. After the message about successful network initialisation, connecting the installation media was retried for about 15 seconds; then the installer started and the disk was visible. There is no separate `ipconfig` output for this boot yet.
- The vioscsi package for Windows 11 x64 from VirtIO 0.1.302 was imported through the folder upload form. The catalog recognised `vioscsi.inf` as `amd64`, class `SCSIAdapter`, with VirtIO SCSI PCI IDs.
- Configuration 3, “Windows 11 VirtIO Net SCSI test”, for the same ISO was prepared with two INF files. The server reports `ready`; the derived WIM contains `netkvm.inf` and `vioscsi.inf`. Both `drvload` calls in the script come before `wpeinit`. Configuration 2 contains only vioscsi and is not used for the combined network and disk check.
- Configuration 3 was published as a separate PXE item. VM 113 with VirtIO networking, the existing 70 GB SATA disk and a new empty 8 GB SCSI disk booted Windows 11 WinPE. Both disks are visible in the installer, and `ipconfig` shows a `10.30.0.x` address. No disk was selected and the installation was not started. After the check, all four PXE services are active, configuration 3 stays `ready` and is present in the active menu.
- After the check, Agent, Control, Boot and dnsmasq are `active` on the server; configuration 1 is `ready`.
- The admin interface now shows record numbers and selected INF files, and unused Windows profiles, XML answer files and driver configurations can be deleted from PXE Platform completely. The ISO on SMB and the imported driver packages are kept. Before deletion, Control compares the current menu snapshot with Boot and blocks published or dependent records.
- Before the deletion update, the binaries and a database backup were saved to `/var/backups/pxe-platform-delete-20261019/`. Agent, Control and the Agent permissions on managed directories were updated. All five services (`pxe-agent`, `pxe-control`, `pxe-boot`, `pxe-images`, `pxe-dnsmasq`) are active. The active menu checksum before and after the update is `cc9d354f9d9de51742bf9caeb3bdf6a4ced9ad1e232556707a27146810c1166a`.
- The user deleted unpublished configuration 2 through the admin interface. A database check showed that configuration 2 is gone and the audit entry `driver-config.delete|ok|id=2` exists; the script directory of configuration 2 and its derived WIM were deleted. Configuration 3, its script and WIM remained, and the menu kept the same checksum. Full deletion of published profiles and answer files on the live server was not tested, so as not to change the working menu.

## SeaBIOS: configuration with network and disk drivers

- The user created configuration 4 with the same Windows 11 26H2 ISO and the same INF files as configuration 3 (vioscsi and netkvm). Control did not build a new WIM: configurations 3 and 4 use one derived `boot.wim` with the key `87c5817f…`. Menu version 24, with item 4 instead of item 3, became active; configuration 3 stayed `ready`.
- VM 112 was switched to SeaBIOS with VirtIO networking, a SATA disk and an additional VirtIO SCSI disk; for the check the user added a TPM and increased the memory. The Boot log showed the BIOS branch being loaded: `wimboot`, `winpeshl.ini` of profile 3, `pxe-setup.cmd` of configuration 4 and the derived WIM, from address `10.30.0.7`.
- In WinPE, `ipconfig` showed `10.30.0.7/24`, and `diskpart` → `list disk` showed two 64 GB disks. Both `drvload` calls run before `wpeinit` and stop the script on error, and the command prompt was in the mounted ISO `Y:\3\sources`; `smbstatus` confirmed a `pxe-iso` session from `10.30.0.7`. The driver log file is `X:\pxe-driver.log`; it was not opened during the check because the instructions gave the wrong name.
- Setup stopped with the message “This PC can’t run Windows 11”. This is a firmware requirement of the Windows 11 installer itself, not a PXE boot error; Windows 11 behaved the same way in SeaBIOS earlier. No disk was selected and the installation was not started.

## Menu rollback with driver items

- **Restore** creates a new version with the same content. Restoring the version without configurations 3 and 4 (SHA-256 `677c9eb1617d…`) created active version 25; VM 112 received this menu, and `Windows/Tests` contained only “Windows 11 VirtIO Net test”.
- The first attempt to restore version 24 was rejected: “Windows ISO profile is unavailable: files of profile 1 are unavailable”. The cause was the server time zone change and the byte-by-byte comparison of the ISO time in Agent; the fix is described in the [Control guide](control-installation.md#version-numbers-and-time-zone-19102026). The active menu did not change.
- After the fix, restoring version 24 created active version 26 (SHA-256 `df16c13198be…`). On 19.10.2026 at 23:59:30 (MSK), VM 112 loaded `wimboot`, `winpeshl.ini` of profile 3, `pxe-setup.cmd` of configuration 4 and the shared derived WIM, then opened a new `pxe-iso` SMB session to the ISO. WinPE started as it did before the rollback.

## Deleting records through the admin interface, 20.10.2026

- An attempt to delete published configuration 4 was rejected with “Remove the item from the published menu first”. An attempt to delete published profile 2, which XML answer files depend on, was rejected with the same message: the publication check runs before the dependency check. The dependency rejection therefore did not show up on the live server; it is covered by automated tests. Rejected requests change nothing; at the time of this check they were not written to the audit log, and since 20.10.2026 they are recorded with the result `rejected`.
- Unpublished configuration 3 was deleted (audit `driver-config.delete|ok|id=3`). Its directory in `windows-driver-configs` was deleted; the shared derived WIM `87c5817f…` (627,892,711 bytes) remained because published configuration 4 uses it, and Boot keeps serving it with HTTP 200. Saved menu versions 22 and 23 refer to the deleted configuration 3 and therefore cannot be restored.
- Unpublished XML answer file 4, “Windows 11 language-only test”, was deleted (audit `windows-answer.delete|ok|id=4`); its directory in `windows-answers` was deleted too. Answer file 3 and profile 2 remained.
- The active menu (SHA-256 `df16c13198be…`) did not change; all five services are active.

## Several XML answer files for one ISO, 20.10.2026

- For profile 2 (Windows 11 x64), answer files 3 “Windows 11 RU test” and 5 “Windows 11 language-only test” were published at the same time (menu version 27). From the server, each item serves only its own `pxe-answer.xml`: the SHA-256 values `3506616f…` and `4d36d834…` match the database records; `boot.wim` and `winpeshl.ini` are shared by the profile, and `pxe-setup.cmd` is the same for both. Answer file 3 runs `CLEAN` on disk 0 by itself, so it was not booted on a client.
- VM 113 (UEFI) booted item 5 and requested only the `windows-answers/5` files. Setup stopped with “Unable to read the <ProductKey> setting from the unattend answer file”: the language example does not contain the key this ISO requires.
- The example [`windows-answer-key-no-disk.xml`](../../examples/windows-answer-key-no-disk.xml) was added: language, the public generic Windows 11 Pro installation key and EULA acceptance, with no disk settings. It was uploaded as answer file 7 and published (menu version 28). VM 113 requested only the `windows-answers/7` files, connected the ISO over SMB, and Setup in Russian reached the Windows 11 Pro edition selection without a key error. No disk was selected.
- At the time of the check, the admin interface allowed uploading the same XML again for one ISO: identical answer files 6 and 7 appeared, and the extra file 6 was deleted. Since 20.10.2026 such a repeated upload is rejected.

## ZIP import on the server, 20.10.2026

- The vioscsi package, uploaded as a folder on 19.10.2026, was uploaded again through the **ZIP archive** form as `virtio-win-0.1.302-vioscsi-w11-amd64.zip` with the same three files. The server computed the same canonical hash `d8c17fef…` and did not create a new package: the catalog still has two packages, and the audit log recorded `driver.import|ok` with this hash. The page showed “Driver package added”, although the package was already in the catalog.
- The audit log also shows a rejected attempt to delete published configuration 4: `driver-config.delete|rejected|id=4 published`.

## Next checks

Real laptop models and their driver packages have not been checked yet.
