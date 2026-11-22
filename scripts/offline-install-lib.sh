# Installation steps for offline-deploy.sh. Source only from its trusted copy.

install_packages() (
    set -euo pipefail
    local release=$1 deb=${2:-}
    shift
    if (( $# )); then shift; fi
    local_bundle=$(mktemp -d /var/tmp/omb-apt.XXXXXXXX) || return 1
    policy=/usr/sbin/policy-rc.d
    owns_policy=false
    trap 'if $owns_policy; then unlink "$policy"; fi; rm -r -- "$local_bundle"' EXIT
    if [[ -n "$release" && ( -e "$policy" || -L "$policy" ) ]]; then
        fail 'A package service policy already exists.'
    fi
    # _apt must be able to traverse these paths. Only verified packages go here.
    chmod 0755 "$local_bundle" || return 1
    install -d -m 0755 "$local_bundle"/{debs,empty,lists,archives} || return 1
    : > "$local_bundle/offline.list" || return 1
    local packages=()
    if [[ -n "$release" ]]; then
        install -m 0644 "$release"/debs/*.deb "$release/debs/Packages.gz" "$local_bundle/debs/" || return 1
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
    if [[ -n "$release" ]]; then apt_local update || return 1; fi
    apt_local -s "${apt_options[@]}" install "${packages[@]}" > "$local_bundle/plan" || return 1
    if [[ -z "$release" ]]; then
        awk '/^(Inst|Conf|Remv) / && $2 != "omb" {exit 1}' "$local_bundle/plan" ||
            fail 'The package change would affect other installed packages.'
    elif grep -Eq '^Inst [^ ]+ \[' "$local_bundle/plan"; then
        fail 'The selected Ubuntu base would require upgrading an installed package.'
    fi
    if [[ -n "$release" ]]; then
        (set -C; printf '%s\n' '#!/bin/sh' 'exit 101' > "$policy") || return 1
        owns_policy=true
        chmod 0755 "$policy" || return 1
    fi
    apt_local -y "${apt_options[@]}" install "${packages[@]}"
)

resolve_pxe_interface() {
    local pxe_iface=$1
    if [[ "$pxe_iface" == none ]]; then
        # The bundled Control requires a nonempty allowlist. Use a reserved
        # name that cannot match a current interface in management-only mode.
        if ip link show dev omb-pxe-off >/dev/null 2>&1; then
            fail 'The reserved interface name omb-pxe-off is in use.'
        fi
        printf '%s\n' omb-pxe-off
        return
    fi
    [[ "$pxe_iface" =~ ^[[:alnum:]_.-]+$ ]] || fail 'Invalid PXE interface name.'
    ip link show dev "$pxe_iface" >/dev/null || fail "PXE interface $pxe_iface is unavailable."
    if ip -o -4 addr show dev "$pxe_iface" | awk -v ip="$address" '{split($4,a,"/"); if (a[1]==ip) found=1} END {exit !found}'; then
        fail 'Use a separate management interface.'
    fi
    printf '%s\n' "$pxe_iface"
}

fresh_install() {
    local archive=$1 key=$2 pxe_iface release="$work/install" commit account svc path
    local password confirmation dbs steps db file iso_password boot_url="http://$address:8080" deb='' package_version=''
    local boot_dsn='host=/var/run/postgresql dbname=omb_boot user=omb-boot'
    local control_dsn='host=/var/run/postgresql dbname=omb_control user=omb-control'
    local images_dsn='host=/var/run/postgresql dbname=omb_images user=omb-images'
    pxe_iface=$(resolve_pxe_interface "$3")
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
    extract "$archive" "$release" "$key" --check-host
    commit=$(manifest_commit "$release")
    if $package_mode; then
        deb="$work/install.deb"
        stage_deb "$4" "$5" "$deb"
        package_version=$(package_identity "$deb" "$commit")
        match_package_binaries "$deb" "$work/package" "$release"
    fi
    steps=$(python3 - "$release/manifest.json" <<'PY'
import json, sys
for step in json.load(open(sys.argv[1]))["migrations"]:
    print(step["database"], step["path"], sep="\t")
PY
    )
    IFS= read -rs -p 'New OMB administrator password (at least 12 characters): ' password </dev/tty
    printf '\n'
    IFS= read -rs -p 'Repeat OMB administrator password: ' confirmation </dev/tty
    printf '\n'
    [[ ${#password} -ge 12 && "$confirmation" == "$password" ]] || fail 'Passwords do not match or are too short.'
    unset confirmation

    install_packages "$release" "$deb"
    if $package_mode; then
        installed_package_matches "$package_version" "$commit" "$deb" || fail 'The installed package failed verification.'
    fi
    systemctl start postgresql.service
    dbs=$(sql -d postgres -Atc "SELECT datname FROM pg_database WHERE datname IN ('omb_boot','omb_control','omb_images')")
    [[ -z "$dbs" ]] || fail 'An OMB database already exists.'
    for svc in boot control images; do
        useradd --system --user-group --home-dir "/var/lib/omb-$svc" --shell /usr/sbin/nologin "omb-$svc"
        sql -d postgres -c "CREATE ROLE \"omb-$svc\" LOGIN"
        runuser -u postgres -- createdb --owner="omb-$svc" "omb_$svc"
    done
    while IFS=$'\t' read -r db file; do
        runuser -u "omb-${db#omb_}" -- psql -Xw -v ON_ERROR_STOP=1 -d "$db" < "$release/$file"
    done <<< "$steps"
    useradd --system --user-group --home-dir /var/lib/omb-iso --shell /usr/sbin/nologin omb-iso

    if ! $package_mode; then
        install -m 0755 "$release"/bin/omb-* /usr/local/bin/
        install -d -m 0755 /usr/local/libexec /usr/share/omb/windows-profiles
        install -m 0755 "$release/scripts/prepare-ubuntu-source.sh" /usr/local/libexec/omb-prepare-ubuntu-source
        install -m 0644 "$release"/deploy/boot-assets/windows-profiles/* /usr/share/omb/windows-profiles/
        install -m 0644 "$release"/deploy/systemd/*.service /etc/systemd/system/
    fi
    install -d -m 0755 /srv/omb
    install -d -m 0750 -o root -g omb-control /etc/omb
    install -d -m 0750 /etc/omb/winiso
    install -d -m 0755 /mnt/omb-{smb,smb-upload,nfs,nfs-upload} /mnt/omb-iso/profiles /var/lib/omb-ubuntu-registry
    # The package ships pinned iPXE and shim inputs. This script has no HTTP source.
    sh "$release/scripts/fetch-ipxe.sh" \
        "file://$release/assets/ipxeboot.tar.gz" "$(sha256sum "$release/assets/ipxeboot.tar.gz" | cut -d' ' -f1)" \
        "file://$release/assets/ipxe-shimx64.efi" "$(sha256sum "$release/assets/ipxe-shimx64.efi" | cut -d' ' -f1)" \
        /srv/omb/tftp
    chmod 0755 /srv/omb/tftp
    runuser -u omb-boot -- test -r /srv/omb/tftp/ipxeboot/x86_64/undionly.kpxe
    install -d -m 0755 /srv/omb/tftp/{windows,windows-profiles,windows-answers,windows-driver-wims,windows-driver-configs,ubuntu-sources}
    install -d -m 0750 -o omb-boot -g omb-control /srv/omb/tftp/ubuntu-autoinstall
    install -m 0644 "$release/assets/wimboot" /srv/omb/tftp/windows/wimboot
    install -d -m 0750 -o omb-images -g omb-images /var/lib/omb-images
    install -d -m 2770 -o root -g omb-images /var/lib/omb-images/iso
    install -d -m 0700 -o omb-control -g omb-control /var/lib/omb-control
    install -d -m 0755 "$state"

    printf 'OMB_BOOT_BASE_URL=%s\nOMB_BOOT_DATABASE_URL="%s"\n' "$boot_url" "$boot_dsn" > /etc/omb/boot.env
    printf 'OMB_CONTROL_LISTEN=%s:8443\nOMB_CONTROL_INTERFACES=%s\nOMB_CONTROL_BOOT_BASE_URL=%s\nOMB_CONTROL_DATABASE_URL="%s"\n' \
        "$address" "$pxe_iface" "$boot_url" "$control_dsn" > /etc/omb/control.env
    printf 'OMB_IMAGES_DATABASE_URL="%s"\n' "$images_dsn" > /etc/omb/images.env

    openssl rand -hex 32 > /etc/omb/secrets.key
    openssl req -x509 -newkey rsa:3072 -sha256 -nodes -days 90 -subj '/CN=omb-control' \
        -addext "subjectAltName=IP:$address" -keyout /var/lib/omb-control/control.key \
        -out /var/lib/omb-control/control.crt
    chown omb-control:omb-control /etc/omb/secrets.key /var/lib/omb-control/control.{key,crt}
    chmod 0600 /etc/omb/secrets.key /var/lib/omb-control/control.key
    runuser -u omb-control -- test -r /etc/omb/secrets.key
    install -m 0644 /var/lib/omb-control/control.crt "$state/control.crt"
    iso_password=$(openssl rand -hex 24)
    printf '%s\n' "$iso_password" > /etc/omb/omb-iso-password
    printf '%s\n%s\n' "$iso_password" "$iso_password" | smbpasswd -s -a omb-iso
    unset iso_password

    install -m 0600 /etc/samba/smb.conf "$state/smb.conf.distribution"
    # Keep Samba local until the operator knows the PXE client subnet.
    sed -e 's/^ *interfaces *=.*/   interfaces = lo/' \
        -e 's/^ *hosts allow *=.*/   hosts allow = 127.0.0.1/' \
        "$release/deploy/samba/smb.conf" > "$work/smb.conf"
    testparm -s "$work/smb.conf" >/dev/null
    install -m 0644 "$work/smb.conf" /etc/samba/smb.conf
    systemctl disable smbd.service nmbd.service >/dev/null
    runuser -u omb-images -- env OMB_IMAGES_DATABASE_URL="$images_dsn" \
        "$binary_dir/omb-imagesctl" add-path -name Local -kind local -root /var/lib/omb-images/iso
    printf '%s\n' "$password" | runuser -u omb-control -- env OMB_CONTROL_DATABASE_URL="$control_dsn" \
        "$binary_dir/omb-controlctl" create-admin -username admin
    unset password

    systemctl daemon-reload
    systemctl enable --now omb-boot.service omb-agent.service omb-images.service omb-control.service
    health
    boot_menu | grep '^#!ipxe$'
    curl --noproxy '*' -fsS --max-time 3 -o /dev/null "$boot_url/assets/windows/wimboot"
    for svc in omb-boot omb-agent omb-images; do systemctl is-active --quiet "$svc"; done
    for svc in omb-dnsmasq smbd nmbd; do
        if systemctl is-active --quiet "$svc"; then fail "Unexpected active service: $svc."; fi
    done
    if $package_mode; then
        write_state PACKAGE "$package_version"
        write_state PACKAGE_SHA256 "$5"
    fi
    write_state COMMIT "$commit"
    printf 'Installed %s. Control: https://%s:8443/login.\n' "$commit" "$address"
}
