# Portable OMB release for Ubuntu 26.04 amd64

[Русская версия и подробное руководство](../offline-release.md).

An offline release consists of signed archives verified with a separately distributed OpenSSL public key. The installation archive contains all eight OMB binaries, systemd units, configuration templates, initial database schemas, pinned boot assets (`ipxeboot.tar.gz`, `ipxe-shimx64.efi`, `wimboot`), and a local APT dependency index. Update archives contain only the changed binaries, systemd units, helper scripts, and ordered SQL migrations.

## Building and verifying signed archives

Build archives on a connected release host with an OpenSSL private key (never copied to target servers):

```sh
python3 scripts/offline-release.py pack payload-a omb-install-a.tar.gz \
  --commit COMMIT_A --sign-key signing-private.pem

python3 scripts/offline-release.py pack payload-b omb-update-a-to-b.tar.gz \
  --commit COMMIT_B --from-commit COMMIT_A \
  --migration omb_control=deploy/sql/2026-11-21-oidc-logout-jti-hash.sql \
  --sign-key signing-private.pem
```

Updates may omit `--migration` when only binaries, systemd units, or helper scripts change.

Verify an archive on the target host without modifying services or databases:

```sh
sudo python3 offline-release.py verify omb-update-a-to-b.tar.gz \
  --public-key trusted-release-public.pem --check-host --extract /var/tmp/omb-update-b
```

## Signed `.deb` APT repository and dependency manifest

On Ubuntu amd64, `scripts/offline-deb.py` builds one `omb` package per release, adds it to `Packages.gz`, and writes `repo-manifest.json` recording all OMB packages, the APT index, all pinned system `.deb` dependencies, boot asset digests, Go version and modules from `go.mod`, and the pinned `internal/unattend/UPSTREAM.md` commit. Pass `--sign-key` to sign `repo-manifest.json` into `repo-manifest.sig` with the same OpenSSL key:

```sh
python3 scripts/offline-deb.py omb-install-a.tar.gz apt-repo \
  --public-key trusted-release-public.pem --version 0.1~rehearsal1 \
  --update-archive omb-update-a-to-b.tar.gz --update-version 0.1~rehearsal2 \
  --license LICENSE --sign-key signing-private.pem

python3 scripts/offline-deb.py verify-repo apt-repo --public-key trusted-release-public.pem
```

## Automated deployment, updates, and rollback

Copy `offline-deploy.sh`, `offline-install-lib.sh`, `offline-release.py`, and `offline-deb.py` from a trusted checkout into the same directory on the target Ubuntu host.

### Using a signed `apt-repo` (recommended)

`install-deb-repo` and `update-deb-repo` verify `repo-manifest.sig`, `Packages.gz`, and every `.deb` against `PUBLIC_KEY`, removing the need to pass 64-character SHA-256 digests manually:

```sh
sudo bash offline-deploy.sh install-deb-repo omb-install-a.tar.gz trusted-release-public.pem \
  apt-repo 0.1~rehearsal1 MANAGEMENT_IP none

sudo bash offline-deploy.sh update-deb-repo omb-install-a.tar.gz omb-update-a-to-b.tar.gz \
  trusted-release-public.pem apt-repo 0.1~rehearsal1 0.1~rehearsal2 MANAGEMENT_IP

sudo bash offline-deploy.sh rollback MANAGEMENT_IP
dpkg-query -W -f='${Version}\n' omb
```

### Explicit `.deb` and SHA-256 mode

```sh
sudo bash offline-deploy.sh install-deb omb-install-a.tar.gz trusted-release-public.pem \
  "$OMB_PACKAGE_A" "$OMB_PACKAGE_A_SHA256" MANAGEMENT_IP none

sudo bash offline-deploy.sh update-deb omb-install-a.tar.gz omb-update-a-to-b.tar.gz \
  trusted-release-public.pem "$OMB_PACKAGE_A" "$OMB_PACKAGE_A_SHA256" \
  "$OMB_PACKAGE_B" "$OMB_PACKAGE_B_SHA256" MANAGEMENT_IP
```

### Scope, safety, and rollback guarantees

- **Automatic scope calculation:** `offline-deploy.sh` computes the affected services (`control`, `images`, `boot`, `agent`, and optionally `dnsmasq`) and databases (`omb_control`, `omb_images`, `omb_boot`, or `none`) directly from the signed update manifest.
- **Minimal disruption:** Only affected services are stopped, updated, restarted, and health-checked; unaffected services keep running. Updates without SQL migrations skip database dumps and restores.
- **Units and helpers:** Package updates may update `/usr/lib/systemd/system/omb-*.service` and `/usr/lib/omb/*` helpers, while `/srv/omb/tftp`, `/usr/share/omb` provisioning inputs, and `/etc/omb` site configuration are never overwritten during `update-deb` / `update-deb-repo`.
- **Atomic database rollback:** Before running any migration, every affected database is dumped with `pg_dump -Fc`, test-restored into a verified recovery database, and checked against pre-update snapshots. On failure or manual `rollback`, APT downgrades `omb` to the saved `.deb` and all affected databases are swapped back in a single PostgreSQL transaction, retaining the newer databases with connections disabled.
- **Management-only mode:** Omit `PXE_INTERFACE` or pass `none` during installation to keep DHCP and Samba disabled until the dedicated PXE interface is configured.

For manual archive-only commands (`install`, `check-update`, `update`, `rollback`) and rehearsal reports, see [docs/offline-release.md](../offline-release.md) and [docs/research/2026-11-22-deb-package-rehearsal.md](../research/2026-11-22-deb-package-rehearsal.md).
