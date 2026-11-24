---
slug: windows-winpe
section: os-scenarios
order: 10
title: Deploying Windows 10/11 via WinPE
summary: How OMB prepares and boots Windows 10 and 11 images over the network using wimboot, WinPE, and an isolated Samba share.
keywords: [windows, winpe, wimboot, omb-setup.cmd, ombisos, setup.exe, iso]
---

OMB deploys Windows 10 and Windows 11 (verified including Windows 11 26H2) on Legacy BIOS and UEFI (with or without Secure Boot) without WDS and without modifying the original ISO files.

## How profile preparation works

1. Upload or connect a Windows ISO on local, SMB, or NFS storage and run “Inspect ISO” on [Library → ISO Images](/images).
2. When you create a boot under [Library → Boots](/installations), `omb-agent` prepares the Windows profile (`omb-winiso@.service`):
   - loop-mounts the ISO read-only (`mount -o loop,ro`) at `/mnt/omb-iso/profiles/<id>`;
   - extracts the minimal WinPE boot set (`boot.wim`, `BCD`, `boot.sdi`) into `/srv/omb/tftp/windows-profiles/<id>/`;
   - exposes the mounted ISO content under a random token inside the read-only Samba share `[ombisos]`.
3. `omb-setup.cmd` is generated from `/etc/omb/windows-profiles/omb-setup.cmd.in` with the current network wait settings and post-Setup exit action.

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

## Common WinPE troubleshooting

- **WinPE gets no IP or cannot reach the share:** `boot.wim` lacks the NIC driver for that hardware model. Upload the INF driver under [Library → Drivers](/drivers) and attach it to the boot (see [WinPE Drivers and Strelec Service Image](/wiki/drivers-and-strelec)).
- **Windows Setup does not see the NVMe/RAID disk:** add the storage controller driver (such as Intel VMD / RST or VirtIO SCSI) to the same WinPE driver configuration.
- **The `ombisos` share is unreachable after a NAS outage:** check the source storage status under [System → Storages](/storages) — if the ISO lives on an external SMB/NFS share that dropped, reconnect the storage.
