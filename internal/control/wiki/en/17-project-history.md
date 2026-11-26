---
slug: project-history
section: operations
order: 30
title: oh my boot_ Development History
summary: Timeline of the project from the first iPXE menu in October 2026 to self-contained offline .deb releases with 2FA, AD/Keycloak, and the embedded Wiki.
keywords: [history, timeline, releases, commits, migration, rebranding, milestones]
---

The history of oh my boot_ is compiled from the project journal and commit history across October–November 2026. It explains why the architecture and interface look the way they do today.

## October 2026: Foundation, Windows WinPE, and Brand Birth

- **October 15 — PXE foundation and web panel:** established the container-free architecture on Ubuntu Server (`omb-boot`, `omb-agent`, `omb-dnsmasq`, PostgreSQL). Verified Legacy BIOS and UEFI booting (with and without Secure Boot) in Full DHCP and Proxy DHCP modes in VLAN 30. Launched the HTTPS control panel with roles, menu publish/rollback, and ISO catalog with managed RO SMB shares.
- **October 16–19 — Ubuntu Desktop, Windows WinPE, and drivers:** added Ubuntu Desktop network boot via signed Ubuntu `shim` and the full Windows 10/11 pipeline via `wimboot`, ISO loopback mounting, and `[ombisos]`. Added browser ISO uploads, immutable `autounattend.xml` answer files, and the WinPE driver catalog with `.inf` injection via `wimlib-imagex` (verified with VirtIO Net/SCSI and Windows 11 26H2).
- **October 20–23 — “Console” design and machine inventory:** introduced the “Console” UI (light and dark themes, Now screen, iPXE menu preview, drag-and-drop, `⌘K` global search), RU/EN localization, and the machine inventory with CSV import and MAC prefix rules.
- **October 23–26 — Secure Boot, `unattend` generator, and USB media:** added “Firmware rejected bootloader” diagnostics, the SCCM script `Enable-MicrosoftUefiCA.ps1`, the embedded Go port of `cschneegans/unattend-generator` (12 sections plus `pe.cmd` for network auto-partitioning), and the WinPE UEFI CA USB builder under “Library → USB media”.
- **October 28–31 — oh my boot_ branding, iPXE shim 16.1, and technical migration:** adopted the **oh my boot_ (OMB)** name and prompt mark. Migrated to official dual-signed `iPXE shim 16.1` (Microsoft UEFI CA 2011 and 2023). Built the Ubuntu Autoinstall editor (`yescrypt`, JSON Schema) and unified boot model. On **October 31**, all services, databases, directories, and sockets on the test stand were migrated to the unified `omb-*` prefix.

## November 2026: Performance, Infrastructure, Security, and `.deb` Releases

- **November 2–6 — UEFI drivers, Strelec, and library ergonomics:** added three-tier UEFI driver selection (`ipxe.efi` / `snponly.efi`) applied via `SIGHUP` without restarting `dnsmasq`, plus `reuse_firmware_network`. Added **Strelec (`SSTR`)** service image support, multi-ISO Ubuntu, ISO auto-detection, and a sticky top navigation bar.
- **November 9–11 — Monitoring, disk accounting, and UI performance:** added WinPE network wait and Setup exit settings, `/proc`-based CPU/RAM/network monitoring, and accurate disk accounting with `boot.wim` deduplication. Completed a 7-stage performance optimization: `/events` TTFB dropped from 4.3 s to ~16 ms, added 50-row server pagination with infinite scroll, Ubuntu readiness TTL cache, and on-demand details; passed a load test of 500 machines and 16,682 requests with zero errors (p95 ≤ 106 ms). Removed all `<details>`/`<summary>` elements.
- **November 12–16 — Unbound scripts and table redesign:** decoupled Windows answer files from specific ISOs with three-level `install.wim` compatibility checks and manual operator check tracking; added smart “OS + Script” title composition, redesigned `/images`, interactive SHA-256 reveal, and the network boot animation on the login screen.
- **November 17–18 — NFS, background tasks, ISO migration, and password policy:** added NFSv4.1/NFSv3 with `ro`/`rw` auto-detection, automatic ISO rescan filtering junk files, seamless ISO migration between storages, table sorting across 5 sections, and local password expiration policies.
- **November 19–20 — Local SMB/SFTP, Maintenance/Drain, NTP, and external auth:** implemented the managed `[iso]` share + built-in SFTPv3 (`:2022`), maintenance mode with Drain, DHCP flood protection, Active/Resolved system events log, background table refresh (`live-refresh.js`), navigation problem badges, web-based NTP configuration, and external authentication via **Active Directory (LDAP)** and **Keycloak (OIDC)** with no new `go.mod` dependencies.
- **November 21–22 — Offline `.deb` packages and signed APT:** built `offline-deb.py` and full-scope `offline-deploy.sh` (signed `repo-manifest`, updating all services and all 3 DBs, transactional auto-rollback, and recovery after `SIGKILL`). Passed offline acceptance on clean Ubuntu in VLAN 30 and migrated the test stand to the `omb` package.
- **November 23 — 2FA (TOTP), `stable`/`dev` channels, GRUB removal, and network form:** added local 2FA (TOTP + built-in SVG QR + 8 recovery codes) and 365-day trusted browsers; established parallel `stable` (`main`) and `dev` (`dev`) release channels with UI version indicators; published up-to-date architecture docs; removed the obsolete `grub-test` route (superseded by dual-signed `shim 16.1`); reorganized `/network`.
- **November 24 — Embedded Wiki and cumulative migrations:** added the local embedded `/wiki` knowledge base (Russian and English) with search and `⌘K` integration, plus self-contained `.deb` releases with `schema_migrations` for skipping versions.
- **November 26 — Stable release `v0.2.6`.** Added independent `boot.wim` and `install.wim` / `.esd` overrides, ISO version inspection, and bulk actions in library and machine tables.
- **November 26–27 — Development branch changes for 0.2.7.** Added HTTP(S) ISO downloads, background SHA-256 indexing, and minimizable upload dialogs. WinPE driver packages gained names and descriptions. The script library gained Ubuntu YAML uploads and in-place editing of Windows XML and editor-generated Ubuntu scripts.
