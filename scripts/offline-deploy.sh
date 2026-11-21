#!/usr/bin/env bash
# Trusted launcher for verified Ubuntu installation and application updates.
set -euo pipefail
umask 077

state=/var/lib/omb-deployment
backups=/var/backups/omb
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
verifier="$script_dir/offline-release.py"

fail() { printf '%s\n' "$*" >&2; exit 1; }

assert_no_pending() {
    [[ ! -e "$1/PENDING" ]] || fail 'An update is pending. Run rollback before starting another operation.'
}

update_sql() {
    python3 - "$1" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    data = json.load(f)
steps = data["migrations"]
bins = {name for name in data["files"] if name.startswith("bin/")}
databases = {step["database"] for step in steps}
control = {"bin/omb-control", "bin/omb-controlctl"}
images = {"bin/omb-images", "bin/omb-imagesctl"}
supported = {frozenset({"omb_control"}): control,
             frozenset({"omb_control", "omb_images"}): control | images}
if not data["from_commit"] or bins != supported.get(frozenset(databases)):
    sys.exit("Unsupported update: expected Control or Control and Images migrations and their binaries.")
for step in steps:
    print(step["database"], step["path"], sep="\t")
PY
}

select_scope() {
    with_images=$1
    binaries=(omb-control omb-controlctl)
    if $with_images; then
        binaries+=(omb-images omb-imagesctl)
    fi
}

manifest_commit() {
    python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["commit"])' "$1/manifest.json"
}

extract() {
    local archive=$1 dest=$2 key=$3
    shift 3
    python3 "$verifier" verify "$archive" --public-key "$key" --extract "$dest" "$@" >/dev/null
}

prepare_update() {
    local base_archive=$1 update_archive=$2 key=$3
    shift 3
    assert_no_pending "$state"
    extract "$base_archive" "$work/base" "$key"
    python3 -c 'import json,sys; sys.exit(json.load(open(sys.argv[1]))["from_commit"] is not None)' \
        "$work/base/manifest.json" || fail 'The base archive is not a fresh installation.'
    from=$(manifest_commit "$work/base")
    extract "$update_archive" "$work/update" "$key" --expect-from "$from" "$@"
    to=$(manifest_commit "$work/update")
    update_sql "$work/update/manifest.json" > "$work/migrations"
    if grep -q $'^omb_images\t' "$work/migrations"; then
        select_scope true
    else
        select_scope false
    fi
    local f
    for f in "$work/base"/bin/*; do
        cmp -s "$f" "/usr/local/bin/${f##*/}" || fail "Installed binary differs from the base archive: ${f##*/}."
    done
    printf 'Verified installed binaries and signed update %s -> %s.\n' "$from" "$to"
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
    local try
    for try in {1..20}; do
        if curl --noproxy '*' -fsS --max-time 3 --cacert /var/lib/omb-control/control.crt \
            -o /dev/null "https://$address:8443/login" 2>/dev/null &&
            systemctl is-active --quiet omb-control.service; then
            if [[ ${with_images:-false} != true ]] || { systemctl is-active --quiet omb-images.service &&
                curl --noproxy '*' -fsS --max-time 3 --unix-socket /run/omb-images/images.sock \
                    -o /dev/null http://localhost/catalog 2>/dev/null; }; then
                return 0
            fi
        fi
        sleep 1
    done
    echo 'Control HTTPS or Images catalog health check failed.' >&2
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
    systemctl stop omb-control.service || return 1
    if $with_images; then
        systemctl stop omb-images.service || return 1
    fi
}

start_services() {
    if $with_images; then
        systemctl start omb-images.service || return 1
    fi
    systemctl start omb-control.service
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

backup_database() {
    local database=$1 owner=$2 recovery_db=$3 dump=$4
    runuser -u postgres -- pg_dump --no-password -Fc -d "$database" > "$dump"
    runuser -u postgres -- createdb --owner="$owner" "$recovery_db"
    runuser -u postgres -- pg_restore --no-password --exit-on-error --single-transaction -d "$recovery_db" < "$dump"
}

check_backup_space() {
    local db_kib free_db free_backup names="'omb_control'"
    if $with_images; then names+=",'omb_images'"; fi
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
    local scope
    [[ "$backup" == "$backups"/offline.* && -d "$backup" && ! -L "$backup" ]] || return 1
    read -r prior target restored retained scope < "$backup/metadata" || return 1
    [[ "$prior" =~ ^[0-9a-f]{40}$ && "$target" =~ ^[0-9a-f]{40}$ &&
       "$restored" =~ ^omb_restore_[0-9a-z]+$ && "$retained" =~ ^omb_retained_[0-9a-z]+$ ]] || return 1
    case "$scope" in
        '') select_scope false ;;
        images) select_scope true ;;
        *) return 1 ;;
    esac
    restored_images="omb_restore_images_${restored#omb_restore_}"
    retained_images="omb_retained_images_${retained#omb_retained_}"
}

verify_restored_data() {
    snapshot_users "$restored" > "$work/users-restored" || return 1
    cmp -s "$backup/users.json" "$work/users-restored" || return 1
    if $with_images; then
        snapshot_catalog "$restored_images" > "$work/catalog-restored" || return 1
        cmp -s "$backup/catalog.sha256" "$work/catalog-restored" || return 1
    fi
}

swap_databases() {
    local existing names="'$restored','$retained'" expected=1
    if $with_images; then
        names+=",'$restored_images','$retained_images'"
        expected=2
    fi
    existing=$(sql -d postgres -Atc "SELECT count(*) FILTER (WHERE left(datname,12)='omb_restore_'),
        count(*) FILTER (WHERE left(datname,13)='omb_retained_' AND NOT datallowconn), count(*)
        FROM pg_database WHERE datname IN ($names)") || return 1
    if [[ "$existing" == "$expected|0|$expected" ]]; then
        verify_restored_data || { echo 'Recovery databases differ from the backup.' >&2; return 1; }
        sql -d postgres -v restored="$restored" -v retained="$retained" -v images="$with_images" \
            -v restored_images="$restored_images" -v retained_images="$retained_images" <<'SQL'
BEGIN;
ALTER DATABASE omb_control RENAME TO :"retained";
ALTER DATABASE :"restored" RENAME TO omb_control;
ALTER DATABASE :"retained" WITH ALLOW_CONNECTIONS false;
\if :images
ALTER DATABASE omb_images RENAME TO :"retained_images";
ALTER DATABASE :"restored_images" RENAME TO omb_images;
ALTER DATABASE :"retained_images" WITH ALLOW_CONNECTIONS false;
\endif
COMMIT;
SQL
    elif [[ "$existing" != "0|$expected|$expected" ]]; then
        echo 'Recovery databases are missing or ambiguous.' >&2
        return 1
    fi
}

restore_snapshot() {
    local installed binary
    read_snapshot || return 1
    installed=$(cat "$state/COMMIT") || return 1
    [[ "$installed" == "$prior" || "$installed" == "$target" ]] || return 1
    if [[ ! -e "$backup/READY" ]]; then
        # No migration or binary replacement is allowed until the restore has been verified.
        [[ "$installed" == "$prior" ]] || return 1
        for binary in "${binaries[@]}"; do
            cmp -s "$backup/$binary" "/usr/local/bin/$binary" || return 1
        done
        start_services && health || return 1
        unlink "$state/PENDING" || return 1
        printf 'Restarted %s. The interrupted backup had not reached the migration step.\n' "$prior"
        return 0
    fi
    cmp -s /etc/omb/secrets.key "$backup/secrets.key" || {
        echo 'Integration key changed. Restore it deliberately before attempting rollback.' >&2
        return 1
    }
    boot_menu > "$work/menu-before-rollback" || return 1
    cmp -s "$work/menu-before-rollback" "$backup/menu.ipxe" || {
        echo 'The published menu changed. A coordinated menu and database restore is required.' >&2
        return 1
    }

    # PENDING also covers an interrupted rollback. Restore can resume after the DB rename.
    write_state PENDING "$backup" || return 1
    stop_services || return 1
    swap_databases || return 1
    install_binaries "$backup" || return 1
    systemctl reset-failed omb-control.service || return 1
    if $with_images; then
        systemctl reset-failed omb-images.service || return 1
    fi
    start_services || return 1
    health || return 1
    boot_menu > "$work/menu-after-rollback" || return 1
    cmp -s "$backup/menu.ipxe" "$work/menu-after-rollback" || return 1
    write_state COMMIT "$prior" || return 1
    unlink "$state/PENDING" || return 1
    printf 'Restored %s. The later database was retained as %s with connections disabled.\n' "$prior" "$retained"
    if $with_images; then
        printf 'The later Images database was retained as %s with connections disabled.\n' "$retained_images"
    fi
}

upgrade() {
    prepare_update "$1" "$2" "$3" --check-host
    check_address
    health
    if [[ ! -d "$backups" ]]; then install -d -m 0700 "$backups"; fi
    check_backup_space
    backup=$(mktemp -d "$backups/offline.XXXXXXXX")
    local suffix scope='' binary database migration
    suffix=$(printf '%s' "${backup##*.}" | tr '[:upper:]' '[:lower:]')
    if $with_images; then scope=' images'; fi
    printf '%s %s %s %s%s\n' "$from" "$to" "omb_restore_$suffix" "omb_retained_$suffix" "$scope" > "$backup/metadata"
    read_snapshot
    for binary in "${binaries[@]}"; do
        install -m 0700 "/usr/local/bin/$binary" "$backup/"
    done
    write_state PENDING "$backup"
    upgrade_paused=true
    stop_services
    install -m 0600 /etc/omb/control.env /etc/omb/secrets.key \
        /var/lib/omb-control/control.crt /var/lib/omb-control/control.key "$backup/"
    if $with_images; then
        install -m 0600 /etc/omb/images.env "$backup/"
    fi
    boot_menu > "$backup/menu.ipxe"
    snapshot_users omb_control > "$backup/users.json"
    backup_database omb_control omb-control "$restored" "$backup/control.dump"
    if $with_images; then
        snapshot_catalog omb_images > "$backup/catalog.sha256"
        backup_database omb_images omb-images "$restored_images" "$backup/images.dump"
    fi
    verify_restored_data || fail 'Restored data differs from the backup source.'
    : > "$backup/READY"

    while IFS=$'\t' read -r database migration; do
        runuser -u "${database/_/-}" -- psql -Xw -v ON_ERROR_STOP=1 -d "$database" < "$work/update/$migration"
    done < "$work/migrations"
    install_binaries "$work/update/bin"
    start_services
    health
    snapshot_users omb_control > "$work/users-after"
    cmp -s "$backup/users.json" "$work/users-after" || fail 'User rows changed during the update.'
    boot_menu > "$work/menu-after"
    cmp -s "$backup/menu.ipxe" "$work/menu-after" || fail 'Published PXE menu changed during the update.'
    write_state COMMIT "$to"
    write_state LAST "$backup"
    unlink "$state/PENDING"
    upgrade_paused=false
    printf 'Updated to %s. Verified rollback backup: %s\n' "$to" "$backup"
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
offline-deploy.sh check-update BASE_ARCHIVE UPDATE_ARCHIVE PUBLIC_KEY
offline-deploy.sh install INSTALL_ARCHIVE PUBLIC_KEY MANAGEMENT_IP [PXE_INTERFACE|none]
offline-deploy.sh update BASE_ARCHIVE UPDATE_ARCHIVE PUBLIC_KEY MANAGEMENT_IP
offline-deploy.sh rollback MANAGEMENT_IP
TEXT
    exit 2
}

main() {
    local command=${1:-}
    case "$command:$#" in
        check-update:4|install:4|install:5|update:5|rollback:2) ;;
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
    case "$command" in
        check-update) prepare_update "$1" "$2" "$3" ;;
        install)
            address=$3
            check_address
            # shellcheck source=offline-install-lib.sh
            source "$script_dir/offline-install-lib.sh"
            fresh_install "$1" "$2" "${4:-none}"
            ;;
        update)
            address=$4
            upgrade "$1" "$2" "$3"
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
