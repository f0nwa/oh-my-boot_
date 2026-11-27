---
slug: drivers-and-strelec
section: os-scenarios
order: 30
title: WinPE Drivers and Strelec Service Image
summary: Uploading INF driver packages, SHA-256 deduplicated boot.wim builds, and network booting Strelec (SSTR) service images.
keywords: [drivers, winpe, inf, boot.wim, wimlib, strelec, sstr, virtio, vmd]
---

The stock `boot.wim` inside a Windows ISO often lacks drivers for modern NICs, USB-C adapters, or NVMe/RAID controllers (Intel VMD, VirtIO). OMB lets you upload driver packages once and attach them to any Windows or Strelec boot.

## Driver catalog (`/drivers`)

Under [Library → Drivers](/drivers), two upload methods are supported:

- **Driver folder** — pick a local directory on your workstation; the browser uploads the full structure with `.inf`, `.sys`, and `.cat` files.
- **ZIP archive** — upload a vendor driver pack (Dell WinPE Driver Pack, Lenovo, VirtIO, and others).

You can give the package a name and description when uploading it. Click its name in the catalog or choose “Properties and contents” from the card’s `☰` menu to view the `.inf` files, edit the name or description, or download the package. You can search these fields with `⌘K` and in the driver picker when configuring a boot entry.

Uploaded driver packages can be exported back as ZIP archives:

- **Single package** — choose “Download ZIP” in the card’s `☰` menu or inside “Properties and contents”.
- **Selected packages** — check the desired package cards and click “Download ZIP” in the bulk bar.
- **All at once** — click “Download all ZIP” in the `/drivers` page header. Each package is placed into its own clean subfolder inside the archive, ready for `DISM` or re-importing into OMB with the exact same `SHA-256`.

While a folder or ZIP file is uploading, click “Minimize” to follow its progress at the bottom of the page. Keep the tab open until the transfer finishes.

> [!NOTE]
> For WinPE network deployment, two driver classes are usually enough: **Net** (NIC driver so WinPE can connect to `[ombisos]`) and **SCSIAdapter / HDC** (storage controller so Windows Setup sees the drive).

## Attaching drivers and `boot.wim` deduplication

Drivers are selected directly on the [New boot](/installations/new) form:

- if a package contains a single `.inf`, it renders as a single selectable card;
- if a package contains multiple `.inf` files, you can select the whole package or pick individual `.inf` entries.

During the build, `omb-agent` injects the selected drivers into a copy of the active `boot.wim` using `wimlib-imagex` — this works identically for `boot.wim` from the base ISO, when swapping in `boot.wim` from another Windows ISO, or when using a standalone file from the **boot.wim** tab under [Library → Images](/images). Built `boot.wim` files are stored in `/srv/omb/tftp/windows-driver-wims/` with **SHA-256 deduplication** (source `boot.wim` + `.inf` set): if multiple boots share the same `boot.wim` and driver set, the image is neither duplicated on disk nor rebuilt.

Disk space used by configurations is shown in the WinPE configurations table and on [System → Storages](/storages) without double-counting shared `boot.wim` files.

## Strelec service image (`SSTR`)

OMB automatically detects Strelec x64 (`SSTR`) service ISOs during ISO inspection:

1. When creating a boot from a Strelec image, choose the **WinPE 11** or **WinPE 10** edition and optionally attach WinPE driver packages. XML answer file and Autoinstall fields are hidden for Strelec.
2. `omb-agent` adapts `BCD` and builds the connection script using the network wait settings from [System → Network](/network).
3. On the client, drivers load one by one — a single incompatible `.inf` does not abort the boot and is logged to `X:\omb-driver.log`.
4. Share connection and service image mounting steps are recorded in `X:\omb-strelec.log`.

> [!IMPORTANT]
> Just like for Windows, the `windows-driver-configs/*/omb-setup.cmd` URL for Strelec is served only on the PXE boot network (`200`), and returns `404` from the management network.
