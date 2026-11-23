#!/usr/bin/env bash
# Trusted launcher for verified Ubuntu installation and application updates.
set -euo pipefail
umask 077

state=/var/lib/omb-deployment
backups=/var/backups/omb
binary_dir=/usr/local/bin
package_mode=false
allow_channel_switch=false
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
verifier="$script_dir/offline-release.py"
deb_tool="$script_dir/offline-deb.py"

fail() { printf '%s\n' "$*" >&2; exit 1; }

resolve_repo_package() {
    local repo=$1 key=$2 version=$3 line fname digest commit channel
    line=$(python3 "$deb_tool" verify-repo "$repo" --public-key "$key" --version "$version") ||
        fail "Repository verification failed for version $version."
    IFS=$'\t' read -r fname digest commit channel <<< "$line"
    [[ -n "$fname" && "$digest" =~ ^[0-9a-f]{64}$ && -f "$repo/$fname" ]] ||
        fail "Invalid package metadata from repository for version $version."
    printf '%s\t%s\n' "$repo/$fname" "$digest"
}

assert_no_pending() {
    [[ ! -e "$1/PENDING" ]] || fail 'An update is pending. Run rollback before starting another operation.'
}

stage_deb() {
    local source=$1 digest=$2 dest=$3
    [[ "$digest" =~ ^[0-9a-f]{64}$ ]] || fail 'Expected a lowercase SHA-256 for the package.'
    cp -- "$source" "$dest"
    printf '%s  %s\n' "$digest" "$dest" | sha256sum --check --status || fail 'Package checksum differs.'
}

package_identity() {
    local deb=$1 commit=$2
    [[ $(dpkg-deb -f "$deb" Package) == omb && $(dpkg-deb -f "$deb" Architecture) == amd64 &&
       $(dpkg-deb -f "$deb" X-OMB-Commit) == "$commit" ]] || fail 'The package does not match the release.'
    dpkg-deb -f "$deb" Version
}

match_package_payload() {
    local deb=$1 root=$2 base=$3 update=${4:-}
    dpkg-deb -x "$deb" "$root"
    python3 - "$root" "$base" "$update" <<'PY'
from pathlib import Path
import sys

root = Path(sys.argv[1])
base = Path(sys.argv[2])
update = Path(sys.argv[3]) if sys.argv[3] else None
ubuntu_helper = "/usr/lib/omb/omb-prepare-ubuntu-source"


def overlay(rel):
    if update is not None:
        candidate = update / rel
        if candidate.is_file():
            return candidate
    return base / rel


def package_unit(name, content):
    lines = []
    for line in content.splitlines(keepends=True):
        if line.startswith("ExecStart=/usr/local/bin/"):
            line = line.replace("ExecStart=/usr/local/bin/", "ExecStart=/usr/bin/", 1)
            if name == "omb-agent.service":
                line = f"{line.rstrip()} -ubuntu-source-script {ubuntu_helper}\n"
        lines.append(line)
    return "".join(lines)


expected_bins = sorted(p.name for p in (base / "bin").glob("omb-*"))
actual_bins = sorted(p.name for p in (root / "usr/bin").glob("*"))
if actual_bins != expected_bins:
    sys.exit("Package binaries do not match the expected inventory.")
for name in expected_bins:
    if overlay(f"bin/{name}").read_bytes() != (root / "usr/bin" / name).read_bytes():
        sys.exit(f"Package binary differs from the signed archive: {name}.")

expected_units = sorted(p.name for p in (base / "deploy/systemd").glob("omb-*.service"))
actual_units = sorted(p.name for p in (root / "usr/lib/systemd/system").glob("*"))
if actual_units != expected_units:
    sys.exit("Package systemd units do not match the expected inventory.")
for name in expected_units:
    expected = package_unit(name, overlay(f"deploy/systemd/{name}").read_text()).encode()
    if expected != (root / "usr/lib/systemd/system" / name).read_bytes():
        sys.exit(f"Package unit differs from the signed archive: {name}.")

helpers = {
    "omb-prepare-ubuntu-source": "scripts/prepare-ubuntu-source.sh",
    "fetch-ipxe": "scripts/fetch-ipxe.sh",
}
actual_helpers = sorted(p.name for p in (root / "usr/lib/omb").glob("*"))
if actual_helpers != sorted(helpers):
    sys.exit("Package helpers do not match the expected inventory.")
for dest_name, rel in helpers.items():
    if overlay(rel).read_bytes() != (root / "usr/lib/omb" / dest_name).read_bytes():
        sys.exit(f"Package helper differs from the signed archive: {dest_name}.")
PY
}

installed_package_matches() {
    local record verification
    record=$(dpkg-query -W -f='${db:Status-Status} ${Version} ${X-OMB-Commit}' omb) || return 1
    [[ "$record" == "installed $1 $2" ]] || return 1
    dpkg-deb -I "$3" md5sums | cmp -s - /var/lib/dpkg/info/omb.md5sums || return 1
    verification=$(dpkg --verify omb) || return 1
    [[ -z "$verification" ]]
}

inspect_update() {
    python3 - "$1" <<'PY'
import json, sys

BINARY_TO_SVC = {
    "bin/omb-control": "control",
    "bin/omb-controlctl": "control",
    "bin/omb-images": "images",
    "bin/omb-imagesctl": "images",
    "bin/omb-boot": "boot",
    "bin/omb-agent": "agent",
    "bin/omb-agentctl": "agent",
    "bin/omb-winiso-prepare": "agent",
}
UNIT_TO_SVC = {
    "deploy/systemd/omb-control.service": "control",
    "deploy/systemd/omb-images.service": "images",
    "deploy/systemd/omb-boot.service": "boot",
    "deploy/systemd/omb-agent.service": "agent",
    "deploy/systemd/omb-winiso@.service": "agent",
    "deploy/systemd/omb-dnsmasq.service": "dnsmasq",
}
HELPER_TO_SVC = {
    "scripts/prepare-ubuntu-source.sh": "agent",
    "scripts/fetch-ipxe.sh": "agent",
}
DB_TO_SVC = {
    "omb_control": "control",
    "omb_images": "images",
    "omb_boot": "boot",
}
METADATA_FILES = {"README.md", "go.mod", "go.sum", "provenance.json"}

with open(sys.argv[1], encoding="utf-8") as f:
    data = json.load(f)

if not data.get("from_commit"):
    sys.exit("Unsupported update: missing from_commit.")

services = set()
databases = set()
binaries = set()
package_only = False
migration_paths = set()

for step in data.get("migrations", []):
    db = step["database"]
    if db not in DB_TO_SVC:
        sys.exit(f"Unsupported migration database: {db}.")
    services.add(DB_TO_SVC[db])
    databases.add(db)
    migration_paths.add(step["path"])

for name in data.get("files", {}):
    if name in BINARY_TO_SVC:
        services.add(BINARY_TO_SVC[name])
        binaries.add(name.split("/", 1)[1])
    elif name in UNIT_TO_SVC:
        services.add(UNIT_TO_SVC[name])
        package_only = True
    elif name in HELPER_TO_SVC:
        services.add(HELPER_TO_SVC[name])
        package_only = True
    elif name in migration_paths or name in METADATA_FILES:
        continue
    else:
        sys.exit(f"Unsupported update file: {name}.")

if not services:
    sys.exit("Unsupported update: no OMB services or databases affected.")

print("SERVICES", ",".join(sorted(services)), sep="\t")
print("DATABASES", ",".join(sorted(databases)) if databases else "none", sep="\t")
print("BINARIES", ",".join(sorted(binaries)) if binaries else "none", sep="\t")
print("PACKAGE_ONLY", "1" if package_only else "0", sep="\t")
for step in data.get("migrations", []):
    print("MIGRATION", step["database"], step["path"], sep="\t")
PY
}

has_service() { [[ ",${scope_services:-}," == *",$1,"* ]]; }

has_database() { [[ "${scope_databases:-none}" != none && ",${scope_databases}," == *",$1,"* ]]; }

manifest_commit() {
    python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["commit"])' "$1/manifest.json"
}

manifest_channel() {
    python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("channel","stable"))' "$1/manifest.json"
}

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

extract() {
    local archive=$1 dest=$2 key=$3
    shift 3
    python3 "$verifier" verify "$archive" --public-key "$key" --extract "$dest" "$@" >/dev/null
}

prepare_update() {
    local base_archive=$1 update_archive=$2 key=$3
    local kind field1 field2 bin_list='' package_only=0
    shift 3
    assert_no_pending "$state"
    extract "$base_archive" "$work/base" "$key"
    python3 -c 'import json,sys; sys.exit(json.load(open(sys.argv[1]))["from_commit"] is not None)' \
        "$work/base/manifest.json" || fail 'The base archive is not a fresh installation.'
    from=$(manifest_commit "$work/base")
    extract "$update_archive" "$work/update" "$key" --expect-from "$from" "$@"
    to=$(manifest_commit "$work/update")
    prior_channel=$(installed_channel)
    target_channel=$(manifest_channel "$work/update")
    inspect_update "$work/update/manifest.json" > "$work/plan"
    : > "$work/migrations"
    scope_services=''
    scope_databases=none
    binaries=()
    while IFS=$'\t' read -r kind field1 field2; do
        case "$kind" in
            SERVICES) scope_services=$field1 ;;
            DATABASES) scope_databases=$field1 ;;
            BINARIES) bin_list=$field1 ;;
            PACKAGE_ONLY) package_only=$field1 ;;
            MIGRATION) printf '%s\t%s\n' "$field1" "$field2" >> "$work/migrations" ;;
        esac
    done < "$work/plan"
    if [[ "$bin_list" != none && -n "$bin_list" ]]; then
        IFS=',' read -r -a binaries <<< "$bin_list"
    fi
    if ! $package_mode && [[ "$package_only" == 1 ]]; then
        fail 'Updates that change systemd units or helpers require update-deb.'
    fi
    local f
    for f in "$work/base"/bin/*; do
        cmp -s "$f" "$binary_dir/${f##*/}" || fail "Installed binary differs from the base archive: ${f##*/}."
    done
    printf 'Verified installed binaries and signed update %s -> %s (services: %s; databases: %s).\n' \
        "$from" "$to" "$scope_services" "$scope_databases"
}

write_state() {
    local name=$1 value=$2 tmp
    tmp=$(mktemp "$state/.state.XXXXXXXX")
    printf '%s\n' "$value" > "$tmp"
    mv -T -- "$tmp" "$state/$name"
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

install_binaries() {
    local src=$1 binary tmp
    for binary in "${binaries[@]}"; do
        tmp=$(mktemp /usr/local/bin/.omb-replacement.XXXXXXXX) || return 1
        install -m 0755 "$src/$binary" "$tmp" && mv -T -- "$tmp" "/usr/local/bin/$binary" || return 1
    done
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
    local line
    [[ "$backup" == "$backups"/offline.* && -d "$backup" && ! -L "$backup" ]] || return 1
    read -r prior target suffix scope_services scope_databases dnsmasq_was_active < "$backup/metadata" || return 1
    [[ "$prior" =~ ^[0-9a-f]{40}$ && "$target" =~ ^[0-9a-f]{40}$ && "$suffix" =~ ^[0-9a-z]+$ &&
       "$scope_services" =~ ^(agent|boot|control|dnsmasq|images)(,(agent|boot|control|dnsmasq|images))*$ &&
       "$scope_databases" =~ ^(none|(omb_boot|omb_control|omb_images)(,(omb_boot|omb_control|omb_images))*)$ &&
       "$dnsmasq_was_active" =~ ^[01]$ ]] || return 1
    restored_control="omb_restore_control_$suffix"
    retained_control="omb_retained_control_$suffix"
    restored_images="omb_restore_images_$suffix"
    retained_images="omb_retained_images_$suffix"
    restored_boot="omb_restore_boot_$suffix"
    retained_boot="omb_retained_boot_$suffix"
    binaries=()
    if [[ -f "$backup/binaries" ]]; then
        while IFS= read -r line; do
            [[ -n "$line" ]] && binaries+=("$line")
        done < "$backup/binaries"
    fi
    if [[ -f "$backup/omb.deb" ]]; then
        package_mode=true
        binary_dir=/usr/bin
    elif [[ -e "$state/PACKAGE" ]]; then
        return 1
    fi
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
    local installed binary previous_version
    read_snapshot || return 1
    installed=$(cat "$state/COMMIT") || return 1
    [[ "$installed" == "$prior" || "$installed" == "$target" ]] || return 1
    if [[ ! -e "$backup/READY" ]]; then
        # No migration or binary replacement is allowed until the restore has been verified.
        [[ "$installed" == "$prior" ]] || return 1
        for binary in ${binaries[@]+"${binaries[@]}"}; do
            cmp -s "$backup/$binary" "$binary_dir/$binary" || return 1
        done
        start_services && health || return 1
        unlink "$state/PENDING" || return 1
        printf 'Restarted %s. The interrupted backup had not reached the migration step.\n' "$prior"
        return 0
    fi
    if [[ -f "$backup/secrets.key" ]]; then
        cmp -s /etc/omb/secrets.key "$backup/secrets.key" || {
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
    if $package_mode; then
        (cd "$backup" && sha256sum --check --status omb.deb.sha256) || return 1
        previous_version=$(package_identity "$backup/omb.deb" "$prior") || return 1
    fi

    # PENDING also covers an interrupted rollback. Restore can resume after the DB rename.
    write_state PENDING "$backup" || return 1
    stop_services || return 1
    if $package_mode; then
        if ! installed_package_matches "$previous_version" "$prior" "$backup/omb.deb"; then
            install_packages '' "$backup/omb.deb" --allow-downgrades --reinstall || return 1
            installed_package_matches "$previous_version" "$prior" "$backup/omb.deb" || return 1
        fi
        systemctl daemon-reload || return 1
    fi
    swap_databases || return 1
    if ! $package_mode && (( ${#binaries[@]} > 0 )); then install_binaries "$backup" || return 1; fi
    reset_failed_services || return 1
    start_services || return 1
    health || return 1
    if [[ -f "$backup/menu.ipxe" ]]; then
        boot_menu > "$work/menu-after-rollback" || return 1
        cmp -s "$backup/menu.ipxe" "$work/menu-after-rollback" || return 1
    fi
    if $package_mode; then
        write_state PACKAGE "$previous_version" || return 1
        write_state PACKAGE_SHA256 "$(cut -d ' ' -f 1 "$backup/omb.deb.sha256")" || return 1
    fi
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
    prepare_update "$1" "$2" "$3" --check-host
    check_address
    dnsmasq_was_active=0
    if has_service dnsmasq && systemctl is-active --quiet omb-dnsmasq.service; then
        dnsmasq_was_active=1
    fi
    health
    local old_version new_version
    if $package_mode; then
        [[ -z $(dpkg --audit) ]] || fail 'The host package database needs attention before the update.'
        stage_deb "$4" "$5" "$work/old.deb"
        stage_deb "$6" "$7" "$work/new.deb"
        old_version=$(package_identity "$work/old.deb" "$from")
        new_version=$(package_identity "$work/new.deb" "$to")
        dpkg --compare-versions "$new_version" gt "$old_version" || fail 'The new package version must be greater.'
        [[ $(cat "$state/PACKAGE") == "$old_version" && $(cat "$state/PACKAGE_SHA256") == "$5" ]] &&
            installed_package_matches "$old_version" "$from" "$work/old.deb" ||
            fail 'The installed package does not match the base release.'
        match_package_payload "$work/old.deb" "$work/old-package" "$work/base"
        match_package_payload "$work/new.deb" "$work/new-package" "$work/base" "$work/update"
        diff -qr --exclude=release.json --exclude=upgrade \
            "$work/old-package/usr/share/omb" "$work/new-package/usr/share/omb" >/dev/null ||
            fail 'The package update changes provisioning inputs under /usr/share/omb.'
        target_channel=$(package_channel "$work/new.deb")
    fi
    check_channel_switch
    if [[ ! -d "$backups" ]]; then install -d -m 0700 "$backups"; fi
    check_backup_space
    backup=$(mktemp -d "$backups/offline.XXXXXXXX")
    local suffix binary database migration
    suffix=$(printf '%s' "${backup##*.}" | tr '[:upper:]' '[:lower:]')
    printf '%s %s %s %s %s %s\n' "$from" "$to" "$suffix" "$scope_services" "$scope_databases" "$dnsmasq_was_active" \
        > "$backup/metadata"
    printf '%s\n' "$prior_channel" > "$backup/channel"
    if (( ${#binaries[@]} > 0 )); then
        printf '%s\n' "${binaries[@]}" > "$backup/binaries"
    fi
    if $package_mode; then
        install -m 0600 "$work/old.deb" "$backup/omb.deb"
        (cd "$backup" && sha256sum omb.deb > omb.deb.sha256)
    fi
    read_snapshot
    for binary in ${binaries[@]+"${binaries[@]}"}; do
        install -m 0700 "$binary_dir/$binary" "$backup/"
    done
    # Capture published menu before stopping omb-boot if boot is in scope.
    boot_menu > "$backup/menu.ipxe"
    write_state PENDING "$backup"
    upgrade_paused=true
    stop_services
    if has_service control; then
        install -m 0600 /etc/omb/control.env /etc/omb/secrets.key \
            /var/lib/omb-control/control.crt /var/lib/omb-control/control.key "$backup/"
    fi
    if has_service images; then
        install -m 0600 /etc/omb/images.env "$backup/"
    fi
    if has_service boot; then
        install -m 0600 /etc/omb/boot.env "$backup/"
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

    while IFS=$'\t' read -r database migration; do
        [[ -n "$database" ]] || continue
        runuser -u "${database/_/-}" -- psql -Xw -v ON_ERROR_STOP=1 -d "$database" < "$work/update/$migration"
    done < "$work/migrations"
    if $package_mode; then
        install_packages '' "$work/new.deb"
        installed_package_matches "$new_version" "$to" "$work/new.deb" || fail 'The updated package failed verification.'
        systemctl daemon-reload
    elif (( ${#binaries[@]} > 0 )); then
        install_binaries "$work/update/bin"
    fi
    start_services
    health
    if has_database omb_control; then
        snapshot_users omb_control > "$work/users-after"
        cmp -s "$backup/users.json" "$work/users-after" || fail 'User rows changed during the update.'
    fi
    boot_menu > "$work/menu-after"
    cmp -s "$backup/menu.ipxe" "$work/menu-after" || fail 'Published PXE menu changed during the update.'
    if $package_mode; then
        write_state PACKAGE "$new_version"
        write_state PACKAGE_SHA256 "$7"
    fi
    write_state CHANNEL "$target_channel"
    write_state COMMIT "$to"
    write_state LAST "$backup"
    unlink "$state/PENDING"
    upgrade_paused=false
    printf 'Updated to %s (services: %s; databases: %s). Verified rollback backup: %s\n' \
        "$to" "$scope_services" "$scope_databases" "$backup"
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
offline-deploy.sh check-update [--allow-channel-switch] BASE_ARCHIVE UPDATE_ARCHIVE PUBLIC_KEY
offline-deploy.sh install INSTALL_ARCHIVE PUBLIC_KEY MANAGEMENT_IP [PXE_INTERFACE|none]
offline-deploy.sh install-deb INSTALL_ARCHIVE PUBLIC_KEY PACKAGE SHA256 MANAGEMENT_IP [PXE_INTERFACE|none]
offline-deploy.sh install-deb-repo INSTALL_ARCHIVE PUBLIC_KEY APT_REPO VERSION MANAGEMENT_IP [PXE_INTERFACE|none]
offline-deploy.sh update [--allow-channel-switch] BASE_ARCHIVE UPDATE_ARCHIVE PUBLIC_KEY MANAGEMENT_IP
offline-deploy.sh update-deb [--allow-channel-switch] BASE_ARCHIVE UPDATE_ARCHIVE PUBLIC_KEY OLD_PACKAGE OLD_SHA256 NEW_PACKAGE NEW_SHA256 MANAGEMENT_IP
offline-deploy.sh update-deb-repo [--allow-channel-switch] BASE_ARCHIVE UPDATE_ARCHIVE PUBLIC_KEY APT_REPO OLD_VERSION NEW_VERSION MANAGEMENT_IP
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
            check-update|update|update-deb|update-deb-repo) ;;
            *) usage ;;
        esac
    fi
    set -- "$command" "${pos_args[@]}"
    case "$command:$#" in
        check-update:4|install:4|install:5|install-deb:6|install-deb:7|install-deb-repo:6|install-deb-repo:7|update:5|update-deb:9|update-deb-repo:8|rollback:2) ;;
        *) usage ;;
    esac
    shift
    if [[ "$command" != check-update ]]; then
        [[ $(id -u) == 0 ]] || fail 'This command needs sudo.'
        export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
        exec 9>/run/lock/omb-offline-deploy.lock
        flock -n 9 || fail 'Another OMB deployment operation is running.'
    fi
    work=$(mktemp -d /var/tmp/omb-deploy.XXXXXXXX)
    upgrade_paused=false
    trap cleanup EXIT
    trap 'exit 130' INT
    trap 'exit 143' TERM
    # shellcheck source=offline-install-lib.sh
    source "$script_dir/offline-install-lib.sh"
    case "$command" in
        check-update)
            [[ ! -e "$state/PACKAGE" ]] || fail 'This host is managed by APT.'
            prepare_update "$1" "$2" "$3"
            check_channel_switch
            ;;
        install)
            address=$3
            check_address
            fresh_install "$1" "$2" "${4:-none}"
            ;;
        install-deb)
            package_mode=true
            binary_dir=/usr/bin
            address=$5
            check_address
            fresh_install "$1" "$2" "${6:-none}" "$3" "$4"
            ;;
        install-deb-repo)
            local pkg_path pkg_sha
            IFS=$'\t' read -r pkg_path pkg_sha <<< "$(resolve_repo_package "$3" "$2" "$4")"
            package_mode=true
            binary_dir=/usr/bin
            address=$5
            check_address
            fresh_install "$1" "$2" "${6:-none}" "$pkg_path" "$pkg_sha"
            ;;
        update)
            [[ ! -e "$state/PACKAGE" ]] || fail 'Use update-deb on an APT-managed installation.'
            address=$4
            upgrade "$1" "$2" "$3"
            ;;
        update-deb)
            [[ -f "$state/PACKAGE" ]] || fail 'This installation is not managed by APT.'
            package_mode=true
            binary_dir=/usr/bin
            address=$8
            upgrade "${@:1:7}"
            ;;
        update-deb-repo)
            [[ -f "$state/PACKAGE" ]] || fail 'This installation is not managed by APT.'
            local old_pkg old_sha new_pkg new_sha
            IFS=$'\t' read -r old_pkg old_sha <<< "$(resolve_repo_package "$4" "$3" "$5")"
            IFS=$'\t' read -r new_pkg new_sha <<< "$(resolve_repo_package "$4" "$3" "$6")"
            package_mode=true
            binary_dir=/usr/bin
            address=$7
            upgrade "$1" "$2" "$3" "$old_pkg" "$old_sha" "$new_pkg" "$new_sha"
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
