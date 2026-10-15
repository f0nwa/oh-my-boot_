#!/bin/sh
set -eu

if [ "$#" -ne 3 ]; then
  echo "usage: $0 RELEASE_URL SHA256 DESTINATION" >&2
  exit 2
fi

url=$1
expected=$2
destination=$3
case "$expected" in
  *[!0-9a-fA-F]* | '') echo 'SHA-256 must be hexadecimal' >&2; exit 2 ;;
esac
if [ "${#expected}" -ne 64 ]; then
  echo 'SHA-256 must contain 64 characters' >&2
  exit 2
fi
if [ -e "$destination" ]; then
  echo "destination already exists: $destination" >&2
  exit 2
fi
parent=$(dirname -- "$destination")
mkdir -p "$parent"
tmp=$(mktemp -d "$parent/.ipxe-download.XXXXXX")
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
curl -fsSL --retry 3 --output "$tmp/archive.tar.gz" "$url"
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp/archive.tar.gz" | cut -d ' ' -f 1)
else
  actual=$(shasum -a 256 "$tmp/archive.tar.gz" | cut -d ' ' -f 1)
fi
if [ "$actual" != "$expected" ]; then
  echo "SHA-256 mismatch: got $actual" >&2
  exit 1
fi
mkdir "$tmp/extracted"
tar -xzf "$tmp/archive.tar.gz" -C "$tmp/extracted"
for path in \
  ipxeboot/x86_64/undionly.kpxe \
  ipxeboot/x86_64-sb/ipxe-shim.efi \
  ipxeboot/x86_64-sb/ipxe.efi; do
  if [ ! -f "$tmp/extracted/$path" ]; then
    echo "missing boot asset: $path" >&2
    exit 1
  fi
done
mv "$tmp/extracted" "$destination"
echo "verified iPXE assets installed in $destination"
