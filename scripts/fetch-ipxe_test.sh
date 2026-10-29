#!/bin/sh
set -eu

project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
mkdir -p "$tmp/fixture/ipxeboot/x86_64-sb" "$tmp/fixture/ipxeboot/x86_64"
printf 'old-shim' > "$tmp/fixture/ipxeboot/x86_64-sb/shimx64.efi"
ln -s shimx64.efi "$tmp/fixture/ipxeboot/x86_64-sb/ipxe-shim.efi"
ln -s shimx64.efi "$tmp/fixture/ipxeboot/x86_64-sb/snponly-shim.efi"
printf 'ipxe' > "$tmp/fixture/ipxeboot/x86_64-sb/ipxe.efi"
printf 'snponly' > "$tmp/fixture/ipxeboot/x86_64-sb/snponly.efi"
printf 'bios' > "$tmp/fixture/ipxeboot/x86_64/undionly.kpxe"
printf 'dual-signed-shim' > "$tmp/dual-shim.efi"
tar -czf "$tmp/fixture.tar.gz" -C "$tmp/fixture" ipxeboot
if command -v sha256sum >/dev/null 2>&1; then
  digest=$(sha256sum "$tmp/fixture.tar.gz" | cut -d ' ' -f 1)
else
  digest=$(shasum -a 256 "$tmp/fixture.tar.gz" | cut -d ' ' -f 1)
fi
if command -v sha256sum >/dev/null 2>&1; then
  shim_digest=$(sha256sum "$tmp/dual-shim.efi" | cut -d ' ' -f 1)
else
  shim_digest=$(shasum -a 256 "$tmp/dual-shim.efi" | cut -d ' ' -f 1)
fi
wrong=0000000000000000000000000000000000000000000000000000000000000000
if "$project_dir/scripts/fetch-ipxe.sh" "file://$tmp/fixture.tar.gz" "$wrong" "file://$tmp/dual-shim.efi" "$shim_digest" "$tmp/rejected"; then
  echo 'wrong digest was accepted' >&2
  exit 1
fi
test ! -e "$tmp/rejected"
if "$project_dir/scripts/fetch-ipxe.sh" "file://$tmp/fixture.tar.gz" "$digest" "file://$tmp/dual-shim.efi" "$wrong" "$tmp/rejected-shim"; then
  echo 'wrong shim digest was accepted' >&2
  exit 1
fi
test ! -e "$tmp/rejected-shim"
"$project_dir/scripts/fetch-ipxe.sh" "file://$tmp/fixture.tar.gz" "$digest" "file://$tmp/dual-shim.efi" "$shim_digest" "$tmp/accepted"
test -f "$tmp/accepted/ipxeboot/x86_64-sb/ipxe-shim.efi"
test -L "$tmp/accepted/ipxeboot/x86_64-sb/ipxe-shim.efi"
test -L "$tmp/accepted/ipxeboot/x86_64-sb/snponly-shim.efi"
test "$(cat "$tmp/accepted/ipxeboot/x86_64-sb/shimx64.efi")" = dual-signed-shim
test -f "$tmp/accepted/ipxeboot/x86_64-sb/ipxe.efi"
test "$(cat "$tmp/accepted/ipxeboot/x86_64-sb/ipxe.efi")" = ipxe
test -f "$tmp/accepted/ipxeboot/x86_64/undionly.kpxe"
echo 'fetch-ipxe tests passed'
