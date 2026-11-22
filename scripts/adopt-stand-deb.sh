#!/usr/bin/env bash
# One-off tool to adopt an existing manually installed OMB stand (192.168.10.135)
# into APT-managed .deb deployment with full backup and rollback.
set -euo pipefail
umask 077

state=/var/lib/omb-deployment
backups=/var/backups/omb
local_bin=/usr/local/bin
local_libexec=/usr/local/libexec
etc_systemd=/etc/systemd/system
usr_systemd=/usr/lib/systemd/system
win_profiles=/usr/share/omb/windows-profiles
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
deb_tool="$script_dir/offline-deb.py"

OMB_BINARIES=(
    omb-agent omb-agentctl omb-boot omb-control omb-controlctl
    omb-images omb-imagesctl omb-winiso-prepare
)
OMB_UNITS=(
    omb-agent.service omb-boot.service omb-control.service
    omb-dnsmasq.service omb-images.service omb-winiso@.service
)
LEGACY_CONTROL_TABLES=(
    integration_settings mail_outbox mail_tokens oidc_flows recovery_rate_limits
)

fail() { printf '%s\n' "$*" >&2; exit 1; }

sql() { runuser -u postgres -- psql -Xqw -v ON_ERROR_STOP=1 "$@"; }

boot_menu() { curl --noproxy '*' -fsS --max-time 4 http://127.0.0.1:8080/boot/menu.ipxe; }

check_address() {
    python3 - "$address" <<'PY'
import ipaddress, sys
ipaddress.IPv4Address(sys.argv[1])
PY
    ip -o -4 addr show | awk -v ip="$address" '{split($4,a,"/"); if (a[1]==ip) found=1} END {exit !found}' ||
        fail "Management address $address is not assigned to this host."
}

check_host_os() {
    python3 - <<'PY'
import platform
from pathlib import Path
os_release = dict(line.split("=", 1) for line in Path("/etc/os-release").read_text().splitlines()
                  if line and not line.startswith("#") and "=" in line)
actual = (os_release.get("ID", "").strip('"'), os_release.get("VERSION_ID", "").strip('"'), platform.machine())
if actual != ("ubuntu", "26.04", "x86_64"):
    raise SystemExit(f"Unsupported host OS/arch: {actual}")
PY
}

resolve_repo_package() {
    local repo=$1 key=$2 version=$3 line fname digest commit
    line=$(python3 "$deb_tool" verify-repo "$repo" --public-key "$key" --version "$version") ||
        fail "Repository verification failed for version $version."
    IFS=$'\t' read -r fname digest commit <<< "$line"
    [[ -n "$fname" && "$digest" =~ ^[0-9a-f]{64}$ && "$commit" =~ ^[0-9a-f]{40}$ && -f "$repo/$fname" ]] ||
        fail "Invalid package metadata from repository for version $version."
    printf '%s\t%s\t%s\n' "$repo/$fname" "$digest" "$commit"
}

check_deb_metadata() {
    local deb=$1 version=$2 commit=$3
    [[ $(dpkg-deb -f "$deb" Package) == omb && $(dpkg-deb -f "$deb" Architecture) == amd64 &&
       $(dpkg-deb -f "$deb" Version) == "$version" && $(dpkg-deb -f "$deb" X-OMB-Commit) == "$commit" ]] ||
        fail 'Package control metadata does not match the signed repository manifest.'
}

run_apt_deb() (
    set -euo pipefail
    local mode=$1 deb=$2 local_bundle
    local_bundle=$(mktemp -d /var/tmp/omb-adopt-apt.XXXXXXXX)
    trap 'rm -r -- "$local_bundle"' EXIT
    chmod 0755 "$local_bundle"
    install -d -m 0755 "$local_bundle"/{empty,lists,archives}
    : > "$local_bundle/offline.list"
    install -m 0644 "$deb" "$local_bundle/omb.deb"
    cat > "$local_bundle/offline.conf" <<APT
Dir::Etc::Parts "$local_bundle/empty";
Dir::Etc::Main "/dev/null";
Dir::Etc::SourceList "$local_bundle/offline.list";
Dir::Etc::SourceParts "$local_bundle/empty";
Dir::State::Lists "$local_bundle/lists";
Dir::Cache::archives "$local_bundle/archives";
Dir::Cache::pkgcache "";
Dir::Cache::srcpkgcache "";
Acquire::Languages "none";
APT
    apt_local() {
        unshare --net -- env APT_CONFIG="$local_bundle/offline.conf" DEBIAN_FRONTEND=noninteractive \
            LC_ALL=C apt-get "$@"
    }
    apt_local -s --no-install-recommends --no-remove install "$local_bundle/omb.deb" > "$local_bundle/plan"
    awk '/^(Inst|Conf|Remv) / && $2 != "omb" {exit 1}' "$local_bundle/plan" ||
        fail 'Installing omb.deb would modify other system packages.'
    if [[ "$mode" == install ]]; then
        apt_local -y --no-install-recommends --no-remove install "$local_bundle/omb.deb"
    fi
)

stop_omb_services() {
    systemctl stop omb-control.service || return 1
    systemctl stop omb-images.service || return 1
    systemctl stop omb-boot.service || return 1
    systemctl stop omb-agent.service || return 1
    if [[ ${dnsmasq_was_active:-0} == 1 ]]; then
        systemctl stop omb-dnsmasq.service || return 1
    fi
}

start_omb_services() {
    systemctl start omb-boot.service || return 1
    systemctl start omb-agent.service || return 1
    systemctl start omb-images.service || return 1
    systemctl start omb-control.service || return 1
    if [[ ${dnsmasq_was_active:-0} == 1 ]]; then
        systemctl start omb-dnsmasq.service || return 1
    fi
}

health_all() {
    local try
    for try in {1..20}; do
        if systemctl is-active --quiet omb-control.service &&
            curl --noproxy '*' -fsS --max-time 3 --cacert /var/lib/omb-control/control.crt \
                -o /dev/null "https://$address:8443/login" 2>/dev/null &&
            systemctl is-active --quiet omb-images.service &&
            curl --noproxy '*' -fsS --max-time 3 --unix-socket /run/omb-images/images.sock \
                -o /dev/null http://localhost/catalog 2>/dev/null &&
            systemctl is-active --quiet omb-boot.service &&
            boot_menu >/dev/null 2>&1 &&
            systemctl is-active --quiet omb-agent.service &&
            curl --noproxy '*' -fsS --max-time 3 --unix-socket /run/omb/agent.sock \
                -o /dev/null http://localhost/status 2>/dev/null &&
            { [[ ${dnsmasq_was_active:-0} != 1 ]] || systemctl is-active --quiet omb-dnsmasq.service; }; then
            return 0
        fi
        sleep 1
    done
    echo 'OMB service health check failed.' >&2
    return 1
}

verify_fragment_paths() {
    local expected_dir=$1 unit query_unit actual
    for unit in "${OMB_UNITS[@]}"; do
        query_unit=${unit/@.service/@check.service}
        actual=$(systemctl show -p FragmentPath --value "$query_unit") || return 1
        [[ "$actual" == "$expected_dir/$unit" ]] || {
            echo "Unexpected FragmentPath for $unit: $actual (expected $expected_dir/$unit)" >&2
            return 1
        }
    done
}

snapshot_users() {
    sql -d "$1" -Atc "SELECT coalesce(jsonb_agg(to_jsonb(u) ORDER BY u.id),'[]'::jsonb) FROM public.users u"
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

snapshot_catalog_stable() {
    sql -d "$1" -At <<'SQL' | sha256sum
SELECT id, name, kind, root, coalesce(storage_key, ''), server, share, subpath, export_path, nfs_version, uploads_enabled
FROM public.storages ORDER BY id;
SELECT storage_id, path, size_bytes, present
FROM public.images ORDER BY storage_id, path;
SELECT to_regclass('public.catalog_settings') IS NOT NULL AS has_settings \gset
\if :has_settings
SELECT id, rescan_enabled, rescan_interval_minutes FROM public.catalog_settings ORDER BY id;
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

check_backup_space() {
    local db_kib free_db free_backup
    db_kib=$(sql -d postgres -Atc "SELECT (sum(pg_database_size(datname::text))::bigint+1023)/1024 FROM pg_database WHERE datname IN ('omb_control','omb_images','omb_boot')")
    free_db=$(df -Pk /var/lib/postgresql | awk 'NR==2 {print $4}')
    free_backup=$(df -Pk "$backups" | awk 'NR==2 {print $4}')
    [[ "$db_kib" =~ ^[0-9]+$ && "$free_db" =~ ^[0-9]+$ && "$free_backup" =~ ^[0-9]+$ ]] ||
        fail 'Could not check disk space for database backups.'
    (( free_db > db_kib + 131072 && free_backup > db_kib + 131072 )) ||
        fail 'Insufficient free disk space for backup and test restore.'
    if [[ $(stat -c %d /var/lib/postgresql) == "$(stat -c %d "$backups")" ]]; then
        (( free_db > 2 * db_kib + 262144 )) || fail 'Insufficient combined disk space.'
    fi
}

backup_database() {
    local database=$1 owner=$2 recovery_db=$3 dump=$4
    runuser -u postgres -- pg_dump --no-password -Fc -d "$database" > "$dump"
    runuser -u postgres -- createdb --owner="$owner" "$recovery_db"
    runuser -u postgres -- pg_restore --no-password --exit-on-error --single-transaction -d "$recovery_db" < "$dump"
}

report_legacy_control_state() {
    local tbl count
    echo '=== Legacy omb_control table row counts ==='
    for tbl in "${LEGACY_CONTROL_TABLES[@]}"; do
        count=$(sql -d omb_control -Atc "SELECT CASE WHEN to_regclass('public.$tbl') IS NULL THEN 'absent' ELSE (SELECT count(*)::text FROM public.\"$tbl\") END")
        printf '  %s: %s\n' "$tbl" "$count"
    done
    if [[ -d "$etc_systemd/omb-boot.service.d" ]]; then
        echo '=== Detected omb-boot.service.d drop-in (will be preserved) ==='
        ls -la "$etc_systemd/omb-boot.service.d"
    fi
}

run_preflight() {
    local repo=$1 key=$2 version=$3 bin unit
    check_host_os
    check_address
    [[ -z $(dpkg --audit) ]] || fail 'dpkg --audit reported issues.'
    if dpkg-query -W -f='${db:Status-Status}\n' omb 2>/dev/null | grep -qx installed; then
        fail 'Package omb is already installed.'
    fi
    [[ ! -e "$state/COMMIT" && ! -e "$state/PACKAGE" ]] ||
        fail "Deployment state already exists under $state."
    for bin in "${OMB_BINARIES[@]}"; do
        [[ -x "$local_bin/$bin" ]] || fail "Missing manual binary: $local_bin/$bin"
    done
    for unit in "${OMB_UNITS[@]}"; do
        [[ -f "$etc_systemd/$unit" ]] || fail "Missing manual unit: $etc_systemd/$unit"
    done
    IFS=$'\t' read -r pkg_path pkg_sha pkg_commit <<< "$(resolve_repo_package "$repo" "$key" "$version")"
    check_deb_metadata "$pkg_path" "$version" "$pkg_commit"
    run_apt_deb simulate "$pkg_path"
    if [[ ! -d "$backups" ]]; then install -d -m 0700 "$backups"; fi
    check_backup_space
    dnsmasq_was_active=0
    if systemctl is-active --quiet omb-dnsmasq.service; then dnsmasq_was_active=1; fi
    health_all
    report_legacy_control_state
    printf 'Preflight passed: ready to adopt %s (version %s, commit %s, dnsmasq_active=%s).\n' \
        "$pkg_path" "$version" "$pkg_commit" "$dnsmasq_was_active"
}

read_adopt_metadata() {
    [[ -d "$backup" && ! -L "$backup" && -f "$backup/metadata" ]] || return 1
    read -r adopt_version adopt_commit adopt_sha adopt_suffix dnsmasq_was_active < "$backup/metadata" || return 1
    [[ "$adopt_commit" =~ ^[0-9a-f]{40}$ && "$adopt_sha" =~ ^[0-9a-f]{64}$ &&
       "$adopt_suffix" =~ ^[0-9a-z]+$ && "$dnsmasq_was_active" =~ ^[01]$ ]] || return 1
    restored_control="omb_restore_control_$adopt_suffix"
    retained_control="omb_retained_control_$adopt_suffix"
    restored_images="omb_restore_images_$adopt_suffix"
    retained_images="omb_retained_images_$adopt_suffix"
    restored_boot="omb_restore_boot_$adopt_suffix"
    retained_boot="omb_retained_boot_$adopt_suffix"
}

ensure_recovery_db() {
    local recovery_db=$1 owner=$2 dump=$3
    local exists
    exists=$(sql -d postgres -Atc "SELECT count(*) FROM pg_database WHERE datname = '$recovery_db'")
    if [[ "$exists" == 0 ]]; then
        runuser -u postgres -- createdb --owner="$owner" "$recovery_db"
        runuser -u postgres -- pg_restore --no-password --exit-on-error --single-transaction -d "$recovery_db" < "$dump"
    fi
}

run_rollback() {
    local bin unit
    read_adopt_metadata || fail "Invalid adopt backup metadata in $backup."
    stop_omb_services || return 1
    if dpkg-query -W -f='${db:Status-Status}\n' omb 2>/dev/null | grep -Eq '^(installed|unpacked|half-configured|half-installed)$'; then
        dpkg --purge omb || return 1
    fi
    for bin in "${OMB_BINARIES[@]}"; do
        if [[ -f "$backup/local-bin/$bin" ]]; then
            install -m 0755 "$backup/local-bin/$bin" "$local_bin/$bin" || return 1
        fi
    done
    if [[ -f "$backup/local-libexec/omb-prepare-ubuntu-source" ]]; then
        install -d -m 0755 "$local_libexec" || return 1
        install -m 0755 "$backup/local-libexec/omb-prepare-ubuntu-source" "$local_libexec/omb-prepare-ubuntu-source" || return 1
    fi
    for unit in "${OMB_UNITS[@]}"; do
        if [[ -f "$backup/systemd/$unit" ]]; then
            install -m 0644 "$backup/systemd/$unit" "$etc_systemd/$unit" || return 1
        fi
    done
    if [[ -d "$backup/windows-profiles" ]]; then
        install -d -m 0755 "$win_profiles" || return 1
        cp -a "$backup/windows-profiles/." "$win_profiles/" || return 1
    fi
    if [[ -f "$backup/READY" && -f "$backup/control.dump" ]]; then
        ensure_recovery_db "$restored_control" omb-control "$backup/control.dump" || return 1
        ensure_recovery_db "$restored_images" omb-images "$backup/images.dump" || return 1
        ensure_recovery_db "$restored_boot" omb-boot "$backup/boot.dump" || return 1
        sql -d postgres \
            -v rc="$restored_control" -v tc="$retained_control" \
            -v ri="$restored_images" -v ti="$retained_images" \
            -v rb="$restored_boot" -v tb="$retained_boot" <<'SQL'
BEGIN;
ALTER DATABASE omb_control RENAME TO :"tc";
ALTER DATABASE :"rc" RENAME TO omb_control;
ALTER DATABASE :"tc" WITH ALLOW_CONNECTIONS false;
ALTER DATABASE omb_images RENAME TO :"ti";
ALTER DATABASE :"ri" RENAME TO omb_images;
ALTER DATABASE :"ti" WITH ALLOW_CONNECTIONS false;
ALTER DATABASE omb_boot RENAME TO :"tb";
ALTER DATABASE :"rb" RENAME TO omb_boot;
ALTER DATABASE :"tb" WITH ALLOW_CONNECTIONS false;
COMMIT;
SQL
    fi
    rm -f "$state/COMMIT" "$state/PACKAGE" "$state/PACKAGE_SHA256" "$state/PENDING"
    systemctl daemon-reload || return 1
    systemctl reenable omb-boot.service omb-agent.service omb-images.service omb-control.service || return 1
    start_omb_services || return 1
    health_all || return 1
    if [[ -f "$backup/menu.ipxe" ]]; then
        boot_menu > "$work/menu-after-rollback" || return 1
        cmp -s "$backup/menu.ipxe" "$work/menu-after-rollback" || return 1
    fi
    printf 'Rolled back manual OMB installation from %s.\n' "$backup"
}

run_adopt() {
    local repo=$1 key=$2 version=$3 migration_sql=${4:-} bin unit suffix
    if [[ -n "$migration_sql" && ! -f "$migration_sql" ]]; then
        fail "Migration file not found: $migration_sql"
    fi
    run_preflight "$repo" "$key" "$version"
    backup=$(mktemp -d "$backups/adopt-deb.XXXXXXXX")
    suffix=$(printf '%s' "${backup##*.}" | tr '[:upper:]' '[:lower:]')
    printf '%s %s %s %s %s\n' "$version" "$pkg_commit" "$pkg_sha" "$suffix" "$dnsmasq_was_active" > "$backup/metadata"
    read_adopt_metadata

    install -d -m 0700 "$backup"/{local-bin,local-libexec,systemd,windows-profiles,etc-omb,tls}
    for bin in "${OMB_BINARIES[@]}"; do
        install -m 0755 "$local_bin/$bin" "$backup/local-bin/$bin"
    done
    if [[ -f "$local_libexec/omb-prepare-ubuntu-source" ]]; then
        install -m 0755 "$local_libexec/omb-prepare-ubuntu-source" "$backup/local-libexec/omb-prepare-ubuntu-source"
    fi
    for unit in "${OMB_UNITS[@]}"; do
        install -m 0644 "$etc_systemd/$unit" "$backup/systemd/$unit"
    done
    if [[ -d "$etc_systemd/omb-boot.service.d" ]]; then
        cp -a "$etc_systemd/omb-boot.service.d" "$backup/systemd/"
    fi
    if [[ -d "$win_profiles" ]]; then
        cp -a "$win_profiles/." "$backup/windows-profiles/"
    fi
    cp -a /etc/omb/. "$backup/etc-omb/"
    install -m 0600 /var/lib/omb-control/control.crt /var/lib/omb-control/control.key "$backup/tls/"
    boot_menu > "$backup/menu.ipxe"

    adopt_paused=true
    stop_omb_services
    snapshot_users omb_control > "$backup/users.json"
    backup_database omb_control omb-control "$restored_control" "$backup/control.dump"
    snapshot_catalog omb_images > "$backup/catalog.sha256"
    snapshot_catalog_stable omb_images > "$backup/catalog-stable.sha256"
    backup_database omb_images omb-images "$restored_images" "$backup/images.dump"
    snapshot_boot omb_boot > "$backup/boot.sha256"
    backup_database omb_boot omb-boot "$restored_boot" "$backup/boot.dump"
    snapshot_users "$restored_control" > "$work/u-check" && cmp -s "$backup/users.json" "$work/u-check"
    snapshot_catalog "$restored_images" > "$work/c-check" && cmp -s "$backup/catalog.sha256" "$work/c-check"
    snapshot_boot "$restored_boot" > "$work/b-check" && cmp -s "$backup/boot.sha256" "$work/b-check"
    : > "$backup/READY"

    if [[ -n "$migration_sql" ]]; then
        runuser -u omb-control -- psql -Xw -v ON_ERROR_STOP=1 -d omb_control < "$migration_sql"
    fi

    run_apt_deb install "$pkg_path"
    [[ -z $(dpkg --verify omb) ]] || fail 'dpkg --verify omb failed after installation.'

    for unit in "${OMB_UNITS[@]}"; do rm -f "$etc_systemd/$unit"; done
    for bin in "${OMB_BINARIES[@]}"; do rm -f "$local_bin/$bin"; done
    rm -f "$local_libexec/omb-prepare-ubuntu-source"

    systemctl daemon-reload
    systemctl reenable omb-boot.service omb-agent.service omb-images.service omb-control.service
    start_omb_services
    verify_fragment_paths "$usr_systemd"
    health_all

    snapshot_users omb_control > "$work/users-after"
    cmp -s "$backup/users.json" "$work/users-after" || fail 'Users changed during adoption.'
    snapshot_catalog_stable omb_images > "$work/catalog-after"
    cmp -s "$backup/catalog-stable.sha256" "$work/catalog-after" || fail 'Images catalog changed during adoption.'
    boot_menu > "$work/menu-after"
    cmp -s "$backup/menu.ipxe" "$work/menu-after" || fail 'Published PXE menu changed during adoption.'

    install -d -m 0755 "$state"
    printf '%s\n' "$pkg_commit" > "$state/COMMIT"
    printf '%s\n' "$version" > "$state/PACKAGE"
    printf '%s\n' "$pkg_sha" > "$state/PACKAGE_SHA256"
    install -m 0644 /var/lib/omb-control/control.crt "$state/control.crt"

    # Drop temporary verified restore DBs; .dump files remain in $backup.
    sql -d postgres -c "DROP DATABASE IF EXISTS \"$restored_control\";"
    sql -d postgres -c "DROP DATABASE IF EXISTS \"$restored_images\";"
    sql -d postgres -c "DROP DATABASE IF EXISTS \"$restored_boot\";"

    adopt_paused=false
    printf 'Adopted OMB stand into package omb %s (commit %s). Backup: %s\n' "$version" "$pkg_commit" "$backup"
}

cleanup() {
    local status=$?
    trap - EXIT
    if [[ ${adopt_paused:-false} == true ]]; then
        echo 'Adoption failed; attempting automatic rollback to manual installation...' >&2
        if ! run_rollback; then
            echo "Automatic rollback failed. Backup preserved at: $backup" >&2
        fi
    fi
    if [[ -n ${work:-} && -d "$work" ]]; then rm -r -- "$work"; fi
    exit "$status"
}

usage() {
    cat >&2 <<'TEXT'
adopt-stand-deb.sh preflight APT_REPO PUBLIC_KEY VERSION MANAGEMENT_IP
adopt-stand-deb.sh adopt APT_REPO PUBLIC_KEY VERSION MANAGEMENT_IP [OIDC_MIGRATION_SQL]
adopt-stand-deb.sh rollback BACKUP_DIR MANAGEMENT_IP
TEXT
    exit 2
}

main() {
    local command=${1:-}
    case "$command:$#" in
        preflight:5|adopt:5|adopt:6|rollback:3) ;;
        *) usage ;;
    esac
    shift
    [[ $(id -u) == 0 ]] || fail 'This command needs sudo.'
    export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
    exec 9>/run/lock/omb-offline-deploy.lock
    flock -n 9 || fail 'Another OMB deployment operation is running.'
    work=$(mktemp -d /var/tmp/omb-adopt.XXXXXXXX)
    adopt_paused=false
    trap cleanup EXIT
    case "$command" in
        preflight)
            address=$4
            run_preflight "$1" "$2" "$3"
            ;;
        adopt)
            address=$4
            run_adopt "$1" "$2" "$3" "${5:-}"
            ;;
        rollback)
            backup=$1
            address=$2
            check_address
            run_rollback
            ;;
    esac
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
