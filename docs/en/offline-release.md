# Portable OMB release for Ubuntu 26.04 amd64

[Русская версия and complete runbook](../offline-release.md).

The installation archive contains the OMB binaries, service units, templates, initial database schemas, boot assets, and a local APT repository. The separate A-to-B update contains two replacement binaries and the OIDC logout migration. The signed manifest lists the target, both commits for an update, the file hashes and sizes, and the ordered SQL steps.

On Ubuntu, `scripts/offline-deb.py` can build one `omb` package per release and add them to a local APT repository. Use `offline-deploy.sh install-deb BASE_ARCHIVE PUBLIC_KEY PACKAGE SHA256 MANAGEMENT_IP [PXE_INTERFACE]` to set up an APT-managed server. Use `offline-deploy.sh update-deb BASE_ARCHIVE UPDATE_ARCHIVE PUBLIC_KEY OLD_PACKAGE OLD_SHA256 NEW_PACKAGE NEW_SHA256 MANAGEMENT_IP` to migrate its database and update the package. Obtain package digests from the release builder through a trusted channel. The standard `rollback` command restores the saved package and its matching database backup. [Package build and APT rehearsal](../research/2026-11-22-deb-package-rehearsal.md), [commands and checks](../offline-release.md#установка-из-deb).

Use the release verifier from a trusted checkout and obtain the public key separately from the archives. An example on the target:

```sh
sudo python3 offline-release.py verify omb-update-a-to-b.tar.gz \
  --public-key trusted-release-public.pem --check-host --extract /var/tmp/omb-update-b
```

The verifier checks the signature, payload, OS, architecture, and installed release marker before extracting to a new directory. It does not install packages, stop services, or apply SQL.

For automatic application, copy `offline-deploy.sh`, `offline-install-lib.sh`, and `offline-release.py` from a trusted checkout into the same directory. Use `offline-deploy.sh install INSTALL_ARCHIVE PUBLIC_KEY MANAGEMENT_IP PXE_INTERFACE`, `offline-deploy.sh update BASE_ARCHIVE UPDATE_ARCHIVE PUBLIC_KEY MANAGEMENT_IP`, and `offline-deploy.sh rollback MANAGEMENT_IP` with sudo. The read-only `check-update BASE_ARCHIVE UPDATE_ARCHIVE PUBLIC_KEY` verifies the archives and installed binaries. Installation leaves DHCP and Samba off.

An update can cover Control on its own or Control and Images together. Include `omb-control`, `omb-controlctl`, and migrations for `omb_control`. For a combined release, add `omb-images`, `omb-imagesctl`, and a `--migration omb_images=deploy/sql/FILENAME.sql` argument for each Images migration. The order of `--migration` arguments determines the execution order across databases. Complete any active ISO operations before the maintenance window. The installer stops both services and verifies both database backups before running the first migration. It checks HTTPS, the Images catalog API, and the Boot menu before recording the new release.

If the installer is interrupted while `PENDING` exists, run `rollback`. A combined rollback switches both databases to their restored copies in one PostgreSQL transaction. It retains the later databases with connections disabled, then restores the previous binaries and verifies the services.

For a management-only host, omit `PXE_INTERFACE` or pass `none`. To enable PXE later, configure the client subnet and set `OMB_CONTROL_INTERFACES` to the dedicated interface before restarting Control.

For a fresh installation, follow the [offline installation guide](offline-installation.md), verify HTTPS and the boot menu, and record the installed commit in `/var/lib/omb-deployment/COMMIT`. For an update, stop Control and back up its database, binaries and keys. Test the restore before applying the manifest's migration and replacing the binaries. After a successful health check, record B. To roll back, stop Control, restore the previous database and binary files together, verify the result, and record A. See the [full runbook](../offline-release.md#обновление-a--b-и-откат).
