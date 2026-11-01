# Ubuntu Desktop: network installation in OMB

[Русская версия](../ubuntu-desktop-pxe.md). See the [Control guide](control-installation.md#ubuntu-autoinstall) for current installation management.

Boot serves one selected ISO at `/images/ubuntu-desktop.iso` and its kernel and initrd under `/assets/ubuntu/`. Other storage ISOs are not published through this route. Multiple Ubuntu installations can share the profile with different Autoinstall files.

## Source preparation

Set the absolute ISO path in `/etc/omb/boot.env`:

~~~ini
OMB_BOOT_UBUNTU_ISO=/var/lib/omb-images/iso/ubuntu-desktop-amd64.iso
~~~

Replace this example with a real file. Boot needs read access through all parent directories. For a local Images source, add `omb-images` to the Boot unit's supplementary groups. For SMB, configure managed storage first.

From the same ISO, copy `casper/vmlinuz` and `casper/initrd` to `/srv/omb/tftp/ubuntu/vmlinuz` and `/srv/omb/tftp/ubuntu/initrd`, and the Ubuntu shim `EFI/boot/bootx64.efi` to `/srv/omb/tftp/ubuntu/bootx64.efi`. Compare source and copy SHA-256 values; Boot must be able to read the files. `/etc/omb/control.env` must contain the client-accessible address:

~~~ini
OMB_CONTROL_BOOT_BASE_URL=http://10.30.0.10:8080
~~~

Restart the relevant services after changing env files. Run `systemctl daemon-reload` first if a unit changed. Check boot file HTTP HEAD and ISO range GET before publishing. The menu generator calls Ubuntu `shim` only on UEFI; BIOS proceeds directly to kernel boot.

## Installations and Autoinstall

Create an Ubuntu installation. Boot is manual without an answer file. For automation, create or select an immutable file under **Library → Answer files → Ubuntu Autoinstall**. The constructor supports interactive storage or disk erasure by exact serial number. Add the installation to the menu draft and publish it.

Check delivery of the matching NoCloud `user-data` and `meta-data` pair and installer behavior on a disposable VM disk. Successful file delivery does not confirm OS installation completion. See the [Control guide](control-installation.md#ubuntu-autoinstall) for data access and constructor behavior.

## Verification and recovery

`casper` downloads the ISO into memory. A VM with 16 GiB RAM was used for the previously tested ISO of about 6.5 GB; with 4 GiB the download failed with `No space left on device`. Check memory needs separately for other ISOs.

Before the technical transition, the user confirmed the Ubuntu desktop in SeaBIOS and OVMF with Secure Boot on and off. A full repeat run with the current build, including Autoinstall, is deferred until the main development work is complete. Retrieve original records through [project history](../project-history.md).

If boot fails, check client memory, ISO availability, matching kernel and initrd, Boot logs and storage state. **Menu → Versions** can restore an available previous menu version. For files and databases, see [recovery](../recovery.md).
