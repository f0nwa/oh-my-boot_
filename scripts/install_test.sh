#!/usr/bin/env bash
set -euo pipefail

repo=$(cd "$(dirname "$0")/.." && pwd)
installer="$repo/scripts/install.sh"
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

sha256_file() {
    local f="$1"
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$f" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$f" | awk '{print $1}'
    else
        fail "neither sha256sum nor shasum found"
    fi
}

# Build a fresh mock environment directory with stub dpkg, apt-get, curl, omb-setup.
setup_mock_env() {
    local dir="$1"
    local arch="${2:-amd64}"
    mkdir -p "$dir/bin" "$dir/logs"

    cat >"$dir/bin/dpkg" <<EOF
#!/usr/bin/env bash
if [[ "\${1:-}" == "--print-architecture" ]]; then
    echo "$arch"
    exit 0
fi
exit 0
EOF

    cat >"$dir/bin/apt-get" <<EOF
#!/usr/bin/env bash
printf 'DEBIAN_FRONTEND=%s apt-get' "\${DEBIAN_FRONTEND:-}" >>"$dir/logs/apt-get.log"
for arg in "\$@"; do
    printf ' %s' "\$arg" >>"$dir/logs/apt-get.log"
done
printf '\n' >>"$dir/logs/apt-get.log"
EOF

    cat >"$dir/bin/omb-setup" <<EOF
#!/usr/bin/env bash
printf 'omb-setup' >>"$dir/logs/omb-setup.log"
for arg in "\$@"; do
    printf ' %s' "\$arg" >>"$dir/logs/omb-setup.log"
done
printf '\n' >>"$dir/logs/omb-setup.log"
EOF

    cat >"$dir/bin/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
out=""
url=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        -o|--output)
            out="$2"
            shift 2
            ;;
        -*)
            shift
            ;;
        *)
            url="$1"
            shift
            ;;
    esac
done
printf '%s\n' "$url" >>"$MOCK_CURL_LOG"
src=""
case "$url" in
    *"/releases?per_page="*)
        src="$MOCK_CURL_FIXTURES/releases.json"
        ;;
    *"/SHA256SUMS")
        src="$MOCK_CURL_FIXTURES/SHA256SUMS"
        ;;
    *.deb)
        src="$MOCK_CURL_FIXTURES/$(basename "$url")"
        ;;
    *)
        echo "unexpected curl URL: $url" >&2
        exit 22
        ;;
esac
if [[ ! -f "$src" ]]; then
    echo "mock fixture missing: $src for $url" >&2
    exit 22
fi
if [[ -n "$out" ]]; then
    cp "$src" "$out"
else
    cat "$src"
fi
EOF

    chmod +x "$dir/bin/dpkg" "$dir/bin/apt-get" "$dir/bin/omb-setup" "$dir/bin/curl"
}

write_os_release() {
    local path="$1"
    local id="$2"
    local version_id="$3"
    cat >"$path" <<EOF
NAME="Test OS"
ID=$id
VERSION_ID="$version_id"
EOF
}

run_installer() {
    local env_dir="$1"
    local os_file="$2"
    shift 2
    env PATH="$env_dir/bin:$PATH" \
        OMB_SKIP_ROOT_CHECK=1 \
        OMB_OS_RELEASE="$os_file" \
        OMB_SETUP_BIN="$env_dir/bin/omb-setup" \
        MOCK_CURL_LOG="$env_dir/logs/curl.log" \
        MOCK_CURL_FIXTURES="$env_dir/fixtures" \
        bash "$installer" "$@" </dev/null
}

# 1. --help exits 0
bash "$installer" --help >/dev/null || fail "--help should exit 0"

# 2. Accepts Ubuntu 26.04 amd64 with --deb and forwards args after --
t2="$work/t2"
setup_mock_env "$t2" amd64
write_os_release "$t2/os-release" ubuntu 26.04
printf 'fake-deb-2604' >"$t2/omb_0.2.0_amd64.deb"
run_installer "$t2" "$t2/os-release" --deb "$t2/omb_0.2.0_amd64.deb" -- --lang en --skip-pxe
grep -Fq "DEBIAN_FRONTEND=noninteractive apt-get update" "$t2/logs/apt-get.log" || fail "t2: apt-get update missing"
grep -Fq "DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends $t2/omb_0.2.0_amd64.deb" "$t2/logs/apt-get.log" || fail "t2: apt-get install args mismatch"
grep -Fq "omb-setup --install-mode online --lang en --skip-pxe" "$t2/logs/omb-setup.log" || fail "t2: omb-setup args mismatch"

# 3. Accepts Ubuntu 24.04 amd64 with bare relative --deb path (must prefix ./)
t3="$work/t3"
setup_mock_env "$t3" amd64
write_os_release "$t3/os-release" ubuntu 24.04
printf 'fake-deb-2404' >"$t3/omb_0.2.0_amd64.deb"
(
    cd "$t3"
    run_installer "$t3" "$t3/os-release" --deb "omb_0.2.0_amd64.deb"
)
grep -Fq "apt-get install -y --no-install-recommends ./omb_0.2.0_amd64.deb" "$t3/logs/apt-get.log" || fail "t3: relative --deb must be prefixed with ./"
grep -Fq "omb-setup --install-mode online" "$t3/logs/omb-setup.log" || fail "t3: omb-setup not invoked"

# 4. Rejects Ubuntu 22.04 without --force-os, accepts with --force-os
t4="$work/t4"
setup_mock_env "$t4" amd64
write_os_release "$t4/os-release" ubuntu 22.04
printf 'fake-deb-2204' >"$t4/omb_0.2.0_amd64.deb"
if run_installer "$t4" "$t4/os-release" --deb "$t4/omb_0.2.0_amd64.deb" >"$t4/out" 2>&1; then
    fail "t4: Ubuntu 22.04 should be rejected without --force-os"
fi
grep -Fq -- "--force-os" "$t4/out" || fail "t4: error message should mention --force-os"
test ! -e "$t4/logs/apt-get.log" || fail "t4: apt-get must not run on rejected OS"
run_installer "$t4" "$t4/os-release" --force-os --deb "$t4/omb_0.2.0_amd64.deb"
grep -Fq "apt-get install -y --no-install-recommends" "$t4/logs/apt-get.log" || fail "t4: --force-os should allow Ubuntu 22.04"

# 5. Rejects Debian without --force-os, accepts with --force-os
t5="$work/t5"
setup_mock_env "$t5" amd64
write_os_release "$t5/os-release" debian 12
printf 'fake-deb-debian' >"$t5/omb_0.2.0_amd64.deb"
if run_installer "$t5" "$t5/os-release" --deb "$t5/omb_0.2.0_amd64.deb" >"$t5/out" 2>&1; then
    fail "t5: Debian should be rejected without --force-os"
fi
run_installer "$t5" "$t5/os-release" --force-os --deb "$t5/omb_0.2.0_amd64.deb"
grep -Fq "omb-setup --install-mode online" "$t5/logs/omb-setup.log" || fail "t5: --force-os should allow Debian"

# 6. Rejects non-amd64 architecture
t6="$work/t6"
setup_mock_env "$t6" arm64
write_os_release "$t6/os-release" ubuntu 26.04
printf 'fake-deb-arm64' >"$t6/omb_0.2.0_amd64.deb"
if run_installer "$t6" "$t6/os-release" --deb "$t6/omb_0.2.0_amd64.deb" >"$t6/out" 2>&1; then
    fail "t6: arm64 should be rejected"
fi
grep -Fq "amd64" "$t6/out" || fail "t6: error should mention amd64"

# 7. Download mode (stable latest): verifies SHA256SUMS and installs
t7="$work/t7"
setup_mock_env "$t7" amd64
write_os_release "$t7/os-release" ubuntu 26.04
mkdir -p "$t7/fixtures"
printf 'valid-deb-payload-v020' >"$t7/fixtures/omb_0.2.0_amd64.deb"
good_sha=$(sha256_file "$t7/fixtures/omb_0.2.0_amd64.deb")
printf '%s  omb_0.2.0_amd64.deb\n' "$good_sha" >"$t7/fixtures/SHA256SUMS"
run_installer "$t7" "$t7/os-release" -- --lang ru
grep -Fq "https://github.com/f0nwa/OMB/releases/latest/download/SHA256SUMS" "$t7/logs/curl.log" || fail "t7: wrong SHA256SUMS URL"
grep -Fq "https://github.com/f0nwa/OMB/releases/latest/download/omb_0.2.0_amd64.deb" "$t7/logs/curl.log" || fail "t7: wrong deb URL"
grep -Fq "apt-get install -y --no-install-recommends" "$t7/logs/apt-get.log" || fail "t7: apt-get install not called"
grep -Fq "omb_0.2.0_amd64.deb" "$t7/logs/apt-get.log" || fail "t7: deb filename missing in apt-get"
grep -Fq "omb-setup --install-mode online --lang ru" "$t7/logs/omb-setup.log" || fail "t7: omb-setup args mismatch"

# 8. Download mode: refuses to install when SHA-256 mismatches
t8="$work/t8"
setup_mock_env "$t8" amd64
write_os_release "$t8/os-release" ubuntu 26.04
mkdir -p "$t8/fixtures"
printf 'tampered-deb-payload' >"$t8/fixtures/omb_0.2.0_amd64.deb"
printf '0000000000000000000000000000000000000000000000000000000000000000  omb_0.2.0_amd64.deb\n' >"$t8/fixtures/SHA256SUMS"
if run_installer "$t8" "$t8/os-release" >"$t8/out" 2>&1; then
    fail "t8: SHA-256 mismatch must fail"
fi
test ! -e "$t8/logs/apt-get.log" || fail "t8: apt-get must NOT run on SHA-256 mismatch"
test ! -e "$t8/logs/omb-setup.log" || fail "t8: omb-setup must NOT run on SHA-256 mismatch"

# 9. Download mode with --version and --repo
t9="$work/t9"
setup_mock_env "$t9" amd64
write_os_release "$t9/os-release" ubuntu 26.04
mkdir -p "$t9/fixtures"
printf 'versioned-deb-payload' >"$t9/fixtures/omb_0.3.0_amd64.deb"
v_sha=$(sha256_file "$t9/fixtures/omb_0.3.0_amd64.deb")
printf '%s  omb_0.3.0_amd64.deb\n' "$v_sha" >"$t9/fixtures/SHA256SUMS"
run_installer "$t9" "$t9/os-release" --version v0.3.0 --repo acme/OMB
grep -Fq "https://github.com/acme/OMB/releases/download/v0.3.0/SHA256SUMS" "$t9/logs/curl.log" || fail "t9: wrong custom repo/version SHA256SUMS URL"
grep -Fq "https://github.com/acme/OMB/releases/download/v0.3.0/omb_0.3.0_amd64.deb" "$t9/logs/curl.log" || fail "t9: wrong custom repo/version deb URL"

# 10. Download mode with --channel dev resolves latest dev prerelease tag
t10="$work/t10"
setup_mock_env "$t10" amd64
write_os_release "$t10/os-release" ubuntu 26.04
mkdir -p "$t10/fixtures"
cat >"$t10/fixtures/releases.json" <<'JSON'
[
  {"tag_name": "v0.2.1-dev.2", "prerelease": true},
  {"tag_name": "v0.2.0", "prerelease": false}
]
JSON
printf 'dev-deb-payload' >"$t10/fixtures/omb_0.2.1~dev.2_amd64.deb"
dev_sha=$(sha256_file "$t10/fixtures/omb_0.2.1~dev.2_amd64.deb")
printf '%s  omb_0.2.1~dev.2_amd64.deb\n' "$dev_sha" >"$t10/fixtures/SHA256SUMS"
run_installer "$t10" "$t10/os-release" --channel dev
grep -Fq "https://api.github.com/repos/f0nwa/OMB/releases?per_page=20" "$t10/logs/curl.log" || fail "t10: did not query GitHub releases API"
grep -Fq "https://github.com/f0nwa/OMB/releases/download/v0.2.1-dev.2/SHA256SUMS" "$t10/logs/curl.log" || fail "t10: wrong dev SHA256SUMS URL"
grep -Fq "https://github.com/f0nwa/OMB/releases/download/v0.2.1-dev.2/omb_0.2.1~dev.2_amd64.deb" "$t10/logs/curl.log" || fail "t10: wrong dev deb URL"

echo "install_test.sh: ALL PASS"
