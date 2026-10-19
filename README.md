# PXE Platform

PXE Platform serves a network boot menu for BIOS and UEFI clients from an isolated Linux server. It supports full DHCP and proxy-DHCP, signed iPXE boot files for UEFI Secure Boot, boot events, and an HTTPS administration page with users and roles.

The Images service catalogs ISO files on multiple storage locations. Administrators can connect SMB shares, upload ISO files into an existing folder, prepare supported Windows x64 profiles, and publish selected profiles in a folder-based PXE menu. One Windows ISO can have several independent `autounattend.xml` scenarios alongside its manual installer entry. Ubuntu Desktop has a separate live-boot profile. Source ISOs remain on their storage; Windows clients receive WinPE startup files over HTTP and installation media over a read-only SMB share.

The test VMs have reached the Ubuntu Desktop with SeaBIOS and with UEFI Secure Boot on and off, and Windows 10 and Windows 11 installers with visible disks in UEFI. The Windows 10 ISO profile also reached its installer and visible disk in SeaBIOS. A Windows 11 profile reached Setup in SeaBIOS, where Windows 11's UEFI and TPM requirements prevented installation on that test VM. A Windows 11 installation using a user-supplied answer file completed and booted from disk. A newly uploaded Windows 11 26H2 ISO also reached its installer and disk selection screen. A physical PXE client remains to be checked. Strelec support depends on examining a supplied ISO. Device-driver catalog work is deferred.

Start with the [server installation guide](docs/installation.md). The [Control guide](docs/control-installation.md), [Images guide](docs/images-installation.md), [Ubuntu Desktop PXE guide](docs/ubuntu-desktop-pxe.md), [Windows 10 WinPE guide](docs/windows-winpe-pxe.md), and [Windows 11 WinPE guide](docs/windows11-winpe-pxe.md) cover the services and observed client results. The [architecture](docs/superpowers/specs/2026-10-15-pxe-platform-design.md) explains the service boundaries and acceptance plan.

For local verification:

```sh
go test ./...
go vet ./...
scripts/fetch-ipxe_test.sh
```
