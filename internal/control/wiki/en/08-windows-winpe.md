---
slug: windows-winpe
section: os-scenarios
order: 10
title: Deploying Windows 10/11 via WinPE
summary: How OMB prepares and boots Windows 10 and 11 images over the network using wimboot, WinPE, and an isolated Samba share.
keywords: [windows, winpe, wimboot, boot.wim, install.wim, install.esd, omb-setup.cmd, ombisos, setup.exe, iso, media not ready, retrying, wpeinit]
---

OMB deploys Windows 10 and Windows 11 (verified including Windows 11 26H2) on Legacy BIOS and UEFI (with or without Secure Boot) without WDS and without modifying the original ISO or WIM/ESD files.

## Windows images: `ISO | boot.wim | install.wim` tabs

Under [Library → Images](/images), three tabs are available:

- **ISO** — full Windows and Linux ISO images on local, SMB, or NFS storage. During inspection, OMB indexes the versions of the embedded `boot.wim` and `install.wim` / `install.esd`.
- **boot.wim** — standalone WinPE boot images (`boot.wim`), such as a newer WinPE or a custom ADK image. During inspection, OMB reads its version/build, boot index, and checks whether `sources\setup.exe` is present inside the image.
- **install.wim** — standalone `install.wim` or `install.esd` installation images (for example, customized corporate images with slipstreamed updates and applications).

## How profile preparation works

1. Upload or connect a Windows ISO (or standalone `boot.wim` / `install.wim` files) on local, SMB, or NFS storage and inspect them on the matching tab under [Library → Images](/images).
2. When you create a boot under [Library → Boots](/installations), `omb-agent` prepares the required artifacts:
   - for a base ISO (`omb-winiso@.service`), it loop-mounts the ISO read-only (`mount -o loop,ro`) at `/mnt/omb-iso/profiles/<id>`, extracts the minimal WinPE boot set (`boot.wim`, `BCD`, `boot.sdi`) into `/srv/omb/tftp/windows-profiles/<id>/`, and exposes the mounted content inside the read-only Samba share `[ombisos]`;
   - for a standalone `boot.wim`, it caches the verified `boot.wim` under `/srv/omb/tftp/windows-boot-images/<id>/`;
   - for a standalone `install.wim` / `install.esd`, it exposes the file via a read-only bind mount inside `[ombisos]` and generates startup scripts under `/srv/omb/tftp/windows-install-images/<id>/`.
3. `omb-setup.cmd` is generated from `/etc/omb/windows-profiles/omb-setup.cmd.in` with the current network wait settings and post-Setup exit action.

## Swapping `boot.wim` and `install.wim`

Both when creating (`/installations/new`) and editing (`/installations/<id>/edit`) a Windows boot, you can use a standard ISO as-is or swap its components independently:

- **Swapping `boot.wim`:** pick `boot.wim` from another inspected Windows ISO or a standalone file from the **boot.wim** tab. This is useful, for example, to boot Windows 10 with a newer Windows 11 WinPE.
- **Swapping `install.wim` / `install.esd`:** select a standalone image from the **install.wim** tab on top of a base ISO, or **without any base ISO** at all (standalone `boot.wim` + standalone `install.wim`).

> [!IMPORTANT]
> **`setup.exe` requirement for no-ISO mode:** when a boot is configured without a base ISO (standalone `boot.wim` + standalone `install.wim`), the selected `boot.wim` must contain `sources\setup.exe` (standard `boot.wim` files from Windows install ISOs include it; a bare ADK WinPE without `setup.exe` cannot be used without a base ISO). When a base ISO is selected, `setup.exe` is launched from the ISO and `install.wim` is passed via `/installfrom`.

> [!NOTE]
> **Version rule `boot.wim ≥ install.wim`:** the `boot.wim` (WinPE) version and build must be greater than or equal to the `install.wim` / `install.esd` version and build. A newer `boot.wim` cleanly installs older Windows editions, whereas launching a newer `install.wim` from an older `boot.wim` can fail during Setup. OMB displays a version warning on the create and edit forms whenever `boot.wim` is older than `install.wim`.

> [!IMPORTANT]
> HTTP access to `omb-setup.cmd` and XML answer files is restricted to the PXE boot subnet (`Cache-Control: no-store`). Requests from the management network return `404`.

## What happens on the client in WinPE

After selecting a Windows entry in the iPXE menu, the client:

1. Downloads `/assets/windows/wimboot` (`v2.9.0`), `boot.wim`, `BCD`, `boot.sdi`, and `omb-setup.cmd` over HTTP into RAM.
2. Starts WinPE and executes `omb-setup.cmd`.
3. Initializes networking (`wpeinit`) and retries mapping `\\<OMB-IP>\ombisos\<token>` to drive `I:` using the read-only `omb-iso` account.
4. Exits the wait loop immediately as soon as the network is up and the share connects, without waiting for the full timeout.
5. If an answer file is attached, copies it to `X:\autounattend.xml` (running `pe.cmd` first when auto-partitioning is enabled), then launches `I:\setup.exe`.

## Network wait and Setup exit settings

Under [System → Network](/network) in the **WinPE (Windows Setup)** section, three options are configurable:

- **Network and share wait timeout (`winpe_timeout_seconds`):** 10 to 600 s, default 90 s.
- **Retry interval (`winpe_retry_seconds`):** 1 to 30 s, default 2 s.
- **Action after Windows Setup exits:** reboot, shut down, or open a WinPE command prompt (useful for troubleshooting).

When you save new values, `omb-agent` atomically updates the template, regenerates all prepared `omb-setup.cmd` scripts (including Strelec service images), recalculates their `script_sha256`, and refreshes the active menu without restarting `dnsmasq`.

## Reading the WinPE console output (`omb-setup.cmd`)

During WinPE startup, `omb-setup.cmd` prints each stage to the console:

1. `OMB: initializing network...` — runs `wpeinit` and `wpeutil WaitForNetwork` (output is saved to `%SystemRoot%\System32\omb-network-wait.log`).
2. `OMB: network is not ready yet; waiting for installation media...` — `WaitForNetwork` returned before an IP address or switch port was ready, so the script enters the retry loop.
3. `OMB: connecting installation media...` followed by lines such as:
   `OMB: media not ready (error <code>); retrying 10/45...`
4. `OMB: starting Windows Setup...` — the share is mapped, `install.wim` / `install.esd` is found, and `setup.exe` has started.

> [!NOTE]
> Seeing `retrying 1/45...10/45` before Setup starts is **normal behavior**, not a failure. With the default 90 s timeout and 2 s retry interval, the script allows up to `45` attempts while the NIC driver initializes, the switch port finishes STP/PortFast, or DHCP responds. As soon as the share connects, Setup starts immediately.

If the counter reaches the final attempt (`45/45`) and prints `OMB: installation media is unavailable or Setup failed.`, the script drops to a `cmd` prompt. Check the following:

- **Error code in `error <N>`** (exit code from `net use` / Windows system error):
  - `53` or `1231` — no network path, no IP address, missing NIC driver in `boot.wim`, or TCP 445 / PXE client access policy is blocking the client;
  - `5`, `86`, or `1326` — Samba authentication failure for the `omb-iso` account;
  - `net use` succeeded but the script jumped straight to `failed` — `sources\install.wim` / `install.esd` is missing (check the ISO mount on the server).
- **Quick checks right inside that WinPE console:**
  - `ipconfig /all` — verify whether the NIC is detected and has an IP address;
  - `ping <OMB-IP>` — verify reachability of the PXE server;
  - `type %SystemRoot%\System32\omb-network-wait.log` — inspect the `wpeutil WaitForNetwork` output.

## Common WinPE troubleshooting

- **WinPE gets no IP or cannot reach the share:** `boot.wim` lacks the NIC driver for that hardware model. Upload the INF driver under [Library → Drivers](/drivers) and attach it to the boot (see [WinPE Drivers and Strelec Service Image](/wiki/drivers-and-strelec)).
- **Windows Setup does not see the NVMe/RAID disk:** add the storage controller driver (such as Intel VMD / RST or VirtIO SCSI) to the same WinPE driver configuration.
- **The `ombisos` share is unreachable after a NAS outage:** check the source storage status under [System → Storages](/storages) — if the ISO lives on an external SMB/NFS share that dropped, reconnect the storage.
