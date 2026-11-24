#!/usr/bin/env bash
set -euo pipefail

# Create a ready-to-boot oh my boot_ VM on a Proxmox VE host from a release .qcow2.xz image.

OMB_RELEASE_REPO="${OMB_RELEASE_REPO:-f0nwa/oh-my-boot_}"
VMID=''
NAME='oh-my-boot'
STORAGE='local-lvm'
BRIDGE='vmbr0'
PXE_BRIDGE=''
MEMORY='4096'
CORES='2'
IMAGE_PATH=''
TAG=''

usage() {
    cat <<'EOF'
Usage:
  bash scripts/proxmox-create-vm.sh --vmid ID [options]

Options:
  --vmid ID           Proxmox VM ID, e.g. 200 (required)
  --name NAME         VM name (default: oh-my-boot)
  --storage NAME      Proxmox storage pool (default: local-lvm)
  --bridge NAME       Management network bridge (default: vmbr0)
  --pxe-bridge NAME   Optional second bridge for dedicated PXE NIC
  --memory MB         RAM in MB (default: 4096)
  --cores N           vCPU cores (default: 2)
  --image PATH        Local .qcow2 or .qcow2.xz image (for offline/air-gapped hosts)
  --tag TAG           GitHub release tag to download (default: latest)
  --repo OWNER/REPO   GitHub repository (default: f0nwa/oh-my-boot_)
  -h, --help          Show this help

Example:
  bash proxmox-create-vm.sh --vmid 200 --storage local-lvm --bridge vmbr0 --pxe-bridge vmbr1
EOF
}

die() {
    printf 'proxmox-create-vm: %s\n' "$*" >&2
    exit 1
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --vmid) [[ $# -ge 2 ]] || die "--vmid requires a value"; VMID=$2; shift 2 ;;
        --name) [[ $# -ge 2 ]] || die "--name requires a value"; NAME=$2; shift 2 ;;
        --storage) [[ $# -ge 2 ]] || die "--storage requires a value"; STORAGE=$2; shift 2 ;;
        --bridge) [[ $# -ge 2 ]] || die "--bridge requires a value"; BRIDGE=$2; shift 2 ;;
        --pxe-bridge) [[ $# -ge 2 ]] || die "--pxe-bridge requires a value"; PXE_BRIDGE=$2; shift 2 ;;
        --memory) [[ $# -ge 2 ]] || die "--memory requires a value"; MEMORY=$2; shift 2 ;;
        --cores) [[ $# -ge 2 ]] || die "--cores requires a value"; CORES=$2; shift 2 ;;
        --image) [[ $# -ge 2 ]] || die "--image requires a value"; IMAGE_PATH=$2; shift 2 ;;
        --tag) [[ $# -ge 2 ]] || die "--tag requires a value"; TAG=$2; shift 2 ;;
        --repo) [[ $# -ge 2 ]] || die "--repo requires a value"; OMB_RELEASE_REPO=$2; shift 2 ;;
        -h|--help) usage; exit 0 ;;
        *) die "unknown argument: $1" ;;
    esac
done

[[ -n "$VMID" ]] || die "--vmid is required (run with --help for usage)"
command -v qm >/dev/null 2>&1 || die "qm not found; run this script on a Proxmox VE host"

WORK_DIR=$(mktemp -d /tmp/omb-pve.XXXXXX)
trap 'rm -rf "$WORK_DIR"' EXIT

QCOW2_FILE=''
if [[ -n "$IMAGE_PATH" ]]; then
    [[ -f "$IMAGE_PATH" ]] || die "image not found: $IMAGE_PATH"
    if [[ "$IMAGE_PATH" == *.xz ]]; then
        echo "==> Decompressing ${IMAGE_PATH}..."
        xz -dc "$IMAGE_PATH" > "$WORK_DIR/omb.qcow2"
        QCOW2_FILE="$WORK_DIR/omb.qcow2"
    else
        QCOW2_FILE="$IMAGE_PATH"
    fi
else
    if [[ -n "$TAG" ]]; then
        BASE_URL="https://github.com/${OMB_RELEASE_REPO}/releases/download/${TAG}"
    else
        BASE_URL="https://github.com/${OMB_RELEASE_REPO}/releases/latest/download"
    fi
    echo "==> Fetching SHA256SUMS from ${BASE_URL}..."
    curl -fsSL "${BASE_URL}/SHA256SUMS" -o "$WORK_DIR/SHA256SUMS"
    IMG_NAME=$(awk '$2 ~ /ubuntu24\.04_amd64\.qcow2\.xz$/ { sub(/^\*/, "", $2); print $2; exit }' "$WORK_DIR/SHA256SUMS")
    [[ -n "$IMG_NAME" ]] || die "no .qcow2.xz VM image found in release SHA256SUMS"
    echo "==> Downloading ${IMG_NAME}..."
    curl -fSL "${BASE_URL}/${IMG_NAME}" -o "$WORK_DIR/${IMG_NAME}"
    grep -F " ${IMG_NAME}" "$WORK_DIR/SHA256SUMS" > "$WORK_DIR/check.sha256"
    (cd "$WORK_DIR" && sha256sum -c check.sha256)
    echo "==> Decompressing ${IMG_NAME}..."
    xz -dc "$WORK_DIR/${IMG_NAME}" > "$WORK_DIR/omb.qcow2"
    QCOW2_FILE="$WORK_DIR/omb.qcow2"
fi

echo "==> Creating Proxmox VM ${VMID} (${NAME})..."
NET_ARGS=(--net0 "virtio,bridge=${BRIDGE}")
if [[ -n "$PXE_BRIDGE" ]]; then
    NET_ARGS+=(--net1 "virtio,bridge=${PXE_BRIDGE}")
fi

qm create "$VMID" \
    --name "$NAME" \
    --machine q35 \
    --cpu host \
    --cores "$CORES" \
    --memory "$MEMORY" \
    --scsihw virtio-scsi-single \
    --agent enabled=1 \
    --ostype l26 \
    --serial0 socket \
    --vga serial0 \
    "${NET_ARGS[@]}"

echo "==> Importing disk into storage '${STORAGE}'..."
qm importdisk "$VMID" "$QCOW2_FILE" "$STORAGE"
qm set "$VMID" --scsi0 "${STORAGE}:vm-${VMID}-disk-0,discard=on"
qm set "$VMID" --boot order=scsi0
qm set "$VMID" --ide2 "${STORAGE}:cloudinit"

cat <<EOF

VM ${VMID} (${NAME}) is ready!
Next steps in Proxmox UI (or via qm set):
  1. Configure Cloud-Init user, SSH key, and static IPv4 on Cloud-Init tab.
  2. Resize disk if you plan to store local ISOs (e.g. qm resize ${VMID} scsi0 +60G).
  3. Start the VM (qm start ${VMID}), sign in via SSH/console, and run:
       sudo omb-setup
EOF
