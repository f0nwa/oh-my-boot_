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

Use `-kind mounted` for an already mounted filesystem, with an absolute path to a real mount point and no symlinks. Add managed SMB connections in **System → Storage**, supplying a direct path such as `//nas/share/iso` and credentials. SMB requires `cifs-utils`; Agent keeps separate credential files under `/etc/omb/smb`, mode `0600`.

Operators and administrators can start scans; observers can view the catalog. Background availability checks preserve previous records on failure. Interrupted scans are marked as failed after a restart and can be retried. See [Control storage recovery](control-installation.md#storage-availability-and-recovery) for managed mounts.

## ISO upload

In the library, select writable storage, an existing folder and an ISO up to 32 GiB. The complete ISO goes to the selected storage; its size and SHA-256 are checked afterwards. Existing files are not overwritten, and cancellation removes the temporary file. Menu publishing is a separate action.

SMB uses `/mnt/omb-smb/<key>` for reading and `/mnt/omb-smb-upload/<key>` for writing. The NAS account must permit writes. Local uploads use `/var/lib/omb-images/iso`; the `omb-images` group and service unit must permit writing there. Free space refers to the selected storage filesystem.

## Windows images, XML and drivers

Under **Boot entries**, create an entry from a Windows x64 ISO. Preparation starts automatically after saving. Agent checks the source, mounts it read-only and prepares boot files under `/srv/omb/tftp/windows-profiles/<ID>/`. The full ISO stays on its storage. Clients read the installation media through the separate `ombisos` share using `omb-iso`; NAS credentials are not sent to WinPE.

Preparation requires `omb-winiso-prepare`, the [profile unit](../../deploy/systemd/omb-winiso@.service), [templates](../../deploy/boot-assets/windows-profiles/) under `/usr/share/omb/windows-profiles`, [Samba configuration](../../deploy/samba/smb.conf) and `/etc/omb/omb-iso-password`. Mount and configuration directories are `/mnt/omb-iso/profiles` and `/etc/omb/winiso`. Install these through the [server bundle](offline-installation.md); installing Images alone does not supply them. Local source ISOs for Windows must be inside `/var/lib/omb-images` or on managed SMB storage.

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
