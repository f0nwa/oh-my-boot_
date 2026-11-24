---
slug: monitoring-and-events
section: panel
order: 20
title: Now Screen, Machines, and Logs
summary: Tracking live boots, dropping stale SMB/HTTP sessions, DHCP flood protection, machine inventory, system events, and ⌘K search.
keywords: [now, monitoring, machines, log, events, dhcp, smb, sessions, search, badges]
---

OMB separates live operational visibility (“what is happening right now”) from historical records (“what happened over the past days”).

## The “Now” home page (`/`)

The [Now](/) page refreshes automatically and shows:

- **Active client activity:** which machines just requested DHCP, downloaded a bootloader, opened the menu, or picked an installation.
- **Secure Boot diagnostics:** if a client downloads `ipxe-shim.efi` but does not request `ipxe.efi` within 30 s, OMB flags the row with “Firmware rejected bootloader”.
- **Active SMB and HTTP transfers:** which machines are reading images from `[ombisos]` or downloading Ubuntu ISOs. Using the `☰` row menu, an operator or administrator can force-close a stuck SMB session or cancel a stale HTTP transfer.
- **DHCP flood protection:** if a faulty NIC floods the server with requests, the DHCP event buffer caps entries at 50 per MAC, and the machine page lets you enable blocking (`<mac>,ignore` in `/var/lib/omb-dhcp/hosts`, applied via `SIGHUP` without restarting `dnsmasq`).
- **Maintenance alerts:** when maintenance mode is active on a storage or image, a warning banner appears on Now.

## Machine inventory (`/machines` and `/events/machines/{mac}`)

[System → Machines](/machines) stores known workstations (`machines` table) and 3–5 byte MAC prefix rules (`mac_prefix_rules`).

- Each machine can be given a readable name, a note, and an individual UEFI network driver (`ipxe.efi` or `snponly.efi`).
- Bulk CSV import (with a change preview) and full CSV export (`/machines.csv`) are supported.
- An individual machine card (`/events/machines/{mac}`) displays SMBIOS manufacturer/model, the effective driver rule, and a targeted history of its HTTP and DHCP events.

## Logs (`/events`) — four tabs

| Tab | What it stores |
| --- | --- |
| **Machines** | Summary feed of recent boot activity per machine |
| **HTTP requests** | `omb-boot` events (`hello`, `menu`, `choice`, asset downloads) with 50-row server-side pagination |
| **DHCP and TFTP** | `omb-dnsmasq` events (IP leases, TFTP transfers), refreshed via the agent's cursor cache |
| **System events** | System incidents (`system_events`): unavailable services, storage mount failures, auth errors. Tracked as **Active** or **✓ Resolved** (`resolved_at`) |

Auto-cleanup retention and manual log clearing are grouped under “Log settings” in the `/events` header. Manual clearing affects the three network tabs only (“Machines”, “HTTP requests”, “DHCP and TFTP”); resolved system events are cleared separately via their dedicated button.

## Navigation badges, live refresh, and `⌘K` search

- **Problem badges:** the background `SystemEventMonitor` checks every 15 s for active system incidents, unavailable storages, and running migrations. A red or amber dot appears in the top bar, and a numeric pill appears on the relevant sub-tab.
- **Background live refresh:** tables on `/images`, `/installations`, and `/storages` refresh seamlessly every 15 s without full page reloads (pausing when the tab is hidden, a dialog is open, or a search field has focus).
- **Global search `⌘K` (or `Ctrl K`, or `/`):** searches simultaneously across MAC addresses, machine names, boots, ISO images, installation scripts, SHA-256 checksums, authors, users, panel sections, and Wiki articles.

## Audit log (`/audit`) and server resources (`/system`)

- [System → Audit](/audit) (available to `admin`) records all system changes: menu publications, logins and password/2FA resets, network and storage changes, ISO migrations, and integration settings.
- [System → Overview](/system) displays CPU usage, RAM (split into processes and image disk cache), live network interface throughput (read from `/proc` with no external agents), uptime, service states, current OMB release and channel (`stable`/`dev`), PXE network summary, server time, and storage usage.
