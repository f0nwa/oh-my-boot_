# PXE Platform

An independent PXE service for Linux servers. It includes a Go HTTP Boot service with a diagnostic iPXE menu, a local Go Network Agent, full DHCP and proxy-DHCP configuration for a dedicated dnsmasq instance, pinned iPXE boot assets, PostgreSQL boot events, and an HTTPS Control dashboard with users and roles.

The project is separate from the neighboring Ventoy/PXE source tree. Images catalogs ISO files across multiple directories. Administrators can add read-only SMB shares in the HTTPS dashboard, retry connections and remove them. A first Ubuntu Desktop PXE profile serves one selected ISO and adds an optional menu entry. Its Live desktop boot succeeded in an OVMF test VM with 16 GiB RAM, both with Secure Boot disabled and, using Ubuntu shim, enabled. The Windows 10 x64 WinPE profile reached the Windows Setup screen in OVMF with Secure Boot both off and on; installation source access over SMB is the next check. Browser uploads, automated ISO validation, and general image publishing remain later slices. A test client has reached the menu in BIOS and UEFI with Secure Boot disabled using full DHCP, and in BIOS and UEFI with Secure Boot both enabled and disabled using proxy-DHCP. The user also reported a full-DHCP menu boot after enabling Secure Boot, but its server trace was not separately identified. One earlier UEFI proxy-DHCP attempt timed out before two successful retries with Secure Boot disabled.

Start with the [Ubuntu installation guide](docs/installation.md). The [Control guide](docs/control-installation.md), [Images guide](docs/images-installation.md), [Ubuntu Desktop PXE guide](docs/ubuntu-desktop-pxe.md), and [Windows WinPE guide](docs/windows-winpe-pxe.md) cover later services. The [architecture](docs/superpowers/specs/2026-10-15-pxe-platform-design.md) explains the service boundaries.

For local verification:

```sh
go test ./...
go vet ./...
scripts/fetch-ipxe_test.sh
```
