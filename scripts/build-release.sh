#!/usr/bin/env bash
# Single-command OMB release builder (local Linux/CI or remote Ubuntu build + optional stand deploy).
set -euo pipefail

BINARIES=(
    omb-agent
    omb-agentctl
    omb-boot
    omb-control
    omb-controlctl
    omb-images
    omb-imagesctl
    omb-winiso-prepare
)

CLEANUP_WORK=''
CLEANUP_REMOTE=''
CLEANUP_REMOTE_WORK=''

cleanup() {
    if [[ -n "${CLEANUP_WORK:-}" ]]; then
        rm -rf -- "$CLEANUP_WORK"
    fi
    if [[ -n "${CLEANUP_REMOTE:-}" && -n "${CLEANUP_REMOTE_WORK:-}" ]]; then
        ssh "$CLEANUP_REMOTE" "rm -rf -- '$CLEANUP_REMOTE_WORK'" >/dev/null 2>&1 || true
    fi
}

fail() { printf 'build-release.sh: %s\n' "$*" >&2; exit 1; }

usage() {
    cat <<'TEXT'
Usage:
  scripts/build-release.sh [OPTIONS]

Build a self-contained signed OMB apt-repo (and optionally deploy it to an Ubuntu stand).

Options:
  --version VER           Debian version (e.g. 0.2.0 or 0.2.0~dev.19). Default: inferred from Git tag on HEAD.
  --channel stable|dev    Release channel. Default: inferred from tag/version.
  --sign-key PATH         OpenSSL private key (required for stable; optional temporary key for dev + --apply).
  --sysdebs-dir PATH      Directory with Ubuntu 26.04 system .deb files and Packages.gz.
                          Default: auto-detected from dist/omb-deb-*/apt-repo-final.
  --assets-dir PATH       Directory with ipxeboot.tar.gz, ipxe-shimx64.efi, wimboot.
                          In --remote mode, defaults to /usr/share/omb/assets on the remote host.
  --out PATH              Destination directory for the built signed apt-repo (must not exist yet).
  --remote USER@HOST      Build the .deb and apt-repo on an Ubuntu host via SSH (for macOS hosts without dpkg-deb).
  --apply MANAGEMENT_IP   After building, run offline-deploy.sh update on the target host and verify services.
  --allow-channel-switch  Pass --allow-channel-switch to offline-deploy.sh update (requires --apply).
  --allow-dirty           Allow building with uncommitted changes in tracked files.
  -h, --help              Show this help.
TEXT
}

parse_release_tag() {
    local tag=$1
    if [[ "$tag" =~ ^v([0-9]+\.[0-9]+\.[0-9]+)$ ]]; then
        printf '%s\tstable\n' "${BASH_REMATCH[1]}"
        return 0
    fi
    if [[ "$tag" =~ ^v([0-9]+\.[0-9]+\.[0-9]+)-dev\.([0-9]+)$ ]]; then
        printf '%s~dev.%s\tdev\n' "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}"
        return 0
    fi
    fail "Unsupported release tag format: $tag (expected vX.Y.Z or vX.Y.Z-dev.N)."
}

infer_channel_from_version() {
    local ver=$1
    if [[ "$ver" == *"~dev"* ]]; then
        printf 'dev\n'
    else
        printf 'stable\n'
    fi
}

resolve_version_from_git() {
    local repo=$1 tag matched='' count=0
    while IFS= read -r tag; do
        [[ -n "$tag" ]] || continue
        if [[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-dev\.[0-9]+)?$ ]]; then
            matched=$tag
            count=$((count + 1))
        fi
    done < <(git -C "$repo" tag --points-at HEAD)
    if (( count == 0 )); then
        fail "No release tag (vX.Y.Z or vX.Y.Z-dev.N) points to HEAD. Pass --version explicitly or tag HEAD first."
    fi
    if (( count > 1 )); then
        fail "Multiple release tags point to HEAD. Pass --version explicitly."
    fi
    parse_release_tag "$matched"
}

auto_find_sysdebs_dir() {
    local repo=$1 candidate
    for candidate in "$repo"/dist/omb-deb-*/apt-repo-final; do
        if [[ -d "$candidate" && -f "$candidate/Packages.gz" ]]; then
            printf '%s\n' "$candidate"
            return 0
        fi
    done
    return 1
}

stage_sysdebs() {
    local src=$1 dst=$2
    [[ -d "$src" && -f "$src/Packages.gz" ]] || fail "System .deb directory missing Packages.gz: $src"
    mkdir -p "$dst"
    local f
    for f in "$src"/*.deb; do
        [[ -f "$f" ]] || continue
        case "$(basename "$f")" in
            omb_*.deb) continue ;;
        esac
        cp "$f" "$dst/"
    done
    python3 - "$src/Packages.gz" "$dst/Packages.gz" <<'PY'
import gzip, pathlib, sys
src_gz, dst_gz = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2])
text = gzip.decompress(src_gz.read_bytes()).decode("utf-8")
stanzas = [s for s in text.strip().split("\n\n") if s.strip() and not s.startswith("Package: omb\n")]
if not stanzas:
    sys.exit("No system packages found in Packages.gz after filtering omb.")
dst_gz.write_bytes(gzip.compress(("\n\n".join(stanzas) + "\n\n").encode("utf-8"), mtime=0))
PY
}

stage_repo_payload() {
    local repo=$1 work=$2 sysdebs_dir=$3 assets_dir=$4
    local payload="$work/payload"
    mkdir -p \
        "$payload/bin" \
        "$payload/deploy/systemd" \
        "$payload/deploy/boot-assets/windows-profiles" \
        "$payload/deploy/samba" \
        "$payload/deploy/sql/migrations/omb_boot" \
        "$payload/deploy/sql/migrations/omb_images" \
        "$payload/deploy/sql/migrations/omb_control" \
        "$payload/scripts" \
        "$payload/assets" \
        "$payload/debs"

    printf 'Building 8 linux/amd64 binaries...\n'
    local bin
    for bin in "${BINARIES[@]}"; do
        (cd "$repo" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "$payload/bin/$bin" "./cmd/$bin")
        chmod 0755 "$payload/bin/$bin"
    done

    cp "$repo"/deploy/systemd/omb-*.service "$payload/deploy/systemd/"
    cp "$repo"/deploy/boot-assets/windows-profiles/omb-setup.cmd.in \
       "$repo"/deploy/boot-assets/windows-profiles/winpeshl.ini \
       "$payload/deploy/boot-assets/windows-profiles/"
    cp "$repo/deploy/samba/smb.conf" "$payload/deploy/samba/"
    cp "$repo"/deploy/sql/boot.sql "$repo"/deploy/sql/images.sql "$repo"/deploy/sql/control.sql "$payload/deploy/sql/"

    local db f
    for db in omb_boot omb_images omb_control; do
        if [[ -d "$repo/deploy/sql/migrations/$db" ]]; then
            for f in "$repo/deploy/sql/migrations/$db"/*.sql; do
                [[ -f "$f" ]] || continue
                cp "$f" "$payload/deploy/sql/migrations/$db/"
            done
        fi
    done

    cp "$repo/scripts/prepare-ubuntu-source.sh" "$repo/scripts/fetch-ipxe.sh" "$payload/scripts/"
    chmod 0755 "$payload/scripts/prepare-ubuntu-source.sh" "$payload/scripts/fetch-ipxe.sh"

    stage_sysdebs "$sysdebs_dir" "$payload/debs"

    if [[ -n "$assets_dir" ]]; then
        local asset
        for asset in ipxeboot.tar.gz ipxe-shimx64.efi wimboot; do
            [[ -f "$assets_dir/$asset" ]] || fail "Missing boot asset: $assets_dir/$asset"
            cp "$assets_dir/$asset" "$payload/assets/$asset"
        done
    fi
}

main() {
    local repo
    repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

    local version='' channel='' sign_key='' sysdebs_dir='' assets_dir='' out_dir='' remote='' apply_ip=''
    local allow_channel_switch=false allow_dirty=false

    while (( $# > 0 )); do
        case "$1" in
            --version) [[ $# -ge 2 ]] || fail "--version requires a value"; version=$2; shift 2 ;;
            --channel) [[ $# -ge 2 ]] || fail "--channel requires a value"; channel=$2; shift 2 ;;
            --sign-key) [[ $# -ge 2 ]] || fail "--sign-key requires a value"; sign_key=$2; shift 2 ;;
            --sysdebs-dir) [[ $# -ge 2 ]] || fail "--sysdebs-dir requires a value"; sysdebs_dir=$2; shift 2 ;;
            --assets-dir) [[ $# -ge 2 ]] || fail "--assets-dir requires a value"; assets_dir=$2; shift 2 ;;
            --out) [[ $# -ge 2 ]] || fail "--out requires a value"; out_dir=$2; shift 2 ;;
            --remote) [[ $# -ge 2 ]] || fail "--remote requires a value"; remote=$2; shift 2 ;;
            --apply) [[ $# -ge 2 ]] || fail "--apply requires a management IP"; apply_ip=$2; shift 2 ;;
            --allow-channel-switch) allow_channel_switch=true; shift ;;
            --allow-dirty) allow_dirty=true; shift ;;
            -h|--help) usage; exit 0 ;;
            *) fail "Unknown argument: $1 (see --help)" ;;
        esac
    done

    [[ -n "$out_dir" || -n "$apply_ip" ]] || fail "Specify at least --out PATH or --apply MANAGEMENT_IP."
    if $allow_channel_switch && [[ -z "$apply_ip" ]]; then
        fail "--allow-channel-switch requires --apply MANAGEMENT_IP."
    fi

    if [[ -z "$version" ]]; then
        local tag_meta
        tag_meta=$(resolve_version_from_git "$repo")
        IFS=$'\t' read -r version tag_channel <<< "$tag_meta"
        [[ -n "$channel" ]] || channel=$tag_channel
    fi
    if [[ -z "$channel" ]]; then
        channel=$(infer_channel_from_version "$version")
    fi
    [[ "$channel" == stable || "$channel" == dev ]] || fail "Unsupported channel: $channel (expected stable or dev)."

    if [[ "$channel" == stable && -z "$sign_key" ]]; then
        fail "Channel 'stable' requires --sign-key PATH."
    fi
    if [[ -n "$sign_key" && ! -f "$sign_key" ]]; then
        fail "Signing key not found: $sign_key"
    fi
    if [[ -n "$out_dir" && -e "$out_dir" ]]; then
        fail "Output directory already exists: $out_dir"
    fi

    if ! $allow_dirty; then
        if ! git -C "$repo" diff --quiet || ! git -C "$repo" diff --cached --quiet; then
            fail "Working tree has uncommitted changes. Commit first or pass --allow-dirty."
        fi
    fi
    local commit
    commit=$(git -C "$repo" rev-parse HEAD)

    if [[ -z "$sysdebs_dir" ]]; then
        sysdebs_dir=$(auto_find_sysdebs_dir "$repo") || fail "Could not find system .deb cache; pass --sysdebs-dir PATH."
    fi
    if [[ -z "$remote" && -z "$assets_dir" ]]; then
        if [[ -d /usr/share/omb/assets ]]; then
            assets_dir=/usr/share/omb/assets
        else
            fail "Local build requires --assets-dir PATH (containing ipxeboot.tar.gz, ipxe-shimx64.efi, wimboot)."
        fi
    fi

    local work
    work=$(mktemp -d "${TMPDIR:-/tmp}/omb-release-build.XXXXXX")
    CLEANUP_WORK=$work
    CLEANUP_REMOTE=$remote
    trap cleanup EXIT

    # Prepare signing key (temporary ephemeral key allowed only for dev)
    local effective_key=$sign_key
    if [[ -z "$effective_key" ]]; then
        printf 'WARNING: no --sign-key provided for dev release; generating ephemeral OpenSSL key.\n' >&2
        effective_key="$work/ephemeral-sign.pem"
        openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out "$effective_key" 2>/dev/null
    fi
    openssl pkey -in "$effective_key" -pubout -out "$work/pub.pem" 2>/dev/null

    stage_repo_payload "$repo" "$work" "$sysdebs_dir" "$assets_dir"

    if [[ -z "$remote" ]]; then
        command -v dpkg-deb >/dev/null 2>&1 || fail "dpkg-deb not found locally. Use --remote USER@UBUNTU_HOST on macOS."
        local target_repo=${out_dir:-"$work/apt-repo"}
        python3 "$repo/scripts/offline-deb.py" build "$work/payload" "$target_repo" \
            --version "$version" \
            --channel "$channel" \
            --commit "$commit" \
            --license "$repo/LICENSE" \
            --go-mod "$repo/go.mod" \
            --unattend-upstream "$repo/internal/unattend/UPSTREAM.md" \
            --sign-key "$effective_key"
        python3 "$repo/scripts/offline-deb.py" verify-repo "$target_repo" --public-key "$work/pub.pem" --print-package

        if [[ -n "$apply_ip" ]]; then
            local extra=()
            if $allow_channel_switch; then extra+=(--allow-channel-switch); fi
            sudo bash "$repo/scripts/offline-deploy.sh" update ${extra[@]+"${extra[@]}"} "$target_repo" "$work/pub.pem" "$apply_ip"
        fi
        printf 'Release %s (%s, %s) complete.\n' "$version" "$channel" "${commit:0:7}"
        return 0
    fi

    # Remote Ubuntu build (+ optional apply and/or fetch back --out)
    local remote_work
    remote_work=$(ssh "$remote" 'mktemp -d /tmp/omb-remote-build.XXXXXX')
    CLEANUP_REMOTE_WORK=$remote_work
    printf 'Syncing payload to %s:%s...\n' "$remote" "$remote_work"
    ssh "$remote" "mkdir -p '$remote_work/scripts' '$remote_work/meta'"
    rsync -az "$work/payload/" "$remote:$remote_work/payload/"
    scp -q "$repo/scripts/offline-deb.py" "$repo/scripts/offline-deploy.sh" "$repo/scripts/offline-install-lib.sh" \
        "$remote:$remote_work/scripts/"
    scp -q "$repo/LICENSE" "$repo/go.mod" "$repo/internal/unattend/UPSTREAM.md" "$remote:$remote_work/meta/"
    scp -q "$effective_key" "$remote:$remote_work/sign.pem"
    scp -q "$work/pub.pem" "$remote:$remote_work/pub.pem"

    local assets_mode=remote
    if [[ -n "$assets_dir" ]]; then assets_mode=local; fi
    local apply_arg=${apply_ip:-none}
    local switch_arg=none
    if $allow_channel_switch; then switch_arg='--allow-channel-switch'; fi

    ssh "$remote" "bash -euo pipefail -s -- '$remote_work' '$version' '$channel' '$commit' '$assets_mode' '$apply_arg' '$switch_arg'" <<'REMOTE'
w=$1
ver=$2
ch=$3
commit=$4
assets_mode=$5
apply_arg=$6
switch_arg=$7

chmod 0600 "$w/sign.pem"
if [[ "$assets_mode" == remote ]]; then
    for a in ipxeboot.tar.gz ipxe-shimx64.efi wimboot; do
        [[ -f "/usr/share/omb/assets/$a" ]] || { echo "Missing /usr/share/omb/assets/$a on remote host" >&2; exit 1; }
        cp "/usr/share/omb/assets/$a" "$w/payload/assets/$a"
    done
fi

python3 "$w/scripts/offline-deb.py" build "$w/payload" "$w/apt-repo" \
    --version "$ver" \
    --channel "$ch" \
    --commit "$commit" \
    --license "$w/meta/LICENSE" \
    --go-mod "$w/meta/go.mod" \
    --unattend-upstream "$w/meta/UPSTREAM.md" \
    --sign-key "$w/sign.pem"

rm -f "$w/sign.pem"
python3 "$w/scripts/offline-deb.py" verify-repo "$w/apt-repo" --public-key "$w/pub.pem" --print-package

if [[ "$apply_arg" != none ]]; then
    if [[ "$switch_arg" != none ]]; then
        sudo bash "$w/scripts/offline-deploy.sh" update "$switch_arg" "$w/apt-repo" "$w/pub.pem" "$apply_arg"
    else
        sudo bash "$w/scripts/offline-deploy.sh" update "$w/apt-repo" "$w/pub.pem" "$apply_arg"
    fi
    dpkg-query -W -f='INSTALLED: ${db:Status-Status} ${Package} ${Version}\n' omb
    verify_out=$(dpkg --verify omb)
    [[ -z "$verify_out" ]] || { echo "dpkg --verify omb failed: $verify_out" >&2; exit 1; }
    echo "DPKG_VERIFY: CLEAN"
    for svc in omb-control omb-boot omb-agent omb-images omb-dnsmasq postgresql smbd; do
        state=$(systemctl is-active "$svc")
        [[ "$state" == active ]] || { echo "Service $svc is $state" >&2; exit 1; }
    done
    echo "SERVICES: 7/7 active"
    code=$(curl -sk -o /dev/null -w '%{http_code}' "https://$apply_arg:8443/login")
    [[ "$code" == 200 ]] || { echo "HTTPS /login returned $code" >&2; exit 1; }
    echo "HTTPS_LOGIN: $code"
    cur_sha=$(sudo sha256sum /var/lib/omb-deployment/current.deb | awk '{print $1}')
    pkg_sha=$(sudo cat /var/lib/omb-deployment/PACKAGE_SHA256)
    [[ "$cur_sha" == "$pkg_sha" ]] || { echo "current.deb SHA mismatch" >&2; exit 1; }
    echo "CURRENT_DEB: OK ($cur_sha)"
fi
REMOTE

    if [[ -n "$out_dir" ]]; then
        mkdir -p "$out_dir"
        rsync -az "$remote:$remote_work/apt-repo/" "$out_dir/"
        printf 'Saved signed apt-repo to %s\n' "$out_dir"
    fi

    printf 'Release %s (%s, %s) complete.\n' "$version" "$channel" "${commit:0:7}"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
