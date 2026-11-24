#!/usr/bin/env bash
set -euo pipefail

repo=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT

# shellcheck source=build-release.sh
source "$repo/scripts/build-release.sh"

# 1. Tag -> (deb_version, channel) conversion
res=$(parse_release_tag "v0.2.0")
test "$res" = $'0.2.0\tstable'

res=$(parse_release_tag "v0.2.0-dev.18")
test "$res" = $'0.2.0~dev.18\tdev'

res=$(parse_release_tag "v1.10.3-dev.1")
test "$res" = $'1.10.3~dev.1\tdev'

if (parse_release_tag "0.2.0") >/dev/null 2>&1; then
    echo "Accepted tag without leading v" >&2
    exit 1
fi
if (parse_release_tag "v0.2.0-rc.1") >/dev/null 2>&1; then
    echo "Accepted unsupported pre-release tag" >&2
    exit 1
fi

# 2. Infer channel from deb version
test "$(infer_channel_from_version "0.2.0")" = "stable"
test "$(infer_channel_from_version "0.2.0~dev.18")" = "dev"

# 3. Argument validation: stable requires --sign-key
if (bash "$repo/scripts/build-release.sh" --version 0.2.0 --channel stable --allow-dirty --out "$work/out1") >"$work/err1" 2>&1; then
    echo "Allowed stable release without --sign-key" >&2
    exit 1
fi
grep -Fq -- "--sign-key" "$work/err1"

# 4. Argument validation: requires at least --out or --apply
if (bash "$repo/scripts/build-release.sh" --version 0.2.0~dev.99 --channel dev --allow-dirty) >"$work/err2" 2>&1; then
    echo "Allowed invocation with neither --out nor --apply" >&2
    exit 1
fi
grep -Fq -- "--out" "$work/err2"

# 5. Argument validation: --apply requires --remote (or Linux host) and refuses missing IP
if (bash "$repo/scripts/build-release.sh" --version 0.2.0~dev.99 --channel dev --allow-dirty --apply) >"$work/err3" 2>&1; then
    echo "Allowed --apply without IP argument" >&2
    exit 1
fi

# 6. Argument validation: --allow-channel-switch only valid with --apply
if (bash "$repo/scripts/build-release.sh" --version 0.2.0~dev.99 --channel dev --allow-dirty --out "$work/out2" --allow-channel-switch) >"$work/err4" 2>&1; then
    echo "Allowed --allow-channel-switch without --apply" >&2
    exit 1
fi
grep -Fq -- "--allow-channel-switch" "$work/err4"

# 7. Clean Packages.gz filter strips old omb_* stanzas cleanly
mkdir -p "$work/sysdebs"
python3 - "$work/sysdebs" <<'PY'
import gzip, pathlib, sys
root = pathlib.Path(sys.argv[1])
stanzas = [
    "Package: curl\nVersion: 8.5.0\nArchitecture: amd64\nFilename: ./curl.deb\nSize: 1\nSHA256: " + "a" * 64,
    "Package: omb\nVersion: 0.1~rehearsal1\nArchitecture: amd64\nFilename: ./omb_0.1~rehearsal1_amd64.deb\nSize: 2\nSHA256: " + "b" * 64,
]
(root / "Packages.gz").write_bytes(gzip.compress(("\n\n".join(stanzas) + "\n\n").encode(), mtime=0))
(root / "curl.deb").write_bytes(b"x")
(root / "omb_0.1~rehearsal1_amd64.deb").write_bytes(b"yy")
PY
mkdir -p "$work/staged-debs"
stage_sysdebs "$work/sysdebs" "$work/staged-debs"
test -f "$work/staged-debs/curl.deb"
test ! -e "$work/staged-debs/omb_0.1~rehearsal1_amd64.deb"
python3 - "$work/staged-debs/Packages.gz" <<'PY'
import gzip, pathlib, sys
text = gzip.decompress(pathlib.Path(sys.argv[1]).read_bytes()).decode()
assert "Package: curl\n" in text
assert "Package: omb\n" not in text
PY

# 8. --help exits 0
bash "$repo/scripts/build-release.sh" --help >/dev/null

echo "build-release_test.sh: ALL PASS"
