# PXE Platform

An independent PXE service for Linux servers. It includes a Go HTTP Boot service with a diagnostic iPXE menu, a local Go Network Agent, full DHCP and proxy-DHCP configuration for a dedicated dnsmasq instance, pinned iPXE boot assets, PostgreSQL boot events, and an HTTPS Control dashboard with users and roles.

The project is separate from the neighboring Ventoy/PXE source tree. Images catalogs ISO files across multiple directories. Administrators can add read-only SMB shares in the HTTPS dashboard, retry connections and remove them. Browser uploads, ISO validation, publishing image entries, and Windows/Ubuntu loading are later slices. A test client has reached the menu in BIOS and UEFI with Secure Boot disabled using full DHCP, and in BIOS and UEFI with Secure Boot both enabled and disabled using proxy-DHCP. The user also reported a full-DHCP menu boot after enabling Secure Boot, but its server trace was not separately identified. One earlier UEFI proxy-DHCP attempt timed out before two successful retries with Secure Boot disabled.

Start with the [Ubuntu installation guide](docs/installation.md). The [Control guide](docs/control-installation.md) and [Images guide](docs/images-installation.md) cover later services. The [architecture](docs/superpowers/specs/2026-10-15-pxe-platform-design.md) explains the service boundaries.

For local verification:

```sh
go test ./...
go vet ./...
scripts/fetch-ipxe_test.sh
```
