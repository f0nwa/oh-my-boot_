# Images: OMB ISO catalog and storage

[Русская версия](../images-installation.md). For boot entries and menu publishing, use the [Control guide](control-installation.md#boot-entries-and-menu).

Images stores its catalog in `omb_images` and serves a local API at `/run/omb-images/images.sock`. Finding an ISO does not create a menu entry: create a boot entry from the image, then publish the menu. Preparation runs automatically when the entry is saved.

## First installation

Prepare Boot, Agent and PostgreSQL using the [server guide](../installation.md). These commands are for a new server. Preserve the database, configuration and registered storage on the existing stand.

Build from the repository root:

~~~sh
mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o dist/omb-images ./cmd/omb-images
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o dist/omb-imagesctl ./cmd/omb-imagesctl
~~~

Copy `dist` and `deploy` to the server. Run the server commands from their parent directory in Bash as root:

~~~sh
install -m 0755 dist/omb-images dist/omb-imagesctl /usr/local/bin/
useradd --system --user-group --home-dir /var/lib/omb-images --shell /usr/sbin/nologin omb-images
install -d -m 0750 -o root -g root /etc/omb
install -d -m 0750 -o omb-images -g omb-images /var/lib/omb-images
install -d -m 2770 -o root -g omb-images /var/lib/omb-images/iso
install -d -m 0755 -o root -g root /mnt/omb-smb-upload
runuser -u postgres -- psql -v ON_ERROR_STOP=1 -c 'CREATE ROLE "omb-images" LOGIN'
runuser -u postgres -- createdb --owner=omb-images omb_images
runuser -u omb-images -- psql -v ON_ERROR_STOP=1 --dbname=omb_images < deploy/sql/images.sql
~~~

The guard protects an existing configuration file:

~~~sh
(
  set -eu
  test ! -e /etc/omb/images.env
  cat > /etc/omb/images.env <<'ENV'
OMB_IMAGES_DATABASE_URL="host=/var/run/postgresql dbname=omb_images user=omb-images"
ENV
  chmod 0640 /etc/omb/images.env
)
install -m 0644 deploy/systemd/omb-images.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now omb-images.service
~~~

Install Control using [its guide](control-installation.md). Its unit includes `SupplementaryGroups=omb-images` for socket access. Restart an existing service after changing its unit groups.

## Registration and scanning

Register the local directory once:

~~~sh
runuser -u omb-images -- env OMB_IMAGES_DATABASE_URL='host=/var/run/postgresql dbname=omb_images user=omb-images' \
  /usr/local/bin/omb-imagesctl add-path -name Local -kind local -root /var/lib/omb-images/iso
~~~

Use `-kind mounted` for an already mounted filesystem, with an absolute path to a real mount point and no symlinks.

Administrators connect SMB and NFS network storages under **System → Storage** (via the **Connect storage ▾** menu button):
- **SMB:** a direct path such as `//nas.example.com/share/iso` and credentials (username, password, domain). SMB requires the `cifs-utils` package; Agent stores credentials under `/etc/omb/smb/<key>` with mode `0600`.
- **NFS:** server address (for example, `nas.example.com` or IPv4/IPv6) and absolute export path (e.g. `/volume1/iso` or `/export/iso`). Supported versions are **NFSv4.1** (default, TCP port 2049) and **NFSv3** (RPC portmapper 111, nfs 2049, `nolock` option). NFS requires the system package `nfs-common` (`mount.nfs`). Agent saves connection parameters in `/etc/omb/nfs/<key>.json` with mode `0600`.

**Access Modes and NFS Permission Auto-Detection:**
NFS evaluates access permissions by Unix process UID/GID rather than mount options. During connection and subsequent availability checks (**Refresh** in the `☰` row menu), Agent mounts the export read-only at `/mnt/omb-nfs/<key>` and automatically performs a write probe at a temporary mount point `/mnt/omb-nfs-upload/<key>` as the `omb-images` system user:
- If the NAS export permits writing and file creation for `omb-images`, the storage enters **read and write** mode (ISO upload and deletion are enabled).
- If the export is read-only or `omb-images` lacks write permissions on the NAS, the storage remains in **read-only** mode (catalog scanning, free space display, and boot entry preparation work normally; ISO upload and deletion are disabled).
- If NAS permissions change later, click **Refresh** in the storage's `☰` menu to re-detect the access mode without reconnecting.

**NAS Export Configuration Requirements and Recommendations:**

Because the `omb-images` catalog service runs under a local system UID/GID on the OMB server (which typically does not exist on an external NAS), choosing `No mapping` for Squash on ACL-enabled NAS systems (such as Synology DSM) causes `permission denied` errors during scans. Configure Squash according to the intended operating mode:

1. **Test and Read-Only Production Mode (ISOs are placed on the NAS externally; OMB only reads and serves them over PXE):**
   - **NAS export privilege:** `Read only` (`ro`).
   - **Squash / user mapping:**
     - On Synology DSM: `Map all users to admin` (if folder access is restricted by ACLs; the NFS rule itself remains `Read only`, so the client cannot modify any files) or `Map all users to guest` (if `guest` has read permissions on that folder).
     - On a Linux NFS server: `ro,all_squash` with `anonuid`/`anongid` of a user that can read the directory (`o+rx` on directories and `0644` on `.iso` files).
   - **Recommended on Synology DSM:** enable asynchronous mode and allow connections from non-privileged ports (above 1024).

2. **Read-Write Production Mode (full management: ISO upload and deletion from the OMB web interface):**
   - In production, dedicate a **separate shared folder / export** exclusively for OMB images (isolated from unrelated user files).
   - **NAS export privilege:** `Read/Write` (`rw`).
   - **Squash / user mapping:**
     - On Synology DSM: `Map all users to admin` (or map to a dedicated NAS service account that has write permissions only on this shared folder).
     - On a Linux NFS server: `rw,all_squash` with `anonuid`/`anongid` of the directory owner, or grant directory write permissions directly to the local `omb-images` UID/GID (check with `id omb-images`).
   - **Security and file modes:** in `root_squash` mode (default on most NAS devices), when `omb-agent` and `omb-winiso-prepare` (running as `root`) loop-mount ISOs, files on NFS are accessed as `nobody`; therefore, export directories must grant `o+rx` and `.iso` files must be `0644` (OMB sets `0644` automatically on uploaded ISOs). With `no_root_squash`, the OMB server root retains direct access.
   - After changing NAS permissions, click **Refresh** in the storage's `☰` menu so OMB re-runs the write probe and updates the access mode.

Operators and administrators can start scans; observers can view the catalog. Background availability checks preserve previous records on failure. Interrupted scans are marked as failed after a restart and can be retried. See [Control storage recovery](control-installation.md#storage-availability-and-recovery) for managed mounts.

## ISO upload

In the library, select writable storage, an existing folder and an ISO up to 32 GiB. The complete ISO goes to the selected storage; its size and SHA-256 are checked afterwards. Existing files are not overwritten, and cancellation removes the temporary file. Menu publishing is a separate action.

SMB and NFS use separate mount points: `/mnt/omb-smb/<key>` or `/mnt/omb-nfs/<key>` for reading and `/mnt/omb-smb-upload/<key>` or `/mnt/omb-nfs-upload/<key>` for writing. The NAS account or directory permissions must permit writes. Local uploads use `/var/lib/omb-images/iso`; the `omb-images` group and service unit must permit writing there. Deleting an ISO from local, mounted, or network (SMB/NFS) storage also requires the `omb-images` user to have write permission in the corresponding directory. Free space refers to the selected storage filesystem.

## Windows images, XML and drivers

Under **Boot entries**, create an entry from a Windows x64 ISO. Preparation starts automatically after saving. Agent checks the source, mounts it read-only and prepares boot files under `/srv/omb/tftp/windows-profiles/<ID>/`. The full ISO stays on its storage. Clients read the installation media through the separate `ombisos` share using `omb-iso`; NAS credentials are not sent to WinPE.

Windows Setup delivery flow when using network storage:
`NAS (NFS export or SMB share) → OMB server (/mnt/omb-nfs/<key> or /mnt/omb-smb/<key>) → loop mount ISO (/mnt/omb-iso/profiles/<ID>) → local Samba ombisos share → WinPE client`. PXE and WinPE clients do not connect directly to the NAS and do not require client-side NFS support; the client runs standard SMB against the OMB server.

Preparation requires `omb-winiso-prepare`, the [profile unit](../../deploy/systemd/omb-winiso@.service), [templates](../../deploy/boot-assets/windows-profiles/) under `/usr/share/omb/windows-profiles`, [Samba configuration](../../deploy/samba/smb.conf) and `/etc/omb/omb-iso-password`. Mount and configuration directories are `/mnt/omb-iso/profiles` and `/etc/omb/winiso`. Install these through the [server bundle](offline-installation.md); installing Images alone does not supply them. Local source ISOs for Windows must be inside `/var/lib/omb-images` or on managed SMB or NFS storage.

Upload a checked Windows XML under **Library → Answer files**, and INF files with their dependencies under **Library → Drivers**. When creating or editing a boot entry, select the ISO, XML and INF files, wait for preparation, then publish the menu. The source ISO stays unchanged. See the [WinPE driver catalog](control-installation.md#winpe-driver-catalog).

XML may contain passwords and keys. Image startup scripts, driver configuration scripts and answer files are served only to the trusted PXE subnet `10.30.0.0/24`; requests from the management network receive 404.

The folder selector appears only when storage has subfolders; otherwise uploads go to its root. The catalog lists every ISO, but finding a file does not guarantee boot support. The type is a file-name hint; media contents are checked during preparation. Each verified Ubuntu Desktop amd64 ISO is selected separately; multiple images can be used at the same time. Strelec x64 images with the `SSTR` layout are supported: OMB selects WinPE 11, or WinPE 10 when 11 is absent, enables the WIM's native PXE startup, and connects the same ISO's application payload through `ombisos`. WIM contents are copied unchanged. BIOS/UEFI boot managers and `boot.sdi` come from the same ISO and are supplied explicitly; its BCD is adapted to the selected WIM and wimboot's virtual paths. WinPE drivers use the same package and individual INF choices in the boot-entry form; OMB prepares a derived WIM and loads the drivers before connecting SSTR. Windows Setup answers do not apply to this source. Other recovery WinPE layouts without a known workflow, and ARM64, are not supported yet.

## Diagnostics

Run as root. The explicit group is needed to check Control socket access outside systemd:

~~~sh
systemctl is-active omb-images omb-control omb-boot omb-agent omb-dnsmasq
runuser -u omb-images -- psql -XAt --dbname=omb_images -c 'SELECT 1'
runuser -u omb-control -g omb-images -- curl --fail --unix-socket /run/omb-images/images.sock http://unix/catalog
journalctl -u omb-images -u omb-agent -n 100 --no-pager
~~~

Save consistent database, configuration and file backups before updating. See [recovery](../recovery.md), [stand status and remaining checks](../research/2026-10-31-omb-technical-migration.md), and [access to previous reports](../project-history.md).
