# Installing oh my boot_ (OMB)

[Русская версия](../installation.md)

This is the unified guide for a fresh, container-free installation of OMB on Ubuntu Server. Once installed, you get the HTTPS admin panel (`https://<ip>:8443`), BIOS/UEFI and UEFI Secure Boot network boot services, the ISO catalog, and — if you configure a PXE interface in the wizard — ready-to-use DHCP (`omb-dnsmasq`) and Samba (`smbd`).

## Server requirements

- **OS and architecture:**
  - **Ubuntu 26.04 LTS amd64** — primary target OS (required for offline releases, recommended for online installs).
  - **Ubuntu 24.04 LTS amd64** — supported for online installation (other versions can be overridden in `install.sh` with `--force-os`).
- **Hardware resources:**
  - **Minimum** *(ISOs stored on external SMB/NFS)*: 2 vCPU (`x86_64`), 2 GB RAM, 20 GB disk, 1 GbE.
  - **Recommended** *(local ISO storage and concurrent installs)*: 4+ vCPU (faster `boot.wim` driver builds), 4–8 GB RAM (PostgreSQL + Linux page cache), 60–100+ GB SSD.
- **Networking:** 1 or 2 network interfaces with a **static IPv4 address**:
  - **2 NICs (recommended):** one for management and the web panel, and a dedicated NIC for the PXE segment (**Full DHCP** or **Proxy DHCP**).
  - **1 NIC:** the web panel and PXE share one interface; use **Proxy DHCP** on shared networks to avoid conflicting with an existing DHCP server.
- **Privileges:** run the installer as `root` (`sudo`).

## Network ports and firewall

On a clean Ubuntu Server installation, UFW is disabled by default so all required ports open automatically when OMB services start. If UFW or an external firewall is enabled in your environment, allow the ports listed below. `omb-setup` checks for conflicting listeners before making changes and warns after installation (as does `omb status`) when UFW is active.

| Segment | Port | Protocol | Purpose | Requirement |
| --- | --- | --- | --- | --- |
| **Management** | `8443` | TCP | HTTPS web control panel (`omb-control`) | Required |
| **Management** | `2022` | TCP | Built-in SFTP server for ISO uploads | Optional (if enabled) |
| **Management** | `445` | TCP | Samba `[iso]` share for uploading ISOs from workstations | Optional (if enabled) |
| **PXE** | `67` | UDP | DHCP (`omb-dnsmasq`, Full DHCP / Proxy DHCP) | Required for PXE |
| **PXE** | `4011` | UDP | Proxy DHCP (`omb-dnsmasq`) | Required for Proxy DHCP |
| **PXE** | `69` | UDP | TFTP — stage-1 bootloaders (`undionly.kpxe`, `ipxe-shim.efi`, `ipxe.efi`, `snponly.efi`) | Required for PXE |
| **PXE** | `8080` | TCP | iPXE boot menu and HTTP delivery of WinPE / ISO files (`omb-boot`) | Required for PXE |
| **PXE** | `445` | TCP | Samba `[ombisos]` share (Windows and Strelec installation media) | Required for Windows/Strelec |
| **Outbound** | `445` | TCP | External SMB storage (NAS) | Optional |
| **Outbound** | `2049` | TCP | External NFSv4.1 storage (NAS) | Optional |
| **Outbound** | `389`, `636` | TCP | Active Directory / LDAP (LDAPS) authentication | Optional |
| **Outbound** | `443` | TCP | Keycloak OIDC sign-in | Optional |
| **Outbound** | `123` | UDP | NTP time synchronization | Optional |

Example UFW rules for a two-NIC host with Management (`ens18`) and PXE (`ens19`):

```sh
# Management interface (ens18)
sudo ufw allow in on ens18 to any port 8443 proto tcp comment 'OMB Control HTTPS'
# Optional, if SFTP upload or Samba [iso] share is enabled:
# sudo ufw allow in on ens18 to any port 2022 proto tcp comment 'OMB SFTP'
# sudo ufw allow in on ens18 to any port 445 proto tcp comment 'OMB Samba [iso]'

# PXE interface (ens19)
sudo ufw allow in on ens19 to any port 67 proto udp comment 'OMB DHCP'
sudo ufw allow in on ens19 to any port 4011 proto udp comment 'OMB Proxy DHCP'
sudo ufw allow in on ens19 to any port 69 proto udp comment 'OMB TFTP'
sudo ufw allow in on ens19 to any port 8080 proto tcp comment 'OMB iPXE HTTP'
sudo ufw allow in on ens19 to any port 445 proto tcp comment 'OMB Samba [ombisos]'
```

## Method 1: Online one-line install

Recommended for servers with internet access. `scripts/install.sh` checks the OS and architecture, prompts to choose the release channel in interactive mode (`stable` or `dev`, default `stable`), downloads the `.deb` package and `SHA256SUMS` from GitHub Releases, verifies the SHA-256 digest, installs the package and system dependencies via `apt-get`, and launches the interactive `omb-setup` wizard.

```sh
curl -fsSL https://raw.githubusercontent.com/f0nwa/oh-my-boot_/main/scripts/install.sh | sudo bash
```

If you already cloned the repository locally:

```sh
sudo bash scripts/install.sh
```

Useful `install.sh` options:

- `--version <ver>` — install a specific release or tag (for example `--version v0.2.0` or `--version v0.2.1-dev.1`);
- `--channel <stable|dev>` — set the release channel without an interactive prompt when `--version` is not set (default: `stable`);
- `--deb <path>` — install from a local `.deb` file instead of downloading;
- `-- <omb-setup args>` — forward extra flags directly to `omb-setup` (for example `sudo bash scripts/install.sh -- --lang en`).

## Method 2: Manual online `.deb` install

If you downloaded `omb_<version>_amd64.deb` manually (for example from GitHub Releases):

```sh
sudo apt update
sudo apt install ./omb_<version>_amd64.deb
sudo omb-setup
```

`apt install` pulls in the required system dependencies (`postgresql`, `dnsmasq-base`, `samba`, `cifs-utils`, `nfs-common`, `wimtools`, etc.), and `omb-setup` initializes databases, keys, certificates, and services.

## Method 3: Offline air-gapped install

For isolated servers without internet access, use a signed local APT repository (`apt-repo`) built as described in the [offline release guide](offline-release.md).

Copy `<apt-repo>`, the release public key `<public-key>`, and the three deployment files (`scripts/offline-deploy.sh`, `scripts/offline-install-lib.sh`, `scripts/offline-deb.py`) into the same directory on the target Ubuntu 26.04 amd64 server.

**Interactive install (recommended):**

```sh
sudo bash scripts/offline-deploy.sh install <apt-repo> <public-key>
```

The script verifies the repository signature and checksums, installs system `.deb` packages and `omb` via `apt-get` inside an isolated network namespace (`unshare --net`), and hands off to the interactive `omb-setup --install-mode offline` wizard.

**Positional non-interactive form:**

```sh
sudo bash scripts/offline-deploy.sh install <apt-repo> <public-key> <management-ip> [pxe-interface|none]
```

In this form, DHCP and Samba stay disabled until you configure the PXE network in the web panel. Passing `none` or omitting the fourth positional argument sets `OMB_CONTROL_INTERFACES=omb-pxe-off` in `/etc/omb/control.env` (so enabling PXE later requires updating `OMB_CONTROL_INTERFACES` in `/etc/omb/control.env` and restarting `omb-control`). By contrast, interactive `offline-deploy.sh install <apt-repo> <public-key>` automatically populates `OMB_CONTROL_INTERFACES` with detected interfaces for web-only PXE setup.

## Method 4: Ready-to-boot VM for Proxmox VE / ESXi (`.qcow2.xz` and `.ova`)

Each GitHub release publishes pre-built VM images based on **Ubuntu 24.04 LTS Cloud Image** with `omb`, PostgreSQL, Samba, `dnsmasq`, `wimtools`, `qemu-guest-agent`, and Cloud-Init pre-installed. The images work in air-gapped environments with no internet access.

### Proxmox VE via helper script (`.qcow2.xz`, recommended)

The fastest option is `scripts/proxmox-create-vm.sh` run on a Proxmox VE node. It automatically creates the VM with `VirtIO SCSI single`, `VirtIO NIC`, `qemu-guest-agent`, and a `CloudInit Drive`:

```sh
curl -fsSL https://raw.githubusercontent.com/f0nwa/oh-my-boot_/main/scripts/proxmox-create-vm.sh | bash -s -- \
  --vmid 200 --storage local-zfs --bridge vmbr0 --pxe-bridge vmbr1
```

For an air-gapped Proxmox node, download the image beforehand and pass `--image`:

```sh
bash scripts/proxmox-create-vm.sh --vmid 200 --storage local-zfs --bridge vmbr0 \
  --image omb_<version>_ubuntu24.04_amd64.qcow2.xz
```

After creation, configure the user, password/SSH key, and network on the **Cloud-Init** tab, start the VM, and run `sudo omb-setup`.

### Importing `.ova` in the Proxmox VE Web UI

1. **Prepare `local` storage (one-time):**
   open **Datacenter → Storage → `local` → Edit → Content** and make sure both **`Import`** and **`Disk image`** are selected.
   - *Why `Disk image` is required on `local` even when the VM lives on `local-zfs` or `local-lvm`:* Proxmox cannot extract a `.vmdk` from an `.ova` archive directly into block storage (ZFS / LVM-thin). During import it stages the extracted disk on a file-based **Import Working Storage** with the `Disk image` content type enabled — otherwise import fails with `import working storage '...' does not support 'images' content type or is not file based`. The temporary file is removed automatically after import, and you may uncheck `Disk image` on `local` afterwards.
2. **Upload the archive:** open **`local` → Import**, upload `omb_<version>_ubuntu24.04_amd64.ova`, select it, and click **Import**.
3. **Verify settings in the Import Guest wizard:**
   - **General** tab:
     - **Default Storage:** target storage for the VM disk (e.g. `local-zfs` or `local-lvm`);
     - **Import Working Storage:** keep **`Source Storage`** (or select `local`) — do **not** pick ZFS/LVM;
     - **Cores / Memory:** minimum `2` vCPU and `2048 MiB` (`4` vCPU and `4096+ MiB` recommended if storing ISOs locally);
   - **Advanced** tab:
     - **Disks:** `Size: Unknown` for `omb-disk1.vmdk` is expected for `streamOptimized` VMDK in Proxmox; the actual `20 GiB` virtual size is applied on import;
     - **SCSI Controller:** switch from `Default (LSI 53C895A)` to **`VirtIO SCSI single`**;
     - **Network Interfaces (`net0`, `net1`):** switch model from `VMware vmxnet3` to **`VirtIO (paravirtualized)`** and select your Management and PXE bridges (uncheck **Use** on `net1` if you only need one NIC).
4. **Optional settings before first boot:**
   - **Console sign-in without Cloud-Init:** the image includes a built-in console account **`omb`** with temporary password **`omb`** (mandatory password change on first sign-in), so you can boot the VM right away and sign in via Proxmox **noVNC** without attaching Cloud-Init.
   - **If you want SSH keys or automatic IP via Cloud-Init:** attach **Hardware → Add → CloudInit Drive** (on `local-zfs` / `local-lvm`) and configure **User**, **SSH public key** / **Password**, and **IP Config** on the **Cloud-Init** tab.
   - **Options → QEMU Guest Agent** — enable **Use QEMU Guest Agent** (already installed inside the image).
   - optionally grow the disk (**Hardware → Hard Disk → Disk Action → Resize**) — the root filesystem expands automatically on boot.
5. **Start the VM**, sign in on the console as `omb` / `omb` (or via SSH if configured via Cloud-Init), and run `sudo omb-setup`.

### Importing `.ova` in VMware ESXi / vSphere

Import `omb_<version>_ubuntu24.04_amd64.ova` via **Create/Register VM → Deploy a virtual machine from an OVF or OVA file**, map the `Management` and `PXE` networks to your port groups, start the VM, sign in on the console as `omb` / `omb` (you will be prompted to set a new password), and run `sudo omb-setup`.

## `omb-setup` walkthrough

When launched interactively, `omb-setup` guides you through six steps:

1. **Step 0 — Language:** choose `Русский` or `English`. The selected language is used by the wizard and saved as the initial UI language of the `admin` account.
2. **Step 1 — Web control panel address (`HTTPS :8443`):** pick an IPv4 address from detected network interfaces or enter one manually.
3. **Step 2 — First administrator password (`admin`):** enter and confirm a password (at least 12 characters, input hidden).
4. **Step 3 — Network boot interface (PXE):**
   - on a 2+ NIC host, the wizard recommends a dedicated PXE interface;
   - on a 1-NIC host, sharing the management interface is supported (with a hint to use **Proxy DHCP**);
   - you can also choose “Configure later in web panel” to defer PXE setup.
5. **Step 4 — DHCP mode** (shown when a PXE interface is selected):
   - **Proxy DHCP** — an existing DHCP server assigns IP addresses while OMB serves PXE options;
   - **Full DHCP** — OMB assigns IP addresses from a pool (start and end addresses are suggested and validated);
   - **Skip** — leave DHCP disabled and enable it later in the web panel.
6. **Step 5 — Summary and confirmation:** displays the final settings and, once confirmed:
   - creates system accounts (`omb-boot`, `omb-control`, `omb-images`, `omb-iso`) and three PostgreSQL databases (`omb_boot`, `omb_images`, `omb_control`) with schemas and `schema_migrations`;
   - extracts pinned iPXE, `ipxe-shimx64.efi`, and `wimboot` boot assets into `/srv/omb/tftp`;
   - generates `/etc/omb/secrets.key` and a self-signed TLS certificate for `https://<ip>:8443`;
   - starts `omb-boot`, `omb-agent`, `omb-images`, and `omb-control`;
   - when a PXE mode (`proxy` or `full`) is selected, applies `omb-dnsmasq` and **automatically configures and starts Samba (`smbd` and `/etc/samba/smb.conf`)** for Windows and Strelec installation shares.

### Non-interactive mode (`--non-interactive`)

For automated provisioning, pass `--non-interactive` and the required flags:

```sh
printf '%s\n' 'YourStrongPassword123!' | sudo omb-setup \
  --non-interactive \
  --lang en \
  --management-ip 192.0.2.10 \
  --pxe-interface enp2s0 \
  --dhcp-mode full \
  --dhcp-start 192.0.2.100 \
  --dhcp-end 192.0.2.200 \
  --admin-password-stdin
```

Key non-interactive flags:

| Flag | Purpose |
| --- | --- |
| `--non-interactive` | Run without interactive prompts |
| `--lang <ru\|en>` | Wizard language and initial `admin` UI language (default: `ru`) |
| `--management-ip <ip>` | Required IPv4 address for the `HTTPS :8443` panel |
| `--pxe-interface <iface\|none>` | PXE network interface name or `none` |
| `--dhcp-mode <proxy\|full\|skip>` | DHCP mode (`proxy`, `full`, or `skip`) |
| `--dhcp-start <ip>`, `--dhcp-end <ip>` | Pool range for `--dhcp-mode full` |
| `--skip-pxe` | Skip PXE/DHCP setup and configure later in the web panel |
| `--admin-password-stdin` | Read the initial `admin` password from `stdin` |

## First steps after installation

1. **Sign in to the web panel:** open `https://<ip>:8443` and log in as `admin` with the password configured in `omb-setup`.
2. **Verify services and network:** open **System → Overview** to confirm all services are active and system time is accurate. If you deferred PXE setup, configure it in **System → Network** — saving PXE settings automatically applies `omb-dnsmasq` and synchronizes Samba (`smbd`).
3. **Add an ISO image:** upload an ISO in **Library → ISO Images** (or mount an SMB/NFS storage in **System → Storages**) and run **Inspect ISO** from the row menu.
4. **Create a boot entry and publish the menu:** open **Library → Boots** to create an entry from the inspected ISO, add it to the menu draft under **Menu**, and click **Publish menu**. PXE clients will then see the new boot item.

## Console administration (`omb`)

After installation, day-2 server administration is available through the unified `omb` CLI:

- `sudo omb status` — display the installed version, release channel (`stable`/`dev`), install mode (`online`/`offline`), and status of all 7 services (`omb-control`, `omb-images`, `omb-boot`, `omb-agent`, `omb-dnsmasq`, `postgresql`, `smbd`);
- `sudo omb channel [stable|dev]` — view or switch the release channel at any time;
- `sudo omb update` — run an online update from GitHub Releases with SHA-256 verification, PostgreSQL backup, and cumulative SQL migrations;
- `sudo omb update --deb ./omb_<version>_amd64.deb` — update an existing server from a locally copied `.deb` file without internet access: automatically verifies `SHA256SUMS` in the same directory (or via `--checksums <path>`), backs up all 3 PostgreSQL databases and `/etc/omb`, installs the package, applies cumulative SQL migrations, runs `daemon-reload` and restarts services, updates `/var/lib/omb-deployment/`, and verifies HTTPS and `omb-boot` health;
- `sudo omb reset-password [username]` — recover a forgotten password for `admin` (default) or any local account;
- `sudo omb reset-2fa [username]` — reset 2FA and revoke trusted browsers for any local account.

## Further reading

- [Control and admin interface](control-installation.md) — users, roles, menu management, machine log, and Ubuntu Autoinstall.
- [Images, ISO catalog, and Windows profiles](images-installation.md) — local/SMB/NFS storages, WinPE, and drivers.
- [Self-contained offline releases, updates, and rollback](offline-release.md) — building `apt-repo`, `stable`/`dev` channels, `build-release.sh`, and `offline-deploy.sh`.
- [Offline build from source](offline-installation.md) — preparing dependencies and pinned boot assets for air-gapped environments.
