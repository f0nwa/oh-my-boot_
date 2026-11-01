# Ubuntu Server: first PXE iteration

> This guide uses current OMB names. See the [stand status and remaining checks](research/2026-10-31-omb-technical-migration.md).

The HTTPS dashboard and second-stage installation are described in [Control installation](control-installation.md).

This guide installs the boot foundation on one Ubuntu Server x86_64 host without containers. It serves a diagnostic iPXE menu, offers BIOS and UEFI x64 network boot files, and has separate full DHCP and proxy-DHCP configurations. The dashboard, users and roles are covered in the Control guide; ISO boot remains a later stage.

## Before you start

- Use an isolated test network with a PXE client VM and a second NIC on the Ubuntu host. In **full** mode, the PXE service assigns addresses; keep every other DHCP server off that test network. In **proxy** mode, keep an existing DHCP server on that test network and let it assign addresses.
- Set a static IPv4 address on the Ubuntu test NIC. The examples use the documentation subnet `192.0.2.0/24` and `enp2s0`; replace **every** sample address and interface before applying a configuration. Check the real interface with `ip -br addr`.
- Ensure clients can reach UDP 67, 69 and 4011 and TCP 8080 on the test network. Firewall rules and VM network mode depend on the lab.
- The full-DHCP BIOS and UEFI test results are recorded in [project history](project-history.md). Proxy-DHCP and ISO boot remain untested.

## Build and install

Install PostgreSQL and the `dnsmasq` binary without enabling the distribution's default dnsmasq service:

```sh
sudo apt update
sudo apt install -y dnsmasq-base postgresql ca-certificates curl
```

Build with Go 1.26 or newer from this repository. The three output files are Linux x86_64 executables:

```sh
mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/omb-boot ./cmd/omb-boot
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/omb-agent ./cmd/omb-agent
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/omb-agentctl ./cmd/omb-agentctl
sudo install -m 0755 dist/omb-boot dist/omb-agent dist/omb-agentctl /usr/local/bin/
```

Create the unprivileged Boot account and configuration directory:

```sh
sudo useradd --system --home /var/lib/omb-boot --shell /usr/sbin/nologin omb-boot
sudo install -d -m 0750 -o root -g root /etc/omb
```

Fetch the pinned iPXE v2.0.0 release. The script verifies SHA-256 before publishing the extracted files and refuses to overwrite an existing destination. Details and the upstream source are in [boot assets](../deploy/boot-assets/README.md).

```sh
sudo ./scripts/fetch-ipxe.sh \
  https://github.com/ipxe/ipxe/releases/download/v2.0.0/ipxeboot.tar.gz \
  01a526d4cc791fc30362259c609d6c506cc64a7bdff51b9a5eb788354e17eee1 \
  https://github.com/ipxe/shim/releases/download/ipxe-16.1/ipxe-shimx64.efi \
  5eecca2780bd49c900565e124516a1bd666ec5e012825f34991b6ba1ef2fa6cf \
  /srv/omb/tftp
```

Copy the units and set the client-visible HTTP address in `/etc/omb/boot.env`:

```sh
sudo install -m 0644 deploy/systemd/*.service /etc/systemd/system/
printf 'OMB_BOOT_BASE_URL=http://192.0.2.10:8080\n' | sudo tee /etc/omb/boot.env
sudo chmod 0640 /etc/omb/boot.env
sudo systemctl daemon-reload
sudo systemctl enable --now omb-boot.service omb-agent.service
curl -fsS http://192.0.2.10:8080/boot/menu.ipxe
```

The last command should return a script starting with `#!ipxe`. `omb-dnsmasq.service` has no install target and is not started by these commands. DHCP begins only after the explicit `omb-agentctl` apply below.

## Optional PostgreSQL event storage

Boot works with local journal logging if PostgreSQL is not configured or becomes unavailable. On this single-host installation, use PostgreSQL's local peer authentication: the database role has the same name as the `omb-boot` system user, so no database password is needed.

```sh
sudo runuser -u postgres -- psql -c 'CREATE ROLE "omb-boot" LOGIN'
sudo runuser -u postgres -- createdb -O omb-boot omb_boot
sudo runuser -u omb-boot -- psql -d omb_boot < deploy/sql/boot.sql
```

Add `OMB_BOOT_DATABASE_URL="host=/var/run/postgresql dbname=omb_boot user=omb-boot"` to `/etc/omb/boot.env` and restart `omb-boot.service`. The Boot process does not require a database connection to answer client requests; failed inserts are written to the journal. This local database setup and fallback were verified on the Ubuntu test VM.

## Review and apply network settings

Copy [network-proxy.json](../deploy/examples/network-proxy.json) or [network-full.json](../deploy/examples/network-full.json) to a local file. Replace the sample interface, address, subnet, DHCP range and boot URL. Preview the exact dnsmasq configuration, then apply it through the local agent socket:

```sh
./dist/omb-agentctl -dry-run -config ./network.json > ./preview.conf
sudo /usr/sbin/dnsmasq --test --conf-file=./preview.conf
sudo /usr/local/bin/omb-agentctl -config ./network.json
sudo systemctl status omb-dnsmasq.service
sudo cat /etc/omb/dnsmasq.conf
```

The agent also runs `dnsmasq --test` before every apply. It writes only `/etc/omb/dnsmasq.conf`, keeps the last successful settings in `/var/lib/omb-agent/network.json`, and restores the previous file and unit state if a restart fails. Do not edit the generated file by hand. The agent API is a root-owned Unix socket at `/run/omb/agent.sock` and has no TCP listener.

To inspect a failed apply, use `journalctl -u omb-agent -u omb-dnsmasq -n 100 --no-pager` and compare the active config with the saved state. To stop this PXE service's DHCP without affecting any other dnsmasq instance, run `sudo systemctl stop omb-dnsmasq.service`.

## PXE acceptance matrix on the supplied VM

For each row, record the client's DHCP result, TFTP file request, iPXE HTTP menu response and local boot exit. The proxy rows require an independent DHCP server. Secure Boot rows must show that firmware accepts the shim and that it loads `ipxe.efi`.

| Client | Full DHCP | Proxy DHCP |
| --- | --- | --- |
| Legacy BIOS | DHCP lease, HTTP menu and Diagnostics passed on SeaBIOS; TFTP transfer not separately recorded | Keenetic lease, dnsmasq proxy PXE request and HTTP menu passed on SeaBIOS; TFTP transfer not separately recorded |
| UEFI x64, Secure Boot off | Firmware showed Disabled after saving; DHCP, TFTP shim and iPXE binary, HTTP menu passed on OVMF | Two repeat boots passed: Keenetic lease, proxy UDP 4011, TFTP shim and iPXE binary, HTTP menu; one earlier attempt timed out |
| UEFI x64, Secure Boot on | User reported menu after enabling; no fresh server trace separately linked to that run | Keenetic lease, dnsmasq proxy on UDP 4011, TFTP shim and iPXE binary, HTTP menu passed after Secure Boot was enabled |

Check HTTP with `curl` and service logs with `journalctl -u omb-boot -u omb-agent -u omb-dnsmasq --no-pager`. Check database rows with `sudo runuser -u omb-boot -- psql -d omb_boot -c 'SELECT kind, path, occurred_at FROM boot_events ORDER BY id DESC LIMIT 10'` once PostgreSQL is configured. Do not interpret a successful syntax check as proof that clients boot.
