# Ubuntu Desktop: network installation in OMB

[Русская версия](../ubuntu-desktop-pxe.md). See the [Control guide](control-installation.md#ubuntu-autoinstall) for current installation management.

OMB supports multiple Ubuntu Desktop amd64 ISOs in connected local and network storage. Agent prepares the kernel, initrd, shim and installation catalog separately for each verified ISO. Multiple boot entries can use one ISO with different Autoinstall files.

## Source preparation

In **Library → ISO images**, upload a file or find it after scanning a storage. Select **Create boot entry** for the Ubuntu image. OMB checks its contents, architecture and checksum before opening the form with that ISO selected. Ubuntu Server, ARM64 and incomplete images are not supported. The type inferred from a filename is only a hint. If the name does not identify the file as Ubuntu, use **Check for Ubuntu**.

Set the entry name and folder and optionally choose an Autoinstall file. Agent prepares the source after saving; a failed preparation leaves the entry available for retry. Add the entry to the menu draft separately, then publish. The original ISO remains in storage; prepared files and new URLs are bound to the source ID and ISO SHA-256. A replaced ISO or mismatched prepared files block publication.

Set the client-visible Boot address in `/etc/omb/control.env` as `OMB_CONTROL_BOOT_BASE_URL`. BIOS boots the kernel directly; UEFI uses the shim from that same ISO. Boot must be able to read the storage and its parent directories.

The old `/images/ubuntu-desktop.iso` and `/assets/ubuntu/` URLs remain only for previously published versions whose source has been verified. Choosing another ISO does not change that binding. A version without a proven source cannot be restored. Do not replace the legacy assets or migrate a running server without a verified ISO match and a backup.

## Installations and Autoinstall

Create an Ubuntu installation. Boot is manual without an answer file. For automation, create or select an immutable file under **Library → Answer files → Ubuntu Autoinstall**. The constructor supports interactive storage, installer-selected disk erasure with a data-loss warning, or erasure of only the disk with an exact serial number. Add the installation to the menu draft and publish it.

Check delivery of the matching NoCloud `user-data` and `meta-data` pair and installer behavior on a disposable VM disk. Successful file delivery does not confirm OS installation completion. See the [Control guide](control-installation.md#ubuntu-autoinstall) for data access and constructor behavior.

## Verification and recovery

`casper` downloads the ISO into memory. A VM with 16 GiB RAM was used for the previously tested ISO of about 6.5 GB; with 4 GiB the download failed with `No space left on device`. Check memory needs separately for other ISOs.

Before the technical transition, the user confirmed the Ubuntu desktop in SeaBIOS and OVMF with Secure Boot on and off. A full repeat run with the current build, including Autoinstall, is deferred until the main development work is complete.

If boot fails, check client memory, ISO availability, matching kernel and initrd, Boot logs and storage state. **Menu → Versions** can restore an available previous menu version. For package rollback, see the [offline release guide](offline-release.md).
