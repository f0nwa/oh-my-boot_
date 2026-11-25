---
slug: offline-updates-and-recovery
section: operations
order: 20
title: Offline Installation, .deb Packages, Channels, and Recovery
summary: How the signed local APT repository, stable and dev channels, routine updates, automatic rollback, and system time settings work.
keywords: [update, offline, deb, apt, rollback, channels, stable, dev, recovery, ntp, time, omb]
---

OMB is designed for servers with no internet access. Every release ships as a self-contained signed local APT repository containing the `omb` package, system dependencies, and cumulative SQL migrations, verified before installation and capable of automatic rollback on any failure.

## Release channels: `stable` and `dev`

| Channel | Git branch | Release tag | `.deb` package version | Panel indicator |
| --- | --- | --- | --- | --- |
| **`stable`** | `main` | `vX.Y.Z` | `X.Y.Z` | Quiet top bar; version and channel shown in the “OMB release” card on `/system` |
| **`dev`** | `dev` | `vX.Y.Z-dev.N` | `X.Y.Z~dev.N` | Amber `dev` badge next to the logo in the top bar + card on `/system` |

The server's current channel is stored in `/var/lib/omb-deployment/CHANNEL`, while package metadata lives in `/usr/share/omb/release.json` (`channel`, `commit`, `display_version`, `package`, `version`). Accidental channel switching during updates is blocked and requires the explicit `--allow-channel-switch` flag.

## Self-contained APT repository and `current.deb`

On the build host, `scripts/offline-deb.py build` produces a self-contained `apt-repo` directory:

- a single `omb_<version>_amd64.deb` package (binaries in `/usr/bin/`, systemd units in `/usr/lib/systemd/system/`, helpers in `/usr/lib/omb/`, bootstrap schemas in `/usr/share/omb/bootstrap/`, cumulative migrations `/usr/share/omb/migrations/manifest.json`, and the embedded Wiki);
- a local `Packages.gz` index and all transitive system `.deb` dependencies for the target Ubuntu release;
- `repo-manifest.json` (format `2`) with checksums of all artifacts, system packages, boot assets (`iPXE`, `shim`, `wimboot`), `go.mod` modules, and the pinned `unattend` generator upstream;
- cryptographic signature `repo-manifest.sig` (OpenSSL SHA-256).

On the server itself, `/var/lib/omb-deployment/current.deb` retains a copy of the currently installed `omb` package (alongside `PACKAGE`, `PACKAGE_SHA256`, `CHANNEL`, `COMMIT`). Because of this, routine updates do not need the previous install archive or manual version/hash arguments — only the new `apt-repo` and public key.

## `schema_migrations` registry and skipping versions

Each of the three databases (`omb_boot`, `omb_images`, `omb_control`) maintains a `schema_migrations (id, sha256, applied_at)` table. Migrations inside the package are numbered sequentially (`NNNN_name.sql`) and listed in `/usr/share/omb/migrations/manifest.json`:

- on fresh install (`install`), after loading the bootstrap schema, all migrations included in the package are recorded in `schema_migrations`;
- on update (`update`), the planner compares the new package's `manifest.json` against `schema_migrations`: it verifies SHA-256 integrity of already-applied migrations (deleting or altering past migrations is forbidden) and transactionally applies only pending migrations in order `omb_boot` → `omb_images` → `omb_control`.

Because every release carries the full cumulative migration set, a server can jump across multiple intermediate releases (for example, from `0.2.0~dev.1` straight to `0.2.0~dev.5`) as long as the new package version is strictly higher (`dpkg --compare-versions`).

## Install, update, and rollback (`offline-deploy.sh`)

Core commands on the OMB server (alongside `offline-deploy.sh`, `offline-install-lib.sh`, and `offline-deb.py`):

```bash
# 1. Verify bundle signature and integrity before installing or updating
python3 scripts/offline-deb.py verify-repo \
  /path/to/apt-repo \
  --public-key /path/to/trusted-release-public.pem

# 2. Fresh installation on clean Ubuntu (pass PXE interface name or none)
sudo bash scripts/offline-deploy.sh install \
  /path/to/apt-repo \
  /path/to/trusted-release-public.pem \
  MANAGEMENT_IP \
  none

# 3. Routine update from a signed local repository
# (for an explicit channel switch, pass --allow-channel-switch right after update)
sudo bash scripts/offline-deploy.sh update \
  /path/to/apt-repo \
  /path/to/trusted-release-public.pem \
  MANAGEMENT_IP

# 4. Roll back to the previous version if needed
sudo bash scripts/offline-deploy.sh rollback MANAGEMENT_IP
```

What `offline-deploy.sh update` does:

1. Verifies `repo-manifest.sig`, checksums, target OS and architecture, absence of a stale `PENDING` marker, version increase (`new_version > old_version`), matching channel, and consistency between the installed package and `/var/lib/omb-deployment/current.deb`.
2. Diffs `current.deb` against the new package and computes pending migrations from `schema_migrations`, automatically calculating the change scope (`scope`): which services and which of the three databases are affected. Untouched services are never restarted.
3. Checks free disk space, creates a full backup in `/var/backups/omb/offline.<id>` (previous `omb.deb`, configs, snapshots, and restore-tested dumps of affected databases), and sets the `PENDING` marker.
4. Installs the new `.deb`, applies pending SQL migrations recording them in `schema_migrations`, verifies user rows, service health, HTTPS response, and unchanged `omb-boot` menu. `/srv/omb/tftp` and `/etc/omb` are never overwritten.
5. Updates `/var/lib/omb-deployment/current.deb` and state markers; if any step fails (or the process is killed with `SIGKILL`), automatic or manual `rollback` restores the previous `.deb` and databases, preserving the migrated databases as `omb_retained_<id>` with connections disabled for post-mortem analysis.

## Console administration and online updates (`omb`)

For servers with internet access and day-2 diagnostics, use the unified `omb` CLI:

```bash
# Inspect installed version, release channel, install mode, and all 7 services
sudo omb status

# View the current release channel or switch it (stable or dev)
sudo omb channel [stable|dev]

# Update online from GitHub Releases (SHA-256 verification, DB backup, cumulative migrations)
sudo omb update

# Update offline from a locally copied .deb (+ SHA256SUMS in the same directory)
sudo omb update --deb ./omb_<version>_amd64.deb
```

For air-gapped servers requiring full signed repository verification and automated rollback, use the signed local APT repository and `offline-deploy.sh` above (`sudo omb status`, `sudo omb channel`, and `sudo omb update --deb` all work without internet access).

## Integrity check and system time (NTP)

After an update or during diagnostics, verify installed package files:

```bash
dpkg --verify omb
```

Empty output means all package files are intact and unmodified.

System time and time zone are configured in the **“Server time”** card on [System → Overview](/system) (“Change…” button):

- **Corporate NTP** mode — `omb-agent` auto-detects the active daemon (`chrony` or `systemd-timesyncd`), writes servers to `/etc/chrony/sources.d/omb.sources` or `/etc/systemd/timesyncd.conf.d/omb.conf`, and applies the config;
- **Manual** mode — sets exact date, time, and time zone for completely air-gapped labs without NTP.

## Emergency recovery

- **Bad menu edit:** open [Menu](/menu) → “Version history” → “Rollback”.
- **PXE network config error:** `omb-agent` validates `dnsmasq.conf` via `dnsmasq --test` and automatically rolls back on failure.
- **Failure after a package update:** run `sudo bash scripts/offline-deploy.sh rollback MANAGEMENT_IP` — the script restores the previous `.deb`, channel, and consistent DB dumps from `/var/backups/omb/`.
- **Manual backups:** back up `/etc/omb/` (including `secrets.key`), `/var/lib/omb-control/` (TLS), and consistent `pg_dump` snapshots of `omb_control`, `omb_images`, and `omb_boot` with writer services stopped.
