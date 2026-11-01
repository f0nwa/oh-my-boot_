#!/usr/bin/env bash
# Prepare a single Ubuntu ISO while Boot is stopped and no clients are installing.
set -euo pipefail
iso= assets=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --iso) [ "$#" -ge 2 ] || exit 2; iso=$2; shift 2 ;;
    --assets) [ "$#" -ge 2 ] || exit 2; assets=$2; shift 2 ;;
    *) echo 'usage: prepare-ubuntu-source.sh --iso ABS_PATH --assets ABS_DIR' >&2; exit 2 ;;
  esac
done
[[ "$iso" = /* && "$assets" = /* && -f "$iso" && ! -L "$iso" && ! -L "$assets" ]] || { echo 'regular ISO and absolute asset directory required' >&2; exit 2; }
[ "$(id -u)" = 0 ] || { echo 'run as root on the Ubuntu server' >&2; exit 1; }
for utility in mount umount sha256sum stat; do command -v "$utility" >/dev/null; done
mkdir -p -- "$assets"
[ ! -L "$assets/ubuntu" ] || { echo 'refusing symlink output' >&2; exit 1; }
mountpoint=$(mktemp -d /tmp/omb-ubuntu-iso.XXXXXX)
stage=$(mktemp -d "$assets/.ubuntu-stage.XXXXXX")
mounted=false
backup=
cleanup() {
  rc=$?
  trap - EXIT
  if [ "$mounted" = true ]; then
    if ! umount -- "$mountpoint"; then printf 'failed to unmount temporary ISO; directory preserved at %s\n' "$mountpoint" >&2; exit 1; fi
  fi
  rm -rf -- "$mountpoint"
  if [ -n "$stage" ]; then rm -rf -- "$stage"; fi
  if [ "$rc" -ne 0 ] && [ -n "$backup" ] && [ ! -e "$assets/ubuntu" ]; then mv -- "$backup" "$assets/ubuntu"; fi
  exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
iso_hash=$(sha256sum -- "$iso"); iso_hash=${iso_hash%% *}
iso_size=$(stat -c %s -- "$iso")
mount -o loop,ro -- "$iso" "$mountpoint"
mounted=true
for directory in casper EFI EFI/boot; do [ ! -L "$mountpoint/$directory" ] || { echo 'symlink in ISO source' >&2; exit 1; }; done
for pair in casper/vmlinuz:vmlinuz casper/initrd:initrd EFI/boot/bootx64.efi:bootx64.efi casper/install-sources.yaml:install-sources.yaml; do
  source=${pair%:*}; name=${pair#*:}
  [ -f "$mountpoint/$source" ] && [ ! -L "$mountpoint/$source" ] || { echo 'incomplete Ubuntu ISO' >&2; exit 1; }
  cp -- "$mountpoint/$source" "$stage/$name"
done
# A modified ISO during preparation must not produce a trusted manifest.
final_hash=$(sha256sum -- "$iso"); final_hash=${final_hash%% *}
[ "$final_hash" = "$iso_hash" ] || { echo 'ISO changed during preparation' >&2; exit 1; }
{
  printf '{"version":1,"iso_sha256":"%s","iso_size":%s,"files":{' "$iso_hash" "$iso_size"
  separator=
  for name in vmlinuz initrd bootx64.efi install-sources.yaml; do
    digest=$(sha256sum -- "$stage/$name"); digest=${digest%% *}
    printf '%s"%s":"%s"' "$separator" "$name" "$digest"
    separator=,
  done
  printf '}}\n'
} > "$stage/manifest.json"
chmod 0755 "$stage"
chmod 0644 "$stage/"*
umount -- "$mountpoint"
mounted=false
if [ -e "$assets/ubuntu" ]; then
  backup=$(mktemp -d "$assets/.ubuntu-backup.XXXXXX")
  rmdir -- "$backup"
  mv -- "$assets/ubuntu" "$backup"
fi
mv -- "$stage" "$assets/ubuntu"
stage=
printf 'Prepared Ubuntu source %s\n' "$iso_hash"
if [ -n "$backup" ]; then printf 'Previous assets preserved at %s\n' "$backup"; fi
