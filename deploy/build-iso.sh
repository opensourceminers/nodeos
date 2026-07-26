#!/usr/bin/env bash
# Builds the NodeOS appliance installer ISO.
#
# Takes the official Ubuntu 24.04 Server ISO and turns it into an unattended
# installer: boot it from a USB stick, and the machine installs Ubuntu +
# NodeOS on its own. On first boot with network, Bitcoin Core is installed
# and started automatically.
#
#   ############################################################
#   #  WARNING: the resulting ISO WIPES the target machine's   #
#   #  first disk without asking. It is an appliance installer.#
#   ############################################################
#
# Run on any Debian/Ubuntu machine (the Proxmox host or a VM works):
#   bash deploy/build-iso.sh [--binary dist/nodeosd-linux-amd64] [--out dist/nodeos-installer-amd64.iso]
#                            [--password nodeos] [--keyboard us|de|ch] [--prune 20000]
#                            [--iso /path/to/ubuntu-24.04-live-server-amd64.iso]
#
# Needs: xorriso curl openssl  (apt install -y xorriso curl openssl)
# The Ubuntu ISO (~2.7 GB) is downloaded and checksum-verified unless --iso is given.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

BINARY="$REPO_ROOT/dist/nodeosd-linux-amd64"
OUT="$REPO_ROOT/dist/nodeos-installer-amd64.iso"
PASSWORD="nodeos"
KEYBOARD="us"
PRUNE=20000
SRC_ISO=""
MIRROR="https://releases.ubuntu.com/noble"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --binary) BINARY="$2"; shift 2 ;;
    --out) OUT="$2"; shift 2 ;;
    --password) PASSWORD="$2"; shift 2 ;;
    --keyboard) KEYBOARD="$2"; shift 2 ;;
    --prune) PRUNE="$2"; shift 2 ;;
    --iso) SRC_ISO="$2"; shift 2 ;;
    *) echo "unknown flag: $1" >&2; exit 1 ;;
  esac
done

for dep in xorriso curl openssl sed; do
  command -v "$dep" >/dev/null || { echo "missing dependency: $dep (apt install -y xorriso curl openssl)" >&2; exit 1; }
done
[[ -f "$BINARY" ]] || { echo "nodeosd binary not found: $BINARY — run ./build.sh first" >&2; exit 1; }
[[ -f "$SCRIPT_DIR/install.sh" ]] || { echo "deploy/install.sh not found" >&2; exit 1; }

log() { echo -e "\033[1;34m[iso]\033[0m $*"; }
WORK="$(mktemp -d)"
trap 'chmod -R u+w "$WORK" 2>/dev/null || true; rm -rf "$WORK"' EXIT

# ---------- get the Ubuntu ISO ----------

if [[ -z "$SRC_ISO" ]]; then
  log "resolving current Ubuntu 24.04 server ISO"
  SUMS="$(curl -fsSL "$MIRROR/SHA256SUMS")"
  ISO_NAME="$(echo "$SUMS" | grep -o 'ubuntu-24\.04[0-9.]*-live-server-amd64\.iso' | head -1)"
  [[ -n "$ISO_NAME" ]] || { echo "could not find live-server ISO in $MIRROR/SHA256SUMS" >&2; exit 1; }
  SRC_ISO="$REPO_ROOT/dist/$ISO_NAME"
  if [[ ! -f "$SRC_ISO" ]]; then
    log "downloading $ISO_NAME (~2.7 GB)"
    mkdir -p "$REPO_ROOT/dist"
    curl -fL -o "$SRC_ISO" "$MIRROR/$ISO_NAME"
  fi
  log "verifying checksum"
  (cd "$(dirname "$SRC_ISO")" && echo "$SUMS" | grep " \*$ISO_NAME\$" | sha256sum --check -)
fi
[[ -f "$SRC_ISO" ]] || { echo "ISO not found: $SRC_ISO" >&2; exit 1; }

# ---------- unpack and inject ----------

log "unpacking ISO"
xorriso -osirrox on -indev "$SRC_ISO" -extract / "$WORK/iso" >/dev/null 2>&1
chmod -R u+w "$WORK/iso"

log "injecting NodeOS payload"
mkdir -p "$WORK/iso/nodeos" "$WORK/iso/nodeos-autoinstall"
cp "$BINARY" "$WORK/iso/nodeos/nodeosd-linux-amd64"
cp "$SCRIPT_DIR/install.sh" "$WORK/iso/nodeos/install.sh"

PASSHASH="$(openssl passwd -6 "$PASSWORD")"
: > "$WORK/iso/nodeos-autoinstall/meta-data"
cat > "$WORK/iso/nodeos-autoinstall/user-data" <<EOF
#cloud-config
autoinstall:
  version: 1
  locale: en_US.UTF-8
  keyboard:
    layout: $KEYBOARD
  identity:
    hostname: nodeos
    username: nodeos
    password: '$PASSHASH'
  ssh:
    install-server: true
    allow-pw: true
  storage:
    layout:
      name: lvm
  # power off when done so the installer ISO can be removed — prevents an
  # install loop when the stick/ISO is still attached on next boot
  shutdown: poweroff
  packages:
    - curl
  late-commands:
    - mkdir -p /target/opt/nodeos
    - cp /cdrom/nodeos/nodeosd-linux-amd64 /target/opt/nodeos/nodeosd-linux-amd64
    - cp /cdrom/nodeos/install.sh /target/opt/nodeos/install.sh
    # install nodeosd itself offline; bitcoind follows on first boot (needs network)
    - curtin in-target -- bash /opt/nodeos/install.sh --binary /opt/nodeos/nodeosd-linux-amd64 --no-start
    - |
      cat > /target/etc/systemd/system/nodeos-firstboot.service <<'UNIT'
      [Unit]
      Description=NodeOS first boot: install Bitcoin Core + DATUM Gateway
      After=network-online.target
      Wants=network-online.target
      ConditionPathExists=!/var/lib/nodeos/.firstboot-done

      [Service]
      Type=oneshot
      RemainAfterExit=yes
      ExecStart=/usr/bin/bash /opt/nodeos/install.sh --binary /opt/nodeos/nodeosd-linux-amd64 --with-bitcoind --with-datum --prune $PRUNE
      ExecStartPost=/usr/bin/touch /var/lib/nodeos/.firstboot-done

      [Install]
      WantedBy=multi-user.target
      UNIT
    - mkdir -p /target/etc/systemd/system/multi-user.target.wants
    - ln -sf /etc/systemd/system/nodeos-firstboot.service /target/etc/systemd/system/multi-user.target.wants/nodeos-firstboot.service
EOF

log "patching boot config for autoinstall + NodeOS branding"
for cfg in "$WORK/iso/boot/grub/grub.cfg" "$WORK/iso/boot/grub/loopback.cfg"; do
  [[ -f "$cfg" ]] || continue
  sed -i 's|---|autoinstall ds=nocloud\\;s=/cdrom/nodeos-autoinstall/ ---|' "$cfg"
  sed -i 's/Try or Install Ubuntu Server/Install NodeOS  (WIPES the first disk!)/' "$cfg"
  sed -i 's/Ubuntu Server with the HWE kernel/Install NodeOS  (HWE kernel, newer hardware)/' "$cfg"
done
# shorter timeout: boot straight into the installer
sed -i 's/timeout=30/timeout=3/' "$WORK/iso/boot/grub/grub.cfg" || true

# ---------- repack ----------

log "repacking bootable ISO"
mkdir -p "$(dirname "$OUT")"
xorriso -indev "$SRC_ISO" -report_el_torito as_mkisofs > "$WORK/mkisofs_args" 2>/dev/null
eval set -- "$(tr '\n' ' ' < "$WORK/mkisofs_args")"
xorriso -as mkisofs "$@" -V "NodeOS-Installer" -o "$OUT" "$WORK/iso" >/dev/null 2>&1

SHA="$(sha256sum "$OUT" | awk '{print $1}')"
SIZE="$(du -h "$OUT" | awk '{print $1}')"

cat <<EOF

==========================================================================
 NodeOS installer ISO ready:
   $OUT  ($SIZE)
   sha256: $SHA

 Write it to a USB stick (>= 4 GB), e.g.:
   Linux:   dd if=$OUT of=/dev/sdX bs=4M status=progress oflag=sync
   Windows: Rufus or balenaEtcher

 Boot the target machine from the stick.
 !! The first disk is WIPED automatically, no questions asked !!
 The machine POWERS OFF when the install is done — remove the stick
 (or detach the ISO in Proxmox), then power it back on.

 After installation:
   login:   nodeos / $PASSWORD   (change it!)
   web UI:  http://<machine-ip>/
   Bitcoin Core installs itself on first boot with network (prune=$PRUNE MiB).

 Tip: test the ISO in a Proxmox VM first (upload as ISO, boot a fresh VM).
==========================================================================
EOF
