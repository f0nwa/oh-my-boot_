# PXE Platform

PXE Platform serves a network boot menu for BIOS and UEFI clients from an isolated Linux server. It supports full DHCP and proxy-DHCP, signed iPXE boot files for UEFI Secure Boot, boot events, and an HTTPS administration page with users and roles.

The Images service catalogs ISO files on multiple storage locations. Administrators can connect SMB shares, upload ISO files into an existing folder, prepare supported Windows x64 profiles, and publish selected profiles in a folder-based PXE menu. One Windows ISO can have several independent `autounattend.xml` scenarios alongside its manual installer entry. Ubuntu Desktop has a separate live-boot profile. Source ISOs remain on their storage; Windows clients receive WinPE startup files over HTTP and installation media over a read-only SMB share.

The test VMs have reached the Ubuntu Desktop with SeaBIOS and with UEFI Secure Boot on and off, and Windows 10 and Windows 11 installers with visible disks in UEFI. The Windows 10 ISO profile also reached its installer and visible disk in SeaBIOS. A Windows 11 profile reached Setup in SeaBIOS, where Windows 11's UEFI and TPM requirements prevented installation on that test VM. A Windows 11 installation using a user-supplied answer file completed and booted from disk. A newly uploaded Windows 11 26H2 ISO also reached its installer and disk selection screen. A physical PXE client remains to be checked. Strelec support depends on examining a supplied ISO. Administrators can import WinPE driver packages from a folder or ZIP and build separate Windows menu entries with selected INF files; a Windows 11 entry with VirtIO network and SCSI drivers reached Setup with both disks visible in UEFI. For laptops outside the domain whose BIOS distrusts third-party loaders, the admin page (**Library → USB media**) builds a WinPE USB stick that turns on the Microsoft UEFI CA key in the BIOS of Lenovo, Dell and HP machines after confirmation ([Secure Boot guide](docs/en/secure-boot.md#usb-stick-for-machines-outside-the-domain)). Unused Windows profiles, answer scenarios and driver configurations can be deleted from the admin page while the source ISO stays on its storage. The same driver entry also reached WinPE with network, both disks and the mounted installer in SeaBIOS, where Windows 11 Setup then rejected the VM on its own hardware requirements. Driver entries have not yet been checked on real laptop hardware. The admin interface is available in Russian and English; each user chooses the language and a light, dark or system theme, and changes their own password in **My account**.

Start with the [server installation guide](docs/installation.md) and the [first VM test report](docs/vm-test-2026-10-15.md), both in English. The other guides are available in English and Russian:

| Guide | English | Русский |
| --- | --- | --- |
| Control and admin interface | [EN](docs/en/control-installation.md) | [RU](docs/control-installation.md) |
| Answer file editor | [EN](docs/en/answer-editor.md) | [RU](docs/answer-editor.md) |
| Images, ISO catalog and Windows profiles | [EN](docs/en/images-installation.md) | [RU](docs/images-installation.md) |
| Secure Boot: how the boot chain works | [EN](docs/en/secure-boot.md) | [RU](docs/secure-boot.md) |
| Network boot paths: iPXE, GRUB, Windows | — | [RU, with diagrams](docs/secure-boot-boot-paths.md) |
| Secure Boot: preparing domain laptops with SCCM | [EN](docs/en/secure-boot-sccm.md) | [RU](docs/secure-boot-sccm.md) |
| Ubuntu Desktop PXE | [EN](docs/en/ubuntu-desktop-pxe.md) | [RU](docs/ubuntu-desktop-pxe.md) |
| Windows 10 WinPE | [EN](docs/en/windows-winpe-pxe.md) | [RU](docs/windows-winpe-pxe.md) |
| Windows 11 WinPE | [EN](docs/en/windows11-winpe-pxe.md) | [RU](docs/windows11-winpe-pxe.md) |
| WinPE driver test report | [EN](docs/en/winpe-driver-test-2026-10-19.md) | [RU](docs/winpe-driver-test-2026-10-19.md) |
| Architecture and acceptance plan | [EN](docs/en/pxe-platform-design.md) | [RU](docs/superpowers/specs/2026-10-15-pxe-platform-design.md) |

Implementation plans and stage specifications in `docs/superpowers` are kept in Russian.

For local verification:

```sh
go test ./...
go vet ./...
scripts/fetch-ipxe_test.sh
```
