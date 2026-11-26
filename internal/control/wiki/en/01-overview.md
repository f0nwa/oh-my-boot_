---
slug: overview
section: start
order: 10
title: What is oh my boot_
summary: Platform purpose, access roles, core glossary, and boundaries of OMB in an air-gapped network.
keywords: [overview, roles, admin, operator, observer, glossary, concepts, console]
---

oh my boot_ (OMB) is a self-contained network boot and operating system deployment server for an isolated corporate network. It runs on Linux with no internet access and serves a PXE menu to Legacy BIOS and UEFI clients, including machines with Secure Boot enabled.

## What OMB does

- **Network boot:** serves an iPXE menu over Full DHCP or Proxy DHCP (`dnsmasq`) and HTTP (`omb-boot`), supporting UEFI network driver selection (`ipxe.efi` or `snponly.efi`) globally, by MAC prefix, and per machine.
- **OS installation and maintenance:** deploys Windows 10 and 11 via WinPE with `autounattend.xml` answer files and driver injection, boots the Strelec service image (`SSTR`), and installs Ubuntu Desktop with NoCloud Autoinstall.
- **Library management.** OMB indexes ISO, WIM, and ESD files on local and network storage. Add ISOs from your computer, from a URL, or over SMB `[iso]` and SFTP. [Storage and image uploads](/wiki/storages-and-file-server).
- **Control and security:** enforces role-based access, supports local passwords with expiration policies and 2FA (TOTP), external sign-in via Active Directory (LDAP) and Keycloak (OIDC), and keeps a machine inventory, event logs, and an audit trail.

## Access roles

| Role | What it can view and do |
| --- | --- |
| **Observer** (`observer`) | View the Now screen, published menu, library, logs, machines, network settings, and this Wiki. No modifications allowed. |
| **Operator** (`operator`) | Everything an observer can do, plus library operations (ISO upload, installation scripts, drivers, creating and editing boots), editing the menu draft, publishing the menu, and editing machine records. |
| **Administrator** (`admin`) | Full access: PXE network configuration, connecting and removing storages, local file server, maintenance mode, user management, security policy, AD/LDAP and Keycloak integrations, system time, and log cleanup. |

> [!NOTE]
> An OMB account always belongs to exactly one authentication source: the local database, Active Directory, or Keycloak. Matching usernames or email addresses never merge accounts across sources.

## Core glossary

- **ISO image** — a source installation or service `.iso` file on the local disk, an SMB share, or an NFS storage.
- **Installation script (answer file)** — a saved `autounattend.xml` scenario for Windows or `user-data` for Ubuntu. Scripts are not hard-bound to a single ISO; Windows script compatibility is verified when selecting an image.
- **Boot** — a ready-to-use combination of an image, an installation script, and (for Windows/Strelec) a WinPE driver set that can be added to the menu.
- **Menu draft** — a working copy of the PXE menu. Until an administrator or operator clicks “Publish menu”, network clients keep seeing the previously published version.
- **Local administrator** — an active local account with the `admin` role. At least one such account always remains enabled so management is never lost if the domain or Keycloak becomes unavailable.

## Scope of responsibility

OMB controls the network boot stage: serving the bootloader, menu, `boot.wim`, ISO, and answer files. Once `setup.exe` or the Ubuntu installer starts, the rest of the installation depends on the OS itself and the target hardware. For this reason, the “Ready” status on an answer file means the file is saved and syntax-checked, not that installation is guaranteed on every hardware model — the library provides manual operator check records for tracking verified runs.
