# PXE Platform: overall design

[Русская версия](../superpowers/specs/2026-10-15-pxe-platform-design.md)

Date: 15 October 2026.

## Goal and scope

Build a standalone PXE service with a web admin interface for a single Linux server without containers. The service must work as its own product, without using the closed server part of iVentoy. Later its processes can be spread over several nodes while keeping the interfaces between them.

The first working version supports two network modes: its own DHCP, and proxy-DHCP next to an existing address server. Target clients: Legacy BIOS and UEFI x64, including a UEFI Secure Boot check. Priority images: Windows 10/11 x64 installers and Ubuntu Desktop x64. Strelec live CD is checked as a separate experimental profile once the user provides a specific ISO.

“Any ISO” is not a goal of the first version. iPXE does not guarantee access to an arbitrary ISO after control passes to the loaded OS. Each supported image type gets a boot profile that can be checked. For Strelec and other WinPE images the result depends on the ISO content, the network drivers and the way additional files are accessed.

## Deployment and service boundaries

Four Go services run as separate processes under systemd. One PostgreSQL instance runs on the same server. Each service that needs a database has its own database and a separate account with no access to its neighbours' tables. Services talk to each other over local APIs; the contract does not depend on the process address, so a service can later move to another node.

| Service | Responsibility | Own state |
| --- | --- | --- |
| Control | Web admin interface, sign-in, roles, settings, action log and menu publishing | PostgreSQL database: users, roles, desired configuration, published versions, audit |
| Images | ISO intake, folder scanning, checks, extraction of boot files and image profiles | PostgreSQL database: catalog and jobs; file storage: source ISO files and prepared files |
| Boot | Generating and serving the iPXE menu, HTTP files and the boot request log | PostgreSQL database: events; a local copy of the last published configuration |
| Network Agent | Checking and applying dnsmasq and Samba configurations, network service state | The local version of the last successfully applied configuration; no persistent database |

Control serves the interface from Go templates and a little client-side JavaScript. Large files are stored on disk, not in PostgreSQL. Images owns the file catalog; Boot gets read-only access to published files. Network Agent is the only process allowed to change system service configuration. Its API is local: at first a local command-line tool calls it, later Control does.

System dependencies: dnsmasq provides DHCP/proxy-DHCP and TFTP, Samba provides the Windows installation files, PostgreSQL stores data. A signed iPXE release and wimboot are used as boot components. Their versions and checksums are pinned at installation; unsigned local iPXE builds are not used for Secure Boot.

## Boot and publishing path

1. An administrator or operator chooses one of several storages: a local directory or an SMB share. Images scans the chosen path, can accept ISO uploads from the browser into the same storage, and prepares an image without having to copy it to the PXE server. Each storage keeps its own connection settings and state. An SMB connection with a username, password and remote folder is managed in the admin interface; the credentials are never sent to PXE clients. Images computes a checksum, recognises the profile and checks the required files before publishing.
2. Images saves the result as a draft: “ready”, “needs setup” or “error”. An unknown ISO can be kept in the catalog, but cannot be served as known to boot.
3. An administrator or operator publishes a ready image manually. Control creates a new full menu version, Boot checks the links and switches to it atomically. On error the previous version keeps working. Files referenced by the current or a previously saved menu version are not deleted.
4. The client gets its network settings from our own DHCP, or from the existing DHCP together with a proxy-DHCP answer. dnsmasq directs a BIOS or UEFI client to the matching iPXE loader and avoids loading iPXE again. Boot serves the menu and files over HTTP.
5. Windows starts through wimboot; WinPE gets the installation files over Samba. Ubuntu Desktop gets a profile with the kernel, initrd and network access to the ISO. A Strelec profile is created only after the provided image is checked.

Boot keeps serving the last published version when Control or PostgreSQL is unavailable. Events that cannot be written to the database go to a local log for diagnostics; boot availability does not depend on writing telemetry. The dashboard distinguishes a confirmed menu request, a file transfer and an image preparation result. Without a separate signal from the loaded OS, it does not claim that an installation succeeded.

## Admin interface and permissions

The dashboard was originally in Russian; since 20.10.2026 each user can switch it between Russian and English. It contains an overview of service and network state, the ISO catalog with upload and publishing, the client request log, network settings, users and the action log. It is designed mainly for a desktop browser and stays usable on a narrow screen.

| Role | Permissions |
| --- | --- |
| Administrator | All actions, including network, users and roles |
| Operator | Import, prepare and publish images; view services and boots |
| Observer | View state, catalog and logs only |

Control checks permissions on the server for every request that changes something. Passwords are stored only as strong hashes; sessions use secure HTTP cookies. The admin interface is served over HTTPS. All changes to the network, publications, images and roles are written to the audit log with the author and time.

## Network modes and fault tolerance

Network Agent does not enable its own DHCP without an explicit choice of interface and address range. A candidate configuration is created first, then dnsmasq/Samba checks run; only after a successful check is the configuration replaced and the service restarted. On failure the agent returns to the previous working version and reports the error to Control. Full DHCP and proxy-DHCP modes are checked on an isolated test network before they are applied to the production network.

If an ISO upload is interrupted, the unfinished file stays a draft and is not published; the upload can be started again. For an external storage, the ISO stays in the chosen directory, and its availability is tracked separately. Losing SMB can interrupt booting an already published image; the dashboard shows the failure, and Boot keeps serving the other menu items. Small prepared boot files can be stored locally.

## Stages and acceptance criteria

1. **Boot foundation.** Boot and Network Agent, both DHCP modes, TFTP, iPXE, a test menu. The menu opens in BIOS, UEFI x64 and UEFI Secure Boot on the test network. Details: [stage specification](../superpowers/specs/2026-10-15-boot-foundation-design.md) (in Russian).
2. **Management.** Control, PostgreSQL, sign-in and three roles, an overview of state and requests, safe application of network settings, manual menu publishing and rollback.
3. **ISO and Ubuntu.** Images, several storages, browser upload and folder import, checks and publishing, booting the provided Ubuntu Desktop ISO in BIOS and UEFI. First an image catalog is built from pre-mounted paths, then SMB storages are managed through the admin interface, then boot profiles are added. Secure Boot is checked separately on the signed content of the image.
4. **Windows 10/11.** wimboot, Samba and profiles for both installers. The installation must reach disk selection with the installation files available in the stated boot modes.
5. **Strelec.** Checking the provided ISO and recording the modes that actually work. The profile is published as supported only for a confirmed combination of image and boot mode.

The first four stages make up the planned first working version; the fifth depends on the provided ISO and the check results. Before the images are provided, the infrastructure, APIs and test menu can be checked, but compatibility of specific installers cannot be confirmed.

## Verification

Each stage needs tests of the service boundaries and critical state transitions, a check of the dnsmasq/Samba configuration, trials on an isolated network in virtual machines with BIOS and UEFI, and then a check on a real PXE client. Secure Boot, restarting Control/PostgreSQL while Boot is running, an interrupted ISO import, a failed network apply and return to the last published menu are checked separately.

## Sources for technical constraints

- [iPXE: chainloading](https://ipxe.org/howto/chainloading) — passing control to iPXE and avoiding boot loops.
- [iPXE: UEFI Secure Boot](https://ipxe.org/secboot) — signed loaders and the limits of a custom build.
- [iPXE: wimboot](https://ipxe.org/wimboot) and [Network-booting Windows PE](https://www.ipxe.org/howto/winpe) — booting Windows PE and accessing the installation files.
- [iVentoy: WinPE](https://www.iventoy.com/en/doc_winpe.html) — limits of arbitrary WinPE ISO files.
- [Ubuntu: network boot](https://ubuntu.com/server/docs/how-to/installation/how-to-netboot-the-server-installer-on-amd64/) — an example of separating boot files and the ISO; the Ubuntu Desktop profile is checked separately.
