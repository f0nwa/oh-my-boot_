#!/bin/sh
set -eu

project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
mkdir -p "$tmp/fixture/ipxeboot/x86_64-sb" "$tmp/fixture/ipxeboot/x86_64"
printf 'shim' > "$tmp/fixture/ipxeboot/x86_64-sb/ipxe-shim.efi"
printf 'ipxe' > "$tmp/fixture/ipxeboot/x86_64-sb/ipxe.efi"
printf 'bios' > "$tmp/fixture/ipxeboot/x86_64/undionly.kpxe"
tar -czf "$tmp/fixture.tar.gz" -C "$tmp/fixture" ipxeboot
if command -v sha256sum >/dev/null 2>&1; then
  digest=$(sha256sum "$tmp/fixture.tar.gz" | cut -d ' ' -f 1)
else
  digest=$(shasum -a 256 "$tmp/fixture.tar.gz" | cut -d ' ' -f 1)
fi
wrong=0000000000000000000000000000000000000000000000000000000000000000
if "$project_dir/scripts/fetch-ipxe.sh" "file://$tmp/fixture.tar.gz" "$wrong" "$tmp/rejected"; then
  echo 'wrong digest was accepted' >&2
  exit 1
fi
test ! -e "$tmp/rejected"
"$project_dir/scripts/fetch-ipxe.sh" "file://$tmp/fixture.tar.gz" "$digest" "$tmp/accepted"
test -f "$tmp/accepted/ipxeboot/x86_64-sb/ipxe-shim.efi"
test -f "$tmp/accepted/ipxeboot/x86_64-sb/ipxe.efi"
test -f "$tmp/accepted/ipxeboot/x86_64/undionly.kpxe"
echo 'fetch-ipxe tests passed'
