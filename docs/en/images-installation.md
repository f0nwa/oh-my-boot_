# ISO catalog on the test VM

> Current installation commands use the target OMB names. Dated deployment accounts, original backup paths and outcomes describe the pre-migration stand; the stand was migrated to OMB on 31 October 2026. See the [technical migration plan](../../docs/superpowers/plans/2026-10-31-omb-technical-migration.md). Results and remaining acceptance: [migration report](../research/2026-10-31-omb-technical-migration.md).

[Русская версия](../images-installation.md)

This step adds the Images service and the list of found ISO files to the existing HTTPS admin interface. It does not change DHCP, Boot or the published PXE menu. A found ISO has the state “Found, not checked” and is not yet bootable over the network.

## Setup

Build `./cmd/omb-images`, `./cmd/omb-imagesctl` and the updated `./cmd/omb-control` for Linux amd64. Create the `omb-images` system user and group, a PostgreSQL role and a separate `omb_images` database. Apply the [Images schema](../../deploy/sql/images.sql) as the database owner. Install the [Images unit](../../deploy/systemd/omb-images.service) and update the [Control unit](../../deploy/systemd/omb-control.service) so that Control can reach the Images Unix socket. Images needs this variable:

```ini
OMB_IMAGES_DATABASE_URL=host=/var/run/postgresql dbname=omb_images user=omb-images
```

The Images service opens only `/run/omb-images/images.sock`. It does not need an external TCP port. For an empty local storage, create `/var/lib/omb-images/iso` readable by `omb-images`, then register it as that user:

```sh
sudo -u omb-images env OMB_IMAGES_DATABASE_URL='host=/var/run/postgresql dbname=omb_images user=omb-images' \
  /usr/local/bin/omb-imagesctl add-path -name Local -kind local -root /var/lib/omb-images/iso
```

To upload ISO files into the local storage from the admin interface, give the `omb-images` group write access to the directory (`sudo chmod 2770 /var/lib/omb-images/iso`). The `omb-images.service` unit already allows writing to `/var/lib/omb-images` (`ReadWritePaths=-/var/lib/omb-images`). While the directory is read-only, the storage works as before, but it is missing from the upload storage list and marked “read-only” on the Storages page. Windows profiles can be prepared from ISO files in local storages inside `/var/lib/omb-images`: the Agent mounts ISO files only from SMB connections and from that directory.

Free space is shown for every available storage (statfs of its root, at most 3 seconds): when choosing the upload storage and on the Storages and System pages. For SMB it is the space on the NAS share; for a local storage, on the server's file system.

Another directory that is already mounted is registered with a separate command, a new name and `-kind mounted`. The path must be a real mount point with no symbolic links in it; Images checks it before registration, before walking the files and after the walk. If the mount disappears or changes, the previous catalog is kept. A managed SMB connection is added in the admin interface.

On the **Images** page, an administrator or operator presses **Refresh**. The request starts a background scan and returns the page at once; the state and the found files appear after the page is reloaded. An observer sees the catalog but cannot start a scan. The audit log records that the request was accepted, and the scan result is shown in the storage state. If reading fails, the previous records are kept and the storage is marked unavailable. After a service restart, an interrupted scan is also marked as failed, and the scan can be started again.

Since 20.10.2026 Images also checks storage availability in the background and rescans a storage that becomes reachable again; see the [Control guide](control-installation.md#storage-availability-and-recovery).

## Check

```sh
systemctl is-active omb-images omb-control omb-boot omb-agent omb-dnsmasq
sudo -u omb-control curl --unix-socket /run/omb-images/images.sock http://unix/catalog
curl --cacert /var/lib/omb-control/control.crt https://192.168.10.135:8443/login
curl -fsS http://10.30.0.10:8080/boot/menu.ipxe
```

## Check on 15.10.2026

The `pxe_images` database was created on the VM, the `pxe-images` service was started, and the empty `Local` directory was registered. The scan request returned HTTP 202; after it the catalog became `ready` with an empty ISO list. Access to the Unix socket was checked with the group used by the Control process. A temporary test with two directories that both contained `same.iso` confirmed that the files are tracked separately; after one file was deleted, a new scan marked only that file as missing. The temporary directories and records were removed, leaving only `Local`. The HTTPS sign-in page is reachable; Boot, Agent, dnsmasq, Control and Images are active. The SHA-256 of the active PXE menu was the same before and after installation: `674ec52f0b9227bd69388dba861da5ecffb470c92da2aa7baf9607085be7c087`.

The user was to provide a real ISO later. The web page with an administrator session on the VM was not checked separately; the HTML, file name escaping and role permissions were checked by local tests. A backup of the previous Control binary and unit is in `/var/backups/pxe-platform-pre-images/`.

After fixes, Images and Control were installed again. A scan deliberately left in the `scanning` state became `unavailable` with an interruption message after Images restarted; a new request returned HTTP 202 and the `ready` state. All five services run on the VM again, the sign-in page returns HTTP 200, and the PXE menu SHA-256 stayed the same.

## Managed SMB storages

This part needs `cifs-utils`. Apply the [Images migration](../../deploy/sql/2026-10-15-managed-smb.sql) to the `omb_images` database, create `/mnt/omb-smb`, then install the new `omb-agent`, `omb-images` and `omb-control` binaries and the [Agent unit](../../deploy/systemd/omb-agent.service). Reload systemd and restart these three services. Boot and DHCP do not change.

On the **Images** page, the administrator enters one direct path to the ISO folder, for example `//192.168.10.10/Downloads/iso`, a username and a password. The domain can be left empty. The connection is read-only. Data for several connections is stored separately. An operator can start a new scan; an observer sees the catalog.

Enter the password only in the HTTPS form. The admin interface does not show it after saving. Agent stores the credentials in `/etc/omb/smb/<key>.cred` with mode `0600`; the PostgreSQL record contains only the address and path. If the connection fails, the record stays “Unavailable”: the administrator can retry with new credentials or delete the connection. Deletion first unmounts the share, then deletes the record from Images.

Found ISO files are shown in the catalog without being published to the PXE menu. If the connection is lost, the previous file list is kept and the storage becomes “Unavailable”.

### Installation on the test VM, 15.10.2026

On `192.168.10.135`, `cifs-utils` was installed, the Images database migration was applied, and Agent, Images and Control were updated. The old binaries, the Agent unit and a database dump are kept in `/var/backups/pxe-platform-pre-managed-smb/`. All five services are active. Access to the Images and Agent sockets was checked with the Control service account; the HTTPS page is reachable with the server certificate. A test connection to a non-existent SMB share returned HTTP 503; after the test connection was deleted, Agent returned HTTP 204, and no temporary credentials file or unit was left behind. After the test, the database contained only the previous local storage. The PXE menu SHA-256 did not change: `674ec52f0b9227bd69388dba861da5ecffb470c92da2aa7baf9607085be7c087`.

After the credentials of the real share were entered, it turned out that the previous `prefixpath=iso` option left the root of `Downloads` visible; the scan stopped at the inaccessible system folder `#recycle`. The mount source was changed to the direct path `//192.168.10.10/Downloads/iso`, and the form was simplified to a single path field. On the VM the `DSM` storage became “Available”, and four ISO files appeared in the catalog. The PXE menu checksum stayed the same.

## Windows ISO profiles

On the **Images** page, a profile can be created next to a found Windows x64 ISO from a connected SMB folder. Use Latin letters for the PXE menu title and folder: the built-in font of the current iPXE menu cannot show Cyrillic. After creating it, press **Prepare**. The service checks that the ISO has not changed since the scan, mounts it read-only and copies to `/srv/omb/tftp/windows-profiles/<ID>/` only `sources/boot.wim` and two small WinPE startup files. The full ISO content stays on the source SMB storage and is shown to the client through a separate read-only share, `\\10.30.0.10\ombisos\<ID>`. An ARM64 image is not available in an x64 profile.

A prepared profile appears on the **Menu** page as a separate item with a folder. Publishing checks the source ISO, the connection and the boot files again. The previous Windows 10/11 items stay until the new profiles are checked on a client. After the manual item is chosen, WinPE connects the ISO by itself and opens Setup; a person then goes through the installation screens manually.

To install this version on the test server, apply the [Control migration](../../deploy/sql/2026-10-17-windows-profiles.sql), update `omb-agent`, `omb-control` and `omb-boot`, and install `omb-winiso-prepare`, the [instance unit](../../deploy/systemd/omb-winiso@.service), the two [startup templates](../../deploy/boot-assets/windows-profiles/) and the `[ombisos]` section of the [Samba configuration](../../deploy/samba/smb.conf). The directories `/mnt/omb-iso/profiles`, `/srv/omb/tftp/windows-profiles`, `/etc/omb/winiso` and `/usr/share/omb/windows-profiles` are needed. The `omb-iso` password stays in `/etc/omb/omb-iso-password`; it never reaches the database or the repository.

## Windows ISO profile acceptance — 18.10.2026

Two independent profiles were prepared on the test server from ISO files on the DSM SMB storage: `Windows 10x64` (ID 1) and `Windows 11 x64` (ID 2). Both are `ready`; the source ISO files are mounted read-only. `boot.wim` and the small WinPE startup files were copied to the boot file directories, while the full ISO files stayed on DSM. Active PXE menu version 14 contains both new items in the `Windows` folder; the previous static items are still kept.

On VM 113 the user chose each new item. In both cases the installer opened without manual commands, and after going through the usual screens the 70 GB SATA disk was visible. The new Windows 11 profile was checked with Secure Boot enabled (`Enabled`). The installation was not started, and the disk was not changed. The Boot log confirmed that `winpeshl.ini`, `pxe-setup.cmd` and `boot.wim` of both profiles were served to the client `10.30.0.19`.

Two preparation bugs were fixed during acceptance: the ISO modification time comparison now uses the catalog's microsecond precision, and the created boot file directory is readable by the Boot service. After the fix, HTTP returns `200` for profile files from the PXE network; the WinPE startup file is still unavailable from the main network (`404`). Repeated mount failures are limited to three attempts per minute. The regression tests, `go test ./...`, `go vet ./...` and `git diff --check` passed.

The driver catalog was deferred at that time; it was added on 19.10.2026 (see the [Control guide](control-installation.md#winpe-driver-catalog)). The result of the later Legacy BIOS check is below.

## autounattend answer files for one Windows ISO

Several ready XML files can be uploaded for a prepared Windows profile on the **Images** page. Each upload creates a separate immutable answer file version: the title and folder use Latin letters, and the file is at most 1 MiB. The service checks the XML structure and the `unattend` root element, but not the Windows Setup settings. To change an answer file, upload a new file and select it instead of the previous one. Uploading the same XML again for the same ISO is rejected.

On the **Menu** page, the manual item and any number of ready answer files of one ISO can be published independently. Each answer file is a separate item, including inside folders. When an answer file is chosen, WinPE uses the same ISO and passes the selected XML to the installer with `/Unattend`. The manual item behaves as before. Disk partitioning and other actions in the XML are carried out by Windows Setup. The [example that only sets the language](../../examples/windows-answer-language-only.xml) is suitable for checking that the file is delivered, but a specific ISO may require a product key or other settings before installation continues. For a test that does not write to disk, prepare an XML with all settings this ISO requires, but without disk selection and partitioning. For Windows 11 Pro ISO files that require a key, use the [example with the generic installation key and no disk settings](../../examples/windows-answer-key-no-disk.xml): it sets the language, the public generic Windows 11 Pro installation key (which does not activate Windows) and EULA acceptance, so Setup stops at manual disk selection.

An XML file can contain passwords and keys. The admin interface does not show its content after upload and does not write it to the audit log. The file is available to devices in the trusted PXE network `10.30.0.0/24` at the boot server address, even if the matching item is not published yet. Keep the source file yourself, and do not upload secrets that must not be disclosed to this network.

To install, apply the [Control migration](../../deploy/sql/2026-10-18-windows-answers.sql) to the `omb_control` database. Create `/srv/omb/tftp/windows-answers` owned by `root`, group `omb-control`, mode `2750`. Update the `omb-agent`, `omb-control` and `omb-boot` binaries and the [Agent unit](../../deploy/systemd/omb-agent.service), reload the systemd configuration and restart only these services. The full ISO files and existing profiles are not prepared again. The active menu does not change until **Publish** is pressed again.

### Installation on the test server, 18.10.2026

The migration was applied, and the three services are active. Before the update, the binaries and a database dump were saved to `/var/backups/pxe-platform-pre-windows-answers-20261018/`. The active menu SHA-256 stayed `783ca9930d0d93f1184748873ef26008d223ce75776d439be3d58de229119f97`. The existing `boot.wim` of the Windows 10 profile returns HTTP 200 from the PXE network. A test XML returned HTTP 200 only from the PXE network and HTTP 404 from the main network, with the `Cache-Control: no-store` header. The temporary file was deleted after the check.

The first XML upload revealed a missing write path in the Agent systemd restrictions. The unit was updated; after that, a direct request to prepare the language test XML returned HTTP 204, and the created files got mode `0640 root:pxe-control`. Two failed records without files were deleted from the database after checking that no menu version referred to them. A profile with the user's XML and a separate language example were prepared; both items appeared in the published menu, and their files return HTTP 200 from the PXE network and HTTP 404 from the main network. The language example stopped in Windows Setup because it lacks the product key this ISO requires. According to the user, Windows 11 installed with the full XML and booted from the VM 113 disk; the XML content and secrets were not written to any log. After the check, the language item was removed from the menu: the active menu with SHA-256 `f8573c7b6da173184dc7745106f3b082757f1297b1256087dbed018c9c477d33` contains only the working answer file, and Agent, Control and Boot are active.

## ISO upload through the admin interface — 18.10.2026

On the **Images** page, an operator or administrator chooses a managed SMB storage, one of its existing subfolders (or the root), then an ISO file up to 32 GiB. The transfer goes straight from the browser to the selected SMB share; no full ISO is created on the PXE server disk. A progress bar shows the transfer; afterwards the server checks the size, computes the SHA-256 and adds a record to the catalog. **Cancel** stops the transfer and deletes the temporary file. An existing file with the same name is not overwritten. Publishing to the PXE menu remains a separate action.

For writing, Agent uses the stored SMB credentials and creates a second mount point, `/mnt/omb-smb-upload/<key>`, in `rw` mode. The original mount point `/mnt/omb-smb/<key>` stays `ro` for reading and scanning. For a new installation, create the `/mnt/omb-smb-upload` directory, install the updated `omb-agent`, `omb-images` and `omb-control`, update the [Agent](../../deploy/systemd/omb-agent.service) and [Images](../../deploy/systemd/omb-images.service) units, then reload the systemd configuration and restart these three services. The previously entered account must have write permission on the SMB side. If writing is denied, the form reports the error; the other services keep working.

On the test server, the binaries and units were saved to `/var/backups/pxe-platform-pre-iso-upload-20261018/` before the update. Agent, Images, Control and Boot are active. A separate connection to DSM opened in `rw` mode, and a test write as `pxe-images` succeeded. A small test file was sent through the Images API; its SHA-256 matched the file on DSM, and the record appeared in the catalog. The test file and record were then deleted. The read connection stayed `ro`; the active PXE menu did not change, and its SHA-256 stayed `f8573c7b6da173184dc7745106f3b082757f1297b1256087dbed018c9c477d33`.

After space was freed, the user uploaded `ru-ru_windows_11_consumer_editions_version_26h2_x64_dvd_463bbf21.iso` (8,748,939,264 bytes) through the HTTPS form. The file size on DSM matched the catalog; the server recognised it as a bootable ISO 9660 image. A Windows 11 26H2 profile (ID 3) was created. The system unit mounted the ISO and prepared `boot.wim`, but the database stayed in `preparing`: the end of the browser request did not record the result. The Agent `/windows-iso/check` call returned HTTP 204, and `/windows-iso/prepare` confirmed `install.wim`; the profile was then set to `ready` manually with an audit record. Control was updated: once preparation starts, it finishes the operation and saves the result even if the browser connection is lost. A regression test reproduced the previous hang in `preparing` and passed after the fix. About 63 GiB of free space was left on DSM.

The user published profile 3 and chose it on VM 113. The installer opened and the virtual disk was visible; the installation was not started. The Boot log confirmed that `boot.wim`, `pxe-setup.cmd` and `winpeshl.ini` were served to the client `10.30.0.19`. The profile stays `ready`; the new checksum of the active menu is `66356dbf2196918957afbccb029c2dec775265aafc0ecfdbb907287e0397a4c4`. The Secure Boot state during this particular check was not confirmed separately.

## Windows check in SeaBIOS — 19.10.2026

On VM 112, the old `Windows 10 installer - WinPE` item, which sends only `boot.wim`, opened the installer. The new `Windows 10x64` profile and the old Windows 11 item, which sent additional startup files with the iPXE `-n` option, stopped in `wimboot` with `Bad CPIO magic` and `FATAL: could not extract initrd files`. The WIM checksums of the old and new Windows 10 items matched.

The menu generator now passes the additional files in SeaBIOS with `initrd <URL> <name>`, and keeps the previous `initrd -n <name> <URL>` for UEFI. After Control was updated and the menu published again, the `Windows 10x64` profile opened the installer; the user went manually to the disk list and saw the 64 GB SATA disk of VM 112. The `Windows 11 x64` profile also opened the installer in SeaBIOS, but it stopped at the compatibility check. Microsoft requires UEFI with Secure Boot support and TPM 2.0 to install Windows 11; this check was run on VM 113 with OVMF. [Windows 11 requirements](https://learn.microsoft.com/en-us/windows/whats-new/windows-11-requirements).

After the menu change, the `Windows 10x64` profile opened the installer again on VM 113 with OVMF and Secure Boot enabled. Nothing was written to the test VM disks. The previous Control binary is kept on the server in `/var/backups/pxe-platform-bios-initrd-20261019/pxe-control`.
