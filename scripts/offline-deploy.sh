#!/usr/bin/env bash
# Trusted launcher for the verified Ubuntu installation and Control-only update.
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
if (not data["from_commit"] or len(steps) != 1 or steps[0]["database"] != "omb_control"
        or bins != {"bin/omb-control", "bin/omb-controlctl"}):
    sys.exit("Unsupported update: expected one Control migration and two Control binaries.")
print(steps[0]["path"])
PY
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
    migration=$(update_sql "$work/update/manifest.json")
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
            -o /dev/null "https://$address:8443/login" 2>/dev/null; then
            systemctl is-active --quiet omb-control.service && return 0
        fi
        sleep 1
    done
    echo 'Control HTTPS health check failed.' >&2
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
    for binary in omb-control omb-controlctl; do
        tmp=$(mktemp /usr/local/bin/.omb-replacement.XXXXXXXX) || return 1
        install -m 0755 "$src/$binary" "$tmp" && mv -T -- "$tmp" "/usr/local/bin/$binary" || return 1
    done
}

snapshot_users() {
    sql -d "$1" -Atc "SELECT coalesce(jsonb_agg(to_jsonb(u) ORDER BY u.id),'[]'::jsonb) FROM public.users u"
}

check_backup_space() {
    local db_kib free_db free_backup
    db_kib=$(sql -d postgres -Atc "SELECT (pg_database_size('omb_control')+1023)/1024")
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
    read -r prior target restored retained < "$backup/metadata" || return 1
    [[ "$prior" =~ ^[0-9a-f]{40}$ && "$target" =~ ^[0-9a-f]{40}$ &&
       "$restored" =~ ^omb_restore_[0-9a-z]+$ && "$retained" =~ ^omb_retained_[0-9a-z]+$ ]]
}

restore_snapshot() {
    local installed existing
    read_snapshot || return 1
    installed=$(cat "$state/COMMIT") || return 1
    [[ "$installed" == "$prior" || "$installed" == "$target" ]] || return 1
    if [[ ! -e "$backup/READY" ]]; then
        # No migration or binary replacement is allowed until the restore has been verified.
        [[ "$installed" == "$prior" ]] || return 1
        cmp -s "$backup/omb-control" /usr/local/bin/omb-control || return 1
        cmp -s "$backup/omb-controlctl" /usr/local/bin/omb-controlctl || return 1
        systemctl start omb-control.service && health || return 1
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
    systemctl stop omb-control.service || return 1
    existing=$(sql -d postgres -Atc "SELECT datname FROM pg_database WHERE datname IN ('$restored','$retained')") || return 1
    if [[ "$existing" == "$restored" ]]; then
        sql -d postgres -v restored="$restored" -v retained="$retained" <<'SQL' || return 1
BEGIN;
ALTER DATABASE omb_control RENAME TO :"retained";
ALTER DATABASE :"restored" RENAME TO omb_control;
ALTER DATABASE :"retained" WITH ALLOW_CONNECTIONS false;
COMMIT;
SQL
    elif [[ "$existing" != "$retained" ]]; then
        echo 'Recovery database is missing or ambiguous.' >&2
        return 1
    fi
    snapshot_users omb_control > "$work/users-restored" || return 1
    cmp -s "$backup/users.json" "$work/users-restored" || return 1
    install_binaries "$backup" || return 1
    systemctl reset-failed omb-control.service || return 1
    systemctl start omb-control.service || return 1
    health || return 1
    boot_menu > "$work/menu-after-rollback" || return 1
    cmp -s "$backup/menu.ipxe" "$work/menu-after-rollback" || return 1
    write_state COMMIT "$prior" || return 1
    unlink "$state/PENDING" || return 1
    printf 'Restored %s. The later database was retained as %s with connections disabled.\n' "$prior" "$retained"
}

upgrade() {
    prepare_update "$1" "$2" "$3" --check-host
    check_address
    health
    if [[ ! -d "$backups" ]]; then install -d -m 0700 "$backups"; fi
    check_backup_space
    backup=$(mktemp -d "$backups/offline.XXXXXXXX")
    local suffix
    suffix=$(printf '%s' "${backup##*.}" | tr '[:upper:]' '[:lower:]')
    printf '%s %s %s %s\n' "$from" "$to" "omb_restore_$suffix" "omb_retained_$suffix" > "$backup/metadata"
    read_snapshot
    install -m 0700 /usr/local/bin/omb-control /usr/local/bin/omb-controlctl "$backup/"
    install -m 0600 /etc/omb/control.env /etc/omb/secrets.key \
        /var/lib/omb-control/control.crt /var/lib/omb-control/control.key "$backup/"
    boot_menu > "$backup/menu.ipxe"
    write_state PENDING "$backup"
    upgrade_paused=true
    systemctl stop omb-control.service
    snapshot_users omb_control > "$backup/users.json"
    runuser -u postgres -- pg_dump --no-password -Fc -d omb_control > "$backup/control.dump"
    runuser -u postgres -- createdb --owner=omb-control "$restored"
    runuser -u postgres -- pg_restore --no-password --exit-on-error --single-transaction \
        -d "$restored" < "$backup/control.dump"
    snapshot_users "$restored" > "$work/users-restored"
    cmp -s "$backup/users.json" "$work/users-restored" || fail 'Restored user rows differ from the backup source.'
    : > "$backup/READY"

    runuser -u omb-control -- psql -Xw -v ON_ERROR_STOP=1 -d omb_control < "$work/update/$migration"
    install_binaries "$work/update/bin"
    systemctl start omb-control.service
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
            systemctl start omb-control.service || :
        fi
    fi
    rm -r -- "$work"
    exit "$status"
}

usage() {
    cat >&2 <<'TEXT'
offline-deploy.sh check-update BASE_ARCHIVE UPDATE_ARCHIVE PUBLIC_KEY
offline-deploy.sh install INSTALL_ARCHIVE PUBLIC_KEY MANAGEMENT_IP PXE_INTERFACE
offline-deploy.sh update BASE_ARCHIVE UPDATE_ARCHIVE PUBLIC_KEY MANAGEMENT_IP
offline-deploy.sh rollback MANAGEMENT_IP
TEXT
    exit 2
}

main() {
    local command=${1:-}
    case "$command:$#" in
        check-update:4|install:5|update:5|rollback:2) ;;
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
            fresh_install "$1" "$2" "$4"
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
