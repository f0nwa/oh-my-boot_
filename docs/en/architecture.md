# oh my boot_ (OMB) Architecture

This document describes the oh my boot_ services, PostgreSQL database separation, entity layers from physical storages to PXE clients, and the end-to-end network boot pipeline.

## 1. Services, Sockets, and Access Boundaries

OMB is structured as a set of isolated, least-privilege services. External network ports are exposed only by services that directly serve administrators or booting machines; all internal inter-service communication uses local Unix domain sockets restricted by system groups.

```mermaid
flowchart LR
    Admin["Administrator (HTTPS :8443 / SFTP :2022)"]
    Client["PXE Client (BIOS / UEFI Secure Boot)"]

    subgraph Core ["OMB Services"]
        Control["omb-control\n(Web UI, SFTP :2022)"]
        Boot["omb-boot\n(HTTP :8080, iPXE menu)"]
        Images["omb-images\n(ISO catalog & scanner)"]
        Agent["omb-agent (root)\n(Network, mounts, WinPE, NTP)"]
        Dnsmasq["omb-dnsmasq\n(DHCP / Proxy DHCP :67, TFTP :69)"]
        WinISO["omb-winiso@<id>\n(Loopback ISO mount)"]
    end

    subgraph Infra ["OS Infrastructure"]
        PG["PostgreSQL\nomb_control · omb_images · omb_boot"]
        Samba["Samba (smbd)\n[ombisos] RO · [iso] intake"]
        TFTP["/srv/omb/tftp\n(loaders, WIMs, profiles)"]
    end

    Admin -->|HTTPS :8443| Control
    Admin -.->|SFTP :2022| Control
    Admin -.->|SMB [iso]| Samba

    Control -->|/run/omb-boot/control.sock| Boot
    Control -->|/run/omb-images/images.sock| Images
    Control -->|/run/omb/agent.sock| Agent

    Control --> PG
    Images --> PG
    Boot --> PG

    Agent -->|config + SIGHUP| Dnsmasq
    Agent -->|unit control| WinISO
    Agent -->|write artifacts| TFTP
    Agent -->|share config| Samba
    WinISO -->|mount -o loop,ro| Samba
    Boot -->|read files| TFTP

    Client -->|DHCP/Proxy + TFTP| Dnsmasq
    Client -->|HTTP :8080| Boot
    Client -->|SMB [ombisos] RO| Samba
```

### Service Roles

| Service / Unit | User | Responsibility |
| --- | --- | --- |
| `omb-control.service` | `omb-control` | HTTPS admin dashboard, users/roles/2FA, external auth (AD/LDAP, Keycloak OIDC), menu drafts, boot entries, and the built-in ISO intake SFTP server (`:2022`). |
| `omb-boot.service` | `omb-boot` | Serves the published iPXE menu (`/boot/menu.ipxe`), boot assets (`/assets/...`), Ubuntu Autoinstall files (`/autoinstall/...`), client telemetry (`/boot/hello`, `/boot/choice`), runtime maintenance mode, and the boot event log. |
| `omb-images.service` | `omb-images` | Indexes local, SMB, and NFS storages, inspects ISOs (`wimlib-imagex`), streams ISO uploads and storage-to-storage migrations, runs scheduled background rescans, and tracks disk usage. |
| `omb-agent.service` | `root` | The single privileged service: manages `omb-dnsmasq` and MAC/OUI driver rules, mounts SMB/NFS shares and loopback ISOs (`omb-winiso@`), builds WinPE `boot.wim` images with drivers, configures Samba, and manages server time/NTP. |
| `omb-dnsmasq.service` | `dnsmasq` | Dedicated `dnsmasq` instance using `/etc/omb/dnsmasq.conf`: full DHCP or Proxy DHCP (port 67/4011) and TFTP delivery of initial bootloaders (port 69). |
| `omb-winiso@.service` | `root` | Template oneshot unit (`/usr/local/bin/omb-winiso-prepare %i`) that loopback-mounts a Windows/Strelec ISO read-only at `/mnt/omb-iso/profiles/<id>` for Samba delivery. |

### Three Isolated PostgreSQL Databases

- **`omb_control`** — accounts, sessions, password policy, TOTP 2FA and trusted browsers, AD/LDAP and Keycloak integrations, audit log, system events, machines and MAC-prefix rules, boot entries (`installations`), menu draft and published versions, Windows/Ubuntu profiles, answer files and manual checks, WinPE driver packages and configs, maintenance locks (`maintenance_locks`), ISO migrations, and local file server settings.
- **`omb_images`** — connected storages (`storages`), discovered ISO catalog (`images`), and background task schedules (`catalog_settings`).
- **`omb_boot`** — HTTP request and PXE client boot event log (`boot_events`).

Secrets (integration credentials, OIDC secrets, TOTP keys) are stored encrypted in `omb_control` (AES-256-GCM) using `/etc/omb/secrets.key`.

---

## 2. Entity Layers and Relationships

OMB separates entities into six layers. This design allows:
- reusing the same installation script across multiple ISOs (answer files are unbound from specific images);
- deduplicating built WinPE driver configurations by SHA-256;
- migrating source ISOs across local disk, SMB, and NFS without breaking boot entries or the published menu;
- placing a storage or image into maintenance/drain mode on the fly without republishing the menu draft.

```mermaid
flowchart TD
    subgraph L1 ["1. Clients & Network Rules"]
        Client["PC / VM (MAC, BIOS / UEFI)"]
        Rules["Machines & OUI Rules\n(iPXE vs SNP, DHCP ignore)"]
        Client -->|DHCP / Proxy DHCP| Rules
    end

    subgraph L2 ["2. Boot Menu & Installations"]
        Menu["Published Menu\n(/var/lib/omb-boot/menu.ipxe)"]
        Maint["Maintenance Mode\n(maintenance_locks → HTTP 503 / pause)"]
        Inst["Boot Entry (Installation)\n— deployment recipe"]
        Rules -->|TFTP loader → HTTP /boot/menu.ipxe| Menu
        Maint -.->|dynamic filter| Menu
        Menu -->|item selected| Inst
    end

    subgraph L3 ["3. Installation Scripts & WinPE"]
        Answers["Installation Scripts\n(autounattend.xml / Ubuntu user-data)"]
        WinPE["WinPE Driver Configs\n(boot.wim + INF + omb-setup.cmd)"]
        Inst -.->|optional| Answers
        Inst -.->|Windows / Strelec| WinPE
    end

    subgraph L4 ["4. OS Profiles & ISO Images"]
        WinISO["Windows ISO Profile\n(install.wim / boot.wim)"]
        UbuSrc["Ubuntu Source\n(vmlinuz / initrd / ISO)"]
        Strelec["Service Image\n(Strelec / WinPE)"]
        Inst ===> WinISO
        Inst ===> UbuSrc
        Inst ===> Strelec
    end

    subgraph L5 ["5. Runtime Client Delivery"]
        SambaRO["Samba [ombisos] (RO)\n/mnt/omb-iso/profiles/<id>"]
        BootHTTP["omb-boot HTTP :8080\n/assets/ · /autoinstall/"]
        WinISO -->|omb-winiso@id| SambaRO
        Strelec -->|omb-winiso@id| SambaRO
        UbuSrc -->|omb-agent| BootHTTP
        WinPE -->|cache /srv/omb/tftp| BootHTTP
    end

    subgraph L6 ["6. Physical Storages"]
        Local["Local Disk\n/var/lib/omb-images/iso"]
        SMB["SMB Storage\n/mnt/omb-smb"]
        NFS["NFS Storage (v4.1 / v3)\n/mnt/omb-nfs"]
        WinISO --- SMB
        UbuSrc --- NFS
        Strelec --- Local
    end

    Client -.->|WinPE: net use I:| SambaRO
    Client -.->|iPXE / cloud-init| BootHTTP
```

### Layer Summary

1. **Clients (`machines`, `mac_prefix_rules`)** — every booting machine is tracked by MAC address. UEFI network driver selection (`ipxe.efi` vs `snponly.efi`) can be configured globally, by 3–5 byte MAC prefix, or per machine. Flooding clients can be isolated with `<mac>,ignore` in `omb-dnsmasq` via `SIGHUP` without restarting DHCP.
2. **Menu and Boot Entries (`installations`, `menu_draft`, `menu_versions`)** — a boot entry combines an OS image, menu title/folder, optional installation script, and WinPE driver configuration. Edits accumulate in `menu_draft` and remain invisible to clients until published. Published versions retain component snapshots for rollback.
3. **Installation Scripts (`windows_answer_variants`, `ubuntu_autoinstall_files`, `answer_installation_checks`)** — an unbound library of Windows XML and Ubuntu cloud-init scenarios. Windows script compatibility is checked against XML metadata and `install.wim`; manual verification records are tracked in `answer_installation_checks`.
4. **WinPE Drivers and Configs (`winpe_driver_packages`, `winpe_driver_configs`)** — imported driver packages (folder or ZIP) are injected via `wimlib-imagex` into the base `boot.wim` along with `omb-setup.cmd` (network wait timeout/retry and post-Setup exit action). Built WIMs are deduplicated by SHA-256 under `/srv/omb/tftp/windows-driver-wims/`.
5. **ISO Images and Runtime Mounts (`omb_images.images`, `windows_profiles`, `ubuntu_sources`, `image_migrations`)** — `omb-images` indexes ISOs and extracts metadata. For Windows and Strelec, `omb-agent` runs `omb-winiso@<id>` to loopback-mount the ISO at `/mnt/omb-iso/profiles/<id>` and share it read-only via Samba `[ombisos]`. For Ubuntu, `vmlinuz` and `initrd` are extracted under `/srv/omb/tftp/ubuntu-sources/`. ISO migration streams the file between storages and atomically rebinds profiles and mounts.
6. **Storages and File Intake (`omb_images.storages`, `file_server_settings`)** — local directory `/var/lib/omb-images/iso`, remote SMB shares, and NFS exports (NFSv4.1/v3 with automatic `ro`/`rw` detection). For convenient ISO uploads to local storage, OMB provides an optional local file server: a managed Samba share `[iso]` (strictly isolated from the PXE share `[ombisos]`) and a built-in SFTP server on port `2022` using a dedicated file-access password configured in the user profile.

---

## 3. End-to-End Network Boot Pipeline

### Windows (BIOS and UEFI Secure Boot)

1. The client sends `DHCP DISCOVER`; `omb-dnsmasq` replies with an IP (or PXE options only in Proxy DHCP mode) and the initial bootloader:
   - **UEFI (Secure Boot):** `ipxe-shim.efi` (or `snponly-shim.efi`, both linking to the Microsoft UEFI CA 2011/2023 dual-signed `shimx64.efi` from iPXE shim 16.1), which chains into signed `ipxe.efi` or `snponly.efi`.
   - **Legacy BIOS:** `ipxeboot/x86_64/undionly.kpxe`.
2. iPXE fetches `http://<OMB-IP>:8080/boot/menu.ipxe` and reports machine attributes (`/boot/hello`).
3. The operator picks a menu item (or the default timer expires); `omb-boot` logs the choice (`/boot/choice`). If the target image or storage is in maintenance mode, the client sees a pause screen instead of starting installation.
4. iPXE downloads `wimboot`, `BCD`, `boot.sdi`, the driver-injected `boot.wim`, and `omb-setup.cmd` over HTTP (access to `windows-driver-configs/*/omb-setup.cmd` is restricted to the boot subnet).
5. Inside WinPE, `omb-setup.cmd` waits for network connectivity, maps the read-only share `\\<OMB-IP>\ombisos\<token>` as `I:`, copies the answer file to `X:\autounattend.xml` if configured, and launches `I:\sources\setup.exe`.
6. When Windows Setup exits, the configured exit action runs (reboot, shutdown, or command prompt).

### Ubuntu Desktop

1. The client follows the same DHCP/TFTP → iPXE → `/boot/menu.ipxe` path.
2. On selecting Ubuntu, iPXE fetches `vmlinuz` and `initrd` over HTTP from `/assets/ubuntu-sources/...`.
3. Kernel parameters pass the ISO HTTP URL and, if an installation script is attached, `autoinstall ds=nocloud-net;s=http://<OMB-IP>:8080/autoinstall/.../`.

### Strelec Service Image

1. Follows the image's native PXE recipe: iPXE downloads `wimboot` and the prepared WinPE image over HTTP.
2. After booting, WinPE maps the read-only `[ombisos]` share to access the utilities and applications inside the source ISO.

---

## 4. Server Paths and Sockets Reference

| Path | Owner / Service | Purpose |
| --- | --- | --- |
| `/etc/omb/` | `omb-agent`, services | `dnsmasq.conf`, `*.env` files, encryption key `secrets.key`, and `windows-profiles/omb-setup.cmd.in` template. |
| `/run/omb/agent.sock` | `omb-agent` | Local Unix socket for network configuration, mounts, and WinPE builds. |
| `/run/omb-boot/control.sock` | `omb-boot` | Local Unix socket for menu publishing, maintenance locks, boot journal, and HTTP transfer cancellation. |
| `/run/omb-images/images.sock` | `omb-images` | Local Unix socket for storage catalog, scanning, uploads, and ISO migration. |
| `/var/lib/omb-images/iso` | `omb-images` | Local ISO storage directory (also the root for local SMB `[iso]` / SFTP). |
| `/var/lib/omb-boot/menu.ipxe` | `omb-boot` | Published active iPXE menu snapshot. |
| `/var/lib/omb-control/` | `omb-control` | Admin HTTPS TLS certificate and key (`control.crt`, `control.key`). |
| `/srv/omb/tftp/` | `omb-dnsmasq`, `omb-boot`, `omb-agent` | iPXE/shim bootloaders (`ipxeboot/`), Windows profiles, cached driver `boot.wim` files, `omb-setup.cmd` scripts, Ubuntu sources and `autoinstall` files. |
| `/mnt/omb-smb`, `/mnt/omb-nfs` | `omb-agent`, `omb-images` | Mount points for remote network storages (plus `*-upload` for write access). |
| `/mnt/omb-iso/profiles/<id>` | `omb-winiso@` | Read-only loopback mounts of Windows and Strelec ISOs served via Samba `[ombisos]`. |

---

## Related Guides

- [Control and admin interface](control-installation.md)
- [Images, ISO catalog and Windows profiles](images-installation.md)
- [Secure Boot chain](secure-boot.md)
- [Portable offline release and updates](offline-release.md)
