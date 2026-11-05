#!/usr/bin/env bash
# Verify a legacy Ubuntu source without modifying the live ISO or boot files.
set -euo pipefail
iso= legacy= output= verifier=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --iso) [ "$#" -ge 2 ] || exit 2; iso=$2; shift 2 ;;
    --legacy-assets) [ "$#" -ge 2 ] || exit 2; legacy=$2; shift 2 ;;
    --output) [ "$#" -ge 2 ] || exit 2; output=$2; shift 2 ;;
    --verifier) [ "$#" -ge 2 ] || exit 2; verifier=$2; shift 2 ;;
    *) echo 'usage: preflight-legacy-ubuntu.sh --iso ABS_PATH --legacy-assets ABS_DIR --output NEW_ABS_DIR --verifier ABS_BINARY' >&2; exit 2 ;;
  esac
done
[ "$(id -u)" = 0 ] || { echo 'run as root' >&2; exit 1; }
[[ "$iso" = /* && "$legacy" = /* && "$output" = /* && "$verifier" = /* ]] || { echo 'absolute paths required' >&2; exit 2; }
[ -f "$iso" ] && [ ! -L "$iso" ] && [ -d "$legacy" ] && [ ! -L "$legacy" ] || { echo 'ISO or legacy assets unavailable' >&2; exit 1; }
[ -f "$verifier" ] && [ ! -L "$verifier" ] && [ -x "$verifier" ] || { echo 'source verifier unavailable' >&2; exit 1; }
[ ! -e "$output" ] && [ ! -L "$output" ] || { echo 'output already exists' >&2; exit 1; }
python3 - "$iso" "$legacy" "$(dirname "$output")" "$output" "$verifier" <<'PY'
import os, stat, sys
iso, legacy, parent, output, verifier = sys.argv[1:]
for path in (iso, legacy, parent, verifier):
    if os.path.realpath(path) != path:
        raise SystemExit('symlink or noncanonical input path')
if os.path.normpath(output) != output or os.path.dirname(output) != parent:
    raise SystemExit('invalid output path')
s = os.stat(parent)
if not stat.S_ISDIR(s.st_mode) or s.st_uid != os.geteuid() or s.st_mode & 0o022:
    raise SystemExit('output parent must be private and owned by the operator')
v = os.stat(verifier)
if not stat.S_ISREG(v.st_mode) or v.st_uid != os.geteuid() or v.st_mode & 0o022:
    raise SystemExit('verifier must be owned by the operator and not writable by others')
PY
mkdir -m 0700 -- "$output"
complete=false
cleanup() { if [ "$complete" = false ]; then rm -rf -- "$output"; fi; }
trap cleanup EXIT
sum=$(sha256sum -- "$iso"); digest=${sum%% *}
[[ "$digest" =~ ^[0-9a-f]{64}$ ]] || { echo 'invalid ISO digest' >&2; exit 1; }
script_dir=$(cd "$(dirname "$0")" && pwd)
bash "$script_dir/prepare-ubuntu-source.sh" --iso "$iso" --assets "$output" --source-id 1 --sha256 "$digest"
prepared="$output/ubuntu-sources/1/$digest"
"$verifier" -iso "$iso" -source "$prepared" -sha256 "$digest"
for file in vmlinuz initrd bootx64.efi; do
  [ -f "$legacy/$file" ] && [ ! -L "$legacy/$file" ] && cmp -s -- "$legacy/$file" "$prepared/$file" || {
    echo "legacy $file does not match the ISO" >&2; exit 1;
  }
done
sum=$(sha256sum -- "$iso"); [ "${sum%% *}" = "$digest" ] || { echo 'ISO changed during preflight' >&2; exit 1; }
python3 - "$output/attestation.json" "$digest" "$prepared" <<'PY'
import hashlib, json, os, sys
out, digest, prepared = sys.argv[1:]
files = {}
for name in ('vmlinuz', 'initrd', 'bootx64.efi', 'install-sources.yaml', 'manifest.json'):
    h = hashlib.sha256()
    with open(os.path.join(prepared, name), 'rb') as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b''):
            h.update(chunk)
    files[name] = h.hexdigest()
with open(out, 'x', encoding='utf-8') as f:
    json.dump({'iso_sha256': digest, 'files': files}, f, sort_keys=True)
    f.write('\n')
PY
complete=true
printf 'Legacy Ubuntu source verified: %s\n' "$digest"
