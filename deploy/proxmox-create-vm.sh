#!/usr/bin/env bash
# Creates a NodeOS test VM on a Proxmox VE host from the Debian 12 cloud
# image. Run this ON the Proxmox host as root:
#
#   bash proxmox-create-vm.sh [--vmid 9100] [--storage local-lvm] [--bridge vmbr0]
#
# Afterwards, copy the NodeOS bundle in and run the installer (the script
# prints the exact commands with the VM's IP).
#
# Flags (all optional):
#   --vmid N        VM ID (default: next free ID)
#   --name S        VM name (default: nodeos)
#   --memory MB     RAM (default: 4096)
#   --cores N       vCPUs (default: 2)
#   --disk SIZE     disk resize target, e.g. 64G (default: 64G; a full
#                   unpruned node needs 800G+ — use a pruned node for tests)
#   --storage S     Proxmox storage for the disk (default: local-lvm)
#   --bridge S      network bridge (default: vmbr0 — must reach your miners' LAN!)
#   --user S        cloud-init user (default: nodeos)
#   --password S    cloud-init password (default: generated, printed at the end)
#   --sshkeys PATH  authorized_keys file to inject (default: ~/.ssh/authorized_keys if present)

set -euo pipefail

NAME=nodeos
MEMORY=4096
CORES=2
DISK=64G
STORAGE=local-lvm
BRIDGE=vmbr0
CIUSER=nodeos
CIPASS=""
SSHKEYS="${HOME}/.ssh/authorized_keys"
VMID=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --vmid) VMID="$2"; shift 2 ;;
    --name) NAME="$2"; shift 2 ;;
    --memory) MEMORY="$2"; shift 2 ;;
    --cores) CORES="$2"; shift 2 ;;
    --disk) DISK="$2"; shift 2 ;;
    --storage) STORAGE="$2"; shift 2 ;;
    --bridge) BRIDGE="$2"; shift 2 ;;
    --user) CIUSER="$2"; shift 2 ;;
    --password) CIPASS="$2"; shift 2 ;;
    --sshkeys) SSHKEYS="$2"; shift 2 ;;
    *) echo "unknown flag: $1" >&2; exit 1 ;;
  esac
done

command -v qm >/dev/null || { echo "qm not found — run this on the Proxmox host" >&2; exit 1; }
[[ -n "$VMID" ]] || VMID="$(pvesh get /cluster/nextid)"
[[ -n "$CIPASS" ]] || CIPASS="$(openssl rand -base64 12)"

IMG_DIR=/var/lib/vz/template
IMG="$IMG_DIR/debian-12-genericcloud-amd64.qcow2"
if [[ ! -f "$IMG" ]]; then
  echo "downloading Debian 12 cloud image..."
  mkdir -p "$IMG_DIR"
  curl -fL -o "$IMG" \
    "https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.qcow2"
fi

echo "creating VM $VMID ($NAME) on $STORAGE, bridge $BRIDGE"
qm create "$VMID" \
  --name "$NAME" \
  --memory "$MEMORY" \
  --cores "$CORES" \
  --cpu host \
  --net0 "virtio,bridge=$BRIDGE" \
  --ostype l26 \
  --agent enabled=1 \
  --scsihw virtio-scsi-pci

qm importdisk "$VMID" "$IMG" "$STORAGE" >/dev/null
qm set "$VMID" --scsi0 "$STORAGE:vm-$VMID-disk-0"
qm disk resize "$VMID" scsi0 "$DISK"
qm set "$VMID" --ide2 "$STORAGE:cloudinit" --boot order=scsi0 --serial0 socket --vga serial0

qm set "$VMID" --ciuser "$CIUSER" --cipassword "$CIPASS" --ipconfig0 ip=dhcp
if [[ -f "$SSHKEYS" ]]; then
  qm set "$VMID" --sshkeys "$SSHKEYS"
  echo "injected SSH keys from $SSHKEYS"
fi

qm start "$VMID"

cat <<EOF

==========================================================================
 VM $VMID ($NAME) is booting.

 Login:    $CIUSER / $CIPASS   (console: qm terminal $VMID)

 Find its IP (DHCP) once booted, e.g.:
   qm guest exec $VMID -- ip -4 addr     # needs qemu-guest-agent, or:
   check your router/DHCP leases for "$NAME"

 Then install NodeOS from your workstation (repo root):
   scp dist/nodeosd-linux-amd64 deploy/install.sh $CIUSER@<VM-IP>:/tmp/
   ssh $CIUSER@<VM-IP> "sudo bash /tmp/install.sh --binary /tmp/nodeosd-linux-amd64 --with-bitcoind --prune 20000"

 Web UI afterwards:  http://<VM-IP>/
 IMPORTANT: the bridge ($BRIDGE) must be on the same L2 network as your
 miners, or discovery will not find them (pass --scan-cidr / UI scan field).
==========================================================================
EOF
