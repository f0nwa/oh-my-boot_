#!/usr/bin/env bash
set -euo pipefail

# Build pre-installed oh my boot_ VM images (qcow2.xz + ova) on Ubuntu 24.04 LTS Cloud Image.
# Uses standard loop devices (losetup --partscan) + chroot, requiring neither KVM nor nbd.

usage() {
    cat <<'EOF'
Usage:
  sudo bash scripts/build-vm-image.sh --deb PATH --version VER --out DIR [--disk-size 20G]

Options:
  --deb PATH        Path to omb_<version>_amd64.deb (required)
  --version VER     Release version string for output filenames, e.g. 0.2.1 (required)
  --out DIR         Output directory for .qcow2.xz, .ova, and SHA256SUMS.vm (required)
  --disk-size SIZE  Virtual disk size for the VM image (default: 20G)
  -h, --help        Show this help
EOF
}

die() {
    printf 'build-vm-image: %s\n' "$*" >&2
    exit 1
}

DEB_PATH=''
VERSION=''
OUT_DIR=''
DISK_SIZE='20G'
CLOUD_BASE_URL='https://cloud-images.ubuntu.com/noble/current'
CLOUD_IMG_NAME='noble-server-cloudimg-amd64.img'

while [[ $# -gt 0 ]]; do
    case "$1" in
        --deb) [[ $# -ge 2 ]] || die "--deb requires a value"; DEB_PATH=$2; shift 2 ;;
        --version) [[ $# -ge 2 ]] || die "--version requires a value"; VERSION=$2; shift 2 ;;
        --out) [[ $# -ge 2 ]] || die "--out requires a value"; OUT_DIR=$2; shift 2 ;;
        --disk-size) [[ $# -ge 2 ]] || die "--disk-size requires a value"; DISK_SIZE=$2; shift 2 ;;
        -h|--help) usage; exit 0 ;;
        *) die "unknown argument: $1" ;;
    esac
done

[[ $(id -u) -eq 0 ]] || die "must be run as root (sudo)"
[[ -n "$DEB_PATH" && -f "$DEB_PATH" ]] || die "--deb file not found: ${DEB_PATH:-<empty>}"
[[ -n "$VERSION" ]] || die "--version is required"
[[ -n "$OUT_DIR" ]] || die "--out is required"

for cmd in curl qemu-img losetup growpart resize2fs e2fsck chroot xz tar sha256sum; do
    command -v "$cmd" >/dev/null 2>&1 || die "required command not found: $cmd"
done

mkdir -p "$OUT_DIR"
OUT_DIR=$(cd "$OUT_DIR" && pwd)
WORK_DIR=$(mktemp -d /tmp/omb-vm-build.XXXXXX)
MNT_DIR="$WORK_DIR/mnt"
LOOP_DEV=''

cleanup() {
    set +e
    if [[ -d "$MNT_DIR" ]]; then
        for sub in dev/pts dev proc sys run; do
            mountpoint -q "$MNT_DIR/$sub" && umount -l "$MNT_DIR/$sub"
        done
        mountpoint -q "$MNT_DIR" && umount -l "$MNT_DIR"
    fi
    if [[ -n "$LOOP_DEV" ]]; then
        losetup -d "$LOOP_DEV"
    fi
    rm -rf "$WORK_DIR"
}
trap cleanup EXIT

echo "==> Downloading Ubuntu 24.04 LTS Cloud Image and SHA256SUMS..."
curl -fsSL "${CLOUD_BASE_URL}/SHA256SUMS" -o "$WORK_DIR/cloud-SHA256SUMS"
curl -fsSL "${CLOUD_BASE_URL}/${CLOUD_IMG_NAME}" -o "$WORK_DIR/${CLOUD_IMG_NAME}"

grep -E " [*]?${CLOUD_IMG_NAME}$" "$WORK_DIR/cloud-SHA256SUMS" > "$WORK_DIR/cloud-verify.sha256"
[[ -s "$WORK_DIR/cloud-verify.sha256" ]] || die "SHA256SUMS entry for ${CLOUD_IMG_NAME} not found"
(cd "$WORK_DIR" && sha256sum -c cloud-verify.sha256)

echo "==> Converting Cloud Image to raw (${DISK_SIZE}) and attaching loop device..."
RAW_IMG="$WORK_DIR/disk.raw"
qemu-img convert -f qcow2 -O raw "$WORK_DIR/${CLOUD_IMG_NAME}" "$RAW_IMG"
qemu-img resize -f raw "$RAW_IMG" "$DISK_SIZE"

LOOP_DEV=$(losetup --find --show --partscan "$RAW_IMG")
udevadm settle 2>/dev/null || sleep 1

# Locate the Linux root ext4 partition (usually p1 on Ubuntu cloud images)
ROOT_PART=''
ROOT_PART_NUM=''
for part in "${LOOP_DEV}"p*; do
    [[ -b "$part" ]] || continue
    fstype=$(blkid -o value -s TYPE "$part" 2>/dev/null || true)
    if [[ "$fstype" == "ext4" ]]; then
        ROOT_PART="$part"
        ROOT_PART_NUM="${part#"${LOOP_DEV}p"}"
        break
    fi
done
[[ -n "$ROOT_PART" ]] || die "failed to locate ext4 root partition on ${LOOP_DEV}"

echo "==> Growing root partition ${ROOT_PART} (partition #${ROOT_PART_NUM})..."
growpart "$LOOP_DEV" "$ROOT_PART_NUM" || true
e2fsck -fy "$ROOT_PART" || true
resize2fs "$ROOT_PART"

mkdir -p "$MNT_DIR"
mount "$ROOT_PART" "$MNT_DIR"

for sub in dev dev/pts proc sys run; do
    mkdir -p "$MNT_DIR/$sub"
    mount --bind "/$sub" "$MNT_DIR/$sub"
done

# Preserve host DNS for apt inside chroot
RESOLV_BACKUP=''
if [[ -e "$MNT_DIR/etc/resolv.conf" || -L "$MNT_DIR/etc/resolv.conf" ]]; then
    RESOLV_BACKUP="$WORK_DIR/resolv.conf.orig"
    cp -a "$MNT_DIR/etc/resolv.conf" "$RESOLV_BACKUP"
    rm -f "$MNT_DIR/etc/resolv.conf"
fi
cp /etc/resolv.conf "$MNT_DIR/etc/resolv.conf"

# Prevent daemons from starting inside chroot during apt-get install
cat > "$MNT_DIR/usr/sbin/policy-rc.d" <<'EOF'
#!/bin/sh
exit 101
EOF
chmod 0755 "$MNT_DIR/usr/sbin/policy-rc.d"

cp "$DEB_PATH" "$MNT_DIR/tmp/omb.deb"

echo "==> Installing omb package and qemu-guest-agent inside VM image..."
chroot "$MNT_DIR" /usr/bin/env DEBIAN_FRONTEND=noninteractive bash -euo pipefail -c '
    apt-get update
    apt-get install -y --no-install-recommends qemu-guest-agent /tmp/omb.deb
    rm -f /tmp/omb.deb
    systemctl enable qemu-guest-agent.service || true
    apt-get clean
    rm -rf /var/lib/apt/lists/*
'

rm -f "$MNT_DIR/usr/sbin/policy-rc.d"

# Add first-boot MOTD banner prompting sudo omb-setup until configured
cat > "$MNT_DIR/etc/update-motd.d/99-oh-my-boot" <<'EOF'
#!/bin/sh
if [ ! -f /var/lib/omb-deployment/INSTALL_MODE ]; then
  printf '\n'
  printf '  oh my boot_ is pre-installed on this VM.\n'
  printf '  Run the setup wizard to initialize databases, certificates, and PXE services:\n'
  printf '\n'
  printf '    sudo omb-setup\n'
  printf '\n'
fi
EOF
chmod 0755 "$MNT_DIR/etc/update-motd.d/99-oh-my-boot"

# Clean cloud-init state, machine-id, and SSH host keys so every VM boots with unique identity
chroot "$MNT_DIR" cloud-init clean --logs --seed || true
truncate -s 0 "$MNT_DIR/etc/machine-id"
rm -f "$MNT_DIR/var/lib/dbus/machine-id"
rm -f "$MNT_DIR"/etc/ssh/ssh_host_*
rm -rf "$MNT_DIR"/tmp/* "$MNT_DIR"/var/tmp/*

# Restore original resolv.conf symlink for systemd-resolved
rm -f "$MNT_DIR/etc/resolv.conf"
if [[ -n "$RESOLV_BACKUP" ]]; then
    cp -a "$RESOLV_BACKUP" "$MNT_DIR/etc/resolv.conf"
fi

# Zero free space for compact qcow2/vmdk compression
echo "==> Compacting free space..."
dd if=/dev/zero of="$MNT_DIR/zero.fill" bs=4M status=none 2>/dev/null || true
rm -f "$MNT_DIR/zero.fill"
sync

for sub in run sys proc dev/pts dev; do
    umount "$MNT_DIR/$sub"
done
umount "$MNT_DIR"
losetup -d "$LOOP_DEV"
LOOP_DEV=''

BASE_NAME="omb_${VERSION}_ubuntu24.04_amd64"
QCOW2_PATH="$WORK_DIR/${BASE_NAME}.qcow2"
QCOW2_XZ_OUT="${OUT_DIR}/${BASE_NAME}.qcow2.xz"
OVA_OUT="${OUT_DIR}/${BASE_NAME}.ova"

echo "==> Exporting compressed QCOW2 (${BASE_NAME}.qcow2.xz)..."
qemu-img convert -f raw -O qcow2 -c "$RAW_IMG" "$QCOW2_PATH"
xz -T0 -3 -c "$QCOW2_PATH" > "$QCOW2_XZ_OUT"
rm -f "$QCOW2_PATH"

echo "==> Exporting streamOptimized VMDK and OVA (${BASE_NAME}.ova)..."
OVA_STAGE="$WORK_DIR/ova"
mkdir -p "$OVA_STAGE"
VMDK_NAME="omb-disk1.vmdk"
qemu-img convert -f raw -O vmdk -o subformat=streamOptimized "$RAW_IMG" "$OVA_STAGE/$VMDK_NAME"
VMDK_SIZE=$(stat -c %s "$OVA_STAGE/$VMDK_NAME")
DISK_BYTES=$(stat -c %s "$RAW_IMG")

cat > "$OVA_STAGE/omb.ovf" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<Envelope xmlns="http://schemas.dmtf.org/ovf/envelope/1"
          xmlns:cim="http://schemas.dmtf.org/wbem/wscim/1/common"
          xmlns:ovf="http://schemas.dmtf.org/ovf/envelope/1"
          xmlns:rasd="http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_ResourceAllocationSettingData"
          xmlns:vmw="http://www.vmware.com/schema/ovf"
          xmlns:vssd="http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_VirtualSystemSettingData">
  <References>
    <File ovf:href="${VMDK_NAME}" ovf:id="file1" ovf:size="${VMDK_SIZE}"/>
  </References>
  <DiskSection>
    <Info>Virtual disk information</Info>
    <Disk ovf:capacity="${DISK_BYTES}" ovf:capacityAllocationUnits="byte" ovf:diskId="vmdisk1" ovf:fileRef="file1" ovf:format="http://www.vmware.com/interfaces/specifications/vmdk.html#streamOptimized"/>
  </DiskSection>
  <NetworkSection>
    <Info>Logical networks</Info>
    <Network ovf:name="Management">
      <Description>Management network (HTTPS :8443)</Description>
    </Network>
    <Network ovf:name="PXE">
      <Description>PXE network segment (DHCP / TFTP / HTTP / SMB)</Description>
    </Network>
  </NetworkSection>
  <VirtualSystem ovf:id="oh-my-boot">
    <Info>oh my boot_ ${VERSION} (Ubuntu 24.04 LTS)</Info>
    <Name>oh-my-boot-${VERSION}</Name>
    <OperatingSystemSection ovf:id="94" vmw:osType="ubuntu64Guest">
      <Info>Ubuntu 64-bit</Info>
    </OperatingSystemSection>
    <VirtualHardwareSection>
      <Info>Virtual hardware requirements</Info>
      <System>
        <vssd:ElementName>Virtual Hardware Family</vssd:ElementName>
        <vssd:InstanceID>0</vssd:InstanceID>
        <vssd:VirtualSystemIdentifier>oh-my-boot-${VERSION}</vssd:VirtualSystemIdentifier>
        <vssd:VirtualSystemType>vmx-14</vssd:VirtualSystemType>
      </System>
      <Item>
        <rasd:Description>Number of Virtual CPUs</rasd:Description>
        <rasd:ElementName>2 virtual CPU(s)</rasd:ElementName>
        <rasd:InstanceID>1</rasd:InstanceID>
        <rasd:ResourceType>3</rasd:ResourceType>
        <rasd:VirtualQuantity>2</rasd:VirtualQuantity>
      </Item>
      <Item>
        <rasd:AllocationUnits>byte * 2^20</rasd:AllocationUnits>
        <rasd:Description>Memory Size</rasd:Description>
        <rasd:ElementName>2048MB of memory</rasd:ElementName>
        <rasd:InstanceID>2</rasd:InstanceID>
        <rasd:ResourceType>4</rasd:ResourceType>
        <rasd:VirtualQuantity>2048</rasd:VirtualQuantity>
      </Item>
      <Item>
        <rasd:Address>0</rasd:Address>
        <rasd:Description>SCSI Controller</rasd:Description>
        <rasd:ElementName>SCSI Controller 0</rasd:ElementName>
        <rasd:InstanceID>3</rasd:InstanceID>
        <rasd:ResourceSubType>VirtualSCSI</rasd:ResourceSubType>
        <rasd:ResourceType>6</rasd:ResourceType>
      </Item>
      <Item>
        <rasd:AddressOnParent>0</rasd:AddressOnParent>
        <rasd:ElementName>Hard Disk 1</rasd:ElementName>
        <rasd:HostResource>ovf:/disk/vmdisk1</rasd:HostResource>
        <rasd:InstanceID>4</rasd:InstanceID>
        <rasd:Parent>3</rasd:Parent>
        <rasd:ResourceType>17</rasd:ResourceType>
      </Item>
      <Item>
        <rasd:AddressOnParent>0</rasd:AddressOnParent>
        <rasd:AutomaticAllocation>true</rasd:AutomaticAllocation>
        <rasd:Connection>Management</rasd:Connection>
        <rasd:ElementName>Management NIC</rasd:ElementName>
        <rasd:InstanceID>5</rasd:InstanceID>
        <rasd:ResourceSubType>VmxNet3</rasd:ResourceSubType>
        <rasd:ResourceType>10</rasd:ResourceType>
      </Item>
      <Item>
        <rasd:AddressOnParent>1</rasd:AddressOnParent>
        <rasd:AutomaticAllocation>true</rasd:AutomaticAllocation>
        <rasd:Connection>PXE</rasd:Connection>
        <rasd:ElementName>PXE NIC</rasd:ElementName>
        <rasd:InstanceID>6</rasd:InstanceID>
        <rasd:ResourceSubType>VmxNet3</rasd:ResourceSubType>
        <rasd:ResourceType>10</rasd:ResourceType>
      </Item>
    </VirtualHardwareSection>
  </VirtualSystem>
</Envelope>
EOF

(
    cd "$OVA_STAGE"
    printf 'SHA256(omb.ovf)= %s\n' "$(sha256sum omb.ovf | awk '{print $1}')" > omb.mf
    printf 'SHA256(%s)= %s\n' "$VMDK_NAME" "$(sha256sum "$VMDK_NAME" | awk '{print $1}')" >> omb.mf
    tar -cf "$OVA_OUT" omb.ovf "$VMDK_NAME" omb.mf
)

(
    cd "$OUT_DIR"
    sha256sum "${BASE_NAME}.qcow2.xz" "${BASE_NAME}.ova" > SHA256SUMS.vm
)

echo "==> Done:"
ls -lh "$QCOW2_XZ_OUT" "$OVA_OUT"
cat "${OUT_DIR}/SHA256SUMS.vm"
