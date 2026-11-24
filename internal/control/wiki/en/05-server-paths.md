---
slug: server-paths
section: architecture
order: 30
title: Server Directories and Ports
summary: Where configurations, encryption keys, ISO images, TFTP boot assets, and logs live on the server, and which network ports OMB uses.
keywords: [paths, directories, ports, /etc/omb, /srv/omb, /var/lib, /mnt, firewall]
---

Reference map of the OMB server filesystem layout and network ports for administration, backups, and firewall configuration.

## Core server directories

| Path | Purpose | Written by |
| --- | --- | --- |
| `/usr/local/bin/omb-*` | OMB service binaries and CLI utilities | `omb` package (`.deb`) |
| `/usr/lib/systemd/system/omb-*.service` | OMB systemd unit files | `omb` package (`.deb`) |
| `/usr/share/omb/` | `windows-profiles/` templates and `release.json` release metadata | `omb` package (`.deb`) |
| `/etc/omb/` | Service configs (`boot.env`, `control.env`, `images.env`, `dnsmasq.conf`), encryption key `secrets.key` (`0600`), `omb-iso-password`, `samba-usermap.conf`, `smb/` and `nfs/` credentials, `windows-profiles/omb-setup.cmd.in` template | Administrator / `omb-agent` |
| `/var/lib/omb-control/` | Web panel TLS certificate and private key (`control.crt`, `control.key` `0600`) | Administrator |
| `/var/lib/omb-boot/` | Snapshot of the published `menu.ipxe` | `omb-boot` |
| `/var/lib/omb-dhcp/hosts` | Per-MAC/OUI UEFI driver rules and `<mac>,ignore` entries | `omb-agent` |
| `/var/lib/omb-images/iso/` | Local ISO storage (also the root for SMB `[iso]` share and SFTP) | `omb-images`, `omb-control`, `smbd` |
| `/var/lib/omb-deployment/` | Package deployment state: `COMMIT`, `PACKAGE`, `PACKAGE_SHA256`, `CHANNEL`, `current.deb` | `offline-deploy.sh` |
| `/srv/omb/tftp/` | TFTP and HTTP asset root: `ipxeboot/`, `windows/wimboot`, `windows-profiles/`, `windows-answers/`, `windows-driver-wims/` (SHA-256 deduplicated), `windows-driver-configs/`, `ubuntu-sources/` | `omb-agent` |
| `/mnt/omb-smb/<key>`, `/mnt/omb-nfs/<key>` | Read-only mount points for network storages (`*-upload/<key>` for write access) | `omb-agent` |
| `/mnt/omb-iso/profiles/<id>` | Loopback mounts of Windows and Strelec ISOs (`mount -o loop,ro`) | `omb-winiso@` |
| `/var/backups/omb/` | Pre-update backups (binaries, configs, DB dumps) | `offline-deploy.sh` |

> [!WARNING]
> `/etc/omb/secrets.key` (32 bytes / 64 hex chars, mode `0600 omb-control:omb-control`) encrypts AD/LDAP and Keycloak secrets plus 2FA TOTP keys (AES-256-GCM). Always include `/etc/omb/` in backups — losing this key requires re-entering integration credentials and resetting user 2FA.

## Network ports

| Port & protocol | Service | Who needs access |
| --- | --- | --- |
| TCP `8443` | `omb-control` | Management network — admin and operator web panel (HTTPS) |
| TCP `8080` | `omb-boot` | PXE boot network — serves `menu.ipxe`, assets, `boot.wim`, Ubuntu ISOs, and Autoinstall |
| UDP `67` | `omb-dnsmasq` | PXE boot network — DHCP server (Full DHCP mode) |
| UDP `4011` | `omb-dnsmasq` | PXE boot network — Proxy DHCP |
| UDP `69` | `omb-dnsmasq` | PXE boot network — TFTP (`undionly.kpxe`, `ipxe-shim.efi`, `ipxe.efi`, `snponly.efi`) |
| TCP `445` | `smbd` | PXE boot network for RO share `[ombisos]` (restricted by `hosts allow`) and management network for `[iso]` share (if enabled) |
| TCP `2022` (configurable) | `omb-control` | Management network — built-in SFTPv3 server for ISO uploads (if enabled) |

## `omb-agent.service` sandboxing

`omb-agent` runs as `root` but is sandboxed by systemd `ProtectSystem=strict` with an explicit writable allowlist (`ReadWritePaths`). On manual deployments, `/etc/omb`, `/etc/omb/winiso`, `/mnt/omb-smb`, `/mnt/omb-smb-upload`, `/mnt/omb-iso/profiles`, and `/srv/omb/tftp/windows-*` subdirectories must exist before starting the agent. When installing via the `.deb` package, they are created automatically.
