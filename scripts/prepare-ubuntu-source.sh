#!/usr/bin/env bash
# Prepare a legacy source or an isolated, digest-addressed Ubuntu ISO.
set -euo pipefail
original_args=("$@")
iso= assets= source_id= expected_sha=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --iso) [ "$#" -ge 2 ] || exit 2; iso=$2; shift 2 ;;
    --assets) [ "$#" -ge 2 ] || exit 2; assets=$2; shift 2 ;;
    --source-id) [ "$#" -ge 2 ] || exit 2; source_id=$2; shift 2 ;;
    --sha256) [ "$#" -ge 2 ] || exit 2; expected_sha=$2; shift 2 ;;
    *) echo 'usage: prepare-ubuntu-source.sh --iso ABS_PATH --assets ABS_DIR [--source-id ID --sha256 SHA256]' >&2; exit 2 ;;
  esac
done
[[ "$iso" = /* && "$assets" = /* && -f "$iso" && ! -L "$iso" && ! -L "$assets" ]] || { echo 'regular ISO and absolute asset directory required' >&2; exit 2; }
[ "$(id -u)" = 0 ] || { echo 'run as root on the Ubuntu server' >&2; exit 1; }
for utility in mount umount sha256sum stat; do command -v "$utility" >/dev/null; done
mkdir -p -- "$assets"
target="$assets/ubuntu"
stage_root="$assets"
if [ -n "$source_id" ] || [ -n "$expected_sha" ]; then
  [[ "$source_id" =~ ^[1-9][0-9]*$ && "$expected_sha" =~ ^[0-9a-f]{64}$ ]] || { echo 'invalid source ID or digest' >&2; exit 2; }
  for utility in python3 grep cmp; do command -v "$utility" >/dev/null || { echo "required utility missing: $utility" >&2; exit 1; }; done
  for directory in "$assets/ubuntu-sources" "$assets/ubuntu-sources/$source_id"; do
    [ ! -L "$directory" ] || { echo 'refusing symlink output' >&2; exit 1; }
    mkdir -p -- "$directory"
    [ -d "$directory" ] && [ ! -L "$directory" ] || { echo 'invalid source directory' >&2; exit 1; }
  done
  stage_root="$assets/ubuntu-sources/$source_id"
  target="$stage_root/$expected_sha"
  if [ "${OMB_UBUNTU_PREP_LOCKED:-}" != 1 ]; then
    python3 - "$stage_root/.lock-$expected_sha" "$0" "${original_args[@]}" <<'PY'
import fcntl
import os
import subprocess
import sys

fd = os.open(sys.argv[1], os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
fcntl.flock(fd, fcntl.LOCK_EX)
env = dict(os.environ, OMB_UBUNTU_PREP_LOCKED="1")
result = subprocess.run(sys.argv[2:], env=env, pass_fds=(fd,), check=False)
sys.exit(result.returncode)
PY
    exit $?
  fi
fi
[ ! -L "$target" ] || { echo 'refusing symlink output' >&2; exit 1; }
mountpoint=$(mktemp -d /tmp/omb-ubuntu-iso.XXXXXX)
stage=$(mktemp -d "$stage_root/.ubuntu-stage.XXXXXX")
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
  if [ "$rc" -ne 0 ] && [ -n "$backup" ] && [ ! -e "$target" ]; then mv -- "$backup" "$target"; fi
  exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
iso_hash=$(sha256sum -- "$iso"); iso_hash=${iso_hash%% *}
[ -z "$expected_sha" ] || [ "$iso_hash" = "$expected_sha" ] || { echo 'ISO digest does not match source identity' >&2; exit 1; }
iso_size=$(stat -c %s -- "$iso")
mount -o loop,ro -- "$iso" "$mountpoint"
mounted=true
for directory in casper EFI EFI/boot; do [ ! -L "$mountpoint/$directory" ] || { echo 'symlink in ISO source' >&2; exit 1; }; done
if [ -n "$source_id" ]; then
  [ ! -L "$mountpoint/.disk" ] && [ ! -L "$mountpoint/.disk/info" ] && [ -f "$mountpoint/.disk/info" ] && \
    LC_ALL=C grep -Eqi '^Ubuntu[[:space:]].*([[:space:]]|-)amd64([[:space:]]|$)' "$mountpoint/.disk/info" || { echo 'unsupported Ubuntu architecture or distribution' >&2; exit 1; }
  [ -f "$mountpoint/casper/install-sources.yaml" ] && [ ! -L "$mountpoint/casper/install-sources.yaml" ] && \
    LC_ALL=C grep -Eq '^[[:space:]]*(-[[:space:]]*)?id:[[:space:]]*ubuntu-desktop(-[a-z0-9-]+)?[[:space:]]*$' "$mountpoint/casper/install-sources.yaml" || { echo 'not a supported Ubuntu Desktop installer' >&2; exit 1; }
fi
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
if [ -n "$source_id" ] && [ -e "$target" ]; then
  [ -d "$target" ] && [ ! -L "$target" ] || { echo 'invalid existing source' >&2; exit 1; }
  for file in manifest.json vmlinuz initrd bootx64.efi install-sources.yaml; do
    [ -f "$target/$file" ] && [ ! -L "$target/$file" ] && cmp -s -- "$stage/$file" "$target/$file" || { echo 'existing source does not match verified ISO' >&2; exit 1; }
  done
  rm -rf -- "$stage"
  stage=
  printf 'Prepared Ubuntu source %s\n' "$iso_hash"
  exit 0
fi
if [ -e "$target" ]; then
  backup=$(mktemp -d "$assets/.ubuntu-backup.XXXXXX")
  rmdir -- "$backup"
  mv -- "$target" "$backup"
fi
mv -- "$stage" "$target"
stage=
printf 'Prepared Ubuntu source %s\n' "$iso_hash"
if [ -n "$backup" ]; then printf 'Previous assets preserved at %s\n' "$backup"; fi
