# Installing without internet access

[Русская версия](../offline-installation.md)

The regular guides assume the server can reach the internet: packages come from `apt`, Go modules are downloaded during the build, and iPXE and wimboot are fetched from GitHub. This guide explains how to build a kit with everything needed on a machine that has internet access, move it to an isolated server, and install there without any outside connections.

It covers only the replacements for steps that download something. Service configuration (`boot.env`, `control.env`, DHCP, storage, Samba) stays the same as in the [Boot installation](../installation.md), the [Control guide](control-installation.md) and the [Images guide](images-installation.md).

> This procedure has not yet been tested on a server fully cut off from the internet. Record the result here after the first such installation.

## What a regular installation downloads

| What | Where it comes from normally | Replaced by |
| --- | --- | --- |
| Ubuntu packages | `apt` from the Ubuntu archive | `.deb` files from the kit, as a local repository |
| Go 1.26 and Go modules (`pgx`, `golang.org/x/...`) | go.dev and proxy.golang.org during `go build` | Prebuilt static binaries (`CGO_ENABLED=0`); no Go needed on the server |
| iPXE v2.0.0 and the 2011/2023 dual-signed shim 16.1 | GitHub via `scripts/fetch-ipxe.sh` | Archive and shim from the kit, same script with `file://` URLs |
| wimboot 2.9.0 | GitHub | File from the kit |
| virtio-win drivers (VMs only) | Fedora mirror | ZIP from the kit, imported on the **Drivers** page |

The admin interface loads nothing from outside: styles, scripts and fonts are embedded in the Control binary. Operating system ISOs come from the connected storage (SMB or a local folder), not from the internet; put them on the NAS or the local storage beforehand.

## Requirements

- **Server:** Ubuntu Server 26.04.1 LTS x86_64, the release the other guides were tested on. The packages in the kit fit only the same Ubuntu release and architecture; for another release, build the kit again.
- **Build machine:** internet access, Go 1.26 or newer, Docker (on Apple Silicon the container runs with `--platform linux/amd64`), `git`, `curl`. macOS works.
- **A medium or channel** to move the kit; it is a few hundred megabytes without drivers.

## 1. Build the kit

The finished kit looks like this:

```text
pxe-offline/
  COMMIT                    the commit the files were built from
  bin/                      eight Linux x86_64 binaries
  debs/                     .deb files with all dependencies and a Packages.gz index
  assets/                   ipxeboot.tar.gz, ipxe-shimx64.efi, wimboot, expected.sha256
  pxe-platform-src.tar.gz   deploy/, scripts/ and docs/ of the same commit
  SHA256SUMS
```

Run all commands in this section from the repository root.

### 1.1 Binaries

Build from a clean working tree so that the binaries and `deploy/` belong to the same commit:

```sh
test -z "$(git status --porcelain)" || { echo 'uncommitted changes'; exit 1; }
go test ./... && go vet ./...

KIT=$HOME/pxe-offline
mkdir -p "$KIT/bin"
git rev-parse HEAD > "$KIT/COMMIT"
for c in pxe-boot pxe-agent pxe-agentctl pxe-control pxe-controlctl \
         pxe-images pxe-imagesctl pxe-winiso-prepare; do
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "$KIT/bin/$c" ./cmd/$c
done
```

With `CGO_ENABLED=0` the binaries are static and do not depend on the server's libraries.

If policy requires building on the isolated side, add Go `go1.26.x.linux-amd64.tar.gz` from [go.dev/dl](https://go.dev/dl/) and a copy of the sources with modules to the kit. Add the modules with `go mod vendor` in a separate copy of the repository; `vendor/` is not committed. On the server, build with `GOFLAGS=-mod=vendor GOTOOLCHAIN=local`, otherwise Go will try to download the modules or another compiler version.

### 1.2 Ubuntu packages

Which services need which packages:

| Package | Purpose |
| --- | --- |
| `dnsmasq-base` | DHCP, proxy-DHCP and TFTP (`pxe-dnsmasq.service`) |
| `postgresql` | The `pxe_boot`, `pxe_control` and `pxe_images` databases |
| `ca-certificates`, `curl` | `fetch-ipxe.sh` and the HTTP checks in the guides |
| `openssl` | The admin interface's self-signed certificate |
| `cifs-utils` | Mounting SMB storage with ISOs |
| `samba` | The `pxeisos` share WinPE starts Setup from; `smbstatus` for the Agent log |
| `wimtools` | `wimlib-imagex` for driver WIMs |

The packages are downloaded in a clean container of the same Ubuntu release. The container has fewer packages installed than Ubuntu Server, so the kit gets every dependency, including ones the server already has. The local repository index is built from the same files:

```sh
mkdir -p "$KIT/debs"
docker run --rm --platform linux/amd64 -v "$KIT/debs:/out" ubuntu:26.04 sh -ec '
  apt-get update
  apt-get install -y --download-only --no-install-recommends \
    dnsmasq-base postgresql ca-certificates curl openssl cifs-utils samba wimtools
  cp /var/cache/apt/archives/*.deb /out/
  apt-get install -y --no-install-recommends dpkg-dev
  cd /out && dpkg-scanpackages --multiversion . /dev/null | gzip -9c > Packages.gz'
```

Build the kit shortly before the installation. If the server was updated after the kit was built, `apt` may not find matching dependency versions; then build the packages again.

### 1.3 Boot files

```sh
mkdir -p "$KIT/assets" && cd "$KIT/assets"
curl -fL -o ipxeboot.tar.gz https://github.com/ipxe/ipxe/releases/download/v2.0.0/ipxeboot.tar.gz
curl -fL -o ipxe-shimx64.efi https://github.com/ipxe/shim/releases/download/ipxe-16.1/ipxe-shimx64.efi
curl -fL -o wimboot https://github.com/ipxe/wimboot/releases/download/v2.9.0/wimboot
cat > expected.sha256 <<'SUMS'
01a526d4cc791fc30362259c609d6c506cc64a7bdff51b9a5eb788354e17eee1  ipxeboot.tar.gz
5eecca2780bd49c900565e124516a1bd666ec5e012825f34991b6ba1ef2fa6cf  ipxe-shimx64.efi
5f067ccdc4d084d5bf77b6c853bd0f8402dfc2b4cd1b103d358993ae97fae8e3  wimboot
SUMS
sha256sum -c expected.sha256      # on macOS: shasum -a 256 -c expected.sha256
cd -
```

The hashes come from the [boot assets description](../../deploy/boot-assets/README.md) and the [Windows 10 guide](windows-winpe-pxe.md). If the check fails, do not use the files.

**VM drivers (optional).** Windows on Proxmox/KVM needs the virtio-win drivers. Download the 0.1.271 ISO using the link and hash from the [Windows 10 guide](windows-winpe-pxe.md), extract the folders you need (for example `vioscsi/w11/amd64` and `NetKVM/w11/amd64`) and pack them into a ZIP in `$KIT/assets/drivers/`. On the server, import that ZIP on the **Drivers** page.

### 1.4 Sources and checksums

```sh
git archive --format=tar.gz -o "$KIT/pxe-platform-src.tar.gz" HEAD
cd "$KIT"
find . -type f ! -name SHA256SUMS | sort | xargs sha256sum > SHA256SUMS   # macOS: xargs shasum -a 256
cd ..
tar -czf "pxe-offline-$(cat pxe-offline/COMMIT | cut -c1-8).tar.gz" pxe-offline
```

Record the hash of the final archive separately from the medium, for example in the transfer request.

## 2. Move it to the server

Copy the archive to the server by any permitted means and extract it into `/opt`:

```sh
sudo tar -xzf pxe-offline-*.tar.gz -C /opt
sudo chown -R root:root /opt/pxe-offline
sudo chmod -R u=rwX,go=rX /opt/pxe-offline
cd /opt/pxe-offline && sha256sum -c --quiet SHA256SUMS && echo 'kit intact'
```

`apt` also needs the `go=rX` permissions: it reads the local repository as the `_apt` service user.

## 3. Install

### 3.1 Packages

Add the `debs` folder as a local repository and refresh the index from it alone; the other sources are not contacted:

```sh
echo 'deb [trusted=yes] file:/opt/pxe-offline/debs ./' | sudo tee /etc/apt/sources.list.d/pxe-offline.list
sudo apt-get update \
  -o Dir::Etc::sourcelist=/etc/apt/sources.list.d/pxe-offline.list \
  -o Dir::Etc::sourceparts=- -o APT::Get::List-Cleanup=0
sudo apt-get install -y --no-install-recommends \
  dnsmasq-base postgresql ca-certificates curl openssl cifs-utils samba wimtools
```

`trusted=yes` turns off the repository signature check. That is acceptable because the kit's integrity has already been checked against `SHA256SUMS`. After installation you can delete `/etc/apt/sources.list.d/pxe-offline.list`.

Once the `samba` package is installed, `smbd` starts at once with Ubuntu's default configuration. Replace `/etc/samba/smb.conf` with the [sample](../../deploy/samba/smb.conf), adjusting `interfaces` and `hosts allow` to your network, before connecting the server to the client network.

### 3.2 Binaries and sources

```sh
sudo install -m 0755 /opt/pxe-offline/bin/* /usr/local/bin/
sudo install -d -m 0755 /opt/pxe-platform-src
sudo tar -xzf /opt/pxe-offline/pxe-platform-src.tar.gz -C /opt/pxe-platform-src
cd /opt/pxe-platform-src
```

From here on, run all commands with `deploy/...` and `scripts/...` paths from `/opt/pxe-platform-src`.

### 3.3 iPXE and wimboot

`fetch-ipxe.sh` reads both the archive and shim with `curl`, which accepts `file://` URLs. The script checks both SHA-256 digests, replaces only `shimx64.efi` in the extracted archive, and does not overwrite an existing folder:

```sh
sudo ./scripts/fetch-ipxe.sh \
  file:///opt/pxe-offline/assets/ipxeboot.tar.gz \
  01a526d4cc791fc30362259c609d6c506cc64a7bdff51b9a5eb788354e17eee1 \
  file:///opt/pxe-offline/assets/ipxe-shimx64.efi \
  5eecca2780bd49c900565e124516a1bd666ec5e012825f34991b6ba1ef2fa6cf \
  /srv/pxe/tftp
```

The Windows menu loads `wimboot` from `/assets/windows/wimboot`, that is, from `/srv/pxe/tftp/windows/wimboot`:

```sh
sudo install -d -m 0755 /srv/pxe/tftp/windows
sudo install -m 0644 /opt/pxe-offline/assets/wimboot /srv/pxe/tftp/windows/wimboot
echo '5f067ccdc4d084d5bf77b6c853bd0f8402dfc2b4cd1b103d358993ae97fae8e3  /srv/pxe/tftp/windows/wimboot' | sha256sum -c
```

### 3.4 Users and databases

System users are created as in the guides. The `pxe-control` group must exist before Boot and Agent start, because their units use it as `Group=`:

```sh
sudo useradd --system --user-group --home /var/lib/pxe-platform-control --shell /usr/sbin/nologin pxe-control
sudo useradd --system --user-group --home /var/lib/pxe-platform-images --shell /usr/sbin/nologin pxe-images
sudo useradd --system --home /var/lib/pxe-platform-boot --shell /usr/sbin/nologin pxe-boot
for svc in boot control images; do
  sudo runuser -u postgres -- psql -c "CREATE ROLE \"pxe-$svc\" LOGIN"
  sudo runuser -u postgres -- createdb -O "pxe-$svc" "pxe_$svc"
done
```

The guides describe migrations as updates to a running server. For empty databases most migrations are already part of the main schemas, so the order is different:

```sh
sudo runuser -u pxe-boot    -- psql -v ON_ERROR_STOP=1 -d pxe_boot    < deploy/sql/boot.sql
sudo runuser -u pxe-boot    -- psql -v ON_ERROR_STOP=1 -d pxe_boot    < deploy/sql/2026-10-23-boot-transfer.sql
sudo runuser -u pxe-images  -- psql -v ON_ERROR_STOP=1 -d pxe_images  < deploy/sql/images.sql
sudo runuser -u pxe-control -- psql -v ON_ERROR_STOP=1 -d pxe_control < deploy/sql/control.sql
sudo runuser -u pxe-control -- psql -v ON_ERROR_STOP=1 -d pxe_control < deploy/sql/2026-10-19-winpe-drivers.sql
```

- `boot.sql` lacks the `status`, `bytes` and `size` columns, so `2026-10-23-boot-transfer.sql` is needed.
- `control.sql` lacks the driver catalog tables, so `2026-10-19-winpe-drivers.sql` is needed. The other Control migrations are already in `control.sql`; running them again changes nothing.
- **Do not apply** `2026-10-15-managed-smb.sql` to a new Images database: its columns are already in `images.sql`, and the migration will fail.

Check migrations added after this guide the same way: if the main schema does not contain them, apply them after the schema.

### 3.5 The rest of the setup

From here the setup is the same as usual:

- [Boot installation](../installation.md): units, `boot.env`, menu check, applying DHCP with `pxe-agentctl`. The `apt` and `fetch-ipxe.sh` steps are already done above.
- [Control guide](control-installation.md): `control.env`, certificate, first administrator. The certificate is created locally, without network access, for example:
  ```sh
  sudo install -d -m 0700 -o pxe-control -g pxe-control /var/lib/pxe-platform-control
  sudo openssl req -x509 -newkey rsa:3072 -nodes -days 90 -subj '/CN=pxe-control' \
    -addext 'subjectAltName=IP:<admin address>' \
    -keyout /var/lib/pxe-platform-control/control.key -out /var/lib/pxe-platform-control/control.crt
  sudo chown pxe-control:pxe-control /var/lib/pxe-platform-control/control.*
  sudo chmod 0600 /var/lib/pxe-platform-control/control.key
  ```
- [Images guide](images-installation.md): `images.env`, storage, Windows profiles, the `deploy/boot-assets/windows-profiles/` templates in `/usr/share/pxe-platform/windows-profiles`, the `pxe-iso` Samba user.

`pxe-agent.service` runs with `ProtectSystem=strict` and will not start if any path in its `ReadWritePaths` is missing. Before the first start, `/etc/pxe-platform`, `/etc/pxe-platform/winiso`, `/mnt/pxe-platform-smb`, `/mnt/pxe-platform-smb-upload`, `/mnt/pxe-platform-iso/profiles`, `/srv/pxe/tftp/windows-profiles`, `/srv/pxe/tftp/windows-answers`, `/srv/pxe/tftp/windows-driver-wims` and `/srv/pxe/tftp/windows-driver-configs` must exist. Owners and modes are given in the Images guide and in the drivers section of the Control guide.

## 4. Check

First make sure the server really is isolated, otherwise the check proves nothing:

```sh
curl -sS -m 5 -o /dev/null https://proxy.golang.org && echo 'WARNING: internet is reachable'
```

Then check the services and the menu as in the guides:

```sh
systemctl is-active postgresql pxe-boot pxe-agent pxe-control pxe-images smbd
curl -fsS http://<Boot address>:8080/boot/menu.ipxe | head -1        # expect #!ipxe
curl -fsSI http://<Boot address>:8080/assets/windows/wimboot | head -1
```

## 5. Running without internet

- **Time.** Without internet the server cannot sync with Ubuntu's NTP servers. A wrong clock breaks certificate validity and the order of events in the logs. Set an NTP server on the local network in `/etc/systemd/timesyncd.conf` (`NTP=<address>`), for example the router, and restart `systemd-timesyncd`.
- **Security updates.** `apt` and `unattended-upgrades` get nothing on an isolated server. Ubuntu updates arrive only with a new kit.
- **Updating Oh My Boot.** Build a new kit from the new commit. New migrations are the files added to `deploy/sql` between the commits: `git diff --name-only --diff-filter=A <old>..<new> -- deploy/sql`. Before replacing anything, save the previous binaries and a database dump as the guides describe, then apply the migrations and replace the binaries. Install packages from the kit only if their list changed or updates are needed.
