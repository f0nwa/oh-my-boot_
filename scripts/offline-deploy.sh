#!/usr/bin/env bash
# Trusted launcher for verified Ubuntu installation and application updates.
set -euo pipefail
umask 077

state=/var/lib/omb-deployment
backups=/var/backups/omb
binary_dir=/usr/local/bin
package_mode=false
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
verifier="$script_dir/offline-release.py"

fail() { printf '%s\n' "$*" >&2; exit 1; }

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

match_package_binaries() {
    local deb=$1 root=$2 base=$3 update=${4:-} original name expected
    dpkg-deb -x "$deb" "$root"
    for original in "$base"/bin/omb-*; do
        name=${original##*/}
        expected=$original
        if [[ -n "$update" && -f "$update/bin/$name" ]]; then expected="$update/bin/$name"; fi
        cmp -s "$expected" "$root/usr/bin/$name" || fail "Package binary differs from the signed archive: $name."
    done
}

installed_package_matches() {
    local record verification
    record=$(dpkg-query -W -f='${db:Status-Status} ${Version} ${X-OMB-Commit}' omb) || return 1
    [[ "$record" == "installed $1 $2" ]] || return 1
    dpkg-deb -I "$3" md5sums | cmp -s - /var/lib/dpkg/info/omb.md5sums || return 1
    verification=$(dpkg --verify omb) || return 1
    [[ -z "$verification" ]]
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
        cmp -s "$f" "$binary_dir/${f##*/}" || fail "Installed binary differs from the base archive: ${f##*/}."
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
    if [[ -f "$backup/omb.deb" ]]; then
        package_mode=true
        binary_dir=/usr/bin
    elif [[ -e "$state/PACKAGE" ]]; then
        return 1
    fi
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
    local installed binary previous_version
    read_snapshot || return 1
    installed=$(cat "$state/COMMIT") || return 1
    [[ "$installed" == "$prior" || "$installed" == "$target" ]] || return 1
    if [[ ! -e "$backup/READY" ]]; then
        # No migration or binary replacement is allowed until the restore has been verified.
        [[ "$installed" == "$prior" ]] || return 1
        for binary in "${binaries[@]}"; do
            cmp -s "$backup/$binary" "$binary_dir/$binary" || return 1
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
    if ! $package_mode; then install_binaries "$backup" || return 1; fi
    systemctl reset-failed omb-control.service || return 1
    if $with_images; then
        systemctl reset-failed omb-images.service || return 1
    fi
    start_services || return 1
    health || return 1
    boot_menu > "$work/menu-after-rollback" || return 1
    cmp -s "$backup/menu.ipxe" "$work/menu-after-rollback" || return 1
    if $package_mode; then
        write_state PACKAGE "$previous_version" || return 1
        write_state PACKAGE_SHA256 "$(cut -d ' ' -f 1 "$backup/omb.deb.sha256")" || return 1
    fi
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
        match_package_binaries "$work/old.deb" "$work/old-package" "$work/base"
        match_package_binaries "$work/new.deb" "$work/new-package" "$work/base" "$work/update"
        diff -qr "$work/old-package/usr/lib" "$work/new-package/usr/lib" >/dev/null &&
            diff -qr --exclude=release.json --exclude=upgrade \
                "$work/old-package/usr/share/omb" "$work/new-package/usr/share/omb" >/dev/null ||
            fail 'The package update changes units, helpers, or provisioning inputs.'
    fi
    if [[ ! -d "$backups" ]]; then install -d -m 0700 "$backups"; fi
    check_backup_space
    backup=$(mktemp -d "$backups/offline.XXXXXXXX")
    local suffix scope='' binary database migration
    suffix=$(printf '%s' "${backup##*.}" | tr '[:upper:]' '[:lower:]')
    if $with_images; then scope=' images'; fi
    printf '%s %s %s %s%s\n' "$from" "$to" "omb_restore_$suffix" "omb_retained_$suffix" "$scope" > "$backup/metadata"
    if $package_mode; then
        install -m 0600 "$work/old.deb" "$backup/omb.deb"
        (cd "$backup" && sha256sum omb.deb > omb.deb.sha256)
    fi
    read_snapshot
    for binary in "${binaries[@]}"; do
        install -m 0700 "$binary_dir/$binary" "$backup/"
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
    if $package_mode; then
        install_packages '' "$work/new.deb"
        installed_package_matches "$new_version" "$to" "$work/new.deb" || fail 'The updated package failed verification.'
        systemctl daemon-reload
    else
        install_binaries "$work/update/bin"
    fi
    start_services
    health
    snapshot_users omb_control > "$work/users-after"
    cmp -s "$backup/users.json" "$work/users-after" || fail 'User rows changed during the update.'
    boot_menu > "$work/menu-after"
    cmp -s "$backup/menu.ipxe" "$work/menu-after" || fail 'Published PXE menu changed during the update.'
    if $package_mode; then
        write_state PACKAGE "$new_version"
        write_state PACKAGE_SHA256 "$7"
    fi
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
offline-deploy.sh install-deb INSTALL_ARCHIVE PUBLIC_KEY PACKAGE SHA256 MANAGEMENT_IP [PXE_INTERFACE|none]
offline-deploy.sh update BASE_ARCHIVE UPDATE_ARCHIVE PUBLIC_KEY MANAGEMENT_IP
offline-deploy.sh update-deb BASE_ARCHIVE UPDATE_ARCHIVE PUBLIC_KEY OLD_PACKAGE OLD_SHA256 NEW_PACKAGE NEW_SHA256 MANAGEMENT_IP
offline-deploy.sh rollback MANAGEMENT_IP
TEXT
    exit 2
}

main() {
    local command=${1:-}
    case "$command:$#" in
        check-update:4|install:4|install:5|install-deb:6|install-deb:7|update:5|update-deb:9|rollback:2) ;;
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
