#!/usr/bin/env bash
set -euo pipefail
repo=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d /tmp/omb-legacy-preflight.XXXXXX)
work=$(cd "$work" && pwd -P)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin" "$work/media/casper" "$work/media/EFI/boot" "$work/media/.disk" "$work/old"
printf 'Ubuntu Desktop - Release amd64\n' > "$work/media/.disk/info"
printf '%s\n' '- id: ubuntu-desktop' '  name: Desktop' > "$work/media/casper/install-sources.yaml"
printf kernel > "$work/media/casper/vmlinuz"
printf initrd > "$work/media/casper/initrd"
printf shim > "$work/media/EFI/boot/bootx64.efi"
printf iso > "$work/desktop.iso"
cp "$work/media/casper/vmlinuz" "$work/old/vmlinuz"
cp "$work/media/casper/initrd" "$work/old/initrd"
cp "$work/media/EFI/boot/bootx64.efi" "$work/old/bootx64.efi"
cat > "$work/bin/id" <<'MOCK'
#!/usr/bin/env bash
printf '0\n'
MOCK
cat > "$work/bin/mount" <<'MOCK'
#!/usr/bin/env bash
cp -R "$TEST_MEDIA/." "${!#}/"
MOCK
cat > "$work/bin/umount" <<'MOCK'
#!/usr/bin/env bash
exit 0
MOCK
cat > "$work/bin/stat" <<'MOCK'
#!/usr/bin/env bash
wc -c < "${!#}" | tr -d ' '
MOCK
chmod +x "$work/bin/"*
export PATH="$work/bin:$PATH" TEST_MEDIA="$work/media"
(cd "$repo" && GOTOOLCHAIN=go1.26.0 TMPDIR=/tmp go build -o "$work/verifier" ./cmd/omb-ubuntu-source-check)
bash "$repo/scripts/preflight-legacy-ubuntu.sh" --iso "$work/desktop.iso" --legacy-assets "$work/old" --output "$work/verified" --verifier "$work/verifier"
test -s "$work/verified/attestation.json"
test -s "$work/verified/ubuntu-sources/1/$(sha256sum "$work/desktop.iso" | cut -d ' ' -f 1)/manifest.json"
cmp "$work/old/vmlinuz" "$work/media/casper/vmlinuz"
printf changed > "$work/old/initrd"
if bash "$repo/scripts/preflight-legacy-ubuntu.sh" --iso "$work/desktop.iso" --legacy-assets "$work/old" --output "$work/rejected" --verifier "$work/verifier" >/dev/null 2>&1; then
  echo 'accepted legacy files from a different ISO' >&2; exit 1
fi
test ! -e "$work/rejected"
test "$(cat "$work/old/initrd")" = changed
ln -s "$work/desktop.iso" "$work/link.iso"
if bash "$repo/scripts/preflight-legacy-ubuntu.sh" --iso "$work/link.iso" --legacy-assets "$work/old" --output "$work/rejected-link" --verifier "$work/verifier" >/dev/null 2>&1; then
  echo 'accepted symlink ISO' >&2; exit 1
fi
if bash "$repo/scripts/preflight-legacy-ubuntu.sh" --iso "$work/desktop.iso" --legacy-assets "$work/old" --output "$work/verified" --verifier "$work/verifier" >/dev/null 2>&1; then
  echo 'overwrote prior attestation' >&2; exit 1
fi
printf 'legacy preflight: verified assets and rejected mismatched or unsafe input\n'
