# Installation steps for offline-deploy.sh. Source only from its trusted copy.

install_packages() (
    set -euo pipefail
    local repo=$1 deb=${2:-}
    shift
    if (( $# )); then shift; fi
    local_bundle=$(mktemp -d /var/tmp/omb-apt.XXXXXXXX) || return 1
    policy=/usr/sbin/policy-rc.d
    owns_policy=false
    trap 'if $owns_policy; then unlink "$policy"; fi; rm -r -- "$local_bundle"' EXIT
    if [[ -n "$repo" && ( -e "$policy" || -L "$policy" ) ]]; then
        fail 'A package service policy already exists.'
    fi
    # _apt must be able to traverse these paths. Only verified packages go here.
    chmod 0755 "$local_bundle" || return 1
    install -d -m 0755 "$local_bundle"/{debs,empty,lists,archives} || return 1
    : > "$local_bundle/offline.list" || return 1
    local packages=()
    if [[ -n "$repo" ]]; then
        install -m 0644 "$repo"/*.deb "$repo/Packages.gz" "$local_bundle/debs/" || return 1
        printf 'deb [trusted=yes] file:%s/debs ./\n' "$local_bundle" > "$local_bundle/offline.list" || return 1
        packages=(dnsmasq-base postgresql ca-certificates curl openssl cifs-utils nfs-common samba wimtools python3)
    fi
    if [[ -n "$deb" ]]; then
        install -m 0644 "$deb" "$local_bundle/omb.deb" || return 1
        packages+=("$local_bundle/omb.deb")
    fi
    cat > "$local_bundle/offline.conf" <<APT || return 1
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
    local apt_options=(--no-install-recommends --no-remove "$@")
    if [[ -n "$repo" ]]; then apt_local update || return 1; fi
    apt_local -s "${apt_options[@]}" install "${packages[@]}" > "$local_bundle/plan" || return 1
    if [[ -z "$repo" ]]; then
        awk '/^(Inst|Conf|Remv) / && $2 != "omb" {exit 1}' "$local_bundle/plan" ||
            fail 'The package change would affect other installed packages.'
    elif grep -Eq '^Inst [^ ]+ \[' "$local_bundle/plan"; then
        fail 'The selected Ubuntu base would require upgrading an installed package.'
    fi
    if [[ -n "$repo" ]]; then
        (set -C; printf '%s\n' '#!/bin/sh' 'exit 101' > "$policy") || return 1
        owns_policy=true
        chmod 0755 "$policy" || return 1
    fi
    apt_local -y "${apt_options[@]}" install "${packages[@]}"
)

fresh_install() {
    local repo=$1 key=$2 mgmt_ip=${3:-} pxe_iface=${4:-none}
    local pkg_meta pkg_path pkg_sha package_version commit install_channel deb="$work/install.deb"
    local account svc path password confirmation

    assert_no_pending "$state"
    for path in /etc/omb /srv/omb/tftp "$state" /usr/local/bin/omb-control /usr/bin/omb-control; do
        [[ ! -e "$path" && ! -L "$path" ]] || fail "Refusing to overwrite $path."
    done
    for account in omb-boot omb-control omb-images omb-iso; do
        if getent passwd "$account" >/dev/null; then fail "Account $account exists."; fi
    done
    for svc in omb-dnsmasq smbd nmbd; do
        if systemctl is-active --quiet "$svc"; then fail "Unexpected active service: $svc."; fi
    done

    pkg_meta=$(resolve_repo_package "$repo" "$key")
    IFS=$'\t' read -r pkg_path pkg_sha package_version commit install_channel <<< "$pkg_meta"
    stage_deb "$pkg_path" "$pkg_sha" "$deb"
    [[ $(package_identity "$deb" "$commit") == "$package_version" ]] || fail 'The package does not match the repository metadata.'
    [[ $(package_channel "$deb") == "$install_channel" ]] || fail 'The package channel does not match the repository metadata.'

    if [[ -n "$mgmt_ip" ]]; then
        IFS= read -rs -p 'New OMB administrator password (at least 12 characters): ' password </dev/tty
        printf '\n'
        IFS= read -rs -p 'Repeat OMB administrator password: ' confirmation </dev/tty
        printf '\n'
        [[ ${#password} -ge 12 && "$confirmation" == "$password" ]] || fail 'Passwords do not match or are too short.'
        unset confirmation
    fi

    install_packages "$repo" "$deb"
    installed_package_matches "$package_version" "$commit" "$deb" || fail 'The installed package failed verification.'

    install -d -m 0755 "$state"
    write_current_deb "$deb"
    write_state PACKAGE "$package_version"
    write_state PACKAGE_SHA256 "$pkg_sha"
    write_state CHANNEL "$install_channel"
    write_state COMMIT "$commit"

    if [[ -z "$mgmt_ip" ]]; then
        (umask 022; "$binary_dir/omb-setup" --install-mode offline --state-dir "$state")
    else
        (umask 022; printf '%s\n' "$password" | "$binary_dir/omb-setup" \
            --non-interactive \
            --install-mode offline \
            --state-dir "$state" \
            --management-ip "$mgmt_ip" \
            --pxe-interface "$pxe_iface" \
            --dhcp-mode skip \
            --admin-password-stdin)
        unset password
    fi
}
