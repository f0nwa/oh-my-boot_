---
slug: services-and-databases
section: architecture
order: 10
title: Services, Unix Sockets, and Databases
summary: "How OMB is built internally: six systemd units, privilege separation, local Unix sockets, and three isolated PostgreSQL databases."
keywords: [architecture, systemd, postgresql, sockets, omb-control, omb-boot, omb-images, omb-agent, dnsmasq]
---

OMB runs on a single Ubuntu Server host with no containers and no external message brokers. The system is split into least-privilege services so the web interface never performs privileged operations directly.

## Six systemd services

| Service | System account | Entry point | Responsibility |
| --- | --- | --- | --- |
| `omb-control.service` | `omb-control` | HTTPS `:8443`, SFTP `:2022` | Web panel, users, 2FA, AD/LDAP, Keycloak, menu draft, audit log, this Wiki |
| `omb-boot.service` | `omb-boot` | HTTP `:8080` | Serves `menu.ipxe`, assets and Autoinstall, boot log, maintenance mode |
| `omb-images.service` | `omb-images` | Unix socket | ISO catalog, periodic rescan, streaming uploads (up to 32 GiB), ISO migration |
| `omb-agent.service` | `root` | Unix socket | The only privileged service: `dnsmasq`, SMB/NFS mounts, `boot.wim` builds, Samba, NTP |
| `omb-dnsmasq.service` | `dnsmasq` | UDP `67`, `69`, `4011` | Full DHCP or Proxy DHCP, TFTP delivery, and per-MAC driver rules |
| `omb-winiso@.service` | `root` (oneshot) | Triggered by agent | Prepares and mounts (`mount -o loop,ro`) Windows/Strelec ISOs under `/mnt/omb-iso/profiles/<id>` |

Alongside the six OMB units, the server runs system `postgresql` and `smbd` (Samba serves the boot share `[ombisos]` and, when enabled, the `[iso]` upload share).

> [!IMPORTANT]
> Stopping `omb-control` does not interrupt network booting: serving the published menu and images to clients is handled by `omb-boot`, `omb-dnsmasq`, and `smbd`.

## Internal communication over Unix sockets

Services communicate over HTTP via local Unix sockets without exposing extra TCP ports:

| Socket | Owner | Callers and purpose |
| --- | --- | --- |
| `/run/omb/agent.sock` | `omb-agent` | `omb-control` and `omb-images` — applying network config, mounting SMB/NFS, WinPE builds, time/NTP, session metrics |
| `/run/omb-boot/control.sock` | `omb-boot` | `omb-control` — publishing and rolling back `menu.ipxe`, reading HTTP boot logs, managing maintenance locks |
| `/run/omb-images/images.sock` | `omb-images` | `omb-control` — storage and ISO catalog, triggering scans, uploads, deletions, and migrations |

## Three isolated PostgreSQL databases

All databases connect locally using peer authentication, with no database passwords in environment files:

1. **`omb_control`** (role `omb-control`, 33 tables) — users, sessions, password policy, TOTP 2FA and trusted browsers, AD/LDAP and Keycloak integrations, menu draft and versions, boots, Windows profiles and Ubuntu sources, answer files and manual checks, WinPE drivers, machine inventory and MAC prefix rules, maintenance locks, system events, file server settings, and audit log.
2. **`omb_images`** (role `omb-images`, 3 tables) — connected storages (`storages`), discovered ISO files (`images`), and background task schedule (`catalog_settings`).
3. **`omb_boot`** (role `omb-boot`, 1 table) — client network request log (`boot_events`).

## Command-line utilities

In addition to the services, the package ships CLI tools in `/usr/local/bin/`:

- `omb-controlctl` — create the initial administrator (`create-admin`), reset a password (`reset-password`), and reset 2FA (`reset-2fa`).
- `omb-agentctl`, `omb-imagesctl`, `omb-winiso-prepare` — diagnostic and profile preparation helpers.

## Quick health check

```bash
systemctl is-active omb-control omb-boot omb-images omb-agent omb-dnsmasq postgresql smbd
ss -xl | grep /run/omb
```
