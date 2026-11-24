---
slug: boot-pipeline
section: architecture
order: 20
title: Network Boot Pipeline
summary: End-to-end journey of a client machine from the first DHCP packet to starting the Windows, Ubuntu, or Strelec installer.
keywords: [pipeline, pxe, ipxe, dhcp, tftp, wimboot, casper, strelec, boot]
---

Understanding the boot stages helps pinpoint failures quickly: simply check the [Now](/) screen to see which step the client reached.

## Stage 1. DHCP and TFTP bootloader delivery

1. The client sends `DHCPDISCOVER` advertising its firmware architecture (Legacy BIOS or UEFI x64).
2. `omb-dnsmasq` replies:
   - in **Full DHCP** mode — assigns an IP address from the pool, gateway/mask, and boot filename;
   - in **Proxy DHCP** mode — an external DHCP server assigns the IP address, while `omb-dnsmasq` responds on UDP `4011` with PXE options only.
3. The client downloads the initial file over TFTP (UDP `69`) from `/srv/omb/tftp/`:
   - **Legacy BIOS:** `ipxeboot/x86_64/undionly.kpxe`;
   - **UEFI x64:** `ipxeboot/x86_64-sb/ipxe-shim.efi` (for the `ipxe.efi` driver) or `ipxeboot/x86_64-sb/snponly-shim.efi` (for the `snponly.efi` firmware driver).
4. On UEFI machines, firmware verifies the `shim` signature (iPXE shim 16.1 is dual-signed with Microsoft UEFI CA 2011 and 2023), and `shim` then launches the iPXE-signed `ipxe.efi` or `snponly.efi`.

## Stage 2. Loading the iPXE menu

Once iPXE starts, it contacts `omb-boot` over HTTP (`:8080`):

- sends `/boot/hello` with MAC address, platform (`pcbios` or `efi`), manufacturer, model, and serial number (when exposed by SMBIOS);
- downloads the published `/boot/menu.ipxe` script.

The client displays the oh my boot_ text menu with a fallback timer to the local disk. Operator selections are recorded via `/boot/choice` in `boot_events`.

> [!NOTE]
> If [maintenance mode](/wiki/storages-and-file-server#maintenance-mode-and-drain) is enabled on the selected image or its storage, `omb-boot` tags the item with `[MAINTENANCE]` on the fly and shows a pause screen instead of starting installation, without changing the published menu version.

## Stage 3. Starting the selected scenario

The next steps depend on the chosen boot type:

### Windows 10 / 11 (WinPE)

1. iPXE downloads `/assets/windows/wimboot`, `BCD`, `boot.sdi`, `boot.wim` (base or with injected WinPE drivers), and `omb-setup.cmd` over HTTP.
2. `wimboot` hands off execution to WinPE in RAM.
3. `omb-setup.cmd` waits for network readiness (90 s timeout, 2 s retry interval by default) and maps the read-only Samba share `\\<OMB-IP>\ombisos\<token>` as drive `I:` (where the ISO is loop-mounted read-only).
4. If an answer file is attached, the script copies it to `X:\autounattend.xml`, launches `I:\setup.exe`, and executes the configured post-exit action (reboot, shutdown, or command prompt).

### Strelec service image (`SSTR`)

Follows the same `wimboot` flow, booting the chosen WinPE 11 or WinPE 10 edition from the Strelec image, loading any injected WinPE drivers (`X:\omb-driver.log`), and mounting the composite image from the `[ombisos]` share (`X:\omb-strelec.log`).

### Ubuntu Desktop amd64

1. On UEFI with Secure Boot, iPXE chains to the signed Ubuntu `shim` (`ubuntu/bootx64.efi`), which boots `vmlinuz` and `initrd` from `/srv/omb/tftp/ubuntu-sources/`.
2. `casper` downloads the ISO over HTTP into client RAM (an image around 6.5 GB requires more than 4 GB of RAM).
3. If an Autoinstall scenario is attached, the installer fetches the NoCloud `user-data` and `meta-data` pair from `/assets/ubuntu-autoinstall/...` and runs unattended installation.
