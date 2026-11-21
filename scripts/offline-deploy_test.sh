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

echo 'Update scope and interrupted-run guards passed.'
