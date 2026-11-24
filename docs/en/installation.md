# Installing Oh My Boot (OMB)

[Русская версия](../installation.md)

This is the unified guide for a fresh, container-free installation of OMB on Ubuntu Server. Once installed, you get the HTTPS admin panel (`https://<ip>:8443`), BIOS/UEFI and UEFI Secure Boot network boot services, the ISO catalog, and — if you configure a PXE interface in the wizard — ready-to-use DHCP (`omb-dnsmasq`) and Samba (`smbd`).

## Server requirements

- **OS and architecture:**
  - **Ubuntu 26.04 LTS amd64** — primary target OS (required for offline releases, recommended for online installs).
  - **Ubuntu 24.04 LTS amd64** — supported for online installation (other versions can be overridden in `install.sh` with `--force-os`).
- **Networking:** 1 or 2 network interfaces with a **static IPv4 address**:
  - **2 NICs (recommended):** one for management and the web panel, and a dedicated NIC for the PXE segment (**Full DHCP** or **Proxy DHCP**).
  - **1 NIC:** the web panel and PXE share one interface; use **Proxy DHCP** on shared networks to avoid conflicting with an existing DHCP server.
- **Ports to allow for clients and administrators:**
  - `TCP 8443` — HTTPS web control panel;
  - `TCP 8080` — iPXE boot menu and HTTP delivery of WinPE / ISO files;
  - `UDP 69` — TFTP (`undionly.kpxe`, `ipxe-shim.efi`, `ipxe.efi`, `snponly.efi`);
  - `UDP 67` and `UDP 4011` — DHCP / Proxy DHCP;
  - `TCP 445` — Samba (Windows and Strelec installation media share).
- **Privileges:** run the installer as `root` (`sudo`).

## Method 1: Online one-line install

Recommended for servers with internet access. `scripts/install.sh` checks the OS and architecture, prompts to choose the release channel in interactive mode (`stable` or `dev`, default `stable`), downloads the `.deb` package and `SHA256SUMS` from GitHub Releases, verifies the SHA-256 digest, installs the package and system dependencies via `apt-get`, and launches the interactive `omb-setup` wizard.

```sh
curl -fsSL https://raw.githubusercontent.com/f0nwa/OMB/main/scripts/install.sh | sudo bash
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
- `sudo omb reset-password [username]` — recover a forgotten password for `admin` (default) or any local account;
- `sudo omb reset-2fa [username]` — reset 2FA and revoke trusted browsers for any local account.

## Further reading

- [Control and admin interface](control-installation.md) — users, roles, menu management, machine log, and Ubuntu Autoinstall.
- [Images, ISO catalog, and Windows profiles](images-installation.md) — local/SMB/NFS storages, WinPE, and drivers.
- [Self-contained offline releases, updates, and rollback](offline-release.md) — building `apt-repo`, `stable`/`dev` channels, `build-release.sh`, and `offline-deploy.sh`.
- [Offline build from source](offline-installation.md) — preparing dependencies and pinned boot assets for air-gapped environments.
