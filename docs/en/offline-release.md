# Self-contained offline OMB release for Ubuntu 26.04 amd64

[Русская версия и подробное руководство](../offline-release.md).

OMB is distributed and upgraded via a single self-contained signed local APT repository (`apt-repo`). Each release bundles the `omb_<version>_amd64.deb` package, all pinned transitive Ubuntu `.deb` dependencies, the `Packages.gz` index, `repo-manifest.json`, and an OpenSSL signature `repo-manifest.sig`.

Delta `tar.gz` archives and `from_commit` pinning are not used: any installed OMB version can be upgraded directly to any newer version, including multi-release jumps. Historical rehearsal reports are kept in [the initial release rehearsal](../research/2026-11-21-ubuntu-offline-release-rehearsal.md) and [the `.deb` package rehearsal](../research/2026-11-22-deb-package-rehearsal.md).

## Building and verifying `apt-repo`

Prepare the compiled binaries from a committed Git revision as described in [the offline installation guide](../offline-installation.md) and stage a payload directory containing:

- `bin/` — nine amd64 ELF binaries (`omb-agent`, `omb-agentctl`, `omb-boot`, `omb-control`, `omb-controlctl`, `omb-images`, `omb-imagesctl`, `omb-setup`, `omb-winiso-prepare`);
- `deploy/systemd/` — six systemd units (`omb-agent.service`, `omb-boot.service`, `omb-control.service`, `omb-dnsmasq.service`, `omb-images.service`, `omb-winiso@.service`);
- `deploy/boot-assets/windows-profiles/` — WinPE profile templates (`omb-setup.cmd.in`, `winpeshl.ini`, and any additional templates);
- `assets/` — pinned boot assets (`ipxeboot.tar.gz`, `ipxe-shimx64.efi`, `wimboot`);
- `scripts/prepare-ubuntu-source.sh`, `scripts/fetch-ipxe.sh`, `deploy/samba/smb.conf`;
- `deploy/sql/{boot.sql,images.sql,control.sql}` — initial bootstrap SQL schemas;
- `deploy/sql/migrations/{omb_boot,omb_images,omb_control}/NNNN_name.sql` — cumulative SQL migrations;
- `debs/` — local system `.deb` dependencies and `Packages.gz` with versions and SHA-256 digests.

On the release host, build and sign the repository using an OpenSSL private key (never copied to target servers; distribute the public key separately):

```sh
python3 scripts/offline-deb.py build <payload-dir> <apt-repo> \
  --version 0.2.0 --channel stable \
  --commit COMMIT_SHA \
  --license LICENSE --sign-key signing-private.pem \
  [--go-mod go.mod] [--unattend-upstream internal/unattend/UPSTREAM.md]
```

`offline-deb.py build` produces:

- `omb_<version>_amd64.deb` (binaries under `/usr/bin/`, units under `/usr/lib/systemd/system/`, helpers under `/usr/lib/omb/`, bootstrap SQL under `/usr/share/omb/bootstrap/`, cumulative migrations and `/usr/share/omb/migrations/manifest.json`, and `/usr/share/omb/release.json` containing `channel`, `commit`, `display_version`, `package`, `version`);
- a combined `Packages.gz` index covering `omb` and all system dependencies;
- `repo-manifest.json` (`format: 2`, target `ubuntu 26.04 amd64`, package metadata, `Packages.gz` and system `.deb` digests, boot asset digests, Go version and modules from `go.mod`, and the pinned `internal/unattend/UPSTREAM.md` commit) signed into `repo-manifest.sig`.

Verify the repository signature and every file checksum without modifying the host:

```sh
python3 scripts/offline-deb.py verify-repo <apt-repo> \
  --public-key trusted-release-public.pem [--print-package]
```

Passing `--print-package` outputs the TSV tuple (`FILENAME`, `SHA256`, `VERSION`, `COMMIT`, `CHANNEL`) consumed by `offline-deploy.sh`.

### Single-command release build (`scripts/build-release.sh`)

To cross-compile all 9 binaries, stage `payload/`, run `offline-deb.py build` + `verify-repo`, and optionally deploy to a stand in one command, use `scripts/build-release.sh`. Version and channel are inferred from the Git tag on `HEAD` (`vX.Y.Z` → `stable`, `vX.Y.Z-dev.N` → `dev`) or passed explicitly via `--version` / `--channel`:

```sh
# 1. On an Ubuntu VM or in CI (local dpkg-deb)
bash scripts/build-release.sh \
  --sysdebs-dir /path/to/sysdebs \
  --assets-dir /path/to/assets \
  --sign-key /path/to/signing-private.pem \
  --out ./apt-repo

# 2. From macOS: build on an Ubuntu stand and apply the update in one step
bash scripts/build-release.sh \
  --remote user@192.0.2.10 \
  --apply 192.0.2.10

# 3. From macOS: build on an Ubuntu host and fetch the signed apt-repo locally
bash scripts/build-release.sh \
  --remote user@192.0.2.10 \
  --sign-key /path/to/signing-private.pem \
  --out ./dist/apt-repo
```

## Deployment, updates, and rollback (`offline-deploy.sh`)

Copy `scripts/offline-deploy.sh`, `scripts/offline-install-lib.sh`, and `scripts/offline-deb.py` together into the same directory on the target Ubuntu host alongside `<apt-repo>` and `<public-key>`:

```sh
# 1. Fresh installation on a clean Ubuntu 26.04 amd64 host (interactive omb-setup wizard)
sudo bash scripts/offline-deploy.sh install <apt-repo> <public-key>

#    Positional form without interactive network selection (prompts for initial admin password)
sudo bash scripts/offline-deploy.sh install <apt-repo> <public-key> <management-ip> [pxe-interface|none]

# 2. Upgrade (including multi-release jumps)
sudo bash scripts/offline-deploy.sh update [--allow-channel-switch] <apt-repo> <public-key> <management-ip>

# 3. Roll back an interrupted or most recent completed upgrade
sudo bash scripts/offline-deploy.sh rollback <management-ip>
```

### Fresh installation (`install`)

- Requires a clean machine with no existing `/etc/omb`, `/srv/omb/tftp`, `/var/lib/omb-deployment`, or `omb-control`.
- Verifies `<apt-repo>`, installs system `.deb`s and `omb` offline via `unshare --net` APT (refusing if any already-installed system package would be upgraded), and in the short form (`install <apt-repo> <public-key>`) launches the interactive `omb-setup --install-mode offline` wizard; in the positional form it creates `omb-boot`, `omb-control`, `omb-images`, `omb-iso` accounts and PostgreSQL databases, loads `/usr/share/omb/bootstrap/` schemas, and seeds `schema_migrations` from `/usr/share/omb/migrations/manifest.json`.
- Extracts pinned boot assets, generates `/etc/omb/secrets.key` and `/var/lib/omb-control/control.{crt,key}`, and saves `/var/lib/omb-deployment/{current.deb,PACKAGE,PACKAGE_SHA256,CHANNEL,COMMIT}`.
- When a PXE interface and DHCP mode (`proxy` or `full`) are selected in `omb-setup`, `omb-dnsmasq` and Samba (`smbd` and `/etc/samba/smb.conf`) are configured and started automatically. If PXE setup is deferred (or the positional form is used), DHCP and Samba stay disabled until PXE settings are applied in the web panel (**System → Network**), where `omb-agent` automatically syncs `omb-dnsmasq` and `/etc/samba/smb.conf` (`none` uses the reserved `omb-pxe-off` placeholder). See the [installation guide](installation.md) for the full `omb-setup` walkthrough.

### Local `current.deb` cache and automatic scope calculation

On install and every completed update, the active `omb` package is cached at `/var/lib/omb-deployment/current.deb` alongside `PACKAGE`, `PACKAGE_SHA256`, `CHANNEL`, `COMMIT`, and `LAST`.

During `offline-deploy.sh update`:

- The script verifies `<apt-repo>`, checks `new_version > old_version` (`dpkg --compare-versions`), enforces channel matching unless `--allow-channel-switch` is set, and confirms that the installed `omb` package matches `/var/lib/omb-deployment/current.deb` (`dpkg --verify omb` and `omb.md5sums`).
- It diffs `current.deb` against the new `.deb` and compares `schema_migrations` across all three databases against `/usr/share/omb/migrations/manifest.json` to compute the exact service scope (`control` always included; `images`, `boot`, `agent`, `dnsmasq` when their files or databases change) and affected databases (`omb_boot`, `omb_images`, `omb_control`, or `none`). Unaffected services keep running.
- Site configuration in `/etc/omb/`, `/srv/omb/tftp/`, and provisioning inputs under `/usr/share/omb/{assets,samba,windows-profiles}/` are never overwritten (updates attempting to modify those provisioning inputs are rejected).

### Cumulative migrations and `schema_migrations`

SQL migrations live at `deploy/sql/migrations/<db>/NNNN_<name>.sql` (`omb_boot`, `omb_images`, `omb_control`) and follow strict append-only rules:

- Filenames must match `NNNN_name.sql` with unique 4-digit prefixes per database and no `BEGIN`, `COMMIT`, `ROLLBACK`, or `VACUUM` statements (the runner executes each migration in its own transaction and inserts `(id, sha256)` into `schema_migrations`).
- Every previously applied migration in `schema_migrations` must still exist in the new package with an identical SHA-256, and unapplied migrations cannot precede an already-applied `id`.

Because every package embeds the full cumulative migration set, an operator can jump across multiple releases in a single `offline-deploy.sh update` run; only missing migrations are applied in `omb_boot` -> `omb_images` -> `omb_control` order.

### Rollback guarantees

Before installing the new `.deb` or running migrations, `offline-deploy.sh update` checks disk space, captures the published `menu.ipxe`, writes `/var/lib/omb-deployment/PENDING`, stops only affected services, saves the prior `omb.deb` and configs to `/var/backups/omb/offline.<id>`, and runs `pg_dump -Fc` plus a verified `pg_restore` into `omb_restore_*` recovery databases.

On failure during `update`, or when running `sudo bash scripts/offline-deploy.sh rollback <management-ip>` (to recover from an interrupted `PENDING` update or revert the `LAST` completed update):

- APT restores the saved previous `.deb` (`--allow-downgrades --reinstall`).
- All affected databases are atomically swapped back to their verified `omb_restore_*` snapshots in a single PostgreSQL transaction, retaining the post-update databases as `omb_retained_*` with `ALLOW_CONNECTIONS false`.
- Services, health endpoints, `menu.ipxe`, `/var/lib/omb-deployment/current.deb`, and state markers are restored. (If `/etc/omb/secrets.key` or the published `menu.ipxe` changed after the backup, rollback refuses and requires coordinated manual restoration.)

## Release channels (`stable` and `dev`)

OMB maintains two parallel release channels tracked in `/var/lib/omb-deployment/CHANNEL` and `/usr/share/omb/release.json`:

- **`stable` (`main` branch):** Git tags `vX.Y.Z`, `.deb` versions `X.Y.Z` (for example `0.2.0`).
- **`dev` (`dev` branch):** Git tags `vX.Y.Z-dev.N`, `.deb` versions `X.Y.Z~dev.N` (where `~` ensures `0.2.0~dev.N < 0.2.0` in `dpkg`).

By default, `offline-deploy.sh update` stays within the installed channel. Pass `--allow-channel-switch` to intentionally switch between `stable` and `dev`:

```sh
sudo bash scripts/offline-deploy.sh update --allow-channel-switch \
  <apt-repo> <public-key> <management-ip>
```
