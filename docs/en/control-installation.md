# Control and the HTTPS admin interface on the test VM

[Русская версия](../control-installation.md)

Address: **https://192.168.10.135:8443**. The certificate is self-signed with the IP address in the SAN, so the browser asks you to trust it. The lab certificate is valid for 90 days.

The admin interface is available in Russian and English. Each user chooses the language in **My account**; this guide uses the English labels.

## Services

`pxe-control` is a separate Go process with its own PostgreSQL database, `pxe_control`. The browser talks only to it. Control calls Boot and Agent over local Unix sockets. When Control is stopped, `pxe-boot` keeps serving the last published menu on `10.30.0.10:8080`, and `pxe-agent` keeps proxy-DHCP running on `ens19`. In the lab, the list of interfaces the dashboard may change contains only `ens19`.

## Boot menu folders

On the **Menu** page, the **PXE menu folders** block sets a path for every prepared profile. For example, `Windows/Installers` creates two nested levels. An empty field keeps the item in the main menu. Folders are built from the selected items and do not depend on where the ISO files are on the SMB share. **Publish** applies the new structure as a whole; the previous version can be restored from the version list. ISO files that were found but not prepared are not added to the menu.

New Windows ISO profiles are created on the **Images** page. After preparation, each one can be selected on the **Menu** page and placed in a folder. A menu version stores the selected IDs, so restoring an older version restores the same set of items. Before publishing or restoring, Control checks that the source ISO and the prepared files are available. The old static Windows 10/11 items are kept during the transition.

On 16.10.2026 a new Control binary was installed on the test VM; HTTPS responded and the service was active. The user published the `Linux` and `Windows` folders and confirmed moving into `Windows` and back. The original `Назад` (Back) label was not shown on the PXE screen because the item was an empty string. The generator was changed so that `.. Back` is the first item of every folder. After publishing again, the user confirmed that this item is visible and returns to the main menu. The previous binaries are kept in `/usr/local/bin/pxe-control.pre-menu-folders`, `/usr/local/bin/pxe-control.pre-back-first` and `/usr/local/bin/pxe-control.pre-ascii-back`.

## Installation

Build Linux x86_64 versions of `./cmd/pxe-boot`, `./cmd/pxe-agent`, `./cmd/pxe-control` and `./cmd/pxe-controlctl` with `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build`. Install the binaries in `/usr/local/bin` and the [units](../../deploy/systemd) in `/etc/systemd/system`. Before replacing Boot and Agent, keep their current binaries and units for rollback.

Create the `pxe-control` system group and user, a PostgreSQL role with the same name and the `pxe_control` database. Apply the [Control schema](../../deploy/sql/control.sql) as `pxe-control`. The `pxe_boot` database stays separate.

Settings in `/etc/pxe-platform/control.env`:

```ini
PXE_CONTROL_LISTEN=192.168.10.135:8443
PXE_CONTROL_INTERFACES=ens19
PXE_CONTROL_DATABASE_URL=host=/var/run/postgresql dbname=pxe_control user=pxe-control
```

The certificate `control.crt` and key `control.key` are in `/var/lib/pxe-platform-control`; the directory belongs to `pxe-control` with mode `0700`, and the key has mode `0600`. For the lab, the certificate was created with `subjectAltName=IP:192.168.10.135`. After installation, run `systemctl daemon-reload`, restart Boot and Agent, then enable Control. **Installing the dashboard does not change the DHCP mode or the Keenetic settings.**

The first administrator was created with the local command `pxe-controlctl create-admin -username fonwa`, which reads the password from stdin. There is no default password. If access is lost, `pxe-controlctl reset-password -username fonwa` reads a new password from stdin and ends the existing sessions. Both commands need `PXE_CONTROL_DATABASE_URL` and run as the `pxe-control` system user.

## Checks on 15.10.2026

| Check | Result |
| --- | --- |
| HTTPS sign-in and all dashboard pages | 200 |
| POST without CSRF | 403 |
| Attempt to select `ens18` | 400 |
| Preview of `ens19` | 200 |
| Reapplying the current proxy-DHCP settings | Succeeded; the dnsmasq file did not change |
| Operator: menu allowed, network and users denied | Verified |
| Observer: log allowed, publishing denied | Verified |
| Disabling a user | The active session stopped working |
| Password change in the dashboard and with the local command | Verified; the previous session ended |
| Publishing a test menu and rolling back | The original menu SHA-256 was restored |
| Boot with Control and PostgreSQL 18-main stopped | The HTTP menu returned 200; the services were restored |
| Boot, Agent, dnsmasq, Control after the checks | All active |

`rolecheck-operator` and `rolecheck-observer` were created and then disabled for the role checks. Their passwords were not stored. The audit log contains entries for publishing, rollback, network apply and user changes. A new PXE client boot after installing Control has not been run yet; the earlier BIOS/UEFI/Secure Boot checks are described in the [first-stage report](../vm-test-2026-10-15.md).

## Resilience recheck on 19.10.2026

On the live server, Control and then PostgreSQL 18/main were stopped briefly. In both cases Boot stayed active and served the menu with the same SHA-256 `43459a54e0d3dc7a8361d731062653a94293ffcadc90fce0fe3ff35f52ddabf9`. After a restart, Boot served the same menu snapshot; its checksum matched active version 20 in the Control database. Control and PostgreSQL were restored; HTTPS sign-in returns `200`, and all five PXE services are active.

SMB failure was checked with isolated tests: when the connection disappears, the ISO catalog is kept and the storage becomes `unavailable`. Boot tests confirmed that the active menu is kept when publishing fails and after the snapshot is reloaded. The live SMB share was not disconnected at that time, because published Windows profiles use it for installation files. An end-to-end SMB failure test on the server was done on 20.10.2026; see below.

## WinPE driver catalog

The **Drivers** page accepts a folder with driver files or a ZIP. The package must contain the INF and every file it refers to. After import, the administrator creates a configuration: a prepared Windows ISO, optionally an existing XML answer file, the menu item title and the required INF files. **Prepare** builds a separate `boot.wim` and startup script. The configuration appears in the **Menu** page form, but reaches PXE only after it is selected and **Publish** is pressed. One ISO can have several such items; the manual item stays separate.

The record number and the selected INF files are shown on the **Menu** page so that items with the same title can be told apart. An administrator can delete an unused configuration on the **Drivers** page, and an XML answer file or Windows profile on the **Images** page. The record must first be removed from the published menu; a profile also cannot be deleted while answer files or configurations depend on it. The prepared files are deleted together with the record. A shared WIM is kept until the last configuration that uses it is deleted; imported drivers and the source ISO on SMB stay. Old menu versions with deleted items can be viewed but not restored.

The first release accepts uploads up to 256 MiB, up to 512 MiB of files after extraction, up to 4096 files and up to 128 selected INF files. INF paths, which WinPE runs from a batch file, must use Latin letters and other ASCII characters. Drivers are loaded only in WinPE; they are not added to the installed Windows. Package files and prepared WIM files are kept for earlier menu versions; automatic cleanup is not enabled yet.

To deploy, install the `wimtools` package, apply the [migration](../../deploy/sql/2026-10-19-winpe-drivers.sql) to the `pxe_control` database as `pxe-control`, create `/srv/pxe/tftp/windows-driver-wims` and `/srv/pxe/tftp/windows-driver-configs`, then update Agent and Control together with their systemd units. The source WIM files under `/srv/pxe/tftp/windows-profiles` are not modified. Before publishing, Control checks the ISO, XML, driver package, derived WIM and startup script again.

On 19.10.2026 `wimtools` 1.14.5 and the new Agent and Control were installed on the test server. The previous binaries and units are kept in `/var/backups/pxe-platform-winpe-drivers-20261019/`. The migration created both tables. Agent, Control, Boot and dnsmasq are active; HTTPS redirects a guest to the sign-in page. Right after deployment the menu kept the SHA-256 `43459a54e0d3dc7a8361d731062653a94293ffcadc90fce0fe3ff35f52ddabf9`; later the user published a test configuration. Importing NetKVM and booting WinPE with VirtIO networking on VM 113 succeeded with OVMF Secure Boot Enabled; see the [test report](winpe-driver-test-2026-10-19.md).

Deleting Windows profiles, XML answer files and driver configurations requires a single running Control instance: the publish, prepare and delete locks live in the process memory. When updating the service, stop the old process completely before starting the new one. Before cleanup, Control compares the saved active version with the menu that Boot actually serves. The source ISO and imported driver packages stay in place.

## Diagnostics and rollback

```sh
sudo systemctl status pxe-boot pxe-agent pxe-dnsmasq pxe-control
sudo journalctl -u pxe-control -u pxe-boot -u pxe-agent -n 100 --no-pager
curl --cacert /var/lib/pxe-platform-control/control.crt https://192.168.10.135:8443/login
curl -fsS http://10.30.0.10:8080/boot/menu.ipxe
```

If the dashboard fails, stop only `pxe-control`: Boot and proxy-DHCP keep working. For a full rollback on the test VM, the previous Boot/Agent binaries and units are in `/var/backups/pxe-platform-pre-control/`. Restore them, run `systemctl daemon-reload` and restart Boot and Agent. Do not edit `/etc/pxe-platform/dnsmasq.conf` while doing this.

## Version numbers and time zone, 19.10.2026

The **Menu → Versions** table shows the number of every version. Times on the overview, request log, versions and audit pages are shown with the server offset from UTC, for example `19.10.2026 23:20 UTC+03:00`. Control shows time in the server system time zone, so the test server was set to `Europe/Moscow` (`timedatectl set-timezone Europe/Moscow`) and only `pxe-control` was restarted. The previous binary and time zone (`Etc/UTC`) are kept in `/var/backups/pxe-platform-version-numbers-20261019/`. All five services are active, HTTPS sign-in returns `200`, and the active menu checksum did not change.

**Restore** does not reactivate the old record; it creates a new one with the same content. Restoring version 21 created active version 25 with the same SHA-256 `677c9eb1617d…`.

After the time zone change, restoring a version with Windows profiles was rejected with “Windows ISO profile is unavailable: files of profile 1 are unavailable”. Control sent the ISO modification time with the `+03:00` offset, while Agent compared the request byte by byte with `/etc/pxe-platform/winiso/<id>.json`, where the same moment is stored in UTC (`Z`). Agent now compares the stored configuration field by field and the time as an instant; new configurations are written in UTC. After Agent was replaced, the check of profiles 1–3 with `+03:00` times returned `204` instead of `503`. The previous Agent is kept in `/var/backups/pxe-platform-version-numbers-20261019/pxe-agent`; the active menu did not change.

## Source SMB storage failure, 20.10.2026

At 00:34, outgoing TCP 445 from the PXE server to the NAS `192.168.10.10` was blocked temporarily with a separate nftables table, `inet pxe_smb_test`, set to be removed automatically after 20 minutes; the NAS and the PXE server Samba were not changed. At 00:37 the kernel reported `\\192.168.10.10 has not responded in 180 seconds. Reconnecting...`; reading and listing the CIFS mount (`soft`) returned `Host is down` instead of hanging.

- Boot kept serving the menu with the same SHA-256 and the local files (`wimboot`, profile `boot.wim` files, XML, the Ubuntu kernel and initrd) with HTTP 200. All five PXE services and `smbd` stayed active, and HTTPS sign-in responded.
- The dashboard did not show the failure by itself: the storage state changed only during a scan. After **Refresh**, storage 6 became “Unavailable · lstat …: host is down”.
- Publishing the menu was rejected with “Windows ISO profile is unavailable: the source ISO of profile 1 has changed”; active version 28 stayed active. The message was inaccurate: the ISO had not changed, it was unavailable.
- Ubuntu loaded its kernel and initrd, but could not get the ISO and did not start.
- VM 113 loaded WinPE for the “Windows 11 key no-disk test” item, and Setup reached disk selection without errors. Samba served the Setup files from the mounted ISO of profile 2 at that time, probably from cache after the 00:26 boot. An installation that has to read `install.wim` was not tested during this failure. The check of reading the profile ISO files during the failure was invalid (`iflag=direct` on UDF returns EINVAL), so the behaviour of uncached reads through the loop device was not confirmed.
- At 00:46 the block was removed. CIFS reconnected without restarting services; the Ubuntu ISO was served again, normal reads of `install.wim` of all three profiles at new offsets succeeded, and Agent returned `204` for profiles 1–3. After **Refresh** the storage became ready again, and publishing again created version 29 with the same SHA-256 `b47d47cb…`.

Possible improvements at that time: a background storage availability check and a separate “storage unavailable” message instead of “ISO has changed”. Both were added the same night; see below.

## Background storage check and errors on pages, 20.10.2026

- Images checks every storage in the ready or unavailable state once a minute: SMB and mounted paths must be mounted and the root must be readable; the check gets 20 seconds. A ready storage that fails the check becomes unavailable with the reason (“Storage is unavailable: not responding for more than 20s” or the system error). An unavailable storage that passes the check again is scanned automatically: the scan refreshes the catalog and makes it ready. Automatic rescans of one storage run at most once every 10 minutes. Storages that are being scanned are not checked, and a hung check is not started again. The interval is set with `pxe-images -storage-check-interval` (default `1m`; `0` disables the check).
- Publishing and restoring the menu, and preparing a Windows profile, report the cause separately: “storage “…” is unavailable: <reason>”, “storage “…” is being scanned, try again later”, “the source ISO of profile N was not found in storage “…”” and “the source ISO of profile N has changed”. The last one is used only when the size or date changed.
- Form errors on the **Menu**, **Images** and **Drivers** pages are shown as a red notice on the same page instead of a separate text page; the response status code is kept. ISO upload and driver import still report errors inside their forms.

Check on the server: outgoing TCP 445 to the NAS was blocked at 00:56:55. Without pressing **Refresh**, the “DSM” storage became “Unavailable · Storage is unavailable: not responding for more than 20s” at 00:58:06. Publishing the menu showed “Windows ISO profile is unavailable: storage “DSM” is unavailable: lstat …: host is down”. The block was removed at 01:02:56; at 01:03:41 Images detected the storage again and started a scan, and at 01:03:47 the storage was ready again. The previous Images and Control binaries are kept in `/var/backups/pxe-platform-storage-check-20261020/`.

## Admin improvements and interface language, 20.10.2026

- If publishing the menu fails, the **Menu** page shows the error together with the submitted checkboxes, folders and texts instead of the active menu.
- Uploading the same XML (by SHA-256) again for the same ISO is rejected, and the message names the existing answer file. The same XML for another ISO is allowed.
- Rejected deletion attempts are written to the audit log with the result `rejected` and a reason: `published`, `used by answer N` or `used by driver config N`.
- The admin interface is available in Russian and English. The language is chosen in the user account page (see below) and stored in the user account (`users.language`, default `ru`). The sign-in page picks the language from the browser `Accept-Language` header. Pages, buttons, notices, form errors, ISO and driver upload messages and known messages of the Images service are translated. User data is not translated; PXE menu item titles still use Latin letters only, because the iPXE console cannot show Cyrillic. The dictionary is in `internal/control/i18n.go`; tests require a translation for every Russian Control string that can reach the browser. The guides are available in Russian and, in `docs/en`, in English.
- Migration: `ALTER TABLE users ADD COLUMN IF NOT EXISTS language TEXT NOT NULL DEFAULT 'ru' CHECK (language IN ('ru','en'))` (added to `deploy/sql/control.sql`). Apply it before starting the new Control. Before the update, the Control binary and the `users` table schema were saved to `/var/backups/pxe-platform-i18n-20261020/`.

## My account and password generation, 20.10.2026

- The username in the page header opens **My account** (`/account`), available to every role. It shows the username, the role and what the role allows, lets the user choose the interface language and change their own password. Changing the password requires the current password; the new one must have at least 12 characters. After the change, sessions on other devices are ended and the current one is kept. The change is recorded in the audit log as `account.password` with the result `ok` or `error` (for example, `wrong current password`). The quick RU / EN switch was removed from the header.
- Every new password field — when creating a user, when an administrator changes a password, and in **My account** — has a **Generate** button. A 20-character password of letters and digits without easily confused characters is created in the browser (`crypto.getRandomValues`) and is not sent to the server separately; the field becomes visible, and **Copy** copies the password. The script is served as `/assets/password.js`.
- No database migration is needed. The previous Control binary is kept in `/var/backups/pxe-platform-account-20261020/`. All services are active, and the active menu did not change.

## Failure after a reboot and automatic connection recovery, 20.10.2026

At 10:57 the PXE server rebooted before the NAS (VM 110 on the same Proxmox host) was reachable. The SMB mount units failed with `cifs_mount failed w/return code = -113`; systemd does not retry mount units, so all `pxe-winiso@N` units stopped after them, and `pxe-windows11-iso` restarted every 10 seconds. Publishing the menu was rejected during this time because the profile ISO files were not mounted. Boot kept serving the menu. At 12:07 the share and profiles were started manually; Images detected the storage by itself and made it ready again at 12:08.

Agent now recovers such failures by itself. On a schedule it restarts inactive SMB mount units that have stored credentials, and, once the mounts succeed, inactive units of prepared profiles `pxe-winiso@N`. While a storage is unavailable, profiles are left alone. Recovery uses the same locks as connecting and deleting, so it does not collide with actions in the admin interface. Every action is written to the Agent log (`Storage recovery: restarted …`).

The setting is stored by Agent in `/etc/pxe-platform/storage-recovery.json` (flag `pxe-agent -storage-recovery-config`), so recovery works right after boot, before Control and PostgreSQL start. Recovery is enabled by default with a 1-minute interval. On the **Images** page, the **Automatic connection recovery** block shows the state, the time of the last check, the actions taken and the last error. An administrator enables or disables recovery, sets the interval from 1 to 60 minutes and can press **Check now**. Changes and manual checks are recorded in the audit log as `storage-recovery.update` and `storage-recovery.run`.

Check on the server: after Agent was installed, the first check changed nothing. The `pxe-winiso@3` unit was stopped at 12:14:38; at 12:15:26 Agent restarted it (`restarted pxe-winiso@3.service`), the ISO was mounted again, and the profile check returned `204`. The active menu did not change. The previous Agent and Control are kept in `/var/backups/pxe-platform-storage-recovery-20261020/`.

## Removal of the old Windows 10/11 items, 20.10.2026

The static “Windows 10 installer - WinPE” and “Windows 11 installer - WinPE” items were removed from the **Menu** form and unpublished together with XML answer file 5 (active version 32, SHA-256 `7ecede91…`). On the server, their full ISO copies `/srv/pxe/installers/windows10` (6.9 GB) and `/srv/pxe/installers/windows11` (9.0 GB), the separate `boot.wim` files in `/srv/pxe/tftp/windows10` and `/srv/pxe/tftp/windows11`, the Samba shares `windows10`, `windows11` and `windows11iso`, and the `pxe-windows11-iso.service` unit with its settings were deleted. `wimboot` in `/srv/pxe/tftp/windows` was kept. Free space on the system disk grew from 5.9 to 25 GB. Before deletion it was checked that the active menu, profiles, answer files and driver configurations do not refer to these files; after deletion all 22 menu URLs return 200, Agent accepts profiles 1–3, and the `pxeisos` share serves the files of all profiles. The Samba configuration, unit and service settings are kept in `/var/backups/pxe-platform-legacy-cleanup-20261020/`. Old menu versions with the static Windows items no longer boot those items.

Then the repository lost the static item generation in Control, the Boot rule for `windows11/pxe-setup.cmd`, the `pxe-winpe-render` tool, the `deploy/boot-assets/windows11/` templates and the `pxe-windows11-iso.service` unit; the menu folder tests now use Ubuntu and ISO profiles. On the server, `pxe-winpe-render`, five old binary copies `/usr/local/bin/*.pre-*`, the `/usr/share/pxe-platform/windows11` template, the `pxe-install` password and user (Samba and system), the `virtio-win-0.1.271` driver ISO in `/srv/pxe/staging`, empty mount points from the first checks, old copies of `boot.env` and `smb.conf`, and backups made before 20.10.2026 were deleted — another 1.3 GB. The backups from 20.10.2026 were kept. Boot and Control were updated; all services are active, the menu and all its URLs work, and the profile startup script is still unavailable from the management network (`404`). 26 GB of 39 GB is free.

## New interface: sidebar and installations, 20.10.2026

The admin interface is now built around the usual path "image → drivers → answer file → installation → menu" ([design, Russian](../superpowers/specs/2026-10-20-admin-ui-redesign-design.md)). The sidebar holds Installations, Boot menu, Library (ISO images, drivers, answer files) and System (storages, network, users, log, audit). Items the role cannot open are hidden.

- **Installations** (`/`) list every ISO profile, XML answer file and driver configuration as an installation with a status: In menu, Will be added, Will be removed, Not in menu, Needs build, Building, Build error or Unavailable. **New installation** (`/installations/new`) is one form: image, driver packages (all x64 INF files of a package by default, individual INF files behind a link), an answer file of that image, menu title and folder. With drivers it creates a configuration that has to be built.
- **Boot menu** changes collect in a draft (`menu_draft`, `menu_draft_items`) and reach clients only through "Publish version N". Order changes with ↑/↓; title and folder under "Edit". Restoring a version rewrites the draft to match it. Until a draft is saved it equals the active version, so nothing shows as pending after an upgrade.
- **Answer files** carry a "wipes the disk", "leaves disks alone" or "not checked" label. It is set on upload; files uploaded earlier get it through "Check", re-uploading the same file (SHA-256 must match).
- **Storages** now hold the SMB, scan and automatic recovery controls moved from the ISO images page.

Rollout 20.10.2026: applied [2026-10-20-installations.sql](../../deploy/sql/2026-10-20-installations.sql) (column `windows_answer_variants.wipes_disk` and the draft tables), replaced only `pxe-control`. Binary backup, a dump of `menu_versions` and `windows_answer_variants` and the menu SHA-256 before the rollout are in `/var/backups/pxe-platform-ui-20261020/`. The client menu SHA-256 is the same before and after: `7ecede91ac4d5e8464d0e3c1f27f492b27557efb90a8c089e57ad2f3c010c8ae`. Reading active version v32 gives a draft of Ubuntu (folder `Linux`) and seven Windows items with their folders and titles.

Rollback: restore `/var/backups/pxe-platform-ui-20261020/pxe-control` to `/usr/local/bin/pxe-control` and restart `pxe-control`. The new tables and column do not affect the previous version.

## Log: machines, MAC addresses and DHCP, 20.10.2026

The Log page has Machines, HTTP requests and DHCP and TFTP tabs. Boot records the client MAC address from the server neighbor table (`/proc/net/arp`) with each HTTP request; `boot_events` gained a `mac` column ([migration](../../deploy/sql/2026-10-20-boot-mac.sql), applied to `pxe_boot` as `pxe-boot`). Agent reads the `pxe-dnsmasq` journal for the last 7 days on request (`GET /dhcp-events`): PXE/DHCP answers with MAC addresses and TFTP boot-loader transfers. TFTP lines get the MAC of the nearest HTTP request or DHCP answer with the same IP within 30 minutes. Machines sums everything up per MAC: last IP, firmware from the loader name (UEFI/BIOS), last stage and chosen installation. Requests made before the upgrade carry no MAC. Boot, Agent and Control binaries were backed up to `/var/backups/pxe-platform-machines-20261020/`.

## Dark theme, 20.10.2026

The **Appearance** section of My account chooses the admin theme: **Same as system** (default; follows the browser and OS `prefers-color-scheme` setting), **Light** or **Dark**. The choice is stored in the user account (`users.theme`) and applies on all of the user's devices. The sign-in page always follows the system theme. Before starting the new Control, apply the [migration](../../deploy/sql/2026-10-20-theme.sql) to the `pxe_control` database as `pxe-control` (the column is also added to `deploy/sql/control.sql`).

Rollout 20.10.2026: applied `2026-10-20-theme.sql` (every user has `theme = system`), replaced only `pxe-control` (SHA-256 `4a81221b…`). The previous binary and the `users` table schema were backed up to `/var/backups/pxe-platform-theme-20261020/`.

## “Console” interface, 20.10.2026

The interface language is described in [DESIGN.md](../../DESIGN.md) (in Russian). The sidebar is replaced by a header with the sections **Installations**, **Menu**, **Library** (tabs **ISO images**, **Drivers**, **Answer files**), **Log** and **System** (tabs **Overview**, **Storages**, **Network**, **Users**, **Audit**, **My account**). While the menu draft differs from the published menu, every page except **Menu** shows a draft bar under the header with the number of added, removed and changed items and a publish button (observers see it without the button). The Geist and Geist Mono fonts (OFL licence) are embedded in the binary and served from `/assets/fonts/`; the page makes no external requests.

In **Log**, a MAC address opens the machine page `/events/machines/{mac}`: the outcome of the latest boot in plain words, the DHCP → TFTP → iPXE → menu → image steps from the dnsmasq log and Boot requests, and a list of earlier boots (a new boot starts after a pause longer than 10 minutes). A TFTP line names only an IP and a proxy-DHCP answer only a MAC; when exactly one machine got a PXE answer within 15 seconds before a TFTP transfer, the transfer is attributed to it (also in the **Machines** table). **System → Overview** (`/system`) shows the state of services, the PXE network, storages and users (users only for administrators). No migrations.

Rollout 20.10.2026, first stage (header, draft bar, tokens, fonts): replaced only `pxe-control` (SHA-256 `f5258490…`). The previous binary was backed up to `/var/backups/pxe-platform-pult-20261020/`. The client menu SHA-256 is the same before and after (`7ecede91…`).

Rollout 21.10.2026, second stage (machine page, system overview, TFTP linking by time): replaced only `pxe-control` (SHA-256 `9ab22c1d…`). To roll back to the state before the new interface, copy `/var/backups/pxe-platform-pult-20261020/pxe-control` to `/usr/local/bin/pxe-control` and restart `pxe-control`.

Third stage of the new interface, 21.10.2026: the interface now follows the mockup.

- **Now** (`/`) is the home page: a strip of services and storages, the machines of the last day with their DHCP → TFTP → iPXE → menu → choice → image steps (the page refreshes itself every 15 seconds while open), the menu draft with a publish button, **Needs attention** (unavailable storages and images, WinPE builds) and, for administrators, recent audit entries. **Installations** moved to `/installations`.
- **Menu** has three columns: the structure (folders and items; drag by ⋮⋮ within one screen), the machine screen and the selected item (title, folder, up/down, remove). The screen is built from the same iPXE script machines get after publishing, so lines, folders and `.. Back` match the real ones. When the draft does not build into a menu, the reason replaces the screen and the structure becomes a flat list with unavailable items marked. The draft bar now shows on this page too; changes and versions are collapsed below.
- **Installations** show their contents as chips (ISO, drivers, answers; a disk-wiping file is highlighted), the folder, filters **In menu**, **In the draft**, **Need attention**, **Not in menu** and a **Chosen** column: how many boots took the installation's files and when the latest was. Requests from the server's own address and localhost do not count.
- **New installation** is four steps as cards (image, drivers, answer file, title) with the line as it will appear on the machine screen and a summary of whether a WinPE build is needed.
- **ISO images** have a drop zone for the file and size and architecture columns.

Rollout 21.10.2026, third stage: replaced only `pxe-control` (SHA-256 `a938fb64…`). The second-stage binary is in `/var/backups/pxe-platform-pult-20261020/pxe-control.stage2`. The client menu SHA-256 did not change (`7ecede91…`).

Follow-ups on 21.10.2026:

- **⌘K search** (Ctrl+K, or `/` outside input fields): a header dialog finds machines by MAC (with or without separators) and IP, installations, images, answer files and pages; arrows choose, Enter opens. Without JavaScript the button opens the `/search` page; the dialog reads `/search?format=json&q=…`.
- **Machine steps follow the furthest step of a boot.** dnsmasq logs TFTP transfers (`sent … to IP`); after the loader iPXE asks for an address itself, which adds another `PXE … proxy` line — this now counts as the iPXE step instead of falling back to DHCP. A transfer aborted and repeated at once (the UEFI shim checking the file size) is not an error.
- The **Сейчас** section is called **Now** in the English interface.

Rollout 21.10.2026: replaced only `pxe-control` (SHA-256 `a637317d…`). The dnsmasq settings were not changed.

## Stale SMB mounts, 23.10.2026

After a NAS disk was replaced and the shared folder recreated, the `//192.168.10.10/Downloads/iso` mount stayed "mounted" for systemd while every access failed with `Stale file handle` (kernel log: `reconnect tcon failed rc = -2`). Storage recovery only restarted inactive mounts and missed it; Images saw the storage as "reachable again" every 10 minutes (the file list came from cache) but its rescan failed on `lstat`.

Agent storage recovery now checks every active SMB mount the way a reader would: `stat` of the root and reading one entry (at most 15 seconds). When a mount is unreadable, Agent stops only the Windows ISO profiles whose ISOs live on that share, restarts the mount, and the regular pass starts the profiles again. Images also checks a storage with `stat` of its root. The mount account is kept in `/etc/pxe-platform/smb/<key>.cred` (currently `v.pogutsa`).

Rollout 23.10.2026: replaced `pxe-agent` (SHA-256 `9650f3f0…`) and `pxe-images` (SHA-256 `848cdc92…`). The first pass after the Agent restart remounted both shares and the three profiles; DSM is "Available" again. The previous binaries are in `/var/backups/pxe-platform-agent-recovery-20261023/`. The client menu SHA-256 did not change (`7ecede91…`).

## Windows Setup progress after WinPE, 23.10.2026

After `boot.wim` the server sees no more Boot requests from the machine, but WinPE connects to the `\\10.30.0.10\pxeisos` share and runs Windows Setup from it. Every 10 seconds the Agent reads `smbstatus --json` and records connections to `pxeisos`: the IP, the profile (the first folder in the paths of open files), whether `install.wim`/`install.esd`/`install*.swm` is open, how much the Samba process serving the client has read (`rchar` in `/proc/<pid>/io`), and when the connection closed. Seven days of history are kept in `/var/lib/pxe-platform-agent/smb-sessions.json` and served to Control as `GET /smb-sessions` on the Agent socket.

Control ties a connection to a machine by IP (the nearest earlier Boot request from the same address, at most 30 minutes before) and adds a seventh step, “Install”, on the Now page, plus “Install” lines in the machine’s steps:

- **WinPE connected to the installer**: connected, copying has not begun (the edition or disk choice is on screen);
- **Copying Windows**: `install.wim` is open and more than 256 MB were read since it was first opened; the amount read is shown;
- **Files copied, rebooting**: Setup released `install.wim` after copying;
- **Connection lost while copying**: the connection closed while `install.wim` was still being copied;
- **Stuck in WinPE**: `boot.wim` was served more than 5 minutes ago and there is no connection (most often WinPE lacks the network card driver). This is concluded only for boots after watching began, and only when the Agent answered;
- **Installer disconnected before copying**: the connection closed without copying.

An open `install.wim` alone does not mean copying: Setup opens it to list the editions, closes it, and keeps it open again on the edition and disk screens. The first version of 23.10 took that for copying and reported “copied” at once; now the amount read decides (the edition list is tens of megabytes, a copy is gigabytes). If the Agent cannot read the process counter, the step stays “WinPE connected”.

While Setup holds the connection, the machine counts as active. The end of the installation (OOBE, first sign-in) is not visible to the server: after the reboot Windows installs from the machine's own disk.

For `smbstatus`, `pxe-agent.service` gains `ReadWritePaths=-/run/samba` (without it smbstatus cannot create its messaging context; the “-” keeps the unit working where Samba is not installed). The smbstatus warning “no network interfaces found” inside the Agent sandbox is expected; the JSON is complete.

Rollout 23.10.2026 (10:30, fixed at 10:45): replaced `pxe-agent` (SHA-256 `feda3ab3…`) and `pxe-control` (SHA-256 `f542e891…`) and updated `/etc/systemd/system/pxe-agent.service`. Previous files: `/usr/local/bin/pxe-agent.bak-202610231029`, `/usr/local/bin/pxe-control.bak-202610231029`, `/root/pxe-agent.service.bak-202610231029`. The client menu did not change.

## ISO uploads to local storage and free space, 23.10.2026

- On the ISO Images page, the Storage list now offers, besides SMB shares, the server's local storages that the Images service may write to. Each entry shows where it is (“network share” or “on this server”) and how much is free; if the file will not fit, the browser says so before the transfer starts.
- Every available storage shows its space: “free X of Y · used N%” with a bar (yellow from 85%, red from 95%), on the Storages page (Space column) and on System → Overview. Images computes it on every catalog request (statfs of the root, at most 3 seconds so that a hung share does not hold the page).
- A Windows profile can be prepared from an ISO in a local storage inside `/var/lib/pxe-platform-images`: the Agent and `pxe-winiso-prepare` now mount ISO files from that directory too, not only from SMB connections.
- Images API: upload and the folder list are available as `/storages/{id}/upload` and `/storages/{id}/folders`; the earlier `/smb-storages/…` paths still work.

Rollout 23.10.2026, 13:16: replaced `pxe-agent` (SHA-256 `acc66103…`), `pxe-control` (`c6e3b050…`), `pxe-images` (`9f19758e…`) and `pxe-winiso-prepare` (`6725b513…`); previous files are `/usr/local/bin/*.bak-202610231316`. `pxe-images.service` gains `ReadWritePaths=-/var/lib/pxe-platform-images` (previous unit copied to `/root/pxe-images.service.bak-202610231316`), and `/var/lib/pxe-platform-images/iso` became `2770 root:pxe-images` (was `750`). A test upload of a 4-byte file to Local succeeded; the file and its catalog row were removed, and the audit log keeps an `iso.upload ok` line by user `claude`. The client menu did not change.

## “Console” look for the boot menu, 23.10.2026

The menu a machine sees is now drawn in the admin interface's style (variant 2 of the canvas mockups):

- colours: grey text on black; the chosen line bright white on amber (brown in text mode); separators and the key line amber. They are set by `cpair` commands at the top of the script; a loader without that command keeps its own colours and boots as before;
- every screen title reads `oh my boot_  |  <MAC>  |  UEFI/BIOS` (until 29.10.2026 it was `PXE Platform` and could be edited in the menu settings); iPXE fills in the MAC and firmware on the machine itself;
- a hint at the right edge of an item: `!! WIPES DISK` for an answer file that wipes the disk, `auto install` for other answer files, the driver list for WinPE builds, and `>` for folders;
- on the main screen “Boot local disk” is the default and boots by itself after 10 seconds (the row shows the countdown), so a network-first machine no longer waits in the menu after Windows Setup reboots;
- the last row of every screen is `ip <machine IP>    Enter: boot    Esc: back`.

iPXE splits a command line on whitespace and joins item text with single spaces, and it has no quoting. Alignment therefore comes from settings `sp1`…`sp64` (1–64 spaces as hex) printed as `${spN:string}` inside a word, since iPXE expands settings after splitting the line. Verified on iPXE built from source (`bin-x86_64-linux/tap.linux` with the script embedded): colours, alignment, countdown, entering a folder and going back.

Machines get the change only after publishing: the draft shows “New look of the machine screen”. The preview on the Menu page draws the screen in the same colours. Rollout 23.10.2026, 14:16: replaced `pxe-control` (SHA-256 `b4784a45…`); the previous one is `/usr/local/bin/pxe-control.bak-202610231416`. The client menu has not changed; an administrator publishes it.

Follow-up 23.10.2026, 14:29 (`pxe-control` SHA-256 `08118533…`, previous `.bak-202610231429`), after checking on machines:

- **UEFI.** The firmware text console is 80×25 in a corner of the screen, in the firmware's palette (OVMF draws “yellow” as olive). The menu now first switches to iPXE's own graphical console: `console --x 640 --y 480` gives the same 80 columns across the whole screen, and `colour --rgb` sets exact colours: background `#0E1013`, text `#C3C8CF`, amber `#F2A93B`. That console draws bold text by adding `0x555555` to the colour, so the chosen line is dark on amber. Without a 640×480 mode the firmware console stays, with the earlier scheme (white on yellow).
- **Legacy BIOS.** BIOS loaders from ipxe.org are built without the `console`, `colour` and `cpair` commands (iPXE's sources disable them for BIOS as “not historically included in BIOS builds”). So BIOS shows iPXE's default colours, while the rest works: title, right-hand hints, countdown and key line. Custom colours on BIOS need our own `undionly.kpxe` build.
- The draft again shows “New look of the machine screen” until the menu is published with the new variant.

Follow-up 23.10.2026, 14:49 (`pxe-control` SHA-256 `32fb3073…`, previous `.bak-202610231449`):

- **No per-machine resolution.** `console --x 640 --y 480` is a minimum, not an exact mode: iPXE looks through the modes the machine's graphics offer and takes the smallest one that is at least 640×480. One setting therefore suits every machine. If there is no such mode at all, the firmware text console stays and the menu still works.
- **The right edge is no longer cut off.** iPXE's graphical console draws each character in a 9-pixel cell, so 640×480 gives 71 columns, not 80, and hints aligned to 80 columns were clipped (“aut”, “vioscs”). Item lines are now sized for the narrowest screen: 65 characters (71 less frame and padding). On wider screens the hints sit slightly left of the edge.
- The menu script now carries `# pxe-platform:look pult-3`; from it the draft can tell that the published menu uses an older look and shows “New look of the machine screen”.

## Steps on the Now page: “Image” merged into “Choice”, 23.10.2026

There are now six steps: DHCP · TFTP · iPXE · Menu · Choice · Install. iPXE does not tell the server which item was chosen; the choice shows up as the first request for that installation's files. So the earlier “Choice” and “Image” always lit up together, and “Image” is gone. Instead, “Choice” shows whether the machine got the installation files:

- the item name: the files arrived;
- `boot.wim · 312.0 MB of 540.0 MB` (amber step): the file is being sent right now;
- `boot.wim · file not found` or `boot.wim · transfer cut off` (red step): the machine did not get the file. The machine page shows the outcome “Installation file not received” with a hint on what to do.

The labels under the steps no longer repeat the column names: DHCP shows the machine's IP, TFTP shows UEFI or BIOS, Menu shows “waiting for a choice” while the machine sits in the menu. The Machine column shows only the MAC.

For this, Boot records how each file request ended: the HTTP status, the bytes sent and the file size; requests for missing files (404) are logged too. Requests logged before this change carry none of it and count as successful. Files being sent right now are served to Control over Boot's socket (`GET /transfers`) with the bytes sent so far.

Rollout 23.10.2026, 15:31: the [migration](../../deploy/sql/2026-10-23-boot-transfer.sql) was applied to the `pxe_boot` database (columns `status`, `bytes`, `size` in `boot_events`; as `pxe-boot`); replaced `pxe-boot` (SHA-256 `efe081a6…`) and `pxe-control` (`ff2a7817…`); previous files are `/usr/local/bin/*.bak-202610231531`. The client menu did not change.

Follow-up 23.10.2026, 15:41 (`pxe-control` SHA-256 `faf7de3f…`, previous `.bak-202610231541`): a machine's row resets when it boots again. Previously only a pause longer than 10 minutes started a new boot, so a quick reboot piled onto the last one: the choice, “Image” and the Setup state stayed. Now a new boot also starts when the firmware fetches a loader over TFTP again after iPXE already ran, or when iPXE fetches the menu again after the installation files. A WinPE connection to the share belongs to the boot it began in. Samba keeps listing a rebooted machine's connection for a while, and such a connection no longer carries over to the new boot.

## Now page: machine filter, 23.10.2026

The machines block on the home page is called “Boots” and shows machines of the last 24 hours, picked by buttons:

- **Now** (default): machines heard from in the last 3 minutes, including WinPE holding its share connection and boot.wim downloads;
- **Last 24 hours**: every machine that booted in that time;
- **Failed**: those of them that stopped on an error (loader, installation file, WinPE, broken copy).

The buttons show counts. When nothing is booting, the empty table is replaced by the last boot (MAC, when, what was chosen). The list stops at 50 machines; the rest are in the Journal. Machines older than a day are not shown on the home page: the full history is in Journal → Machines. The chosen filter is kept in the address (`/?show=day`, `/?show=failed`), and the page's auto-refresh keeps it.

Rollout 23.10.2026, 15:50: replaced `pxe-control` (SHA-256 `8ae95cba…`); the previous one is `.bak-202610231550`.

## Journal keeping: automatic cleanup and full clear, 23.10.2026

At the bottom of the Journal page an administrator sees a “Journal keeping” block:

- how many Boot requests the log holds, since when, and the table size;
- **Automatic cleanup**: “Keep everything” (default), or older than 7, 30, 90, 180 days or a year. Boot deletes old requests once an hour and right after the period changes. The setting lives in `/var/lib/pxe-platform-boot/journal.json` and survives restarts;
- **Clear the whole log** (with confirmation): Boot deletes all `boot_events` rows, the Agent forgets finished WinPE share connections (an installation in progress stays), and Control stops showing DHCP/TFTP lines logged before that moment. journald keeps those lines on its own, and they cannot be deleted on our command. Machines disappear from the Now and Journal pages until they boot again.

Both actions are audited (`journal.retention`, `journal.clear`). The system keeps DHCP and TFTP lines in journald for up to 7 days and WinPE connections for 7 days, whatever the setting.

API on the Boot socket: `GET /journal`, `PUT /journal/retention` (`{"days": N}`), `POST /journal/clear`; on the Agent socket: `POST /smb-sessions/clear`.

Rollout 23.10.2026, 16:10: replaced `pxe-boot` (SHA-256 `09b437f4…`), `pxe-control` (`19f3df0d…`), `pxe-agent` (`f34cd6bb…`); previous files are `/usr/local/bin/*.bak-202610231610`. Automatic cleanup is off and the log is untouched (534 records since 15.10.2026, 192 KB).

## Machine list: model, maker and own name, 23.10.2026

Control keeps a list of every machine it has seen in the `machines` table of the `pxe_control` database ([migration](../../deploy/sql/2026-10-23-machines.sql)). Clearing the boot log does not clear it. For each machine it holds:

- the MAC, when it was first and last seen, the last IP and firmware;
- **what the machine reported about itself**: maker, model, serial number and UUID from SMBIOS. The boot menu sends them with one `imgfetch` line to `/boot/hello` (relative to the menu's own address); Boot logs it as kind `hello`, and Control moves it into the list. Firmware placeholders (“To Be Filled By O.E.M.”, “Default string”, an all-zero UUID) are dropped. If sending fails, the menu still shows. For machines to start reporting, the menu has to be published again: the draft shows “Updated menu script”;
- **the network card maker** from the first three MAC bytes in the IEEE registry (built into Control, `internal/oui`, about 40 thousand prefixes). This is the card's maker, not the computer's; an address the machine or hypervisor made up is labelled “locally administered address”;
- **what a person set**: an own name, a maker and model instead of the reported ones, and a note.

Where it shows: on Now and in Journal → Machines the own name replaces the MAC (with the MAC small beneath it), and under the MAC the maker and model or, until the machine reaches the menu, the network card maker. The Journal also lists machines no longer in the boot log (“Not in the log”). A machine's page has a block with all details, a “Change name and model” form (administrator and operator) and “Remove the machine from the list” (administrator only). ⌘K search finds machines by name, model, serial number, note and network card maker. Changes are audited (`machine.edit`, `machine.delete`).

Rollout 23.10.2026, 18:04: the migration was applied to `pxe_control` (as `pxe-control`); replaced `pxe-boot` (SHA-256 `ba1b1e7e…`) and `pxe-control` (`f6ab41ff…`); previous files are `/usr/local/bin/*.bak-202610231804`. The client menu does not change until it is published.

## Firmware refusing to start the loader (Secure Boot), 23.10.2026

If a machine downloaded `ipxe-shim.efi` over TFTP but did not ask for the next file, `ipxe.efi`, within 30 seconds, its firmware refused to start the shim. This is almost always Secure Boot without the Microsoft UEFI CA 2011 key, as on Secured-core laptops. On Now such a machine's iPXE step is red with the note “rejected by firmware”; on the machine's page the outcome is “Firmware rejected the loader”, with a hint about what to turn on in the BIOS. A firmware retry after a refusal no longer counts as iPXE starting. What it looks like, how to fix it and how to reproduce it on a VM: [secure-boot.md](secure-boot.md#if-a-machine-does-not-boot).

Rollout 23.10.2026, 21:35: replaced `pxe-control`; the previous file is `/usr/local/bin/pxe-control.bak-202610232135`.

## System → Machines page, 23.10.2026

A table of every machine in the `machines` list, including those long gone from the boot log. Columns: name (or MAC), model or network card maker, serial number, last IP, firmware, when the machine was first and last seen, and the note (shortened; the full note is in the tooltip). A row leads to the machine's page, where its name, model and note are set.

- **Search** (`?q=`) covers name, MAC, maker, model, network card maker, serial number, UUID, IP and note, ignoring case. A MAC can be typed in any form: `001b21`, `00-1B-21`, `00:1b:21`.
- **Sorting** (`?sort=`) works on every column except the note. The default is last seen, newest first. Dates sort newest first, text and addresses alphabetically and numerically; empty values go last. Header links keep the search.
- **Counters** above the table: total, named, and without a model (the machine has not reached the menu yet and no model was set by hand). While searching: “Found: N of M”.
- Before showing the page, Control moves what the logs show right now into the list, so a machine that booted a minute ago is already there.
- Every role can view the page. It works without JavaScript.

Rollout 23.10.2026, 23:14: replaced `pxe-control` (SHA-256 `4c0dc98d…`); the previous file is `/usr/local/bin/pxe-control.bak-202610232314`.

## Machines: editing in the table and adding a MAC by hand, 23.10.2026

- **Edit in the row.** Every machine in the System → Machines table has “Edit”: name, maker, model, note. An empty maker or model means “as the machine reported”; the reported values show as placeholders in the fields. With JavaScript the row saves without reloading the page, and an error (text too long) shows inside the row. Without JavaScript the form posts normally and returns to the same page with the same search and order, at that row (`#m-<MAC>`). Route `POST /machines/{mac}`.
- **Add a MAC.** A machine can be added before its first boot, for example to name it right away. The MAC is accepted in any form (`bc:24:11:aa:bb:01`, `BC-24-11-AA-BB-01`, `bc2411aabb01`, `bc24.11aa.bb01`) and stored as `bc:24:11:aa:bb:01`. All-zero, broadcast and multicast addresses are rejected: they are not network cards. If the MAC is already there, the form answers “This MAC is already in the list” with a link to the machine and changes nothing. Route `POST /machines`.
- **“Has not booted yet”.** A machine added by hand shows “has not booted yet” instead of the last-seen time, and “added by hand, has not booted yet” on its page. First seen is the time it was added. The mark clears at its first boot. It is stored in the new `machines.manual` column ([migration](../../deploy/sql/2026-10-23-machines-manual.sql)).
- **Remove from the list** is in the same row, administrator only, with a confirmation. Route `POST /machines/{mac}/delete`.
- Rights: every role can view; administrators and operators can edit and add; only administrators can remove. `machine.add`, `machine.edit` and `machine.delete` are audited.

Rollout 23.10.2026, 23:26: the `2026-10-23-machines-manual.sql` migration was applied to `pxe_control` (as `pxe-control`); replaced `pxe-control` (SHA-256 `4079bd4a…`); the previous file is `/usr/local/bin/pxe-control.bak-202610232326`.

## Machines: CSV download and upload, 23.10.2026

- **Download CSV** (`GET /machines.csv`, every role) gives the table as shown on the page, with the same search. Columns: MAC, Name, Maker, Model, Serial number, UUID, Note, First seen, Last seen. The file is UTF-8 with a BOM, semicolon-separated, with CRLF line ends, so Excel opens it with Cyrillic intact. A cell Excel would treat as a formula (starting with `=`, `+`, `-`, `@`) is written with a leading apostrophe, which is removed again on upload. Titles follow the interface language.
- **Upload CSV** (`POST /machines/import`, administrators and operators). The first line holds column titles, in Russian or English, in any order. A MAC column is required. Name, Maker, Model and Note are loaded. Serial number, UUID and dates are read-only, and their columns are listed as not loaded. The separator is `;` or `,` (detected from the first line); quoted cells may contain the separator and line breaks. Files are limited to 1 MB and 5000 lines, UTF-8 only; for another encoding the page suggests saving as CSV UTF-8.
- **Check first.** After the upload the page shows “To add”, “To change”, “Unchanged” and “Errors”, the lines with errors (invalid MAC, repeated MAC, text too long — those lines are skipped) and a table of changes, with the old value struck through and the new one highlighted. The list is not touched yet.
- **Merge rule.** New MACs are added and marked “has not booted yet”. For known machines a filled cell replaces the name, maker, model or note, and an empty cell erases nothing. What the machine reported itself (SMBIOS) is never changed by an upload. A maker or model equal to the reported one means “as the machine reported” and clears a value a person set earlier. So a file that was just downloaded changes nothing when uploaded back.
- **Apply** (`POST /machines/import-apply`). The check is kept in Control's memory for 30 minutes, only for the person who uploaded the file, and can be applied once. When applied, the file is compared with the list again so that changes made after the check are not overwritten. Everything is written in one transaction: all or nothing. `machine.import` is audited with the numbers of added and changed machines.

## Fix: “+” in a machine's model, 23.10.2026

iPXE sends a space in the machine report as `%20` but leaves `+` as it is. Boot read `+` as a space (as HTML forms do), so QEMU's model “Standard PC (Q35 + ICH9, 2009)” was stored as “Standard PC (Q35   ICH9, 2009)”. The `+` is now kept. Models already stored correct themselves the next time the machine boots to the menu.

Rollout 23.10.2026, 23:36–23:38: replaced `pxe-control` (SHA-256 `044289c5…`, previous file `/usr/local/bin/pxe-control.bak-202610232336`) and `pxe-boot` (`3d2c17a5…`, previous file `/usr/local/bin/pxe-boot.bak-202610232338`). No migrations.

## Machines: MAC prefix rules, 23.10.2026

A rule labels machines by the first 3, 4 or 5 bytes of the MAC: a batch of laptops, a virtual machine lab, a network card maker. Rules live in the new `mac_prefix_rules` table ([migration](../../deploy/sql/2026-10-23-mac-prefix-rules.sql)): prefix, title (required), network card maker, note.

- **A machine's label** is chosen in this order: its own model (set by a person) → the model from BIOS → the title of the longest matching rule → the network card maker from the IEEE registry. The machine's name, if set, is shown separately as before. The rule label shows everywhere: Now, Journal → Machines, the machine's page, System → Machines, and ⌘K search (which also finds machines by rule title).
- **A rule's maker** replaces the IEEE entry for the network card, for example rule `bc:24:11` “Proxmox lab” with maker “Proxmox (virtual card)”.
- **The “MAC prefix rules” section** is at the bottom of System → Machines. Each rule shows how many machines in the list match; the number links to the table with only those machines (`/machines?rule=bc:24:11`; search, sorting and Download CSV work there). A machine's page has a “Rule” line linking to the same place.
- The prefix is accepted in any form (`bc:24:11`, `BC-24-11-52`, `bc241152`) and stored as `bc:24:11`. A whole MAC and a multicast first byte are rejected. A prefix has one rule; editing a rule changes its title, maker and note, while the prefix stays.
- Rights: every role can view; administrators and operators can add and edit; only administrators can delete. Audited as `mac_rule.add`, `mac_rule.edit`, `mac_rule.delete`.
- These rules will later carry the choice of network loader per vendor (stage 5 of the machine list plan).

Rollout 23.10.2026, 23:46: the `2026-10-23-mac-prefix-rules.sql` migration was applied to `pxe_control` (as `pxe-control`); replaced `pxe-control` (SHA-256 `956c9dd3…`); the previous file is `/usr/local/bin/pxe-control.bak-202610232346`.

## Answer file editor, 24.10.2026

“Library → Answer files → Create in editor” and “Open in editor” on every ready file. Details: [answer-editor.md](answer-editor.md).

- The generator is a Go port of cschneegans/unattend-generator (MIT) in `internal/unattend`; on 106 golden files its output matches the original byte for byte.
- Foreign files open with nothing lost: known settings go into the form, the rest stays verbatim under “Custom XML”.
- Saving to an image creates a new answer file the same way as an XML upload; editing an existing file does not change it. Audit `windows-answer.create … editor=1`.
- The Agent returns a stored file by number and SHA-256: `POST /windows-answer/xml` (404 — no file, 409 — another hash).
- The WinPE script of an answer file maps the profile folder once more as `I:` and copies the answer file to `X:\autounattend.xml`, so the “wipe and partition automatically” mode (`pe.cmd`) finds the image in network installs. Variants published earlier keep their old script and pass the check as before.
- The “wipes disk” label is set only for running diskpart with a script (`diskpart /s`, `diskpart <`), not for any mention of diskpart.

Rollout 24.10.2026: no migrations. 03:28 — `pxe-control` (“wipes disk” label); 09:32 — `pxe-agent` (XML download); 10:35 — `pxe-control` (editor); 10:59 — `pxe-agent` and `pxe-control` (`I:` mapping). Now: `pxe-control` SHA-256 `39a26fc6…`, `pxe-agent` `ec73e582…`; previous files are `/usr/local/bin/*.bak-<date>`. After the rollout answer files #3, 5 and 7 pass the Agent check (HTTP 204); #3 opens in the editor with a summary and “wipes disk” label that match the file.

## USB stick "enable the UEFI CA key", 26.10.2026

**Library → USB media**. Details: [secure-boot.md](secure-boot.md#usb-stick-for-machines-outside-the-domain).

- Package `internal/ueficausb`: the stick's scripts (`uefica.cmd`, `uefica.vbs`, `winpeshl.ini`, `README.txt`) are embedded in the agent; the build runs `wimlib-imagex export … 1 --boot`, then `update` with the three files in `Windows/System32`; for "UEFI CA 2023" it adds `bootmgfw_EX.efi` and the `Fonts_EX` fonts.
- Agent: `POST /uefica-usb/build {profile_id, loader}` returns the archive key; `GET /uefica-usb/file/{key}` returns the archive. The cache is `/var/lib/pxe-platform-agent/uefica-usb`, keyed by the SHA-256 of the source `boot.wim`, the scripts version and the loader; a rebuild removes the old archive of the same profile and loader.
- Control: `POST /usb/uefica` with fields `profile_id` and `loader` (action `view`, all roles) streams the archive with no write time limit; audit `uefica-usb.download` (`ok`/`error`, `<profile>-<loader>`).

Rollout 26.10.2026 20:43: no migrations. `pxe-agent` SHA-256 `8032a17e…`, `pxe-control` `9afd921b…`; previous files are `/usr/local/bin/*.bak-202610262043`. Check: as `claude`, archives of profile 3 (Windows 11 26H2) were downloaded: "2011" is 613 MB in 17 s (8 s from the cache), its `boot.wim` has one bootable image with the three stick files, `bootx64.efi` is signed by Microsoft Windows Production PCA 2011; in "2023" `bootx64.efi` and `bootmgr.efi` are signed by Windows UEFI CA 2023 and the fonts come from `Fonts_EX`. The audit log has `uefica-usb.download|ok|3-2011` and `3-2023`. The stick has not yet booted a VM or real hardware (see TODO).

Fixes after review, 26.10.2026 21:05 (`pxe-agent` only, SHA-256 `c72af814…`): "changed" is now exit code 20, not 0, so a script failure can no longer restart the machine; any unknown code counts as an error; an array WMI value (Dell `CurrentValue`) is read by its first element; WMI is called with `authenticationLevel=pktPrivacy`; a quote typed at a prompt no longer aborts the script; the password is read before any check, so it never lands in the next prompt. The cache rebuilt itself: the archive key includes the scripts version.

**Library → USB media**, 26.10.2026 22:41: the stick moved from the profile row under ISO images to its own tab after Answer files, laid out like the answer file editor: "1 · WinPE image" (ready profiles only, each with the archives already cached and their sizes), "2 · Boot loader", "3 · What the screen shows" on the left; a summary (image, loader, archive name, size or "built on download") and **Download the archive** on the right. Under ISO images a ready profile links to the tab with **USB media →**. The download is `POST /usb/uefica`; the old address answers 404. Agent: `GET /uefica-usb/archives` lists the cached archives by file name, without hashing. Rollout: `pxe-agent` `8db3051b…`, `pxe-control` `5b2923e0…`, previous files `*.bak-202610262241`; downloading profile 3 through the new tab gives 200, 613 MB.

File dates in the archive, 27.10.2026 00:24: files from the ISO keep their date, `boot.wim` and `README.txt` get the build time (all were 1980 before). `pxe-agent` only, `a9f8f632…`, previous file `pxe-agent.bak-202610270024`; the archive cache was cleared and rebuilds on download.

Audit of a cancelled download, 27.10.2026: the `uefica-usb.download|error` row is written even when the browser cancels the download (the cancelled request context used to lose it). `pxe-control` only.

## The Oh My Boot name, 29.10.2026

The product is called **Oh My Boot**. The browser tab shows “Oh My Boot” and an icon; the header shows the `oh my boot_` wordmark; the sign-in page shows the wordmark above the form (the “Sign in to Oh My Boot” heading is for screen readers only).

The boot menu title is no longer configurable: it is always `oh my boot_`, and `oh my boot_ / <folder>` on nested screens. The “Title” field is gone from the menu settings, and a `title` field sent directly to the server is ignored. After Control is updated, the draft gets the new name at once and the draft bar shows a settings change; machines keep the previous menu until it is published. Published versions and rollback keep their original titles. The default diagnostic message is now `Oh My Boot network boot is ready`; a custom message is left as it is.

WinPE messages start with `Oh My Boot:`. Windows profiles prepared earlier keep the old messages until the profile is prepared again or rebuilt with drivers. Program, service and directory names (`pxe-control`, `/etc/pxe-platform` and so on) are unchanged. Details (in Russian): [stage 4 summary](../brand/2026-10-29-stage-4-visible-name.md).

Rollout 29.10.2026, 16:01–16:28: replaced `pxe-control`, `pxe-agent`, `pxe-agentctl`, `pxe-boot` (Go 1.27.2, commit `a808589` plus later fixes) and the template `/usr/share/pxe-platform/windows-profiles/pxe-setup.cmd.in`; the five units now have `Description=` “Oh My Boot …”. The previous files, units and the menu before the rollout are in `/var/backups/pxe-platform-omb-20261029/` (with `SHA256SUMS`). Publishing first failed: the check of driver configurations regenerates the WinPE script, whose messages had changed, so configurations 1 and 4, built before the rename, were rejected (“files of driver configuration 1 are unavailable”). The Agent now also accepts the previous script that differs only in these messages (`RenderDriverStartupLegacy`); after the Agent was replaced both configurations passed. Version 38 was published: only the titles of four screens (`PXE Platform` → `oh my boot_`) and the diagnostic message changed. Menu SHA-256: before `b9f0fe0951de56978ef6958cfcc024a969740a42ed92d2621bd6f4a1a4d96b15`, after `d7148e0aa070ebd232e9bf9bbc432540485021661db6bbdee0cf5d9e692aeb3d`.

## Discarding draft changes, 29.10.2026

The draft bar has **Discard changes** next to **Publish menu**. It opens a confirmation, **Discard all changes**: the draft becomes the published version again and machines see no change, since nothing is published. Operators and administrators can do this (`POST /menu/discard` with `confirm=yes`); it is audited as `menu.draft.discard`.
