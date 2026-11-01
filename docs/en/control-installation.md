# OMB Control and the HTTPS dashboard

Current OMB operations guide. The existing stand is at **https://192.168.10.135:8443**. The first-installation commands below are for a new server; do not recreate accounts, databases, certificates or configuration on the running stand.

[Русская версия](../control-installation.md). Past rollout reports and previous-version instructions are preserved in the [project history](../project-history.md). The technical transition is recorded in the [migration report](../research/2026-10-31-omb-technical-migration.md).

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

Prepare Boot and Agent using the [server guide](../installation.md), install Images using the [storage guide](images-installation.md), and start PostgreSQL first. The `omb-images` group must exist: the Control unit names it in `SupplementaryGroups`. These examples use `ens19` for boot traffic and `192.168.10.135` for management; use your own values on another server.

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
    -addext 'subjectAltName=IP:192.168.10.135' \
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
OMB_CONTROL_LISTEN=192.168.10.135:8443
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

## Installations and menu

Prepare a boot profile, answer files and drivers in Library. Create separate Windows or Ubuntu choices on the Installations page; multiple installations can share one boot profile.

Select installations, titles and folders on the Menu page. Clients do not see the draft before publication. Control validates dependencies, creates a version and sends the matching snapshot to Boot. Saving a draft alone does not change the client menu.

Restoring a version through Menu → Versions also checks dependencies. If an ISO, profile or prepared file is unavailable, the current active menu stays in place. Menu restoration does not restore deleted ISOs or NAS data.

## WinPE driver catalog

Library → Drivers accepts directories containing INF files and their supporting files, or a ZIP with the same structure. An installation selects specific INFs. Its derived driver WIM is used by that installation; the source ISO remains on storage.

Answer files are under Library → Answer files. A Windows XML belongs to a profile. Publish a choice only after its files have been prepared and checked. Dependencies prevent deletion of profiles or files that are still used.

## Ubuntu Autoinstall

In Library → Answer files, use Create Ubuntu file in the Ubuntu Autoinstall block. The same action is available while creating an Ubuntu installation. The constructor offers interactive storage setup or erasure by exact disk serial number.

Saved files are immutable; create a copy for a new variant. Select a validated file in an installation and publish through the menu draft. Without an answer file the installation stays manual. Boot serves a matching NoCloud `user-data` and `meta-data` pair under `/assets/ubuntu-autoinstall/…`. File availability alone does not prove OS installation completion.

## Storage availability and recovery

System → Storages shows storage state and errors. Images checks reachability in the background; an error does not erase the previous catalog. Publication rejects an installation with an unavailable source.

Agent recovers its SMB mounts before Windows ISO profiles. Automatic recovery settings and a manual recovery action are on the storage page. On the existing stand recovery is enabled with a 300-second interval. The external NAS account and permissions determine whether writes are allowed.

## Diagnostics and rollback

Run as root:

~~~sh
systemctl status omb-control.service omb-boot.service omb-agent.service omb-images.service omb-dnsmasq.service
journalctl -u omb-control.service -u omb-boot.service -u omb-agent.service -n 100 --no-pager
runuser -u omb-control -- psql -XAt --dbname=omb_control -c 'SELECT 1'
curl --fail --cacert /var/lib/omb-control/control.crt https://192.168.10.135:8443/login
curl --fail http://10.30.0.10:8080/boot/menu.ipxe
~~~

To stop only a broken dashboard, use `systemctl stop omb-control.service`. Boot, Agent, Images and DHCP keep running. Start it with `systemctl start omb-control.service`. Do not edit generated `/etc/omb/dnsmasq.conf` to repair Control.

Before an update, preserve configuration, TLS material, binaries, units and the relevant databases. Changes to shared formats or relationships require a consistent backup of every affected database and file with writers stopped. Roll back a compatible set of code and schema; replacing one binary may be insufficient.

Rollback of the technical migration has its own [procedure](../recovery.md). It restores the state at the saved backup, so preserve newer data first and arrange a maintenance window. The verified transition backup on the existing stand is `/root/omb-migration-20261031/backup`.

## Verification limits

The server passed file/database restoration, WinPE rebuild, TFTP, SMB/NAS and reboot checks. The user confirmed UEFI Secure Boot loading with XML and drivers. The full client regression pass is deferred until the main development work is finished; see the [migration report](../research/2026-10-31-omb-technical-migration.md).

Past deployment dates, previous rollback commands and rollout checksums are in the [project history](../project-history.md).
