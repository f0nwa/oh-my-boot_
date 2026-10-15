# Ubuntu Server: first PXE iteration

This guide installs the boot foundation on one Ubuntu Server x86_64 host without containers. It serves a diagnostic iPXE menu, offers BIOS and UEFI x64 network boot files, and has separate full DHCP and proxy-DHCP configurations. ISO boot, dashboard, users and roles are later stages.

## Before you start

- Use an isolated test network with a PXE client VM and a second NIC on the Ubuntu host. In **full** mode, the PXE service assigns addresses; keep every other DHCP server off that test network. In **proxy** mode, keep an existing DHCP server on that test network and let it assign addresses.
- Set a static IPv4 address on the Ubuntu test NIC. The examples use the documentation subnet `192.0.2.0/24` and `enp2s0`; replace **every** sample address and interface before applying a configuration. Check the real interface with `ip -br addr`.
- Ensure clients can reach UDP 67, 69 and 4011 and TCP 8080 on the test network. Firewall rules and VM network mode depend on the lab.
- The full-DHCP BIOS and UEFI test results are recorded in [the VM test report](vm-test-2026-10-15.md). Proxy-DHCP and ISO boot remain untested.

## Build and install

Install PostgreSQL and the `dnsmasq` binary without enabling the distribution's default dnsmasq service:

```sh
sudo apt update
sudo apt install -y dnsmasq-base postgresql ca-certificates curl
```

Build with Go 1.26 or newer from this repository. The three output files are Linux x86_64 executables:

```sh
mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/pxe-boot ./cmd/pxe-boot
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/pxe-agent ./cmd/pxe-agent
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/pxe-agentctl ./cmd/pxe-agentctl
sudo install -m 0755 dist/pxe-boot dist/pxe-agent dist/pxe-agentctl /usr/local/bin/
```

Create the unprivileged Boot account and configuration directory:

```sh
sudo useradd --system --home /var/lib/pxe-platform-boot --shell /usr/sbin/nologin pxe-boot
sudo install -d -m 0750 -o root -g root /etc/pxe-platform
```

Fetch the pinned iPXE v2.0.0 release. The script verifies SHA-256 before publishing the extracted files and refuses to overwrite an existing destination. Details and the upstream source are in [boot assets](../deploy/boot-assets/README.md).

```sh
sudo ./scripts/fetch-ipxe.sh \
  https://github.com/ipxe/ipxe/releases/download/v2.0.0/ipxeboot.tar.gz \
  01a526d4cc791fc30362259c609d6c506cc64a7bdff51b9a5eb788354e17eee1 \
  /srv/pxe/tftp
```

Copy the units and set the client-visible HTTP address in `/etc/pxe-platform/boot.env`:

```sh
sudo install -m 0644 deploy/systemd/*.service /etc/systemd/system/
printf 'PXE_BOOT_BASE_URL=http://192.0.2.10:8080\n' | sudo tee /etc/pxe-platform/boot.env
sudo chmod 0640 /etc/pxe-platform/boot.env
sudo systemctl daemon-reload
sudo systemctl enable --now pxe-boot.service pxe-agent.service
curl -fsS http://192.0.2.10:8080/boot/menu.ipxe
```

The last command should return a script starting with `#!ipxe`. `pxe-dnsmasq.service` has no install target and is not started by these commands. DHCP begins only after the explicit `pxe-agentctl` apply below.

## Optional PostgreSQL event storage

Boot works with local journal logging if PostgreSQL is not configured or becomes unavailable. On this single-host installation, use PostgreSQL's local peer authentication: the database role has the same name as the `pxe-boot` system user, so no database password is needed.

```sh
sudo runuser -u postgres -- psql -c 'CREATE ROLE "pxe-boot" LOGIN'
sudo runuser -u postgres -- createdb -O pxe-boot pxe_boot
sudo runuser -u pxe-boot -- psql -d pxe_boot < deploy/sql/boot.sql
```

Add `PXE_BOOT_DATABASE_URL="host=/var/run/postgresql dbname=pxe_boot user=pxe-boot"` to `/etc/pxe-platform/boot.env` and restart `pxe-boot.service`. The Boot process does not require a database connection to answer client requests; failed inserts are written to the journal. This local database setup and fallback were verified on the Ubuntu test VM.

## Review and apply network settings

Copy [network-proxy.json](../deploy/examples/network-proxy.json) or [network-full.json](../deploy/examples/network-full.json) to a local file. Replace the sample interface, address, subnet, DHCP range and boot URL. Preview the exact dnsmasq configuration, then apply it through the local agent socket:

```sh
./dist/pxe-agentctl -dry-run -config ./network.json > ./preview.conf
sudo /usr/sbin/dnsmasq --test --conf-file=./preview.conf
sudo /usr/local/bin/pxe-agentctl -config ./network.json
sudo systemctl status pxe-dnsmasq.service
sudo cat /etc/pxe-platform/dnsmasq.conf
```

The agent also runs `dnsmasq --test` before every apply. It writes only `/etc/pxe-platform/dnsmasq.conf`, keeps the last successful settings in `/var/lib/pxe-platform-agent/network.json`, and restores the previous file and unit state if a restart fails. Do not edit the generated file by hand. The agent API is a root-owned Unix socket at `/run/pxe-platform/agent.sock` and has no TCP listener.

To inspect a failed apply, use `journalctl -u pxe-agent -u pxe-dnsmasq -n 100 --no-pager` and compare the active config with the saved state. To stop this PXE service's DHCP without affecting any other dnsmasq instance, run `sudo systemctl stop pxe-dnsmasq.service`.

## PXE acceptance matrix on the supplied VM

For each row, record the client's DHCP result, TFTP file request, iPXE HTTP menu response and local boot exit. The proxy rows require an independent DHCP server. Secure Boot rows must show that firmware accepts the shim and that it loads `ipxe.efi`.

| Client | Full DHCP | Proxy DHCP |
| --- | --- | --- |
| Legacy BIOS | DHCP lease, HTTP menu and Diagnostics passed on SeaBIOS; TFTP transfer not separately recorded | Pending VM |
| UEFI x64, Secure Boot off | Firmware showed Disabled after saving; DHCP, TFTP shim and iPXE binary, HTTP menu passed on OVMF | Pending VM |
| UEFI x64, Secure Boot on | OVMF showed Enabled before it was switched off; DHCP, TFTP shim and iPXE binary, HTTP menu passed without manual key enrollment | Pending VM |

Check HTTP with `curl` and service logs with `journalctl -u pxe-boot -u pxe-agent -u pxe-dnsmasq --no-pager`. Check database rows with `sudo runuser -u pxe-boot -- psql -d pxe_boot -c 'SELECT kind, path, occurred_at FROM boot_events ORDER BY id DESC LIMIT 10'` once PostgreSQL is configured. Do not interpret a successful syntax check as proof that clients boot.
