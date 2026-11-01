# Images: OMB ISO catalog and storage

[Русская версия](../images-installation.md). For installations and menu publishing, use the [Control guide](control-installation.md#installations-and-menu).

Images stores its catalog in `omb_images` and serves a local API at `/run/omb-images/images.sock`. Finding an ISO does not create a boot entry: prepare a profile, create an installation, then publish the menu.

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

## Windows profiles, XML and drivers

Create a profile from a Windows x64 ISO in the library and choose **Prepare**. Agent checks the source, mounts it read-only and prepares boot files under `/srv/omb/tftp/windows-profiles/<ID>/`. The full ISO stays on its storage. Clients read the installation media through the separate `ombisos` share using `omb-iso`; NAS credentials are not sent to WinPE.

Preparation requires `omb-winiso-prepare`, the [profile unit](../../deploy/systemd/omb-winiso@.service), [templates](../../deploy/boot-assets/windows-profiles/) under `/usr/share/omb/windows-profiles`, [Samba configuration](../../deploy/samba/smb.conf) and `/etc/omb/omb-iso-password`. Mount and configuration directories are `/mnt/omb-iso/profiles` and `/etc/omb/winiso`. Install these through the [server bundle](offline-installation.md); installing Images alone does not supply them. Local source ISOs for Windows must be inside `/var/lib/omb-images` or on managed SMB storage.

Upload a checked Windows XML under **Library → Answer files**, and INF files with their dependencies under **Library → Drivers**. Create an installation selecting the profile, XML and INF files, wait for preparation, then publish it. The source ISO stays unchanged. See the [WinPE driver catalog](control-installation.md#winpe-driver-catalog).

XML may contain passwords and keys. Boot files are intended for the trusted PXE subnet. Access restrictions for driver configuration scripts still need fixing; see the [stand report](../research/2026-10-31-omb-technical-migration.md#существующая-проблема-доступа).

## Diagnostics

Run as root. The explicit group is needed to check Control socket access outside systemd:

~~~sh
systemctl is-active omb-images omb-control omb-boot omb-agent omb-dnsmasq
runuser -u omb-images -- psql -XAt --dbname=omb_images -c 'SELECT 1'
runuser -u omb-control -g omb-images -- curl --fail --unix-socket /run/omb-images/images.sock http://unix/catalog
journalctl -u omb-images -u omb-agent -n 100 --no-pager
~~~

Save consistent database, configuration and file backups before updating. See [recovery](../recovery.md), [stand status and remaining checks](../research/2026-10-31-omb-technical-migration.md), and [access to previous reports](../project-history.md).
