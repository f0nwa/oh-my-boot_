#!/usr/bin/env bash
# Trusted launcher for verified Ubuntu installation and application updates.
set -euo pipefail
umask 077

state=/var/lib/omb-deployment
backups=/var/backups/omb
etc_omb_dir=/etc/omb
binary_dir=/usr/bin
lock_file=/run/lock/omb-offline-deploy.lock
allow_channel_switch=false
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
deb_tool="$script_dir/offline-deb.py"

fail() { printf '%s\n' "$*" >&2; exit 1; }

resolve_repo_package() {
    local repo=$1 key=$2 line fname digest version commit channel
    line=$(python3 "$deb_tool" verify-repo "$repo" --public-key "$key" --print-package) ||
        fail 'Repository verification failed.'
    IFS=$'\t' read -r fname digest version commit channel <<< "$line"
    [[ -n "$fname" && "$digest" =~ ^[0-9a-f]{64}$ && -n "$version" &&
       "$commit" =~ ^[0-9a-f]{40}$ && ( "$channel" == stable || "$channel" == dev ) &&
       -f "$repo/$fname" ]] ||
        fail 'Invalid package metadata from repository.'
    printf '%s\t%s\t%s\t%s\t%s\n' "$repo/$fname" "$digest" "$version" "$commit" "$channel"
}

assert_no_pending() {
    [[ ! -e "$1/PENDING" ]] || fail 'An update is pending. Run rollback before starting another operation.'
}

verify_sha256() {
    local expected=$1 file=$2 actual
    [[ "$expected" =~ ^[0-9a-f]{64}$ && -f "$file" ]] || return 1
    actual=$(sha256sum "$file" | awk '{print $1}') || return 1
    [[ "$actual" == "$expected" ]]
}

stage_deb() {
    local source=$1 digest=$2 dest=$3
    [[ "$digest" =~ ^[0-9a-f]{64}$ ]] || fail 'Expected a lowercase SHA-256 for the package.'
    cp -- "$source" "$dest"
    verify_sha256 "$digest" "$dest" || fail 'Package checksum differs.'
}

package_identity() {
    local deb=$1 commit=$2
    [[ $(dpkg-deb -f "$deb" Package) == omb && $(dpkg-deb -f "$deb" Architecture) == amd64 &&
       $(dpkg-deb -f "$deb" X-OMB-Commit) == "$commit" ]] || fail 'The package does not match the release.'
    dpkg-deb -f "$deb" Version
}

installed_package_matches() {
    local record verification
    record=$(dpkg-query -W -f='${db:Status-Status} ${Version} ${X-OMB-Commit}' omb) || return 1
    [[ "$record" == "installed $1 $2" ]] || return 1
    dpkg-deb -I "$3" md5sums | cmp -s - /var/lib/dpkg/info/omb.md5sums || return 1
    verification=$(dpkg --verify omb) || return 1
    [[ -z "$verification" ]]
}

plan_migrations() {
    python3 - "$1" "$2" <<'PY'
import json, re, sys

DB_ORDER = ("omb_boot", "omb_images", "omb_control")
SHA_RE = re.compile(r"^[0-9a-f]{64}$")
ID_RE = re.compile(r"^[0-9]{4}_[a-z0-9_]+$")

with open(sys.argv[1], encoding="utf-8") as f:
    manifest = json.load(f)

if manifest.get("format") != 1 or not isinstance(manifest.get("migrations"), list):
    sys.exit("Invalid migrations manifest format.")

manifest_by_db = {db: [] for db in DB_ORDER}
manifest_map = {db: {} for db in DB_ORDER}
for entry in manifest["migrations"]:
    db = entry.get("database")
    mid = entry.get("id")
    mpath = entry.get("path")
    msha = entry.get("sha256")
    if db not in manifest_by_db:
        sys.exit(f"Unsupported migration database in manifest: {db}.")
    if not isinstance(mid, str) or not ID_RE.match(mid):
        sys.exit(f"Invalid migration id in manifest: {mid}.")
    if not isinstance(msha, str) or not SHA_RE.match(msha):
        sys.exit(f"Invalid migration sha256 in manifest: {db}/{mid}.")
    if not isinstance(mpath, str) or not mpath:
        sys.exit(f"Invalid migration path in manifest: {db}/{mid}.")
    if mid in manifest_map[db]:
        sys.exit(f"Duplicate migration id in manifest: {db}/{mid}.")
    manifest_by_db[db].append(entry)
    manifest_map[db][mid] = entry

applied_by_db = {db: [] for db in DB_ORDER}
with open(sys.argv[2], encoding="utf-8") as f:
    for raw in f:
        line = raw.strip()
        if not line:
            continue
        parts = line.split("\t")
        if len(parts) != 3:
            sys.exit(f"Invalid applied migration record: {line}.")
        db, mid, msha = parts
        if db not in applied_by_db:
            sys.exit(f"Unsupported applied migration database: {db}.")
        applied_by_db[db].append((mid, msha))

for db in DB_ORDER:
    by_id = manifest_map[db]
    applied = applied_by_db[db]
    applied_ids = set()
    for aid, asha in applied:
        if aid not in by_id:
            sys.exit(f"Applied migration {db}/{aid} is missing from the new package.")
        if by_id[aid]["sha256"] != asha:
            sys.exit(f"Migration checksum drift detected for {db}/{aid}.")
        applied_ids.add(aid)

    sorted_manifest = sorted(manifest_by_db[db], key=lambda item: item["id"])
    if applied_ids:
        max_applied = max(applied_ids)
        for entry in sorted_manifest:
            if entry["id"] not in applied_ids and entry["id"] < max_applied:
                sys.exit(
                    f"Append-only violation: unapplied migration {db}/{entry['id']} "
                    f"precedes applied {max_applied}."
                )

    for entry in sorted_manifest:
        if entry["id"] not in applied_ids:
            print(db, entry["id"], entry["path"], entry["sha256"], sep="\t")
PY
}

collect_applied_migrations() {
    local out=$1 db rows row
    : > "$out"
    for db in omb_boot omb_images omb_control; do
        rows=$(sql -d "$db" -At -F $'\t' -c "SELECT id, sha256 FROM schema_migrations ORDER BY id") || return 1
        if [[ -n "$rows" ]]; then
            while IFS= read -r row; do
                [[ -n "$row" ]] || continue
                printf '%s\t%s\n' "$db" "$row" >> "$out"
            done <<< "$rows"
        fi
    done
}

compute_update_scope() {
    python3 - "$1" "$2" "$3" <<'PY'
from pathlib import Path
import sys

BINARY_TO_SVC = {
    "usr/bin/omb-control": "control",
    "usr/bin/omb-controlctl": "control",
    "usr/bin/omb-images": "images",
    "usr/bin/omb-imagesctl": "images",
    "usr/bin/omb-boot": "boot",
    "usr/bin/omb-agent": "agent",
    "usr/bin/omb-agentctl": "agent",
    "usr/bin/omb-winiso-prepare": "agent",
}
UNIT_TO_SVC = {
    "usr/lib/systemd/system/omb-control.service": "control",
    "usr/lib/systemd/system/omb-images.service": "images",
    "usr/lib/systemd/system/omb-boot.service": "boot",
    "usr/lib/systemd/system/omb-agent.service": "agent",
    "usr/lib/systemd/system/omb-winiso@.service": "agent",
    "usr/lib/systemd/system/omb-dnsmasq.service": "dnsmasq",
}
HELPER_TO_SVC = {
    "usr/lib/omb/omb-prepare-ubuntu-source": "agent",
    "usr/lib/omb/fetch-ipxe": "agent",
}
DB_TO_SVC = {
    "omb_control": "control",
    "omb_images": "images",
    "omb_boot": "boot",
}
FORBIDDEN_PREFIXES = (
    "usr/share/omb/assets/",
    "usr/share/omb/samba/",
    "usr/share/omb/windows-profiles/",
)
ALLOWED_EXACT = {
    "usr/share/omb/release.json",
    "usr/share/doc/omb/copyright",
}
ALLOWED_PREFIXES = (
    "usr/share/omb/bootstrap/",
    "usr/share/omb/migrations/",
)

old_root = Path(sys.argv[1])
new_root = Path(sys.argv[2])
pending_path = Path(sys.argv[3])


def collect_files(root: Path) -> dict[str, Path]:
    files = {}
    for p in root.rglob("*"):
        if p.is_file() or p.is_symlink():
            rel = p.relative_to(root).as_posix()
            files[rel] = p
    return files


old_files = collect_files(old_root)
new_files = collect_files(new_root)

services = {"control"}
databases = set()

for rel in sorted(set(old_files) | set(new_files)):
    old_p = old_files.get(rel)
    new_p = new_files.get(rel)
    changed = (
        old_p is None
        or new_p is None
        or old_p.read_bytes() != new_p.read_bytes()
    )
    if not changed:
        continue
    if rel in BINARY_TO_SVC:
        services.add(BINARY_TO_SVC[rel])
    elif rel in UNIT_TO_SVC:
        services.add(UNIT_TO_SVC[rel])
    elif rel in HELPER_TO_SVC:
        services.add(HELPER_TO_SVC[rel])
    elif rel in ALLOWED_EXACT or rel.startswith(ALLOWED_PREFIXES):
        continue
    elif rel.startswith(FORBIDDEN_PREFIXES):
        sys.exit(f"The package update changes provisioning inputs under /usr/share/omb: {rel}.")
    else:
        sys.exit(f"Unsupported package file change: {rel}.")

with pending_path.open(encoding="utf-8") as f:
    for raw in f:
        line = raw.strip()
        if not line:
            continue
        db = line.split("\t", 1)[0]
        if db not in DB_TO_SVC:
            sys.exit(f"Unsupported migration database: {db}.")
        databases.add(db)
        services.add(DB_TO_SVC[db])

print("SERVICES", ",".join(sorted(services)), sep="\t")
print("DATABASES", ",".join(sorted(databases)) if databases else "none", sep="\t")
PY
}

apply_migration() {
    local database=$1 id=$2 sql_file=$3 sha256=$4
    [[ "$database" == omb_boot || "$database" == omb_images || "$database" == omb_control ]] ||
        fail "Unsupported migration database: $database."
    [[ "$id" =~ ^[0-9]{4}_[a-z0-9_]+$ && "$sha256" =~ ^[0-9a-f]{64}$ && -f "$sql_file" ]] ||
        fail "Invalid migration metadata for $database/$id."
    verify_sha256 "$sha256" "$sql_file" ||
        fail "Migration file checksum differs: $database/$id."
    {
        printf 'BEGIN;\n'
        cat -- "$sql_file"
        printf "\nINSERT INTO schema_migrations (id, sha256) VALUES (:'mig_id', :'mig_sha');\nCOMMIT;\n"
    } | runuser -u "${database/_/-}" -- psql -Xw -v ON_ERROR_STOP=1 -d "$database" \
        -v mig_id="$id" -v mig_sha="$sha256"
}

apply_pending_migrations() {
    local migrations_dir=$1 pending_file=$2
    local database id relpath sha256
    while IFS=$'\t' read -r database id relpath sha256; do
        [[ -n "$database" ]] || continue
        apply_migration "$database" "$id" "$migrations_dir/$relpath" "$sha256"
    done < "$pending_file"
}

has_service() { [[ ",${scope_services:-}," == *",$1,"* ]]; }

has_database() { [[ "${scope_databases:-none}" != none && ",${scope_databases}," == *",$1,"* ]]; }

package_channel() {
    local ch ver
    ch=$(dpkg-deb -f "$1" X-OMB-Channel 2>/dev/null || true)
    if [[ -z "$ch" ]]; then
        ver=$(dpkg-deb -f "$1" Version)
        [[ "$ver" == *"~dev"* ]] && ch=dev || ch=stable
    fi
    [[ "$ch" == stable || "$ch" == dev ]] || fail "Unsupported package release channel: $ch."
    printf '%s\n' "$ch"
}

installed_channel() {
    local ch ver
    if [[ -f "$state/CHANNEL" ]]; then
        ch=$(cat "$state/CHANNEL")
        [[ "$ch" == stable || "$ch" == dev ]] || fail "Invalid installed release channel: $ch."
        printf '%s\n' "$ch"
        return
    fi
    if [[ -f "$state/PACKAGE" ]]; then
        ver=$(cat "$state/PACKAGE")
        [[ "$ver" == *"~dev"* ]] && printf 'dev\n' || printf 'stable\n'
        return
    fi
    printf 'stable\n'
}

check_channel_switch() {
    if [[ "$prior_channel" != "$target_channel" ]] && ! $allow_channel_switch; then
        fail "Release channel switch ($prior_channel -> $target_channel) requires --allow-channel-switch."
    fi
}

write_state() {
    local name=$1 value=$2 tmp
    tmp=$(mktemp "$state/.state.XXXXXXXX")
    printf '%s\n' "$value" > "$tmp"
    mv -T -- "$tmp" "$state/$name"
}

write_current_deb() {
    local src=$1 tmp
    tmp=$(mktemp "$state/.current.deb.XXXXXXXX") || return 1
    install -m 0600 "$src" "$tmp" && mv -T -- "$tmp" "$state/current.deb"
}

sql() { runuser -u postgres -- psql -Xqw -v ON_ERROR_STOP=1 "$@"; }

boot_menu() { curl --noproxy '*' -fsS --max-time 4 http://127.0.0.1:8080/boot/menu.ipxe; }

health() {
    local try ok
    for try in {1..20}; do
        ok=true
        if [[ -z ${scope_services:-} ]] || has_service control; then
            if ! systemctl is-active --quiet omb-control.service ||
                ! curl --noproxy '*' -fsS --max-time 3 --cacert /var/lib/omb-control/control.crt \
                    -o /dev/null "https://$address:8443/login" 2>/dev/null; then
                ok=false
            fi
        fi
        if $ok && has_service images; then
            if ! systemctl is-active --quiet omb-images.service ||
                ! curl --noproxy '*' -fsS --max-time 3 --unix-socket /run/omb-images/images.sock \
                    -o /dev/null http://localhost/catalog 2>/dev/null; then
                ok=false
            fi
        fi
        if $ok && has_service boot; then
            if ! systemctl is-active --quiet omb-boot.service ||
                ! boot_menu >/dev/null 2>&1; then
                ok=false
            fi
        fi
        if $ok && has_service agent; then
            if ! systemctl is-active --quiet omb-agent.service ||
                ! curl --noproxy '*' -fsS --max-time 3 --unix-socket /run/omb/agent.sock \
                    -o /dev/null http://localhost/status 2>/dev/null; then
                ok=false
            fi
        fi
        if $ok && has_service dnsmasq && [[ ${dnsmasq_was_active:-0} == 1 ]]; then
            if ! systemctl is-active --quiet omb-dnsmasq.service; then
                ok=false
            fi
        fi
        if $ok; then return 0; fi
        sleep 1
    done
    echo "Service health check failed (services: ${scope_services:-control})." >&2
    return 1
}

check_address() {
    python3 - "$address" <<'PY'
import ipaddress, sys
ipaddress.IPv4Address(sys.argv[1])
PY
    ip -o -4 addr show | awk -v ip="$address" '{split($4,a,"/"); if (a[1]==ip) found=1} END {exit !found}' ||
        fail "Management address $address is not assigned to this host."
}

stop_services() {
    if has_service control; then systemctl stop omb-control.service || return 1; fi
    if has_service images; then systemctl stop omb-images.service || return 1; fi
    if has_service boot; then systemctl stop omb-boot.service || return 1; fi
    if has_service agent; then systemctl stop omb-agent.service || return 1; fi
    if has_service dnsmasq && [[ ${dnsmasq_was_active:-0} == 1 ]]; then
        systemctl stop omb-dnsmasq.service || return 1
    fi
}

start_services() {
    if has_service boot; then systemctl start omb-boot.service || return 1; fi
    if has_service agent; then systemctl start omb-agent.service || return 1; fi
    if has_service images; then systemctl start omb-images.service || return 1; fi
    if has_service control; then systemctl start omb-control.service || return 1; fi
    if has_service dnsmasq && [[ ${dnsmasq_was_active:-0} == 1 ]]; then
        systemctl start omb-dnsmasq.service || return 1
    fi
}

reset_failed_services() {
    local svc
    for svc in control images boot agent; do
        if has_service "$svc"; then
            systemctl reset-failed "omb-$svc.service" || return 1
        fi
    done
    if has_service dnsmasq && [[ ${dnsmasq_was_active:-0} == 1 ]]; then
        systemctl reset-failed omb-dnsmasq.service || return 1
    fi
}

snapshot_users() {
    sql -d "$1" -Atc "SELECT coalesce(jsonb_agg(jsonb_build_object('id',u.id,'username',u.username,'role',u.role,'enabled',u.enabled,'password_hash',u.password_hash) ORDER BY u.id),'[]'::jsonb) FROM public.users u"
}

snapshot_catalog() {
    sql -d "$1" -At <<'SQL' | sha256sum
SELECT to_jsonb(s) FROM public.storages s ORDER BY s.id;
SELECT to_jsonb(i) FROM public.images i ORDER BY i.storage_id, i.path;
SELECT to_regclass('public.catalog_settings') IS NOT NULL AS has_settings \gset
\if :has_settings
SELECT to_jsonb(c) FROM public.catalog_settings c ORDER BY c.id;
\endif
SQL
}

snapshot_boot() {
    sql -d "$1" -At <<'SQL' | sha256sum
SELECT count(*), coalesce(max(id), 0) FROM public.boot_events;
SELECT id, occurred_at, kind, path, remote_addr, mac FROM (
    SELECT id, occurred_at, kind, path, remote_addr, mac
    FROM public.boot_events ORDER BY id DESC LIMIT 100
) recent ORDER BY id;
SQL
}

backup_database() {
    local database=$1 owner=$2 recovery_db=$3 dump=$4
    runuser -u postgres -- pg_dump --no-password -Fc -d "$database" > "$dump"
    runuser -u postgres -- createdb --owner="$owner" "$recovery_db"
    runuser -u postgres -- pg_restore --no-password --exit-on-error --single-transaction -d "$recovery_db" < "$dump"
}

check_backup_space() {
    if [[ "${scope_databases:-none}" == none ]]; then return 0; fi
    local db_kib free_db free_backup names='' db
    for db in omb_control omb_images omb_boot; do
        if has_database "$db"; then
            names+="${names:+,}'$db'"
        fi
    done
    db_kib=$(sql -d postgres -Atc "SELECT (sum(pg_database_size(datname::text))::bigint+1023)/1024 FROM pg_database WHERE datname IN ($names)")
    free_db=$(df -Pk /var/lib/postgresql | awk 'NR==2 {print $4}')
    free_backup=$(df -Pk "$backups" | awk 'NR==2 {print $4}')
    [[ "$db_kib" =~ ^[0-9]+$ && "$free_db" =~ ^[0-9]+$ && "$free_backup" =~ ^[0-9]+$ ]] ||
        fail 'Could not check disk space for the backup and restored database.'
    (( free_db > db_kib + 65536 && free_backup > db_kib + 65536 )) ||
        fail 'Insufficient space for the backup and restored database.'
    if [[ $(stat -c %d /var/lib/postgresql) == "$(stat -c %d "$backups")" ]]; then
        (( free_db > 2 * db_kib + 131072 )) || fail 'Insufficient combined space for backup and restore.'
    fi
}

read_snapshot() {
    [[ "$backup" == "$backups"/offline.* && -d "$backup" && ! -L "$backup" ]] || return 1
    read -r prior target suffix scope_services scope_databases dnsmasq_was_active < "$backup/metadata" || return 1
    [[ "$prior" =~ ^[0-9a-f]{40}$ && "$target" =~ ^[0-9a-f]{40}$ && "$suffix" =~ ^[0-9a-z]+$ &&
       "$scope_services" =~ ^(agent|boot|control|dnsmasq|images)(,(agent|boot|control|dnsmasq|images))*$ &&
       "$scope_databases" =~ ^(none|(omb_boot|omb_control|omb_images)(,(omb_boot|omb_control|omb_images))*)$ &&
       "$dnsmasq_was_active" =~ ^[01]$ ]] || return 1
    [[ -f "$backup/omb.deb" && -f "$backup/omb.deb.sha256" ]] || return 1
    restored_control="omb_restore_control_$suffix"
    retained_control="omb_retained_control_$suffix"
    restored_images="omb_restore_images_$suffix"
    retained_images="omb_retained_images_$suffix"
    restored_boot="omb_restore_boot_$suffix"
    retained_boot="omb_retained_boot_$suffix"
}

verify_restored_data() {
    if has_database omb_control; then
        snapshot_users "$restored_control" > "$work/users-restored" || return 1
        cmp -s "$backup/users.json" "$work/users-restored" || return 1
    fi
    if has_database omb_images; then
        snapshot_catalog "$restored_images" > "$work/catalog-restored" || return 1
        cmp -s "$backup/catalog.sha256" "$work/catalog-restored" || return 1
    fi
    if has_database omb_boot; then
        snapshot_boot "$restored_boot" > "$work/boot-restored" || return 1
        cmp -s "$backup/boot.sha256" "$work/boot-restored" || return 1
    fi
}

swap_databases() {
    if [[ "${scope_databases:-none}" == none ]]; then return 0; fi
    local existing names='' expected=0 has_ctrl=false has_img=false has_bt=false
    if has_database omb_control; then
        names+="${names:+,}'$restored_control','$retained_control'"
        ((expected += 1))
        has_ctrl=true
    fi
    if has_database omb_images; then
        names+="${names:+,}'$restored_images','$retained_images'"
        ((expected += 1))
        has_img=true
    fi
    if has_database omb_boot; then
        names+="${names:+,}'$restored_boot','$retained_boot'"
        ((expected += 1))
        has_bt=true
    fi
    existing=$(sql -d postgres -Atc "SELECT count(*) FILTER (WHERE left(datname,12)='omb_restore_'),
        count(*) FILTER (WHERE left(datname,13)='omb_retained_' AND NOT datallowconn), count(*)
        FROM pg_database WHERE datname IN ($names)") || return 1
    if [[ "$existing" == "$expected|0|$expected" ]]; then
        verify_restored_data || { echo 'Recovery databases differ from the backup.' >&2; return 1; }
        sql -d postgres \
            -v has_control="$has_ctrl" -v restored_control="$restored_control" -v retained_control="$retained_control" \
            -v has_images="$has_img" -v restored_images="$restored_images" -v retained_images="$retained_images" \
            -v has_boot="$has_bt" -v restored_boot="$restored_boot" -v retained_boot="$retained_boot" <<'SQL'
BEGIN;
\if :has_control
ALTER DATABASE omb_control RENAME TO :"retained_control";
ALTER DATABASE :"restored_control" RENAME TO omb_control;
ALTER DATABASE :"retained_control" WITH ALLOW_CONNECTIONS false;
\endif
\if :has_images
ALTER DATABASE omb_images RENAME TO :"retained_images";
ALTER DATABASE :"restored_images" RENAME TO omb_images;
ALTER DATABASE :"retained_images" WITH ALLOW_CONNECTIONS false;
\endif
\if :has_boot
ALTER DATABASE omb_boot RENAME TO :"retained_boot";
ALTER DATABASE :"restored_boot" RENAME TO omb_boot;
ALTER DATABASE :"retained_boot" WITH ALLOW_CONNECTIONS false;
\endif
COMMIT;
SQL
    elif [[ "$existing" != "0|$expected|$expected" ]]; then
        echo 'Recovery databases are missing or ambiguous.' >&2
        return 1
    fi
}

restore_snapshot() {
    local installed previous_version
    read_snapshot || return 1
    installed=$(cat "$state/COMMIT") || return 1
    [[ "$installed" == "$prior" || "$installed" == "$target" ]] || return 1
    if [[ ! -e "$backup/READY" ]]; then
        # No migration or package replacement is allowed until the restore has been verified.
        [[ "$installed" == "$prior" ]] || return 1
        start_services && health || return 1
        unlink "$state/PENDING" || return 1
        printf 'Restarted %s. The interrupted backup had not reached the migration step.\n' "$prior"
        return 0
    fi
    if [[ -f "$backup/secrets.key" ]]; then
        cmp -s "$etc_omb_dir/secrets.key" "$backup/secrets.key" || {
            echo 'Integration key changed. Restore it deliberately before attempting rollback.' >&2
            return 1
        }
    fi
    if [[ -f "$backup/menu.ipxe" ]] && systemctl is-active --quiet omb-boot.service; then
        boot_menu > "$work/menu-before-rollback" || return 1
        cmp -s "$work/menu-before-rollback" "$backup/menu.ipxe" || {
            echo 'The published menu changed. A coordinated menu and database restore is required.' >&2
            return 1
        }
    fi
    verify_sha256 "$(awk '{print $1}' "$backup/omb.deb.sha256")" "$backup/omb.deb" || return 1
    previous_version=$(package_identity "$backup/omb.deb" "$prior") || return 1

    # PENDING also covers an interrupted rollback. Restore can resume after the DB rename.
    write_state PENDING "$backup" || return 1
    stop_services || return 1
    if ! installed_package_matches "$previous_version" "$prior" "$backup/omb.deb"; then
        install_packages '' "$backup/omb.deb" --allow-downgrades --reinstall || return 1
        installed_package_matches "$previous_version" "$prior" "$backup/omb.deb" || return 1
    fi
    systemctl daemon-reload || return 1
    swap_databases || return 1
    reset_failed_services || return 1
    start_services || return 1
    health || return 1
    if [[ -f "$backup/menu.ipxe" ]]; then
        boot_menu > "$work/menu-after-rollback" || return 1
        cmp -s "$backup/menu.ipxe" "$work/menu-after-rollback" || return 1
    fi
    write_current_deb "$backup/omb.deb" || return 1
    write_state PACKAGE "$previous_version" || return 1
    write_state PACKAGE_SHA256 "$(cut -d ' ' -f 1 "$backup/omb.deb.sha256")" || return 1
    if [[ -f "$backup/channel" ]]; then
        write_state CHANNEL "$(cat "$backup/channel")" || return 1
    fi
    write_state COMMIT "$prior" || return 1
    unlink "$state/PENDING" || return 1
    printf 'Restored %s (services: %s; databases: %s).\n' "$prior" "$scope_services" "$scope_databases"
    if has_database omb_control; then
        printf 'The later Control database was retained as %s with connections disabled.\n' "$retained_control"
    fi
    if has_database omb_images; then
        printf 'The later Images database was retained as %s with connections disabled.\n' "$retained_images"
    fi
    if has_database omb_boot; then
        printf 'The later Boot database was retained as %s with connections disabled.\n' "$retained_boot"
    fi
}

upgrade() {
    local repo=$1 key=$2
    local pkg_path pkg_sha new_version to target_channel
    local old_version old_sha from prior_channel kind field1 suffix
    assert_no_pending "$state"
    check_address
    [[ -z $(dpkg --audit) ]] || fail 'The host package database needs attention before the update.'

    IFS=$'\t' read -r pkg_path pkg_sha new_version to target_channel <<< "$(resolve_repo_package "$repo" "$key")"
    stage_deb "$pkg_path" "$pkg_sha" "$work/new.deb"
    [[ $(package_identity "$work/new.deb" "$to") == "$new_version" ]] || fail 'The package does not match the repository metadata.'
    [[ $(package_channel "$work/new.deb") == "$target_channel" ]] || fail 'The package channel does not match the repository metadata.'

    [[ -f "$state/current.deb" && -f "$state/PACKAGE" && -f "$state/PACKAGE_SHA256" && -f "$state/COMMIT" ]] ||
        fail 'Host deployment state is incomplete. Expected current.deb, PACKAGE, PACKAGE_SHA256, and COMMIT.'
    old_version=$(cat "$state/PACKAGE")
    old_sha=$(cat "$state/PACKAGE_SHA256")
    from=$(cat "$state/COMMIT")
    prior_channel=$(installed_channel)

    dpkg --compare-versions "$new_version" gt "$old_version" || fail 'The new package version must be greater.'
    check_channel_switch

    verify_sha256 "$old_sha" "$state/current.deb" ||
        fail 'Stored current.deb checksum differs from PACKAGE_SHA256.'
    [[ $(package_identity "$state/current.deb" "$from") == "$old_version" ]] ||
        fail 'Stored current.deb does not match recorded PACKAGE or COMMIT.'
    installed_package_matches "$old_version" "$from" "$state/current.deb" ||
        fail 'The installed package does not match current.deb.'

    dpkg-deb -x "$state/current.deb" "$work/old-package"
    dpkg-deb -x "$work/new.deb" "$work/new-package"

    collect_applied_migrations "$work/applied-migrations"
    plan_migrations "$work/new-package/usr/share/omb/migrations/manifest.json" "$work/applied-migrations" > "$work/pending-migrations"
    compute_update_scope "$work/old-package" "$work/new-package" "$work/pending-migrations" > "$work/scope"

    scope_services=''
    scope_databases=none
    while IFS=$'\t' read -r kind field1; do
        case "$kind" in
            SERVICES) scope_services=$field1 ;;
            DATABASES) scope_databases=$field1 ;;
        esac
    done < "$work/scope"

    printf 'Verified signed update %s (%s) -> %s (%s) (services: %s; databases: %s).\n' \
        "$old_version" "$prior_channel" "$new_version" "$target_channel" "$scope_services" "$scope_databases"

    dnsmasq_was_active=0
    if has_service dnsmasq && systemctl is-active --quiet omb-dnsmasq.service; then
        dnsmasq_was_active=1
    fi
    health

    if [[ ! -d "$backups" ]]; then install -d -m 0700 "$backups"; fi
    check_backup_space
    backup=$(mktemp -d "$backups/offline.XXXXXXXX")
    suffix=$(printf '%s' "${backup##*.}" | tr '[:upper:]' '[:lower:]')
    printf '%s %s %s %s %s %s\n' "$from" "$to" "$suffix" "$scope_services" "$scope_databases" "$dnsmasq_was_active" \
        > "$backup/metadata"
    printf '%s\n' "$prior_channel" > "$backup/channel"
    install -m 0600 "$state/current.deb" "$backup/omb.deb"
    (cd "$backup" && sha256sum omb.deb > omb.deb.sha256)
    read_snapshot

    # Capture published menu before stopping omb-boot if boot is in scope.
    boot_menu > "$backup/menu.ipxe"
    write_state PENDING "$backup"
    upgrade_paused=true
    stop_services

    if has_service control; then
        install -m 0600 "$etc_omb_dir/control.env" "$etc_omb_dir/secrets.key" \
            /var/lib/omb-control/control.crt /var/lib/omb-control/control.key "$backup/"
    fi
    if has_service images; then
        install -m 0600 "$etc_omb_dir/images.env" "$backup/"
    fi
    if has_service boot; then
        install -m 0600 "$etc_omb_dir/boot.env" "$backup/"
    fi
    if has_database omb_control; then
        snapshot_users omb_control > "$backup/users.json"
        backup_database omb_control omb-control "$restored_control" "$backup/control.dump"
    fi
    if has_database omb_images; then
        snapshot_catalog omb_images > "$backup/catalog.sha256"
        backup_database omb_images omb-images "$restored_images" "$backup/images.dump"
    fi
    if has_database omb_boot; then
        snapshot_boot omb_boot > "$backup/boot.sha256"
        backup_database omb_boot omb-boot "$restored_boot" "$backup/boot.dump"
    fi
    verify_restored_data || fail 'Restored data differs from the backup source.'
    : > "$backup/READY"

    install_packages '' "$work/new.deb"
    installed_package_matches "$new_version" "$to" "$work/new.deb" || fail 'The updated package failed verification.'
    systemctl daemon-reload

    apply_pending_migrations /usr/share/omb/migrations "$work/pending-migrations"

    start_services
    health
    if has_database omb_control; then
        snapshot_users omb_control > "$work/users-after"
        cmp -s "$backup/users.json" "$work/users-after" || fail 'User rows changed during the update.'
    fi
    boot_menu > "$work/menu-after"
    cmp -s "$backup/menu.ipxe" "$work/menu-after" || fail 'Published PXE menu changed during the update.'

    write_current_deb "$work/new.deb"
    write_state PACKAGE "$new_version"
    write_state PACKAGE_SHA256 "$pkg_sha"
    write_state CHANNEL "$target_channel"
    write_state COMMIT "$to"
    write_state LAST "$backup"
    unlink "$state/PENDING"
    upgrade_paused=false
    printf 'Updated to %s (%s, %s; services: %s; databases: %s). Verified rollback backup: %s\n' \
        "$new_version" "$target_channel" "$to" "$scope_services" "$scope_databases" "$backup"
}

cleanup() {
    local status=$?
    trap - EXIT
    if $upgrade_paused; then
        if [[ -f "$state/PENDING" ]]; then
            if ! restore_snapshot; then
                echo "Automatic rollback failed. Recovery data and PENDING were kept: $backup" >&2
            fi
        else
            start_services || :
        fi
    fi
    rm -r -- "$work"
    exit "$status"
}

usage() {
    cat >&2 <<'TEXT'
offline-deploy.sh install APT_REPO PUBLIC_KEY MANAGEMENT_IP [PXE_INTERFACE|none]
offline-deploy.sh update [--allow-channel-switch] APT_REPO PUBLIC_KEY MANAGEMENT_IP
offline-deploy.sh rollback MANAGEMENT_IP
TEXT
    exit 2
}

main() {
    local command=${1:-}
    local pos_args=()
    local arg
    if (( $# > 0 )); then shift; fi
    for arg in "$@"; do
        if [[ "$arg" == --allow-channel-switch ]]; then
            allow_channel_switch=true
        else
            pos_args+=("$arg")
        fi
    done
    if $allow_channel_switch; then
        case "$command" in
            update) ;;
            *) usage ;;
        esac
    fi
    set -- "$command" ${pos_args[@]+"${pos_args[@]}"}
    case "$command:$#" in
        install:4|install:5|update:4|rollback:2) ;;
        *) usage ;;
    esac
    shift
    [[ $(id -u) == 0 ]] || fail 'This command needs sudo.'
    export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
    exec 9>"$lock_file"
    flock -n 9 || fail 'Another OMB deployment operation is running.'
    work=$(mktemp -d /var/tmp/omb-deploy.XXXXXXXX)
    upgrade_paused=false
    trap cleanup EXIT
    trap 'exit 130' INT
    trap 'exit 143' TERM
    # shellcheck source=offline-install-lib.sh
    source "$script_dir/offline-install-lib.sh"
    case "$command" in
        install)
            address=$3
            check_address
            fresh_install "$1" "$2" "${4:-none}"
            ;;
        update)
            address=$3
            upgrade "$1" "$2"
            ;;
        rollback)
            address=$1
            check_address
            if [[ -f "$state/PENDING" ]]; then
                backup=$(cat "$state/PENDING")
            else
                backup=$(cat "$state/LAST")
                read_snapshot || fail 'Invalid rollback metadata.'
                [[ $(cat "$state/COMMIT") == "$target" ]] || fail 'No rollback for the currently installed release.'
            fi
            restore_snapshot || fail "Rollback did not finish. Recovery data: $backup"
            ;;
    esac
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
