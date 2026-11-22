#!/usr/bin/env bash
set -euo pipefail
repo=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -r -- "$work"' EXIT
# shellcheck source=adopt-stand-deb.sh
source "$repo/scripts/adopt-stand-deb.sh"

# 1. resolve_repo_package extracts verified path, SHA-256, and commit.
(
    fake_repo="$work/repo"
    mkdir -p "$fake_repo"
    touch "$fake_repo/omb_0.1.0_amd64.deb"
    sha=1111111111111111111111111111111111111111111111111111111111111111
    expected_commit=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    python3() { printf '%s\t%s\t%s\n' omb_0.1.0_amd64.deb "$sha" "$expected_commit"; }
    res=$(resolve_repo_package "$fake_repo" /dev/null 0.1.0)
    test "$res" = "$fake_repo/omb_0.1.0_amd64.deb"$'\t'"$sha"$'\t'"$expected_commit"
)

# 2. Stop/start ordering respects dnsmasq_was_active=0 vs 1.
(
    dnsmasq_was_active=0
    calls="$work/svc0"
    : > "$calls"
    systemctl() { printf '%s\n' "$*" >> "$calls"; }
    stop_omb_services
    start_omb_services
    ! grep -Fq 'omb-dnsmasq.service' "$calls"

    dnsmasq_was_active=1
    calls="$work/svc1"
    : > "$calls"
    stop_omb_services
    start_omb_services
    grep -Fq 'stop omb-dnsmasq.service' "$calls"
    grep -Fq 'start omb-dnsmasq.service' "$calls"
)

# 3. verify_fragment_paths accepts /usr/lib/systemd/system and rejects /etc/systemd/system override.
(
    systemctl() {
        local unit=${@: -1}
        unit=${unit/@check.service/@.service}
        printf '/usr/lib/systemd/system/%s\n' "$unit"
    }
    verify_fragment_paths /usr/lib/systemd/system

    systemctl() {
        local unit=${@: -1}
        unit=${unit/@check.service/@.service}
        if [[ "$unit" == omb-control.service ]]; then
            printf '/etc/systemd/system/%s\n' "$unit"
        else
            printf '/usr/lib/systemd/system/%s\n' "$unit"
        fi
    }
    if verify_fragment_paths /usr/lib/systemd/system 2>/dev/null; then
        echo 'Accepted an overriding unit under /etc/systemd/system' >&2
        exit 1
    fi
)

# 4. Rollback restores manual binaries, helper, units, purges omb, and clears deployment state.
(
    state="$work/state"
    local_bin="$work/local-bin"
    local_libexec="$work/local-libexec"
    etc_systemd="$work/etc-systemd"
    win_profiles="$work/win-profiles"
    backup="$work/adopt-deb.abc12345"
    mkdir -p "$state" "$local_bin" "$local_libexec" "$etc_systemd" "$win_profiles" \
        "$backup"/{local-bin,local-libexec,systemd,windows-profiles}
    printf '%s %s %s %s %s\n' 0.1.0 aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
        1111111111111111111111111111111111111111111111111111111111111111 abc12345 1 > "$backup/metadata"
    for b in "${OMB_BINARIES[@]}"; do printf 'bin-%s\n' "$b" > "$backup/local-bin/$b"; done
    printf '#!/bin/sh\n' > "$backup/local-libexec/omb-prepare-ubuntu-source"
    for u in "${OMB_UNITS[@]}"; do printf '[Unit]\n' > "$backup/systemd/$u"; done
    printf 'menu-ok\n' > "$backup/menu.ipxe"
    printf '0.1.0\n' > "$state/PACKAGE"
    printf 'aaaa\n' > "$state/COMMIT"
    calls="$work/rb-calls"
    : > "$calls"
    dpkg-query() { printf 'installed\n'; }
    dpkg() { printf 'dpkg %s\n' "$*" >> "$calls"; }
    systemctl() { printf 'systemctl %s\n' "$*" >> "$calls"; }
    health_all() { return 0; }
    boot_menu() { printf 'menu-ok\n'; }
    run_rollback
    grep -Fq 'dpkg --purge omb' "$calls"
    grep -Fq 'systemctl daemon-reload' "$calls"
    grep -Fq 'systemctl start omb-dnsmasq.service' "$calls"
    test -x "$local_bin/omb-control"
    test -x "$local_libexec/omb-prepare-ubuntu-source"
    test -f "$etc_systemd/omb-control.service"
    test ! -e "$state/PACKAGE"
    test ! -e "$state/COMMIT"
)

echo 'adopt-stand-deb.sh guards and rollback tests passed.'
