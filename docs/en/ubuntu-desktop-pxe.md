# First Ubuntu Desktop boot over PXE

> Current installation commands use the target OMB names. Dated deployment accounts, original backup paths and outcomes describe the pre-migration stand; its migration is pending. See the [technical migration plan](../../docs/superpowers/plans/2026-10-31-omb-technical-migration.md).

> This is the historical first-boot report. For current installation creation and automation, see “Automated installation” below.

[Русская версия](../ubuntu-desktop-pxe.md)

A profile was prepared for `ubuntu-26.04.1-desktop-amd64.iso` from the connected SMB folder. Boot serves only this ISO at `/images/ubuntu-desktop.iso`, and the `casper/vmlinuz` and `casper/initrd` files extracted from it at `/assets/ubuntu/`. In the Control menu the Ubuntu item is enabled in its properties; publishing keeps the previous version for rollback. The other ISO files in the SMB folder are not served over HTTP.

After the kernel starts, `casper` gets an address over DHCP, downloads the ISO over HTTP and mounts it. This was confirmed by reading `scripts/casper` in the initrd of this image: the `iso-url=*.iso` and `url=*.iso` parameters select `NETBOOT=url`, and then `wget` downloads the file. For a 6.5 GB ISO, the test client needs at least 12 GB of RAM; 16 GB was planned for the check. The first full client boot still needed checking; a successful HTTP response alone does not confirm that the live system starts or that Secure Boot works with this kernel.

## Setup

On the server, Boot must be able to read the selected ISO: add `omb-images` to `SupplementaryGroups` of the `omb-boot.service` unit. In `/etc/omb/boot.env`, set the absolute path to the file:

```ini
OMB_BOOT_UBUNTU_ISO=/mnt/omb-smb/84e4b95528e2b8c5a405adb7061bc5bf/ubuntu-26.04.1-desktop-amd64.iso
```

In `/etc/omb/control.env`, set the Boot address that clients can reach:

```ini
OMB_CONTROL_BOOT_BASE_URL=http://10.30.0.10:8080
```

Copy the kernel and initrd from the same ISO to `/srv/omb/tftp/ubuntu/vmlinuz` and `/srv/omb/tftp/ubuntu/initrd`; for Secure Boot also copy the signed Ubuntu shim `EFI/boot/bootx64.efi` to `/srv/omb/tftp/ubuntu/bootx64.efi`. After copying, compare the SHA-256 of the source files and the copies. Then update the Boot and Control binaries, reload the units and restart both services. Before publishing the menu, check `HEAD` for all URLs and a range `GET` of the ISO. If the SMB connection is lost, serving the ISO returns 404; the Boot diagnostics menu keeps working.

## Check and rollback

In the HTTPS admin interface, open **Menu**, select **Ubuntu Desktop 26.04.1**, check **Show Ubuntu Desktop item** in its properties, then press **Publish menu**. Then start the test PXE VM with 16 GB of RAM, choose Ubuntu and wait for the live system desktop. Check SeaBIOS, OVMF without Secure Boot and OVMF with Secure Boot separately. Network access to the menu already worked before this check, but kernel boot compatibility in the three modes was not yet established.

If the boot fails, restore the previous diagnostics version in **Menu → Versions** in the admin interface. On the test server, the previous binaries, unit and settings are kept in `/var/backups/pxe-platform-pre-ubuntu-20261016/`. Removing the Ubuntu item from the menu does not require turning off PXE, DHCP or the SMB storage.

## Test server state on 16.10.2026

The updated Boot and Control were installed on `192.168.10.135`, and the settings and boot files were added. Five services are active. `HEAD` returned 200 for the kernel (17,295,752 bytes), initrd (95,487,354 bytes) and ISO (6,482,409,472 bytes). A request for the first kilobyte of the ISO returned HTTP 206 and exactly 1024 bytes. The admin sign-in page returned HTTP 200. Before publishing, the Ubuntu item was absent; the SHA-256 of the active menu stayed `674ec52f0b9227bd69388dba861da5ecffb470c92da2aa7baf9607085be7c087`.

After the item was published, the first client attempt with Secure Boot stopped at `Verification failed: Security Policy Violation` while loading the kernel and initrd. The initial profile lacked the iPXE `shim` command. This matches the [iPXE recommendation for Linux with Secure Boot](https://ipxe.org/secboot). The ISO contains an Ubuntu `bootx64.efi` signed by Microsoft and a kernel signed by Canonical. The `shim` command was added to the menu generator; the file from the ISO was installed on Boot and responds with HTTP 200. Applying the fix required publishing the menu again in the admin interface; booting with Secure Boot enabled was not yet checked at that point.

With Secure Boot disabled, the client got past the kernel start and DHCP and began downloading the ISO. The download stopped with `wget: short write: No space left on device` after about 850 MB. The settings screenshot of test PXE VM 112 showed 4 GB of RAM, so its memory had to be increased before the next test. The PXE server and the SMB storage were responding at that time; the Boot log shows the ISO request from the client `10.30.0.7`.

After VM 112 memory was increased to 16 GB, the user confirmed that the Ubuntu desktop appeared with Secure Boot disabled. The Boot log recorded the menu, kernel, initrd and ISO requests from `10.30.0.7`. The check with Secure Boot enabled and the updated menu containing the `shim` command remained a separate step.

The updated menu with the `shim` command was published. In the next attempt, VM 112 requested the signed Ubuntu shim, the kernel, initrd and ISO. This confirms that the boot passed the previous `Security Policy Violation` failure point and reached `casper`. The user confirmed that the desktop appeared with Secure Boot enabled. Both OVMF modes, with and without Secure Boot, were therefore checked for this ISO. Booting the ISO itself in BIOS was not yet checked.

In the first Ubuntu check under SeaBIOS, the kernel and initrd loaded over HTTP, and then iPXE stopped at `shim: command not found`. The BIOS build of iPXE does not have this command. The menu generator now checks `${platform}` and calls `shim` only on UEFI; SeaBIOS goes straight to `boot`. Both menu generation paths were changed: the plain one and the one with folders. Regression tests reproduced the previous script and passed after the fix; `go test ./...` and `go vet ./...` also passed. The updated Control was installed on the test server on 19.10.2026, and Boot stayed active. The menu was published again, and HTTP serves the branch with the platform check. In the next attempt SeaBIOS downloaded the ISO, after which the user confirmed that the Ubuntu desktop appeared. SeaBIOS, OVMF with Secure Boot disabled and OVMF with Secure Boot enabled were therefore all checked for this ISO.

## Automated installation

In the current model, create an Ubuntu installation and optionally select an immutable Autoinstall file from the library. See the [Control guide](control-installation.md#ubuntu-autoinstall) for the constructor, disk selection, password-hash exposure on the PXE subnet, deployment and rollback. The boots above used older builds: NoCloud retrieval and both disk modes still need BIOS, UEFI and Secure Boot tests on this build. Test automatic erasure only on a disposable VM disk.
