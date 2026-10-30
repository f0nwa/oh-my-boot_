<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/brand/logo/omb-logo-dark.svg">
    <img src="docs/brand/logo/omb-logo-light.svg" alt="Oh My Boot" width="420">
  </picture>
</p>

# Oh My Boot

**Network boot, OS deployment, and machine maintenance.**

Oh My Boot (OMB) serves a network boot menu for BIOS and UEFI machines from an isolated Linux server and manages what that menu offers: Windows and Linux installers, answer files, WinPE drivers, and a log of every machine that booted. It works with full DHCP or alongside an existing DHCP server (proxy-DHCP), boots with UEFI Secure Boot on through signed iPXE files, and is administered from an HTTPS page with users and roles.

<p align="center"><img src="docs/brand/stage-3/1-text-title.png" alt="The Oh My Boot boot menu in iPXE on a test VM" width="560"></p>

## What works

- **Boot menu** for BIOS and UEFI clients, with folders, a draft that clients do not see until it is published, version history and rollback.
- **Images:** an ISO catalog across local folders and SMB shares, upload from the browser, prepared Windows x64 profiles. Source ISOs stay on their storage; Windows clients receive WinPE startup files over HTTP and installation media over a read-only SMB share.
- **Windows installs:** several independent `autounattend.xml` scenarios per ISO next to the manual installer, an answer file editor in the admin page, and WinPE driver packages imported from a folder or ZIP and built into separate menu entries.
- **Unified installations** for Windows and Ubuntu Desktop: multiple independent choices can share a boot profile, with their own names, menu folders and selection counts.
- **Machines:** every machine that reached the menu, with its model and maker, editable names, CSV import and export, and MAC-prefix rules.
- **Secure Boot:** a signed iPXE chain, a hint when firmware rejects the loader, an SCCM script and a WinPE USB stick (**Library → USB media**) that turn on the Microsoft UEFI CA key on Lenovo, Dell and HP laptops ([Secure Boot guide](docs/en/secure-boot.md)).
- **Admin page** in Russian and English, with a light, dark or system theme chosen by each user.

## Tested so far

Test VMs reached the Ubuntu Desktop with SeaBIOS and with UEFI Secure Boot on and off, and the Windows 10 and Windows 11 installers with visible disks in UEFI. The Windows 10 profile also reached its installer and disk in SeaBIOS; a Windows 11 profile reached Setup in SeaBIOS, where Windows 11's UEFI and TPM requirements stopped installation on that VM. A Windows 11 installation with a user-supplied answer file completed and booted from disk, and a newly uploaded Windows 11 26H2 ISO reached disk selection. A Windows 11 entry with VirtIO network and SCSI drivers reached Setup with both disks visible in UEFI and, in SeaBIOS, WinPE with network, both disks and the mounted installer. Physical PXE clients and driver entries on real laptops remain to be checked. These firmware results predate the unified-installations build; that build has local PostgreSQL and HTTP integration coverage, with fresh stand boots still required. Strelec support depends on examining a supplied ISO.

For existing test stands, follow the backup, one-time import, fresh draft publication and rollback procedure in [Control installation](docs/en/control-installation.md#unified-windows-and-ubuntu-installations-transition-on-30102026). The import preserves Windows library objects and discards old menu drafts, versions and their choice counts. Empty databases use only the new schema.

## Guides

Start with the [server installation guide](docs/installation.md) ([offline installation](docs/en/offline-installation.md)) and the [first VM test report](docs/vm-test-2026-10-15.md). The other guides are available in English and Russian:

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
| Brand: mark, files and usage rules | — | [RU](docs/brand/README.md) |

Implementation plans and stage specifications in `docs/superpowers` are kept in Russian.

## Development

For local verification:

```sh
go test ./...
go vet ./...
scripts/fetch-ipxe_test.sh
```

## Names

The product was called PXE Platform until 29 October 2026. Component, service and path names (`pxe-control`, `pxe-agent`, `/etc/pxe-platform`, …) still use the `pxe-` prefix; whether to rename them is an open question in the [branding roadmap](docs/roadmaps/2026-10-28-oh-my-boot-branding.md). Historical plans and test reports keep the old name.

## License

The code is released under the [MIT License](LICENSE). The Oh My Boot mark and its files are described in [docs/brand](docs/brand/README.md).
