---
slug: storages-and-file-server
section: infra
order: 30
title: ISO Storages, SMB/NFS, and Local File Server
summary: Connecting SMB and NFSv4.1/v3, disk accounting, auto-rescan, ISO migration, local SMB/SFTP file server, and maintenance mode.
keywords: [storages, smb, nfs, sftp, file server, migration, rescan, maintenance, drain]
---

All ISO image sources are managed under [System → Storages](/storages). The same page shows detailed local disk usage across ISOs, `boot.wim`, Ubuntu sources, answer files, and USB archives (without double-counting deduplicated driver WIMs).

## Storage types

- **Local storage (`/var/lib/omb-images/iso`):** available out of the box with read/write access.
- **SMB / CIFS (`//host/share/subdir`):** mounted via `cifs-utils` at `/mnt/omb-smb/<key>`. Credentials are stored in `/etc/omb/smb/<key>` with mode `0600`. Supports mounting a subdirectory directly and automatic recovery after NAS drops (including `stale file handle`).
- **NFSv4.1 and NFSv3 (`host:/export/path`):** defaults to NFSv4.1 (TCP `2049` only), with NFSv3 (RPC `111` + `2049`, `nolock`) available when needed. Write permissions (`ro` or `rw`) are detected automatically via a write probe as `omb-images`.

> [!NOTE]
> On NFS exports with `root_squash` enabled (such as Synology DSM), ISO files must be world-readable (`0644`) and directories traversable (`o+rx`).

## Storage background tasks

The **“Background tasks”** button in the `/storages` header opens a single dialog for two periodic `omb-images` jobs:

- **Connection recovery:** automatically reconnects dropped SMB/NFS shares on a schedule.
- **Automatic ISO rescan:** periodically scans directories (1 to 1,440 min interval). Automatically skips macOS metadata files (`._*`, `.DS_Store`) and recycle/trash folders (`#recycle`, `.trash`, hidden `.*` directories).

If a storage goes offline, [Library → ISO Images](/images) shows an honest outage state, hides unavailable row actions, and `/menu` highlights affected items.

## Migrating ISOs between storages

An administrator can move any ISO image from one storage to another without recreating boots or republishing the menu:

1. On [ISO Images](/images), open the `☰` row menu and select **“Move to another storage…”**.
2. OMB checks free space, write access on the target storage, and confirms no active WinPE SMB sessions are reading the image.
3. The file streams to a temporary `.part` file preserving its relative path, renames atomically, `omb-control` transactionally rebinds `windows_profiles`, `ubuntu_sources`, `image_names`, and `image_inspections`, `omb-agent` remounts active loop mounts, and the source file is then removed.

## Local file server (SMB `[iso]` and SFTPv3)

For convenient uploading of large images (Windows, Strelec, Ubuntu) from admin workstations straight into `/var/lib/omb-images/iso`, OMB includes a local file server. Enable it from the `☰` menu on the local storage row in `/storages`:

- **Samba share `[iso]`:** managed via `/etc/omb/samba-usermap.conf` (`omb-fs-<id>` accounts). Strictly isolated from the boot-only RO share `[ombisos]`, which is restricted to the PXE subnet.
- **Built-in SFTPv3 (`:2022`, configurable in `1024–65535`):** runs inside `omb-control`, strictly jailed to `/var/lib/omb-images/iso`, blocks `../` traversal, symlinks, and junk uploads (`.DS_Store`, `._*`, `Thumbs.db`).

Administrators grant per-user permissions (`none` / `ro` / `rw`) on [System → Users](/users). Each permitted user sets their own separate file-access password (12+ characters) in [Account](/account), which also displays ready-to-copy connection addresses for Windows, macOS, and Linux.

## Maintenance mode and Drain

Before planned NAS maintenance or replacing an image, enable **maintenance mode** via the `☰` menu on [Storages](/storages) or [ISO Images](/images):

- in-progress installations finish normally (**Drain** mode);
- new image download requests receive HTTP `503`, while `omb-boot` dynamically marks affected menu entries with `[MAINTENANCE]` and displays a pause screen without republishing the draft;
- a maintenance banner appears on the [Now](/) page.
