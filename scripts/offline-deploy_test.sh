#!/usr/bin/env bash
set -euo pipefail
repo=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -r -- "$work"' EXIT
# shellcheck source=offline-deploy.sh
source "$repo/scripts/offline-deploy.sh"

# 1. stage_deb rejects mismatched SHA-256 and accepts matching SHA-256.
printf '%s\n' 'not a deb' > "$work/artifact.deb"
valid_sum=$(sha256sum "$work/artifact.deb" | awk '{print $1}')
stage_deb "$work/artifact.deb" "$valid_sum" "$work/staged-ok.deb"
cmp -s "$work/artifact.deb" "$work/staged-ok.deb"
invalid_sum=0000000000000000000000000000000000000000000000000000000000000000
if (stage_deb "$work/artifact.deb" "$invalid_sum" "$work/staged.deb") > "$work/deb-error" 2>&1; then
    echo 'Accepted a package that failed its checksum' >&2
    exit 1
fi
grep -Fq 'Package checksum differs' "$work/deb-error"

# 2. Fresh installation health check without scope_services.
(
    unset scope_services
    address=127.0.0.1
    curl() { [[ "$*" == *https://127.0.0.1:8443/login ]]; }
    systemctl() { [[ "$*" == 'is-active --quiet omb-control.service' ]]; }
    health
)

# 3. Full scope health check verifies control, images, boot, agent, and active dnsmasq.
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

# 4. Stop and start ordering skips inactive dnsmasq.
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

# 5. compute_update_scope tests.
make_pkg_tree() {
    local root=$1
    mkdir -p "$root/usr/bin" "$root/usr/lib/systemd/system" "$root/usr/lib/omb" \
        "$root/usr/share/omb/assets" "$root/usr/share/omb/samba" \
        "$root/usr/share/omb/windows-profiles" "$root/usr/share/omb/bootstrap" \
        "$root/usr/share/omb/migrations" "$root/usr/share/doc/omb"
    printf 'boot-v1' > "$root/usr/bin/omb-boot"
    printf 'ctrl-v1' > "$root/usr/bin/omb-control"
    printf 'img-v1' > "$root/usr/bin/omb-images"
    printf 'agent-v1' > "$root/usr/bin/omb-agent"
    printf 'unit-v1' > "$root/usr/lib/systemd/system/omb-dnsmasq.service"
    printf 'helper-v1' > "$root/usr/lib/omb/omb-prepare-ubuntu-source"
    printf 'wimboot-v1' > "$root/usr/share/omb/assets/wimboot"
    printf 'smb-v1' > "$root/usr/share/omb/samba/smb.conf"
    printf 'winpeshl-v1' > "$root/usr/share/omb/windows-profiles/winpeshl.ini"
    printf 'bootstrap-v1' > "$root/usr/share/omb/bootstrap/control.sql"
    printf '{"format":1,"migrations":[]}\n' > "$root/usr/share/omb/migrations/manifest.json"
    printf '{"version":"0.1.0"}\n' > "$root/usr/share/omb/release.json"
    printf 'MIT\n' > "$root/usr/share/doc/omb/copyright"
}

: > "$work/no-pending"

# 5a. Only omb-boot binary changed and 0 migrations -> SERVICES=boot,control, DATABASES=none.
make_pkg_tree "$work/old-boot"
make_pkg_tree "$work/new-boot"
printf 'boot-v2' > "$work/new-boot/usr/bin/omb-boot"
printf '{"version":"0.2.0"}\n' > "$work/new-boot/usr/share/omb/release.json"
test "$(compute_update_scope "$work/old-boot" "$work/new-boot" "$work/no-pending")" = $'SERVICES\tboot,control\nDATABASES\tnone'

# 5b. Changing /usr/share/omb/assets/wimboot -> hard-fail.
make_pkg_tree "$work/new-bad-asset"
printf 'wimboot-v2' > "$work/new-bad-asset/usr/share/omb/assets/wimboot"
if compute_update_scope "$work/old-boot" "$work/new-bad-asset" "$work/no-pending" >/dev/null 2>&1; then
    echo 'Accepted package update changing /usr/share/omb/assets/wimboot' >&2
    exit 1
fi

# 5c. Changing /usr/share/omb/samba/smb.conf -> hard-fail.
make_pkg_tree "$work/new-bad-smb"
printf 'smb-v2' > "$work/new-bad-smb/usr/share/omb/samba/smb.conf"
if compute_update_scope "$work/old-boot" "$work/new-bad-smb" "$work/no-pending" >/dev/null 2>&1; then
    echo 'Accepted package update changing /usr/share/omb/samba/smb.conf' >&2
    exit 1
fi

# 5d. Changing /usr/share/omb/windows-profiles/winpeshl.ini -> hard-fail.
make_pkg_tree "$work/new-bad-winpe"
printf 'winpeshl-v2' > "$work/new-bad-winpe/usr/share/omb/windows-profiles/winpeshl.ini"
if compute_update_scope "$work/old-boot" "$work/new-bad-winpe" "$work/no-pending" >/dev/null 2>&1; then
    echo 'Accepted package update changing /usr/share/omb/windows-profiles/winpeshl.ini' >&2
    exit 1
fi

# 5e. Changing bootstrap SQL, migrations manifest, and release.json is allowed.
make_pkg_tree "$work/new-allowed"
printf 'bootstrap-v2' > "$work/new-allowed/usr/share/omb/bootstrap/control.sql"
printf '{"format":1,"migrations":[{"database":"omb_control","id":"0001_init","path":"omb_control/0001_init.sql","sha256":"%s"}]}\n' \
    "1111111111111111111111111111111111111111111111111111111111111111" \
    > "$work/new-allowed/usr/share/omb/migrations/manifest.json"
printf '{"version":"0.2.0"}\n' > "$work/new-allowed/usr/share/omb/release.json"
test "$(compute_update_scope "$work/old-boot" "$work/new-allowed" "$work/no-pending")" = $'SERVICES\tcontrol\nDATABASES\tnone'

# 5f. Pending migrations + unit + helper expand services and databases.
make_pkg_tree "$work/new-multi"
printf 'unit-v2' > "$work/new-multi/usr/lib/systemd/system/omb-dnsmasq.service"
printf 'helper-v2' > "$work/new-multi/usr/lib/omb/omb-prepare-ubuntu-source"
printf 'omb_images\t0001_add_col\tomb_images/0001_add_col.sql\t1111111111111111111111111111111111111111111111111111111111111111\n' > "$work/pending-img"
test "$(compute_update_scope "$work/old-boot" "$work/new-multi" "$work/pending-img")" = $'SERVICES\tagent,control,dnsmasq,images\nDATABASES\tomb_images'

# 5g. Removing legacy usr/share/omb/upgrade/* succeeds, while adding/modifying it or touching forbidden paths fails.
make_pkg_tree "$work/old-legacy-upgrade"
mkdir -p "$work/old-legacy-upgrade/usr/share/omb/upgrade"
printf '{"migrations":[]}\n' > "$work/old-legacy-upgrade/usr/share/omb/upgrade/migrations.json"
make_pkg_tree "$work/new-no-upgrade"
test "$(compute_update_scope "$work/old-legacy-upgrade" "$work/new-no-upgrade" "$work/no-pending")" = $'SERVICES\tcontrol\nDATABASES\tnone'

# Adding usr/share/omb/upgrade/migrations.json -> hard-fail.
if compute_update_scope "$work/new-no-upgrade" "$work/old-legacy-upgrade" "$work/no-pending" >/dev/null 2>&1; then
    echo 'Accepted adding usr/share/omb/upgrade/migrations.json' >&2
    exit 1
fi

# Modifying usr/share/omb/upgrade/migrations.json -> hard-fail.
make_pkg_tree "$work/new-mod-upgrade"
mkdir -p "$work/new-mod-upgrade/usr/share/omb/upgrade"
printf '{"migrations":["v2"]}\n' > "$work/new-mod-upgrade/usr/share/omb/upgrade/migrations.json"
if compute_update_scope "$work/old-legacy-upgrade" "$work/new-mod-upgrade" "$work/no-pending" >/dev/null 2>&1; then
    echo 'Accepted modifying usr/share/omb/upgrade/migrations.json' >&2
    exit 1
fi

# Removing usr/share/omb/upgrade/migrations.json while touching assets/samba/windows-profiles -> hard-fail.
for forbidden_rel in usr/share/omb/assets/wimboot usr/share/omb/samba/smb.conf usr/share/omb/windows-profiles/winpeshl.ini; do
    make_pkg_tree "$work/new-bad-combo"
    printf 'changed' > "$work/new-bad-combo/$forbidden_rel"
    if compute_update_scope "$work/old-legacy-upgrade" "$work/new-bad-combo" "$work/no-pending" >/dev/null 2>&1; then
        echo "Accepted forbidden change to $forbidden_rel alongside legacy upgrade removal" >&2
        exit 1
    fi
    rm -rf "$work/new-bad-combo"
done

# 6. plan_migrations tests.
sha1=1111111111111111111111111111111111111111111111111111111111111111
sha2=2222222222222222222222222222222222222222222222222222222222222222
sha3=3333333333333333333333333333333333333333333333333333333333333333
sha_bad=ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff

# 6a. Empty manifest and empty DB -> empty plan.
printf '{"format":1,"migrations":[]}\n' > "$work/manifest-empty.json"
: > "$work/applied-empty.tsv"
test -z "$(plan_migrations "$work/manifest-empty.json" "$work/applied-empty.tsv")"

# 6b. Jump 0 -> 0001, 0002 (and multi-DB order omb_boot -> omb_images -> omb_control).
cat > "$work/manifest-jump.json" <<JSON
{
  "format": 1,
  "migrations": [
    {"database": "omb_control", "id": "0002_second", "path": "omb_control/0002_second.sql", "sha256": "$sha2"},
    {"database": "omb_control", "id": "0001_first", "path": "omb_control/0001_first.sql", "sha256": "$sha1"},
    {"database": "omb_boot", "id": "0001_boot", "path": "omb_boot/0001_boot.sql", "sha256": "$sha3"}
  ]
}
JSON
expected_jump=$(printf 'omb_boot\t0001_boot\tomb_boot/0001_boot.sql\t%s\nomb_control\t0001_first\tomb_control/0001_first.sql\t%s\nomb_control\t0002_second\tomb_control/0002_second.sql\t%s' "$sha3" "$sha1" "$sha2")
test "$(plan_migrations "$work/manifest-jump.json" "$work/applied-empty.tsv")" = "$expected_jump"

# 6c. Append-only violation: 0002 in DB while 0001 is unapplied -> hard-fail.
printf 'omb_control\t0002_second\t%s\n' "$sha2" > "$work/applied-gap.tsv"
if plan_migrations "$work/manifest-jump.json" "$work/applied-gap.tsv" >/dev/null 2>&1; then
    echo 'Accepted append-only violation (0002 applied while 0001 unapplied)' >&2
    exit 1
fi

# 6d. Drift check: 0001 in DB with different sha256 -> hard-fail.
printf 'omb_control\t0001_first\t%s\n' "$sha_bad" > "$work/applied-drift.tsv"
if plan_migrations "$work/manifest-jump.json" "$work/applied-drift.tsv" >/dev/null 2>&1; then
    echo 'Accepted migration checksum drift' >&2
    exit 1
fi

# 6e. Missing migration: 0001 in DB absent from new package -> hard-fail.
printf 'omb_control\t0001_old\t%s\n' "$sha1" > "$work/applied-missing.tsv"
if plan_migrations "$work/manifest-empty.json" "$work/applied-missing.tsv" >/dev/null 2>&1; then
    echo 'Accepted applied migration missing from new package' >&2
    exit 1
fi

# 6f. Non-canonical migration path in manifest -> hard-fail.
cat > "$work/manifest-bad-path.json" <<JSON
{
  "format": 1,
  "migrations": [
    {"database": "omb_control", "id": "0001_first", "path": "../evil.sql", "sha256": "$sha1"}
  ]
}
JSON
if plan_migrations "$work/manifest-bad-path.json" "$work/applied-empty.tsv" >/dev/null 2>&1; then
    echo 'Accepted non-canonical migration path in manifest' >&2
    exit 1
fi

# 6g. Duplicate 4-digit migration prefix per DB -> hard-fail.
cat > "$work/manifest-dup-prefix.json" <<JSON
{
  "format": 1,
  "migrations": [
    {"database": "omb_control", "id": "0001_first", "path": "omb_control/0001_first.sql", "sha256": "$sha1"},
    {"database": "omb_control", "id": "0001_second", "path": "omb_control/0001_second.sql", "sha256": "$sha2"}
  ]
}
JSON
if plan_migrations "$work/manifest-dup-prefix.json" "$work/applied-empty.tsv" >/dev/null 2>&1; then
    echo 'Accepted duplicate 4-digit migration prefix in manifest' >&2
    exit 1
fi

# 7. apply_migration runs SQL + schema_migrations INSERT in a single transaction as DB owner.
(
    mig_sql="$work/0001_example.sql"
    printf 'ALTER TABLE public.users ADD COLUMN test_col TEXT;\n' > "$mig_sql"
    mig_sha=$(sha256sum "$mig_sql" | cut -d' ' -f1)
    captured_args="$work/psql-args"
    captured_stdin="$work/psql-stdin"
    runuser() {
        printf '%s\n' "$*" > "$captured_args"
        cat > "$captured_stdin"
        return 0
    }
    apply_migration omb_control 0001_example "$mig_sql" "$mig_sha"
    grep -Fq -- '-u omb-control -- psql -Xw -v ON_ERROR_STOP=1 -d omb_control -v mig_id=0001_example' "$captured_args"
    grep -Fq -- "-v mig_sha=$mig_sha" "$captured_args"
    head -n 1 "$captured_stdin" | grep -Fxq 'BEGIN;'
    grep -Fq 'ALTER TABLE public.users ADD COLUMN test_col TEXT;' "$captured_stdin"
    grep -Fq "INSERT INTO public.schema_migrations (id, sha256) VALUES (:'mig_id', :'mig_sha');" "$captured_stdin"
    tail -n 1 "$captured_stdin" | grep -Fxq 'COMMIT;'

    # Failure in psql propagates non-zero exit code.
    runuser() { cat >/dev/null; return 1; }
    if (apply_migration omb_control 0001_example "$mig_sql" "$mig_sha") >/dev/null 2>&1; then
        echo 'apply_migration succeeded despite psql error' >&2
        exit 1
    fi

    # Checksum mismatch aborts before calling psql.
    runuser() { echo 'should not be called' >&2; return 0; }
    if (apply_migration omb_control 0001_example "$mig_sql" "$sha_bad") >/dev/null 2>&1; then
        echo 'apply_migration accepted mismatched file checksum' >&2
        exit 1
    fi
)

# 8. seed_schema_migrations handles empty, valid, and invalid manifests.
(
    source "$repo/scripts/offline-install-lib.sh"
    calls="$work/seed-calls"
    sql_in="$work/seed-sql"
    : > "$calls"
    : > "$sql_in"
    runuser() { printf '%s\n' "$*" >> "$calls"; cat >> "$sql_in"; }
    seed_schema_migrations "$work/manifest-empty.json"
    test ! -s "$calls"
    seed_schema_migrations "$work/manifest-jump.json"
    grep -Fq -- '-u omb-control -- psql -Xw -v ON_ERROR_STOP=1 -d omb_control -v mig_id=0001_first' "$calls"
    grep -Fq -- '-u omb-boot -- psql -Xw -v ON_ERROR_STOP=1 -d omb_boot -v mig_id=0001_boot' "$calls"
    grep -Fq "INSERT INTO public.schema_migrations (id, sha256)" "$sql_in"

    printf '{"format":1,"migrations":[{"database":"omb_other","id":"0001_x","path":"omb_other/0001_x.sql","sha256":"%s"}]}\n' "$sha1" > "$work/seed-bad-db.json"
    if (seed_schema_migrations "$work/seed-bad-db.json") >/dev/null 2>&1; then
        echo 'seed_schema_migrations accepted unsupported database' >&2
        exit 1
    fi
    printf '{"format":1,"migrations":[{"database":"omb_control","id":"bad","path":"omb_control/bad.sql","sha256":"%s"}]}\n' "$sha1" > "$work/seed-bad-id.json"
    if (seed_schema_migrations "$work/seed-bad-id.json") >/dev/null 2>&1; then
        echo 'seed_schema_migrations accepted invalid id' >&2
        exit 1
    fi
    printf '{"format":1,"migrations":[{"database":"omb_control","id":"0001_x","path":"omb_control/0001_x.sql","sha256":"notasha"}]}\n' > "$work/seed-bad-sha.json"
    if (seed_schema_migrations "$work/seed-bad-sha.json") >/dev/null 2>&1; then
        echo 'seed_schema_migrations accepted invalid sha256' >&2
        exit 1
    fi
)

# 9. assert_no_pending guard.
assert_no_pending "$work"
printf '%s\n' /var/backups/omb/offline.fixture > "$work/PENDING"
if (assert_no_pending "$work"); then
    echo 'Accepted a second operation while an update is pending' >&2
    exit 1
fi

# 10. restore_snapshot tests.
prior_id=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
target_id=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb

# 10a. Interrupted backup before READY restarts services and clears PENDING.
(
    state="$work/state-early"
    backups="$work/backups-early"
    backup="$backups/offline.early"
    mkdir -p "$state" "$backup"
    printf '%s %s %s %s %s %s\n' "$prior_id" "$target_id" early control omb_control 0 > "$backup/metadata"
    printf 'old-deb-bytes' > "$backup/omb.deb"
    (cd "$backup" && sha256sum omb.deb > omb.deb.sha256)
    printf '%s\n' "$prior_id" > "$state/COMMIT"
    printf '%s\n' "$backup" > "$state/PENDING"
    calls="$work/early-calls"
    : > "$calls"
    health() { test "$(cat "$calls")" = 'start omb-control.service'; }
    systemctl() { printf '%s\n' "$*" >> "$calls"; }
    restore_snapshot >/dev/null
    test ! -e "$state/PENDING"
    test "$(cat "$state/COMMIT")" = "$prior_id"
    test "$(cat "$calls")" = 'start omb-control.service'
)

# 10b. Full rollback restores omb.deb, current.deb, PACKAGE, PACKAGE_SHA256, COMMIT, CHANNEL; preserves /etc/omb/*.
(
    state="$work/state-rb"
    backups="$work/backups-rb"
    backup="$backups/offline.rb"
    etc_omb_dir="$work/etc-omb-rb"
    mkdir -p "$state" "$backup" "$etc_omb_dir"
    printf 'old-deb-payload' > "$backup/omb.deb"
    old_deb_sha=$(sha256sum "$backup/omb.deb" | cut -d' ' -f1)
    printf '%s  omb.deb\n' "$old_deb_sha" > "$backup/omb.deb.sha256"
    printf '%s %s %s %s %s %s\n' "$prior_id" "$target_id" rb control none 0 > "$backup/metadata"
    printf 'stable\n' > "$backup/channel"
    printf 'secret-key-v1\n' > "$backup/secrets.key"
    printf 'secret-key-v1\n' > "$etc_omb_dir/secrets.key"
    printf 'current-env\n' > "$etc_omb_dir/control.env"
    printf 'old-env\n' > "$backup/control.env"
    : > "$backup/READY"

    printf 'new-deb-payload' > "$state/current.deb"
    printf '0.2.0~dev.1\n' > "$state/PACKAGE"
    printf '%s\n' "$sha_bad" > "$state/PACKAGE_SHA256"
    printf 'dev\n' > "$state/CHANNEL"
    printf '%s\n' "$target_id" > "$state/COMMIT"
    printf '%s\n' "$backup" > "$state/PENDING"

    installed_pkg_flag="$work/rb-pkg-installed"
    package_identity() { printf '0.1.4\n'; }
    installed_package_matches() { test -f "$installed_pkg_flag"; }
    install_packages() {
        test "$1" = ''
        test "$2" = "$backup/omb.deb"
        : > "$installed_pkg_flag"
    }
    systemctl() { return 0; }
    health() { return 0; }
    mv() { [[ "${1:-}" == -T ]] && shift; command mv "$@"; }

    restore_snapshot >/dev/null
    test ! -e "$state/PENDING"
    cmp -s "$backup/omb.deb" "$state/current.deb"
    test "$(cat "$state/PACKAGE")" = '0.1.4'
    test "$(cat "$state/PACKAGE_SHA256")" = "$old_deb_sha"
    test "$(cat "$state/CHANNEL")" = 'stable'
    test "$(cat "$state/COMMIT")" = "$prior_id"
    # /etc/omb/* must never be overwritten.
    test "$(cat "$etc_omb_dir/control.env")" = 'current-env'
    test "$(cat "$etc_omb_dir/secrets.key")" = 'secret-key-v1'
)

# 10c. Rollback aborts if secrets.key changed.
(
    state="$work/state-rb-key"
    backups="$work/backups-rb-key"
    backup="$backups/offline.rbkey"
    etc_omb_dir="$work/etc-omb-rb-key"
    mkdir -p "$state" "$backup" "$etc_omb_dir"
    printf 'old-deb-payload' > "$backup/omb.deb"
    (cd "$backup" && sha256sum omb.deb > omb.deb.sha256)
    printf '%s %s %s %s %s %s\n' "$prior_id" "$target_id" rbkey control none 0 > "$backup/metadata"
    printf 'secret-key-v1\n' > "$backup/secrets.key"
    printf 'secret-key-CHANGED\n' > "$etc_omb_dir/secrets.key"
    : > "$backup/READY"
    printf '%s\n' "$target_id" > "$state/COMMIT"
    printf '%s\n' "$backup" > "$state/PENDING"

    if restore_snapshot > "$work/rb-key-err" 2>&1; then
        echo 'Allowed rollback after secrets.key changed' >&2
        exit 1
    fi
    grep -Fq 'Integration key changed' "$work/rb-key-err"
)

# 11. swap_databases tests.
(
    backups="$work/backups-swap"
    backup="$backups/offline.swap"
    mkdir -p "$backup"
    printf 'old-deb' > "$backup/omb.deb"
    (cd "$backup" && sha256sum omb.deb > omb.deb.sha256)

    # DATABASES=none skips SQL.
    printf '%s %s %s %s %s %s\n' "$prior_id" "$target_id" swap boot none 0 > "$backup/metadata"
    read_snapshot
    sql() { echo 'Unexpected SQL call for DATABASES=none' >&2; return 1; }
    swap_databases

    # Interrupted rollback after swap can be resumed for all three DBs.
    printf '%s %s %s %s %s %s\n' "$prior_id" "$target_id" swap boot,control,images omb_boot,omb_control,omb_images 0 > "$backup/metadata"
    read_snapshot
    test "$restored_control" = omb_restore_control_swap
    test "$retained_control" = omb_retained_control_swap
    test "$restored_images" = omb_restore_images_swap
    test "$retained_images" = omb_retained_images_swap
    test "$restored_boot" = omb_restore_boot_swap
    test "$retained_boot" = omb_retained_boot_swap
    sql() { printf '%s\n' '0|3|3'; }
    verify_restored_data() { return 1; }
    swap_databases
)

# 12. resolve_pxe_interface tests.
(
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
)

# 13. resolve_repo_package parses 5-column TSV from offline-deb.py verify-repo --print-package.
(
    fake_repo="$work/fake-repo"
    mkdir -p "$fake_repo"
    touch "$fake_repo/omb_0.2.0~dev.1_amd64.deb"
    python3() {
        [[ "$*" == *"verify-repo $fake_repo --public-key /dev/null --print-package" ]] || return 1
        printf '%s\t%s\t%s\t%s\t%s\n' omb_0.2.0~dev.1_amd64.deb "$sha1" 0.2.0~dev.1 "$prior_id" dev
    }
    res=$(resolve_repo_package "$fake_repo" /dev/null)
    expected=$(printf '%s\t%s\t%s\t%s\t%s' "$fake_repo/omb_0.2.0~dev.1_amd64.deb" "$sha1" 0.2.0~dev.1 "$prior_id" dev)
    test "$res" = "$expected"
)

# 14. Release channel checks.
(
    state="$work/ch-state"
    mkdir -p "$state"
    printf '0.1.4\n' > "$state/PACKAGE"
    test "$(installed_channel)" = stable
    printf '0.2.0~dev.1\n' > "$state/PACKAGE"
    test "$(installed_channel)" = dev
    rm -f "$state/PACKAGE"
    printf 'dev\n' > "$state/CHANNEL"
    test "$(installed_channel)" = dev

    prior_channel=stable
    target_channel=dev
    allow_channel_switch=false
    if (check_channel_switch) > "$work/ch-err" 2>&1; then
        echo 'Allowed channel switch without --allow-channel-switch' >&2
        exit 1
    fi
    grep -Fq -- '--allow-channel-switch' "$work/ch-err"
    allow_channel_switch=true
    check_channel_switch
)

# 15. CLI usage and --allow-channel-switch flag validation.
(
    usage_out="$work/usage.txt"
    set +e
    bash "$repo/scripts/offline-deploy.sh" >"$usage_out" 2>&1
    rc=$?
    set -e
    test "$rc" -eq 2
    grep -Fq 'offline-deploy.sh install ' "$usage_out"
    grep -Fq 'offline-deploy.sh update [--allow-channel-switch] ' "$usage_out"
    grep -Fq 'offline-deploy.sh rollback ' "$usage_out"
    if grep -Eq 'check-update|install-deb|update-deb' "$usage_out"; then
        echo 'Legacy commands still present in usage' >&2
        exit 1
    fi

    # --allow-channel-switch rejected for install and rollback.
    for bad_cmd in "install --allow-channel-switch repo key 127.0.0.1" "rollback --allow-channel-switch 127.0.0.1" "check-update a b c" "update-deb-repo a b c d e f g"; do
        set +e
        # shellcheck disable=SC2086
        bash "$repo/scripts/offline-deploy.sh" $bad_cmd >/dev/null 2>&1
        rc=$?
        set -e
        test "$rc" -eq 2
    done

    # --allow-channel-switch accepted for update and stripped before calling upgrade.
    cat > "$work/parse-update.sh" <<'SH'
set -euo pipefail
source "$1/scripts/offline-deploy.sh"
lock_file="$2/test.lock"
id() { printf '0\n'; }
flock() { return 0; }
upgrade() {
    $allow_channel_switch || fail 'missing flag'
    printf '%s|%s|%s\n' "$1" "$2" "$address"
}
main update --allow-channel-switch /tmp/repo /tmp/key.pem 192.168.10.135
SH
    out=$(bash "$work/parse-update.sh" "$repo" "$work")
    test "$out" = '/tmp/repo|/tmp/key.pem|192.168.10.135'
)

# 16. collect_applied_migrations propagates sql failures.
(
    sql() { return 1; }
    if collect_applied_migrations "$work/applied-fail.tsv" >/dev/null 2>&1; then
        echo 'collect_applied_migrations ignored sql failure' >&2
        exit 1
    fi
)

echo 'All offline-deploy unit tests passed.'
