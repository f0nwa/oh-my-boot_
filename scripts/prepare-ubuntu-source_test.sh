#!/usr/bin/env bash
set -euo pipefail
repo=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d /tmp/omb-source-test.XXXXXX)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin" "$work/media/casper" "$work/media/EFI/boot" "$work/media/.disk" "$work/assets/ubuntu"
printf 'Ubuntu Desktop - Release amd64\n' > "$work/media/.disk/info"
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
# Force publication to fail after the old tree has already moved aside.
cp "$work/assets/ubuntu/initrd" "$work/media/casper/initrd"
export REAL_MV
REAL_MV=$(command -v mv)
cat > "$work/bin/mv" <<'MOCK'
#!/usr/bin/env bash
if [[ "${2:-}" == */.ubuntu-stage.* ]]; then exit 1; fi
exec "$REAL_MV" "$@"
MOCK
chmod +x "$work/bin/mv"
if bash "$repo/scripts/prepare-ubuntu-source.sh" --iso "$work/desktop.iso" --assets "$work/assets" >/dev/null 2>&1; then
  echo 'publication failure was ignored' >&2; exit 1
fi
cmp "$work/manifest-before" "$work/assets/ubuntu/manifest.json"
test "$(cat "$work/assets/ubuntu/initrd")" = initrd
rm "$work/bin/mv"
printf 'first ISO' > "$work/first.iso"
printf 'second ISO' > "$work/second.iso"
first_sha=$(sha256sum "$work/first.iso"); first_sha=${first_sha%% *}
second_sha=$(sha256sum "$work/second.iso"); second_sha=${second_sha%% *}
bash "$repo/scripts/prepare-ubuntu-source.sh" --iso "$work/first.iso" --assets "$work/assets" --source-id 1 --sha256 "$first_sha"
first="$work/assets/ubuntu-sources/1/$first_sha"
test -s "$first/manifest.json"
cp "$first/manifest.json" "$work/first-manifest"
printf second-kernel > "$work/media/casper/vmlinuz"
bash "$repo/scripts/prepare-ubuntu-source.sh" --iso "$work/second.iso" --assets "$work/assets" --source-id 2 --sha256 "$second_sha"
test -s "$work/assets/ubuntu-sources/2/$second_sha/manifest.json"
cmp "$work/first-manifest" "$first/manifest.json"
test "$(cat "$first/vmlinuz")" = kernel
if bash "$repo/scripts/prepare-ubuntu-source.sh" --iso "$work/second.iso" --assets "$work/assets" --source-id 3 --sha256 "$first_sha" >/dev/null 2>&1; then
  echo 'accepted incorrect ISO digest' >&2; exit 1
fi
test ! -e "$work/assets/ubuntu-sources/3/$first_sha"
cmp "$work/first-manifest" "$first/manifest.json"
printf 'Ubuntu Server - Release amd64\n' > "$work/media/.disk/info"
printf '%s\n' '- id: ubuntu-server' '  name: Server' > "$work/media/casper/install-sources.yaml"
if bash "$repo/scripts/prepare-ubuntu-source.sh" --iso "$work/second.iso" --assets "$work/assets" --source-id 6 --sha256 "$second_sha" >/dev/null 2>&1; then
  echo 'accepted Ubuntu Server ISO' >&2; exit 1
fi
printf 'Ubuntu Desktop - Release arm64\n' > "$work/media/.disk/info"
printf '%s\n' '- id: ubuntu-desktop' '  name: Desktop' > "$work/media/casper/install-sources.yaml"
if bash "$repo/scripts/prepare-ubuntu-source.sh" --iso "$work/second.iso" --assets "$work/assets" --source-id 6 --sha256 "$second_sha" >/dev/null 2>&1; then
  echo 'accepted non-amd64 ISO' >&2; exit 1
fi
printf 'Ubuntu Desktop - Release amd64\n' > "$work/media/.disk/info"
bash "$repo/scripts/prepare-ubuntu-source.sh" --iso "$work/second.iso" --assets "$work/assets" --source-id 4 --sha256 "$second_sha" >"$work/concurrent-1.log" 2>&1 &
first_pid=$!
bash "$repo/scripts/prepare-ubuntu-source.sh" --iso "$work/second.iso" --assets "$work/assets" --source-id 4 --sha256 "$second_sha" >"$work/concurrent-2.log" 2>&1 &
second_pid=$!
wait "$first_pid"
wait "$second_pid"
test -s "$work/assets/ubuntu-sources/4/$second_sha/manifest.json"
test "$(cat "$work/assets/ubuntu-sources/4/$second_sha/vmlinuz")" = second-kernel
test -z "$(find "$work/assets/ubuntu-sources/4" -name '.ubuntu-stage.*' -print)"
ln -s "$work/assets/ubuntu-sources/4" "$work/assets/ubuntu-sources/5"
if bash "$repo/scripts/prepare-ubuntu-source.sh" --iso "$work/second.iso" --assets "$work/assets" --source-id 5 --sha256 "$second_sha" >/dev/null 2>&1; then
  echo 'accepted symlinked source directory' >&2; exit 1
fi
printf 'source preparation: success, incomplete source and failed publication preserve assets\n'
