#!/usr/bin/env bash
set -euo pipefail
repo=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -r -- "$work"' EXIT
# shellcheck source=offline-deploy.sh
source "$repo/scripts/offline-deploy.sh"

printf '%s\n' 'not a deb' > "$work/artifact.deb"
invalid_sum=0000000000000000000000000000000000000000000000000000000000000000
if (stage_deb "$work/artifact.deb" "$invalid_sum" "$work/staged.deb") > "$work/deb-error" 2>&1; then
    echo 'Accepted a package that failed its checksum' >&2
    exit 1
fi
grep -Fq 'Package checksum differs' "$work/deb-error"

# The fresh installation does not select an update scope before its HTTPS check.
(
    unset scope_services
    address=127.0.0.1
    curl() { [[ "$*" == *https://127.0.0.1:8443/login ]]; }
    systemctl() { [[ "$*" == 'is-active --quiet omb-control.service' ]]; }
    health
)

# Full scope health check verifies control, images, boot, agent, and active dnsmasq.
(
    scope_services=agent,boot,control,dnsmasq,images
    dnsmasq_was_active=1
    address=127.0.0.1
    calls="$work/health-calls"
    : > "$calls"
    curl() { printf 'curl %s\n' "$*" >> "$calls"; return 0; }
    boot_menu() { printf 'boot_menu\n' >> "$calls"; return 0; }
    systemctl() { printf 'systemctl %s\n' "$*" >> "$calls"; return 0; }
    health
    grep -Fq 'systemctl is-active --quiet omb-control.service' "$calls"
    grep -Fq 'systemctl is-active --quiet omb-images.service' "$calls"
    grep -Fq 'systemctl is-active --quiet omb-boot.service' "$calls"
    grep -Fq 'systemctl is-active --quiet omb-agent.service' "$calls"
    grep -Fq 'systemctl is-active --quiet omb-dnsmasq.service' "$calls"
    grep -Fq -- '--unix-socket /run/omb/agent.sock' "$calls"
    grep -Fq 'http://localhost/status' "$calls"
)

# Stop and start ordering skips inactive dnsmasq.
(
    scope_services=agent,boot,control,dnsmasq,images
    dnsmasq_was_active=0
    calls="$work/svc-order"
    : > "$calls"
    systemctl() { printf '%s\n' "$*" >> "$calls"; }
    stop_services
    start_services
    expected=$'stop omb-control.service\nstop omb-images.service\nstop omb-boot.service\nstop omb-agent.service\nstart omb-boot.service\nstart omb-agent.service\nstart omb-images.service\nstart omb-control.service'
    test "$(cat "$calls")" = "$expected"
)

cat > "$work/manifest.json" <<'JSON'
{"from_commit":"aaa","commit":"bbb","files":{"bin/omb-control":{},"bin/omb-controlctl":{},"deploy/sql/oidc.sql":{}},"migrations":[{"database":"omb_control","path":"deploy/sql/oidc.sql"}]}
JSON
test "$(inspect_update "$work/manifest.json")" = $'SERVICES\tcontrol\nDATABASES\tomb_control\nBINARIES\tomb-control,omb-controlctl\nPACKAGE_ONLY\t0\nMIGRATION\tomb_control\tdeploy/sql/oidc.sql'

cat > "$work/boot-only.json" <<'JSON'
{"from_commit":"aaa","commit":"bbb","files":{"bin/omb-boot":{}},"migrations":[]}
JSON
test "$(inspect_update "$work/boot-only.json")" = $'SERVICES\tboot\nDATABASES\tnone\nBINARIES\tomb-boot\nPACKAGE_ONLY\t0'

cat > "$work/full-scope.json" <<'JSON'
{"from_commit":"aaa","commit":"bbb","files":{"bin/omb-control":{},"bin/omb-images":{},"bin/omb-boot":{},"bin/omb-agent":{},"deploy/systemd/omb-agent.service":{},"deploy/systemd/omb-dnsmasq.service":{},"scripts/prepare-ubuntu-source.sh":{},"deploy/sql/oidc.sql":{},"deploy/sql/catalog.sql":{},"deploy/sql/boot.sql":{}},"migrations":[{"database":"omb_control","path":"deploy/sql/oidc.sql"},{"database":"omb_images","path":"deploy/sql/catalog.sql"},{"database":"omb_boot","path":"deploy/sql/boot.sql"}]}
JSON
test "$(inspect_update "$work/full-scope.json")" = $'SERVICES\tagent,boot,control,dnsmasq,images\nDATABASES\tomb_boot,omb_control,omb_images\nBINARIES\tomb-agent,omb-boot,omb-control,omb-images\nPACKAGE_ONLY\t1\nMIGRATION\tomb_control\tdeploy/sql/oidc.sql\nMIGRATION\tomb_images\tdeploy/sql/catalog.sql\nMIGRATION\tomb_boot\tdeploy/sql/boot.sql'

cat > "$work/bad-asset.json" <<'JSON'
{"from_commit":"aaa","commit":"bbb","files":{"bin/omb-boot":{},"assets/wimboot":{}},"migrations":[]}
JSON
if inspect_update "$work/bad-asset.json" >/dev/null 2>&1; then
    echo 'Accepted an update changing /srv/omb/tftp assets' >&2
    exit 1
fi

cat > "$work/empty.json" <<'JSON'
{"from_commit":"aaa","commit":"bbb","files":{"README.md":{}},"migrations":[]}
JSON
if inspect_update "$work/empty.json" >/dev/null 2>&1; then
    echo 'Accepted an update affecting no OMB services' >&2
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
printf '%s %s %s %s %s %s\n' "$prior_id" "$target_id" example control omb_control 0 > "$backup/metadata"
printf '%s\n%s\n' omb-control omb-controlctl > "$backup/binaries"
printf '%s\n' "$prior_id" > "$state/COMMIT"
printf '%s\n' "$backup" > "$state/PENDING"
cmp() { [[ "$1" == -s && "$2" == "$backup/"* && "$3" == /usr/local/bin/omb-control* ]]; }
health() { test "$(cat "$work/calls")" = 'start omb-control.service'; }
systemctl() { printf '%s\n' "$*" >> "$work/calls"; }
restore_snapshot
test ! -e "$state/PENDING"
test "$(cat "$state/COMMIT")" = "$prior_id"
test "$(cat "$work/calls")" = 'start omb-control.service'

# Rollback with no databases skips PostgreSQL swap.
(
    printf '%s %s %s %s %s %s\n' "$prior_id" "$target_id" example boot none 0 > "$backup/metadata"
    read_snapshot
    sql() { echo 'Unexpected SQL call for DATABASES=none' >&2; return 1; }
    swap_databases
)

# A rollback interrupted after the swap can be resumed for all three databases.
(
    printf '%s %s %s %s %s %s\n' "$prior_id" "$target_id" example boot,control,images omb_boot,omb_control,omb_images 0 > "$backup/metadata"
    read_snapshot
    test "$restored_control" = omb_restore_control_example
    test "$retained_control" = omb_retained_control_example
    test "$restored_images" = omb_restore_images_example
    test "$retained_images" = omb_retained_images_example
    test "$restored_boot" = omb_restore_boot_example
    test "$retained_boot" = omb_retained_boot_example
    sql() { printf '%s\n' '0|3|3'; }
    verify_restored_data() { return 1; }
    swap_databases
)

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

echo 'Update scope, multi-service health, and interrupted-run guards passed.'
