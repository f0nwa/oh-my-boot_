#!/usr/bin/env bash
set -euo pipefail

OMB_RELEASE_REPO="${OMB_RELEASE_REPO:-f0nwa/OMB}"
OMB_GITHUB_API_BASE="${OMB_GITHUB_API_BASE:-https://api.github.com}"
OMB_GITHUB_RELEASE_BASE="${OMB_GITHUB_RELEASE_BASE:-https://github.com}"
OMB_OS_RELEASE="${OMB_OS_RELEASE:-/etc/os-release}"
OMB_SKIP_ROOT_CHECK="${OMB_SKIP_ROOT_CHECK:-0}"
OMB_SETUP_BIN="${OMB_SETUP_BIN:-/usr/bin/omb-setup}"

usage() {
    cat <<'EOF'
Usage:
  install.sh [options] [-- <omb-setup args>]

Options:
  --version <ver>        Install specific release version or tag (e.g. v0.2.0, v0.2.1-dev.1)
  --channel <stable|dev> Release channel when --version is not set (default: stable)
  --deb <path>           Install from a local .deb package instead of downloading
  --repo <owner/repo>    GitHub repository for releases (default: f0nwa/OMB)
  --force-os             Allow installation on OS versions other than Ubuntu 26.04 / 24.04
  -h, --help             Show this help message

Examples:
  curl -fsSL https://raw.githubusercontent.com/f0nwa/OMB/main/scripts/install.sh | sudo bash
  sudo bash scripts/install.sh --version v0.2.0
  sudo bash scripts/install.sh --deb ./omb_0.2.0_amd64.deb -- --lang en
EOF
}

die() {
    echo "omb-install: $*" >&2
    exit 1
}

warn() {
    echo "omb-install: warning: $*" >&2
}

read_os_release_field() {
    local file="$1"
    local key="$2"
    local line val
    line=$(grep -E "^${key}=" "$file" | head -n 1 || true)
    val="${line#*=}"
    val="${val#\"}"
    val="${val%\"}"
    val="${val#\'}"
    val="${val%\'}"
    printf '%s' "$val"
}

verify_sha256() {
    local sums_file="$1"
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum -c "$sums_file"
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 -c "$sums_file"
    else
        die "neither sha256sum nor shasum found to verify SHA-256 checksums"
    fi
}

normalize_tag() {
    local v="$1"
    v="${v//\~/-}"
    if [[ "$v" != v* ]]; then
        v="v$v"
    fi
    printf '%s' "$v"
}

version=""
channel="stable"
deb_path=""
repo="$OMB_RELEASE_REPO"
force_os=false
setup_args=()

while [[ $# -gt 0 ]]; do
    case "$1" in
        --version)
            [[ $# -ge 2 && -n "$2" ]] || die "--version requires a value"
            version="$2"
            shift 2
            ;;
        --channel)
            [[ $# -ge 2 && -n "$2" ]] || die "--channel requires a value"
            channel="$2"
            shift 2
            ;;
        --deb)
            [[ $# -ge 2 && -n "$2" ]] || die "--deb requires a path"
            deb_path="$2"
            shift 2
            ;;
        --repo)
            [[ $# -ge 2 && -n "$2" ]] || die "--repo requires owner/repo"
            repo="$2"
            shift 2
            ;;
        --force-os)
            force_os=true
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        --)
            shift
            setup_args=("$@")
            break
            ;;
        *)
            die "unknown option: $1 (use --help for usage)"
            ;;
    esac
done

case "$channel" in
    stable|dev) ;;
    *) die "unsupported --channel: $channel (expected stable or dev)" ;;
esac

# 1. Root check
if [[ "$OMB_SKIP_ROOT_CHECK" != "1" && "$(id -u)" -ne 0 ]]; then
    die "please run as root (e.g. sudo bash install.sh)"
fi

# 2. Architecture check
arch=$(dpkg --print-architecture 2>/dev/null || true)
if [[ "$arch" != "amd64" ]]; then
    die "only amd64 architecture is supported (detected: ${arch:-unknown})"
fi

# 3. OS check
os_id=""
os_version_id=""
if [[ -r "$OMB_OS_RELEASE" ]]; then
    os_id=$(read_os_release_field "$OMB_OS_RELEASE" "ID")
    os_version_id=$(read_os_release_field "$OMB_OS_RELEASE" "VERSION_ID")
fi

os_supported=false
if [[ "$os_id" == "ubuntu" && ( "$os_version_id" == "26.04" || "$os_version_id" == "24.04" ) ]]; then
    os_supported=true
fi

if [[ "$os_supported" != "true" ]]; then
    if [[ "$force_os" == "true" ]]; then
        warn "unsupported OS (${os_id:-unknown} ${os_version_id:-unknown}), continuing because --force-os was set"
    else
        die "unsupported OS: ${os_id:-unknown} ${os_version_id:-unknown}. Supported: Ubuntu 26.04 or 24.04 amd64 (pass --force-os to override)."
    fi
fi

# 4. Acquire .deb package
work_dir=""
cleanup() {
    if [[ -n "$work_dir" && -d "$work_dir" ]]; then
        rm -rf -- "$work_dir"
    fi
}
trap cleanup EXIT

deb_file=""
if [[ -n "$deb_path" ]]; then
    [[ -f "$deb_path" ]] || die "local .deb file not found: $deb_path"
    if [[ "$deb_path" == /* || "$deb_path" == ./* || "$deb_path" == ../* ]]; then
        deb_file="$deb_path"
    else
        deb_file="./$deb_path"
    fi
else
    command -v curl >/dev/null 2>&1 || die "curl is required to download OMB releases"
    work_dir=$(mktemp -d)

    release_base=""
    if [[ -n "$version" ]]; then
        tag=$(normalize_tag "$version")
        release_base="${OMB_GITHUB_RELEASE_BASE}/${repo}/releases/download/${tag}"
    elif [[ "$channel" == "stable" ]]; then
        release_base="${OMB_GITHUB_RELEASE_BASE}/${repo}/releases/latest/download"
    else
        releases_json="$work_dir/releases.json"
        curl -fsSL "${OMB_GITHUB_API_BASE}/repos/${repo}/releases?per_page=20" -o "$releases_json"
        tag=$(grep -Eo '"tag_name"[[:space:]]*:[[:space:]]*"v[0-9]+\.[0-9]+\.[0-9]+-dev\.[0-9]+"' "$releases_json" \
            | head -n 1 \
            | sed -E 's/.*"([^"]+)"$/\1/' || true)
        [[ -n "$tag" ]] || die "no dev release found in ${repo}"
        release_base="${OMB_GITHUB_RELEASE_BASE}/${repo}/releases/download/${tag}"
    fi

    sums_file="$work_dir/SHA256SUMS"
    echo "Downloading SHA256SUMS from ${release_base}/SHA256SUMS..."
    curl -fsSL "${release_base}/SHA256SUMS" -o "$sums_file"

    deb_name=$(awk '
        NF == 2 && length($1) == 64 && $1 ~ /^[0-9a-fA-F]+$/ {
            f = $2
            sub(/^\*/, "", f)
            if (f ~ /^omb_[A-Za-z0-9.+~-]+_amd64\.deb$/) {
                print f
                exit
            }
        }
    ' "$sums_file")
    [[ -n "$deb_name" ]] || die "SHA256SUMS does not contain a valid omb_*_amd64.deb entry"

    deb_sha=$(awk -v target="$deb_name" '
        NF == 2 && length($1) == 64 && $1 ~ /^[0-9a-fA-F]+$/ {
            f = $2
            sub(/^\*/, "", f)
            if (f == target) {
                print $1
                exit
            }
        }
    ' "$sums_file")
    [[ -n "$deb_sha" ]] || die "failed to read SHA-256 digest for ${deb_name}"
    printf '%s  %s\n' "$deb_sha" "$deb_name" >"$work_dir/SHA256SUMS.deb"

    echo "Downloading ${deb_name}..."
    curl -fsSL "${release_base}/${deb_name}" -o "$work_dir/$deb_name"

    echo "Verifying SHA-256 checksum..."
    (cd "$work_dir" && verify_sha256 "SHA256SUMS.deb") || die "SHA-256 verification failed for ${deb_name}"

    deb_file="$work_dir/$deb_name"
fi

# 5. Install .deb via apt-get
DEBIAN_FRONTEND=noninteractive apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "$deb_file"

# 6. Hand off to omb-setup
[[ -x "$OMB_SETUP_BIN" ]] || die "omb-setup binary not found or not executable: $OMB_SETUP_BIN"

if [[ ! -t 0 ]] && (: </dev/tty) 2>/dev/null; then
    "$OMB_SETUP_BIN" --install-mode online ${setup_args[@]+"${setup_args[@]}"} </dev/tty
else
    "$OMB_SETUP_BIN" --install-mode online ${setup_args[@]+"${setup_args[@]}"}
fi
