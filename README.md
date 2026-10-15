# PXE Platform

An independent PXE service for Linux servers. The first iteration contains a Go HTTP Boot service with a diagnostic iPXE menu, a local Go Network Agent, full DHCP and proxy-DHCP configuration for a dedicated dnsmasq instance, pinned iPXE boot assets, and optional PostgreSQL boot events.

The project is separate from the neighboring Ventoy/PXE source tree. ISO management, Windows/Ubuntu image loading, the dashboard, and user roles are planned stages; they are not implemented in this iteration. A test client has reached the menu in BIOS and UEFI with Secure Boot disabled using full DHCP, and in BIOS and UEFI with Secure Boot both enabled and disabled using proxy-DHCP. The user also reported a full-DHCP menu boot after enabling Secure Boot, but its server trace was not separately identified. One earlier UEFI proxy-DHCP attempt timed out before two successful retries with Secure Boot disabled.

Start with the [Ubuntu installation guide](docs/installation.md). The [architecture](docs/superpowers/specs/2026-10-15-pxe-platform-design.md) and [boot foundation design](docs/superpowers/specs/2026-10-15-boot-foundation-design.md) explain the service boundaries.

For local verification:

```sh
go test ./...
go vet ./...
scripts/fetch-ipxe_test.sh
```
