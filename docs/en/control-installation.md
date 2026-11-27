# OMB Control and the HTTPS dashboard

Current OMB control panel operations guide. The initial setup commands below are for a new server; do not recreate accounts, databases, certificates, or configuration on an already running server.

[Русская версия](../control-installation.md). Past rollout reports and previous-version instructions are preserved in the repository commit history. The technical transition is recorded in the project documentation.

## Services and paths

`omb-control` handles HTTPS, users, roles, installations, the menu draft and menu versions. It runs as the `omb-control` system account and uses the matching PostgreSQL role through local peer authentication.

| Purpose | Current name or path |
| --- | --- |
| Program and service | `/usr/local/bin/omb-control`, `omb-control.service` |
| Administrative command | `/usr/local/bin/omb-controlctl` |
| Database | `omb_control` |
| Startup configuration | `/etc/omb/control.env` |
| Certificate and key | `/var/lib/omb-control/control.crt`, `control.key` |
| Boot API | `/run/omb-boot/control.sock` |
| Agent API | `/run/omb/agent.sock` |
| Images API | `/run/omb-images/images.sock` |
| Published menu snapshot | `/var/lib/omb-boot/menu.ipxe` |

Boot serves `http://10.30.0.10:8080/boot/menu.ipxe` to clients. Agent manages the boot network and mounts; Images manages the ISO catalog. Stopping Control does not stop these services or replace the published menu.

## First Control installation

Prepare Boot and Agent using the [server guide](../installation.md), install Images using the [storage guide](images-installation.md), and start PostgreSQL first. The `omb-images` group must exist: the Control unit names it in `SupplementaryGroups`. These examples use `ens19` for boot traffic and `192.0.2.10` for management; use your own values on another server.

Build from the repository root:

~~~sh
mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o dist/omb-control ./cmd/omb-control
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o dist/omb-controlctl ./cmd/omb-controlctl
~~~

If you built on another machine, transfer the `dist` and `deploy` directories to the server. Run the following server commands from their parent directory as root in Bash, only for the first installation:

~~~sh
install -m 0755 dist/omb-control dist/omb-controlctl /usr/local/bin/
useradd --system --user-group --home-dir /var/lib/omb-control --shell /usr/sbin/nologin omb-control
install -d -m 0700 -o omb-control -g omb-control /var/lib/omb-control
install -d -m 0750 -o root -g root /etc/omb

runuser -u postgres -- psql -v ON_ERROR_STOP=1 -c 'CREATE ROLE "omb-control" LOGIN'
runuser -u postgres -- createdb --owner=omb-control omb_control
runuser -u omb-control -- psql -v ON_ERROR_STOP=1 --dbname=omb_control < deploy/sql/control.sql
~~~

On the existing stand, preserve the complete schema and data, including additional tables. Do not replace the working database with an empty schema.

### TLS

For a lab server, a self-signed certificate can include the management IP in its SAN. This block refuses to overwrite an existing certificate or key:

~~~sh
(
  set -eu
  umask 077
  test ! -e /var/lib/omb-control/control.crt
  test ! -e /var/lib/omb-control/control.key
  openssl req -x509 -newkey rsa:3072 -sha256 -nodes -days 90 \
    -subj '/CN=omb-control' \
    -addext 'subjectAltName=IP:192.0.2.10' \
    -keyout /var/lib/omb-control/control.key \
    -out /var/lib/omb-control/control.crt
  chown omb-control:omb-control /var/lib/omb-control/control.crt /var/lib/omb-control/control.key
  chmod 0600 /var/lib/omb-control/control.key
  chmod 0644 /var/lib/omb-control/control.crt
)
~~~

If your infrastructure provides a certificate, install it and the matching key at the same paths. It must cover the address or name used to open the dashboard. A self-signed certificate needs browser trust.

### Configuration and startup

Create `/etc/omb/control.env` on the first installation. The initial check protects an existing file from replacement:

~~~sh
(
  set -eu
  test ! -e /etc/omb/control.env
  cat > /etc/omb/control.env <<'ENV'
OMB_CONTROL_LISTEN=192.0.2.10:8443
OMB_CONTROL_INTERFACES=ens19
OMB_CONTROL_DATABASE_URL="host=/var/run/postgresql dbname=omb_control user=omb-control"
OMB_CONTROL_BOOT_BASE_URL=http://10.30.0.10:8080
ENV
  chmod 0640 /etc/omb/control.env
)
install -m 0644 deploy/systemd/omb-control.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now omb-control.service
~~~

The dashboard listens on the management address. Only the boot interface is allowed for network changes. Starting Control does not change DHCP mode or the external DHCP server.

## First administrator and access recovery

There is no default password. `create-admin` creates the initial administrator only when the users table is empty. Run this block as root in Bash and replace `admin` with the desired username. Input is hidden and the password is passed over stdin:

~~~sh
set +x
set -a
. /etc/omb/control.env
set +a
read -r -s -p 'New password: ' omb_admin_password
printf '\n'
printf '%s\n' "$omb_admin_password" |
  runuser -u omb-control -- env OMB_CONTROL_DATABASE_URL="$OMB_CONTROL_DATABASE_URL" \
  /usr/local/bin/omb-controlctl create-admin -username admin
unset omb_admin_password
~~~

To reset an existing user's password, replace `admin` with that username in this block:

~~~sh
set +x
set -a
. /etc/omb/control.env
set +a
read -r -s -p 'New password: ' omb_admin_password
printf '\n'
printf '%s\n' "$omb_admin_password" |
  runuser -u omb-control -- env OMB_CONTROL_DATABASE_URL="$OMB_CONTROL_DATABASE_URL" \
  /usr/local/bin/omb-controlctl reset-password -username admin
unset omb_admin_password
~~~

A reset ends that user's previous sessions. Roles are `admin`, `operator` and `observer`. Users choose RU/EN and a theme in My account. The technical rename requires a new sign-in; existing password hashes were preserved.

## Boot entries and menu

Upload source ISO images, answer files and drivers in Library. Select Create boot entry on an image, or open New boot entry under Boot entries. Ubuntu ISOs are checked first; then choose a title, folder and applicable options. The image and selected drivers are prepared automatically. The application creates one internal source record per image and reuses it; there is no separate Create profile step. Strelec starts its native recovery environment and applications from the ISO; Windows Setup options are hidden for this source.

A preparation failure retains the saved entry: its row shows the cause and Retry preparation. Edit changes its title, folder, answer file and drivers while keeping the entry ID. Create another entry to use a different source image. Component changes appear in the draft even when the title stays the same. Published versions retain their own component references, so restoring a version uses its previous components as long as the files are still available.

After preparation, add the entry to the menu draft separately. Review titles and folders on the Menu page. An item normally inherits the entry name; you can give it a separate name or restore inheritance. Clients do not see the draft before publication. Control validates dependencies, creates a version and sends the matching snapshot to Boot. Saving a draft alone does not change the client menu.

Restoring a version through Menu → Versions also checks dependencies. If an ISO, profile or prepared file is unavailable, the current active menu stays in place. Menu restoration does not restore deleted ISOs or NAS data.

The entry list shows the source ISO path and storage name below its components; both can be searched. Ubuntu entries show the specific ISO. If the ISO catalog is unavailable, the saved path remains visible and the storage ID replaces its name.

If Images reports a storage outage, entries using it show a warning and also appear under **Need attention**. Their menu placement and actions remain available. Once Images confirms that the storage is available again, the warning disappears after refreshing the page; the default check interval is one minute. If the Images catalog is unavailable, the interface reports that the storage could not be checked.

Administrators can delete entries that are absent from both the published menu and its draft. For a published entry, first select **Remove from menu** and publish the changes. For an entry only in the draft, cancel its addition first. The source ISO, prepared files, drivers and answer files remain in the library. Menu history is preserved, but versions containing the deleted entry can no longer be restored. **Remove from menu** alone still allows the entry to be returned to the menu.

Under **Library → ISO images**, the table can be searched by title, file, type, storage or author and filtered (**All**, **In boot entries**, **Unused**, **Need attention**). The **Size** column shows the prepared files size on the server below the ISO size, and **Boot entries & status** links to dependent entries. **Delete ISO** in the `☰` row menu removes both the catalog record and the source file from local, mounted, SMB, or NFS storage. Delete dependent boot entries, XML files and driver configurations first; an image in use or in the published menu cannot be deleted. **Delete prepared files** in the `☰` row menu (available to administrators for unused Windows profiles) still preserves the source ISO. When adding or reconnecting SMB or NFS storage, OMB checks directory reading and performs a temporary file write probe: the account or directory needs read and write permissions to enable write mode.

### Updating an existing server

Moving to multiple Ubuntu ISOs requires a coordinated Control, Agent and Boot update. Back up the database, binaries, configuration, draft and active script first. Verify that the configured ISO matches the previous boot files with `scripts/preflight-legacy-ubuntu.sh` and `omb-ubuntu-source-check`; do not migrate without a verified match. With Control stopped, apply `2026-11-04-multi-ubuntu.sql` (folded into `deploy/sql/control.sql`) using the verified storage ID, relative path and SHA-256 in the session settings `omb.ubuntu_storage_id`, `omb.ubuntu_path`, and `omb.ubuntu_sha256`. Prepare that source with the new Agent and pin the legacy URLs to it before starting the new Boot. Verify the old and new URLs, active menu checksum and network settings, then start Control. If a step fails, keep Control stopped and restore the previous schema and files from backup.

Before starting the new Control, back up `omb_control`, stop `omb-control`, and apply `2026-11-03-download-components.sql` and `2026-11-03-strelec-pxe.sql` (folded into `deploy/sql/control.sql`) as the database owner. Update Control, Agent and `omb-winiso-prepare` together, then start the services. The first migration captures components for existing draft items and menu versions, preserving IDs, order and published scripts; the second adds the recovery ISO mode. Re-running them does not rewrite captured components. `deploy/sql/control.sql` defines the clean-install schema.

When updating a server that already has boot entries and Strelec support, also apply `2026-11-03-download-deletion.sql` (folded into `deploy/sql/control.sql`) as the database owner before starting the new Control, after backing up the database and stopping Control. The migration preserves entries, drafts and history, allowing deletion of entries referenced only by old menu versions. Control protects the active menu. Re-running the migration is safe. This change only requires updating Control; Agent and Boot do not need an update.

## WinPE driver catalog

Strelec uses the same packages and individual INF choices under **WinPE drivers**. Open **Boot entries → Edit**, select the package or INF, and save. OMB prepares a derived WIM and loads the selected drivers before network initialization and the SSTR share connection. Publish the menu draft afterwards. Installer XML and Ubuntu Autoinstall do not apply to recovery WinPE. Deploy matching Control, Agent and Boot binaries for this capability; no additional database migration is needed.

Recovery WinPE attempts every selected INF. A nonzero driver result is recorded in `X:\omb-driver.log` and does not skip the remaining drivers or SSTR connection; one shared entry may be used on different hardware. After connecting the share, OMB extracts the native components and starts MInst with the native Strelec profile to create shortcuts. Network and application stages are recorded in `X:\omb-strelec.log`. Device and network operation still require client validation. Windows installer entries retain their strict driver failure behavior.

Library → Drivers accepts directories containing INF files and their supporting files, or a ZIP with the same structure; uploaded packages can also be downloaded back individually (from the card’s `☰` menu or “Properties and contents”), selectively via checkboxes, or all at once (“Download all ZIP”). A boot entry selects specific INFs when created or edited. Its derived driver WIM is used by that entry; the source ISO remains on storage.

Answer files are under Library → Installation scripts. You can reuse Windows XML with compatible images. OMB checks the selected script against the image's `install.wim` metadata. Publish a choice only after its files have been prepared and checked. Dependencies prevent deletion of images or files that are still used.

## Ubuntu Autoinstall

In Library → Installation scripts, open Add script… → Ubuntu → Create in editor. When creating an Ubuntu boot entry, the editor is available through Create Ubuntu file. The editor targets Ubuntu Desktop and has eight sections: source, language, user, disk, hardware, software and repository, networking and SSH, and completion.

The extra package list accepts package names and rejects the removal suffix `-`; names such as `g++` remain valid.

Settings include the user's full name, up to four keyboard layouts with variants and a switching shortcut, an application source from the prepared ISO, third-party drivers and codecs, up to 128 APT packages, an Ubuntu mirror, static IPv4 matched by exact MAC address, an optional SSH server with public keys, and a script up to 16 KiB. The name `root` is reserved, so the editor requires a regular user. Defaults use DHCP, disable SSH, and leave partitioning interactive. Three disk modes are available: interactive partitioning, wiping the disk selected by the installer, or wiping only the disk with an exact serial number. Installer-selected mode erases all data on that disk. Exact-serial mode never falls back to the first disk when the number does not match.

Sections are on the left. The right panel contains Result, exact YAML, and Validation. The main workflow works without JavaScript. JavaScript updates the preview after edits and enables saving once the current input has been checked. When creating a script, enter a password for the first preview. When editing, leave the field empty to reuse the existing password hash. A successful preview clears the password field. The library stores the password hash.

Application sources come from the same ISO served by Boot; prepare metadata using the [Ubuntu guide](ubuntu-desktop-pxe.md#source-preparation). Without it, only ISO defaults are available. An unavailable explicit source blocks saving or publication. Changing the ISO never rewrites saved files.

Extra packages, the SSH server, drivers and codecs need a package archive accessible from the installing machine. A custom HTTP(S) mirror must serve both the primary and security archives; it may be local. Offline mode skips mirror selection and installer refresh and excludes these extra components. A custom script can still access the network itself. Static networking is applied after the installer receives its answers; initial PXE boot continues to use DHCP.

The script runs as root inside the installed system using `/bin/sh -eu`, before reboot. A non-zero exit stops the installation. It is not a graphical first-login script. Completion can restart or power off the machine.

Click the title of an editor-generated script in the library to edit it. “Save” updates the current record and “Save as new” creates a separate one. To add an existing NoCloud `user-data` file, choose “Add script…” → Ubuntu → “Upload YAML…”. Use UTF-8, up to 64 KiB, with a `#cloud-config` header and `autoinstall.version: 1`. You can view and download the uploaded YAML. Upload a revised file as a separate record to replace it.

Select a checked script in an Ubuntu boot entry and publish the draft. Republish the menu after editing a script so clients receive the updated data. Without an answer file, installation stays manual. Boot serves NoCloud `user-data` and `meta-data` under `/assets/ubuntu-autoinstall/…`. “Checked by the editor” confirms that the structure and settings were validated. Verify installation, desktop login, drivers, and Secure Boot on a client.

## UEFI network driver

On **PXE network**, an administrator selects the **Default UEFI driver**: iPXE or **firmware (snponly)**. SNP uses the signed `snponly-shim.efi` → `snponly.efi` chain; iPXE uses `ipxe-shim.efi` → `ipxe.efi`. Changing the global default follows the existing network preview and confirmation flow and restarts dnsmasq. A missing value in an older `network.json` means iPXE.

**Use firmware network settings** opens the menu using the IP configuration already obtained by UEFI, without configuring the network again in iPXE. It applies only with full OMB DHCP and the firmware (snponly) driver selected for the client, including machine and prefix overrides. If loading the menu fails, iPXE performs ordinary DHCP configuration and attempts normal network boot. This can help with repeated PXE boot failures after leaving Windows Setup. The setting defaults to off; its JSON field is `reuse_firmware_network`. It passes through network preview and confirmation and survives Agent restart. ProxyDHCP and GRUB retain the preference without applying it. BIOS and clients using the native iPXE driver do not use it. The menu URL must fit the DHCP suboption; use percent encoding for spaces and special URL characters.

A saved machine and each MAC-prefix rule offer **inherit**, **iPXE** and **firmware (snponly)**. Prefixes contain the first 3–5 MAC bytes. Administrators and operators can change the selection; observers can see the effective driver and its source. Save a new machine's name/model or add it to the list before selecting its driver. Ordinary edits and CSV import preserve the driver.

Priority is an explicit machine choice, then the longest matching prefix with an explicit choice, then the global default. Inherit skips that rule, so an empty longer prefix does not cancel a shorter prefix's choice. Explicit iPXE on a machine overrides prefix SNP and a global SNP default. BIOS receives `undionly.kpxe`.

Individual changes are stored in PostgreSQL. Control sends a complete snapshot; Agent atomically replaces `/var/lib/omb-dhcp/hosts` (file mode `0644`, directory mode `0755`) and signals dnsmasq with SIGHUP without restarting DHCP/TFTP. Saving an unchanged choice also triggers a reread. Applying the network configuration preserves the hosts file. Agent's `-hosts-path` flag allows a different hosts file path for testing.

When Agent is unavailable, the page reports **Setting saved but not applied: Agent is unavailable**, and the audit result is `apply_error`. The database keeps the setting. The next individual rule change or Control startup retries the complete snapshot; after recovering Agent, save the choice again or restart Control. Restarting Agent alone does not request a snapshot from Control.

For an update, back up binaries, configuration, the Agent unit and the Control database, apply only `deploy/sql/2026-11-02-network-driver-selection.sql`, then update Agent and Control together with the new unit. Use canonical `control.sql` for clean installations. Replace temporary manual MAC rules by regenerating the network configuration. Follow the internal acceptance plan for rollout and real client boot checks. WinPE error `0x80074005` remains a separate issue.

## Storage availability and recovery

System → Storages shows storage state and errors. Images checks reachability in the background; an error does not erase the previous catalog. Publication rejects an installation with an unavailable source.

Agent recovers its SMB and NFS mounts before Windows ISO profiles. On System → Storages, Connection recovery beside the **Connect storage ▾** menu button opens a dialog with settings and the last check. Administrators can enable recovery, set a 1–60 minute interval and run a check manually. Save becomes available only after settings change; restoring the original values disables it again. Closing the dialog discards unsaved changes. Operators and observers can view the status without editing it. The file storage account (SMB) or `omb-images` user permissions (NFS) determine whether writes are allowed.

### Disconnecting and removing storage

Administrators can unbind a connected SMB or NFS storage via the row menu **☰ → Delete** on System → Storages. A modal confirmation dialog with a warning is displayed before deletion.

1. **Protection of active installations:** If ISO images from this storage are used by active Windows profiles (`WindowsProfiles`) or active Ubuntu installations (`Installations`), the server blocks removal with HTTP `409 Conflict`. To unbind the storage, first remove or reassign the referencing installations in OMB.
2. **If storage was disconnected externally:** Remote network unreachability (status `unavailable`) does not prevent clean unbinding. Agent safely stops and disables systemd mount units (`systemctl disable --now`; NFS uses `TimeoutSec=15s`), removes local mount points in `/mnt/omb-*/...`, cleans up credentials and configuration files, and Images removes the database entry while purging catalog records for its ISOs.
3. **Data safety on NAS/server:** Removing storage from OMB removes only local mount points and OMB database entries. The original ISO files on the remote NAS/server remain untouched and are never deleted.

## Local File Server (SMB and SFTP)

OMB allows uploading and downloading ISO images directly to and from local storage `/var/lib/omb-images/iso` via standard SMB and SFTP protocols, without requiring root SSH access to the host.

### Protocols and Architecture

- **SMB:** network share `[iso]` in Samba (`smbd`), managed by `omb-agent`. The WinPE boot share `[ombisos]` remains strictly isolated (read-only and restricted to the PXE boot network via `hosts allow`). Access to `[iso]` uses system accounts `omb-fs-<id>`, mapped to web dashboard logins via `/etc/omb/samba-usermap.conf`.
- **SFTP:** an isolated SFTPv3 server built into `omb-control` based on SSH. By default, it listens on TCP port `2022` (configurable between `1024..65535`). The sandbox is strictly confined to `/var/lib/omb-images/iso`. Directory traversal via `../` or symlinks is blocked (`SSH_FX_PERMISSION_DENIED`). OS metadata files (`.DS_Store`, `._*`, `Thumbs.db`, `.Spotlight-*`) are rejected on creation. ISO files referenced by active boots or under maintenance are protected against overwrite, rename, and deletion. After an ISO upload finishes, the server automatically triggers an `omb-images` catalog scan and records an `iso.upload` audit event.

### Access Permissions and Passwords

- **Access Modes:** Disabled (`none`), Read-only (`ro`), and Read-write (`rw`). Configured by administrators per-user under System → Users.
- **File Access Password:** a separate dedicated password (at least 12 characters) independent of the web dashboard login. Plaintext passwords are never stored: PostgreSQL (`omb_control`) stores a `yescrypt` hash (for SFTP), and Samba's `passdb.tdb` receives an NT-hash via stdin through `smbpasswd -s`.
- **Self-Service:** users with `ro` or `rw` access see their personalized connection addresses in their profile (Account / `/account`) and can change their file access password independently.
- **Maintenance Mode:** when local storage maintenance is activated under System → Storages, both protocols automatically enforce read-only access, preventing file modifications and deletions.

### Management in the Web Dashboard

1. Enable/disable SMB and SFTP, configure SFTP port, and view server connection endpoints under **System → Storages** (via the header button or row menu for `Local`).
2. Grant user permissions and set initial passwords under **System → Users** (via the "SMB / SFTP" column or row menu **☰ → SMB / SFTP access…**).
3. Change file access password under **Account** (`/account`).

## Server time and corporate NTP

**System → Overview** displays the **Server time** card with the current host clock, IANA time zone, and NTP synchronization status. Operators and observers have read-only access; administrators can click **Edit…** to:

- change the host time zone (`timedatectl set-timezone`; Control updates displayed timestamps immediately without restarting);
- enable NTP synchronization and configure up to 8 corporate NTP servers (space-separated IPs or hostnames; leaving the field empty reverts to OS defaults);
- or disable NTP and set the date and time manually (with a **Use browser time** button) for fully air-gapped hosts without an internal NTP server.

Agent auto-detects the host NTP backend (`chrony` via `/etc/chrony/sources.d/omb.sources` or `systemd-timesyncd` via `/etc/systemd/timesyncd.conf.d/omb.conf`). `omb-agent.service` requires `-/etc/chrony/sources.d -/etc/systemd/timesyncd.conf.d` in `ReadWritePaths`. Every update is recorded in the audit log as `system.time`.

## Diagnostics and rollback

Run as root:

~~~sh
systemctl status omb-control.service omb-boot.service omb-agent.service omb-images.service omb-dnsmasq.service
journalctl -u omb-control.service -u omb-boot.service -u omb-agent.service -n 100 --no-pager
runuser -u omb-control -- psql -XAt --dbname=omb_control -c 'SELECT 1'
curl --fail --cacert /var/lib/omb-control/control.crt https://192.0.2.10:8443/login
curl --fail http://10.30.0.10:8080/boot/menu.ipxe
~~~

To stop only a broken dashboard, use `systemctl stop omb-control.service`. Boot, Agent, Images and DHCP keep running. Start it with `systemctl start omb-control.service`. Do not edit generated `/etc/omb/dnsmasq.conf` to repair Control.

Before an update, preserve configuration, TLS material, binaries, units and the relevant databases. Changes to shared formats or relationships require a consistent backup of every affected database and file with writers stopped. Roll back a compatible set of code and schema; replacing one binary may be insufficient.

Package rollback and backup restoration are described in the [offline release guide](offline-release.md).

## Verification limits

The server passed file/database restoration, WinPE rebuild, TFTP, SMB/NAS and reboot checks. The user confirmed UEFI Secure Boot loading with XML and drivers. The full client regression pass is deferred until the main development work is finished; see the project documentation.

Past deployment dates, previous rollback commands and rollout checksums are in the repository commit history.

## External authentication: Active Directory / LDAP and Keycloak (OIDC)

The control dashboard supports two independent external authentication providers configured by an administrator under `System → Integrations`:

1. **Active Directory / LDAP** — direct connection over LDAPS (636) or StartTLS (389) with strict CA verification, a read-only service account, immutable `objectGUID` binding, and three AD role groups (`admin > operator > observer`, with optional nested groups via `LDAP_MATCHING_RULE_IN_CHAIN`).
2. **Keycloak (OIDC)** — integration with an existing external Keycloak server (Authorization Code Flow + PKCE S256), client roles `admin`, `operator`, `observer`, a service client with `view-users` to query live status and effective roles via the Admin REST API, RP-Initiated Logout, and Back-Channel Logout (`/auth/oidc/backchannel-logout`).

Integration secrets are encrypted with AES-256-GCM before storage in PostgreSQL using `/etc/omb/secrets.key` (32 bytes / 64 hex chars, mode `0600`, owned by `omb-control:omb-control`). Create the key before configuring integrations:

```bash
install -d -m 755 /etc/omb
openssl rand -hex 32 | tee /etc/omb/secrets.key >/dev/null
chown omb-control:omb-control /etc/omb/secrets.key
chmod 600 /etc/omb/secrets.key
```

Local sign-in and the last active local administrator always remain available even when AD or Keycloak is unreachable.

On an existing server, stop Control and back up the `omb_control` database, the previous binary, and the integrations key. If the external authentication migration (folded into `deploy/sql/control.sql`) has not been applied, apply it first. Then run the OIDC logout hash migration (folded into `deploy/sql/control.sql`) as the database owner with `psql -v ON_ERROR_STOP=1`, and start the new Control.

The migration preserves existing hashes and expiration times. It hashes any identifiers stored in their original form. Unexpected table layouts cause the migration to stop. To roll back, stop Control and restore the previous binary together with its schema and data. Fresh installations use the correct schema from `deploy/sql/control.sql`.
