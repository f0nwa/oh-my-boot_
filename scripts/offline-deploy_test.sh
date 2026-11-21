#!/usr/bin/env bash
set -euo pipefail
repo=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -r -- "$work"' EXIT
# shellcheck source=offline-deploy.sh
source "$repo/scripts/offline-deploy.sh"

cat > "$work/manifest.json" <<'JSON'
{"from_commit":"aaa","commit":"bbb","files":{"bin/omb-control":{},"bin/omb-controlctl":{},"deploy/sql/oidc.sql":{}},"migrations":[{"database":"omb_control","path":"deploy/sql/oidc.sql"}]}
JSON
test "$(update_sql "$work/manifest.json")" = deploy/sql/oidc.sql
sed 's/"omb_control"/"omb_boot"/' "$work/manifest.json" > "$work/wrong-db.json"
if update_sql "$work/wrong-db.json"; then
    echo 'Accepted a change to another database' >&2
    exit 1
fi
sed 's/"bin\/omb-controlctl"/"bin\/omb-agent"/' "$work/manifest.json" > "$work/wrong-bin.json"
if update_sql "$work/wrong-bin.json"; then
    echo 'Accepted a change to another binary' >&2
    exit 1
fi

assert_no_pending "$work"
printf '%s\n' /var/backups/omb/offline.fixture > "$work/PENDING"
if (assert_no_pending "$work"); then
    echo 'Accepted a second operation while an update is pending' >&2
    exit 1
fi

state="$work/state"
backups="$work/backups"
backup="$backups/offline.example"
mkdir -p "$state" "$backup"
prior_id=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
target_id=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
printf '%s %s %s %s\n' "$prior_id" "$target_id" omb_restore_example omb_retained_example > "$backup/metadata"
printf '%s\n' "$prior_id" > "$state/COMMIT"
printf '%s\n' "$backup" > "$state/PENDING"
cmp() { [[ "$1" == -s && "$2" == "$backup/"* && "$3" == /usr/local/bin/omb-control* ]]; }
health() { test "$(cat "$work/calls")" = 'start omb-control.service'; }
systemctl() { printf '%s\n' "$*" >> "$work/calls"; }
restore_snapshot
test ! -e "$state/PENDING"
test "$(cat "$state/COMMIT")" = "$prior_id"
test "$(cat "$work/calls")" = 'start omb-control.service'

source "$repo/scripts/offline-install-lib.sh"
address=192.168.10.45
ip() {
    case "$*" in
        'link show dev omb-pxe-off') test -e "$work/pxe-off-present" ;;
        'link show dev ens18'|'link show dev ens19') return 0 ;;
        '-o -4 addr show dev ens18') printf '2: ens18 inet 192.168.10.45/24 scope global ens18\n' ;;
        '-o -4 addr show dev ens19') return 0 ;;
        *) return 1 ;;
    esac
}
test "$(resolve_pxe_interface none)" = omb-pxe-off
test "$(resolve_pxe_interface ens19)" = ens19
if (resolve_pxe_interface ens18); then
    echo 'Accepted the management device for PXE' >&2
    exit 1
fi
touch "$work/pxe-off-present"
if (resolve_pxe_interface none); then
    echo 'Accepted a real device with the reserved name for disabled PXE' >&2
    exit 1
fi

echo 'Update scope and interrupted-run guards passed.'
