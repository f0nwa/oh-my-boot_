# Windows 10: first WinPE check over PXE

> Current installation commands use the target OMB names. Dated deployment accounts, original backup paths and outcomes describe the pre-migration stand; the stand was migrated to OMB on 31 October 2026. See the [technical migration plan](../../docs/superpowers/plans/2026-10-31-omb-technical-migration.md). Results and remaining acceptance: [migration report](../research/2026-10-31-omb-technical-migration.md).

[Русская версия](../windows-winpe-pxe.md)

> On 20.10.2026 this static item, the local ISO copy and the `windows10` share were removed; Windows 10 boots through an ISO profile (see the [Images guide](images-installation.md)). The history of the first check follows.

This part adds Windows 10 x64 to the boot menu and loads `sources/boot.wim` through the signed `wimboot`. WinPE then connects the full installation files over SMB. The check stops before anything is written to disk.

## Sources and files

- ISO: `19045.6280.250817-2324.22H2_RELEASE_SVC_PROD3_CLIENTPRO_OEMRET_X64FRE_RU-RU.ISO` in the connected SMB folder `//192.168.10.10/Downloads/iso`.
- `wimboot` 2.9.0: `https://github.com/ipxe/wimboot/releases/download/v2.9.0/wimboot`; SHA-256 `5f067ccdc4d084d5bf77b6c853bd0f8402dfc2b4cd1b103d358993ae97fae8e3`.
- `sources/boot.wim` of this ISO: 597,191,021 bytes; SHA-256 `8401543d872d0feb851efa2faf87a6d45e472db6b1e3c65d144af2cbd8df0de4`.

The server stores these two files in `/srv/pxe/tftp/windows/wimboot` and `/srv/pxe/tftp/windows10/boot.wim`. It does not publish the whole SMB share. A `cmp` comparison confirmed that the WIM copy equals the file inside the ISO. `sbverify --list` on Ubuntu showed `wimboot` signatures from the Microsoft Corporation UEFI CA 2011 and Microsoft UEFI CA 2023 chains. Having a signature does not replace a separate client boot with Secure Boot.

## Publishing and checking

In the admin interface, on **Menu**, select **Windows 10 x64 — start WinPE** and keep Ubuntu enabled if needed. Press **Publish**. The previous menu version stays available for rollback.

On test PXE VM 112 with 16 GB of RAM, first boot Windows 10 in OVMF with Secure Boot disabled. Check that the WinPE environment or the installer window opens; if the installer asks for files from the media, record it as the expected limit of this stage. Do not select a disk and do not start the installation. After a successful start, repeat with Secure Boot enabled, and then with SeaBIOS.

The user confirmed that the Windows 10 installation window appeared in OVMF with Secure Boot both disabled and enabled. At first `ipconfig` inside WinPE showed no adapter with either VirtIO or Intel E1000. After `wpeutil InitializeNetwork`, the E1000 adapter appeared and got an address from the PXE network. This confirms that the network must be initialised explicitly in this scenario; whether the WIM contains a VirtIO NIC driver was not checked. Later the Windows 10 profile was also checked in SeaBIOS: the installer opened and showed the 64 GB SATA disk of VM 112. Access to the full installation files and the disk is checked below; a successful OS installation was not checked.

## Installation files over SMB

On the test server, the content of the same ISO was copied from the read-only connection to `/srv/pxe/installers/windows10` (905 files, 6.9 GiB). Samba publishes it as `\\10.30.0.10\windows10`. The configuration is in [`deploy/samba/smb.conf`](../../deploy/samba/smb.conf): TCP 445 only on `lo` and `ens19`, access only from `10.30.0.0/24`, mandatory SMB signing, read-only access, and access only for a separate `pxe-install` account. The Synology password is not used and is not sent to the client.

The `pxe-install` password was generated on the server and is stored only in `/etc/pxe-platform/winpe-smb-password` with mode `0600`. The user can read it over SSH with `sudo -n cat`; never publish the password in documentation or in the boot menu. In WinPE, after `wpeutil InitializeNetwork`, run `net use Z: \\10.30.0.10\windows10 /user:pxe-install *` and enter the password when asked. Check `dir Z:\sources\install.wim`. After a successful read, `Z:\setup.exe` can be started; stop before disk selection.

On the server, `testparm -s` accepted the configuration. `smbd` listens on `127.0.0.1:445` and `10.30.0.10:445`, but not on the management address `192.168.10.135`. `smbclient` with the account read `setup.exe` and `sources/install.wim` (6,438,122,734 bytes); creating a directory was rejected with `NT_STATUS_ACCESS_DENIED`, as was connecting to the share without an account. The user connected the share from WinPE and saw `Z:\sources\install.wim`.

The first start of `Z:\setup.exe` returned “Access is denied”: the files copied from the ISO had Unix mode `0644`, and Samba checks the execute permission when a client runs a file. After `setup.exe` was set to `0755`, the user confirmed that the installer opened. All Windows executable files in the local copy (`.exe`, `.dll`, `.sys`, `.com`, `.ocx`, `.cpl`, `.drv`, `.efi`) were then set to `0755` — 212 files. `install.wim` stayed `0644`, and writing over SMB is still rejected after the fix. The installer reached disk selection but did not see the 32 GB disk of VM 112 on the VirtIO SCSI controller.

## Disk driver for the test VM

For Windows 10 x64, only `vioscsi.inf`, `vioscsi.cat` and `vioscsi.sys` (`vioscsi/w10/amd64`) were taken from the virtio-win 0.1.271 image. They are in the network folder `Z:\drivers\vioscsi\w10\amd64`. SHA-256 of the source ISO: `bbe6166ad86a490caefad438fef8aa494926cb0a1b37fa1212925cfd81656429`. The [virtio-win project](https://github.com/virtio-win/kvm-guest-drivers-windows/wiki/Driver-installation) describes loading the driver on the disk selection screen. The ISO was downloaded from a mirror of the [archived release](https://fedora-virt.repo.nfrance.com/virtio-win/direct-downloads/archive-virtio/virtio-win-0.1.271-1/); its hash matched the [recorded hash of this release](https://github.com/virtio-win/virtio-win-pkg-scripts/issues/121).

On the server, reading all three files over SMB with the `pxe-install` account was checked, and they matched the local copies byte for byte. In WinPE the files are also visible on `Z:`. `drvload Z:\drivers\vioscsi\w10\amd64\vioscsi.inf` returned `0x80070103`, so the installation of this driver is not confirmed. The list of problem devices contained `PCI\VEN_1AF4&DEV_1002`, which is VirtIO Balloon, while the VirtIO SCSI INF contains `DEV_1004` and `DEV_1048`. After `diskpart` → `rescan` → `list disk`, the user saw **Disk 0, 32 GB, 32 GB free**. So the disk is available to the current WinPE; its appearance cannot be attributed to the `drvload` attempt without further checks. Do not clean the disk or continue the installation.

Server file check: `HEAD` returned HTTP 200 and sizes of 76,064 bytes for `wimboot` and 597,191,021 bytes for `boot.wim`. A range request for the first 1024 bytes of the WIM returned HTTP 206 and exactly 1024 bytes. After Control was replaced, all five services stayed active, the HTTPS sign-in page responded with HTTP 200, and the SHA-256 of the active menu before and after the replacement stayed `2777c5df23b82398483177bb8dc73999dd1bdc052f2601642682698bbb6d30f6`. Until it is published manually, the Windows 10 item is absent from the active menu.

## Rollback

For the current OMB stand, use the [current diagnostics and recovery instructions](control-installation.md#diagnostics-and-rollback). The previous lab procedure below applies only when restoring the server state from 16 October 2026.

### Historical procedure, 16 October 2026

If the profile does not boot, restore the previous version in **Menu → Versions**. The previous Control binary is in `/var/backups/pxe-platform-pre-winpe-20261016/pxe-control`. Restore it to `/usr/local/bin/pxe-control` and restart only `pxe-control`; Boot, proxy-DHCP and the currently published menu snapshot work independently of the dashboard. The Windows 10 files in `/srv/pxe/tftp` can be deleted after the item is removed from the menu.

To roll back SMB, stop and disable `smbd.service`, then remove `/srv/pxe/installers/windows10` once the client checks are finished. `nmbd.service` stays masked: TCP 445 is enough for access by IP address. These actions do not change the PXE menu or DHCP.

## Next step

The next check is Windows 11 x64. Use the same pinned `wimboot` release, which supports extracting `boot.stl` from newer WIM files. The disk was confirmed in Windows 10 WinPE with DiskPart; the OS was not installed on the disk.

Basis of the boot method: [iPXE wimboot](https://ipxe.org/wimboot), [iPXE Windows PE](https://ipxe.org/howto/winpe), [wimboot 2.9.0 release](https://github.com/ipxe/wimboot/releases/tag/v2.9.0).
