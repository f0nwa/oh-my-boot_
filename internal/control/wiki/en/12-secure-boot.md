---
slug: secure-boot
section: infra
order: 10
title: Secure Boot, iPXE shim 16.1, and UEFI CA Keys
summary: How network booting works with Secure Boot enabled, why Microsoft UEFI CA 2011/2023 is required, and how to enable it via SCCM or a USB stick.
keywords: [secure boot, shim, uefi, ca 2011, ca 2023, sccm, usb, bitlocker, db]
---

OMB boots UEFI machines over the network **without disabling Secure Boot**. It uses the official `iPXE shim 16.1`, dual-signed with both Microsoft third-party UEFI CA certificates.

## Trust chain and pinned versions

The server keeps the following pinned boot assets in `/srv/omb/tftp/` (checksums are verified during offline bundle builds and by `scripts/fetch-ipxe.sh`):

| Component | Version | Purpose |
| --- | --- | --- |
| `iPXE` | `v2.0.0` | `undionly.kpxe` (BIOS), `ipxe.efi` and `snponly.efi` (UEFI x64, signed with the iPXE key) |
| `iPXE shim` | `16.1` | Initial EFI binary (`ipxe-shim.efi` / `snponly-shim.efi`), dual-signed with **Microsoft Corporation UEFI CA 2011** and **Microsoft UEFI CA 2023** |
| `wimboot` | `v2.9.0` | Boots Windows WinPE and Strelec over HTTP |

On network boot, UEFI firmware verifies `shim` against its built-in `db` signature database. If third-party **Microsoft UEFI CA 2011** or **Microsoft UEFI CA 2023** is allowed in `db`, firmware executes `shim`, which in turn verifies and launches `ipxe.efi` or `snponly.efi`.

> [!NOTE]
> After migrating to dual-signed `iPXE shim 16.1` (2011 + 2023), the old experimental GRUB route became obsolete and was completely removed from OMB on 2026-11-23.

## Why “Firmware rejected bootloader” happens

Some corporate laptops (Secured-core PCs and newer Lenovo, Dell, and HP models) ship with only Windows certificates enabled in `db` (`Windows Production PCA 2011` / `Windows UEFI CA 2023`), while the BIOS option for third-party Microsoft UEFI CA bootloaders is disabled.

In that case, the client downloads `ipxe-shim.efi` over TFTP, firmware silently rejects it, and `ipxe.efi` is never requested. If no iPXE request arrives within 30 s after TFTP delivers `shim`, the [Now](/) page displays the hint **“Firmware rejected bootloader”**.

## Method 1. Enabling the key in a domain (SCCM / PowerShell)

For domain-joined Windows fleets, the repository provides `scripts/windows/Enable-MicrosoftUefiCA.ps1`:

- supports corporate **Lenovo** (ThinkPad/ThinkCentre), **Dell** (2018+), and **HP** laptops via standard BIOS WMI interfaces;
- checks whether Microsoft UEFI CA 2011 **or** 2023 is already present in `db`;
- suspends **BitLocker** for one reboot before changing BIOS settings;
- supports reading BIOS passwords from a CSV file keyed by `SerialNumber`;
- provides `-CheckOnly` (audit only) and `-Report` (fleet summary) modes;
- writes a detailed log to `C:\Windows\Temp\OMB-MicrosoftUefiCA.log`.

Main exit codes: `0` (already enabled), `3010` (enabled, reboot required), `2` (unsupported model/WMI), `3` (missing or wrong BIOS password), `4` (failed to suspend BitLocker), `1` (general error).

## Method 2. UEFI CA USB stick for non-domain machines

If a laptop is not domain-joined or has no Windows installed yet, open [Library → USB media](/usb):

1. Select a prepared Windows profile (its WinPE `boot.wim` is used).
2. Choose the USB bootloader: **PCA 2011** (for most existing hardware) or **UEFI CA 2023** (for newer systems whose `db` only retains the 2023 certificate).
3. Download the generated archive `omb-uefica-<profile>-<2011|2023>.zip` (~600 MB) and extract it to the root of a standard FAT32 USB flash drive.

Booting from the USB stick starts a minimal WinPE with a VBScript helper that detects the vendor (Lenovo, Dell, HP), prompts for a BIOS password if required, and enables third-party Microsoft UEFI CA without auto-rebooting (leaving a status screen with exit code `20`/`10`/`2`/`3`/`1`).
