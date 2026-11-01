#!/usr/bin/env bash
set -euo pipefail
repo=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d /tmp/omb-source-test.XXXXXX)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin" "$work/media/casper" "$work/media/EFI/boot" "$work/assets/ubuntu"
printf old > "$work/assets/ubuntu/initrd"
printf iso > "$work/desktop.iso"
printf kernel > "$work/media/casper/vmlinuz"
printf initrd > "$work/media/casper/initrd"
printf shim > "$work/media/EFI/boot/bootx64.efi"
printf '%s\n' '- id: ubuntu-desktop' '  name: Desktop' > "$work/media/casper/install-sources.yaml"
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
printf 'unmounted\n' >> "$TEST_UNMOUNT_LOG"
MOCK
cat > "$work/bin/stat" <<'MOCK'
#!/usr/bin/env bash
wc -c < "${!#}" | tr -d ' '
MOCK
chmod +x "$work/bin/"*
export PATH="$work/bin:$PATH" TEST_MEDIA="$work/media" TEST_UNMOUNT_LOG="$work/unmount.log"
bash "$repo/scripts/prepare-ubuntu-source.sh" --iso "$work/desktop.iso" --assets "$work/assets"
test -s "$work/assets/ubuntu/manifest.json"
test "$(cat "$work/assets/ubuntu/initrd")" = initrd
test -s "$work/unmount.log"
cp "$work/assets/ubuntu/manifest.json" "$work/manifest-before"
rm "$work/media/casper/initrd"
if bash "$repo/scripts/prepare-ubuntu-source.sh" --iso "$work/desktop.iso" --assets "$work/assets" >/dev/null 2>&1; then
  echo 'accepted incomplete ISO' >&2; exit 1
fi
cmp "$work/manifest-before" "$work/assets/ubuntu/manifest.json"
test "$(wc -l < "$work/unmount.log" | tr -d ' ')" = 2
printf 'source preparation: success and failure preserve existing assets\n'
