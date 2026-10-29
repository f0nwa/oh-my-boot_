#!/bin/sh
set -eu

if [ "$#" -ne 5 ]; then
  echo "usage: $0 RELEASE_URL RELEASE_SHA256 SHIM_URL SHIM_SHA256 DESTINATION" >&2
  exit 2
fi

url=$1
expected=$2
shim_url=$3
shim_expected=$4
destination=$5
for digest in "$expected" "$shim_expected"; do
  case "$digest" in
    *[!0-9a-fA-F]* | '') echo 'SHA-256 must be hexadecimal' >&2; exit 2 ;;
  esac
  if [ "${#digest}" -ne 64 ]; then
    echo 'SHA-256 must contain 64 characters' >&2
    exit 2
  fi
done
if [ -e "$destination" ]; then
  echo "destination already exists: $destination" >&2
  exit 2
fi
parent=$(dirname -- "$destination")
mkdir -p "$parent"
tmp=$(mktemp -d "$parent/.ipxe-download.XXXXXX")
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
curl -fsSL --retry 3 --output "$tmp/archive.tar.gz" "$url"
curl -fsSL --retry 3 --output "$tmp/shimx64.efi" "$shim_url"
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp/archive.tar.gz" | cut -d ' ' -f 1)
  shim_actual=$(sha256sum "$tmp/shimx64.efi" | cut -d ' ' -f 1)
else
  actual=$(shasum -a 256 "$tmp/archive.tar.gz" | cut -d ' ' -f 1)
  shim_actual=$(shasum -a 256 "$tmp/shimx64.efi" | cut -d ' ' -f 1)
fi
if [ "$actual" != "$expected" ]; then
  echo "release SHA-256 mismatch: got $actual" >&2
  exit 1
fi
if [ "$shim_actual" != "$shim_expected" ]; then
  echo "shim SHA-256 mismatch: got $shim_actual" >&2
  exit 1
fi
mkdir "$tmp/extracted"
tar -xzf "$tmp/archive.tar.gz" -C "$tmp/extracted"
for path in \
  ipxeboot/x86_64/undionly.kpxe \
  ipxeboot/x86_64-sb/ipxe-shim.efi \
  ipxeboot/x86_64-sb/ipxe.efi \
  ipxeboot/x86_64-sb/snponly-shim.efi \
  ipxeboot/x86_64-sb/snponly.efi; do
  if [ ! -f "$tmp/extracted/$path" ]; then
    echo "missing boot asset: $path" >&2
    exit 1
  fi
done
shim_dir=$tmp/extracted/ipxeboot/x86_64-sb
for link in ipxe-shim.efi snponly-shim.efi; do
  if [ ! -L "$shim_dir/$link" ] || [ "$(readlink "$shim_dir/$link")" != shimx64.efi ]; then
    echo "unexpected shim link: $link" >&2
    exit 1
  fi
done
cp "$tmp/shimx64.efi" "$shim_dir/shimx64.efi"
mv "$tmp/extracted" "$destination"
echo "verified iPXE assets installed in $destination"
