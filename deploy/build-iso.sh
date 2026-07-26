#!/usr/bin/env bash
# Builds the NodeOS appliance installer ISO.
#
# Takes the official Debian 13 (trixie) netinst ISO and turns it into an
# unattended installer: boot it from a USB stick, and the machine installs
# Debian + NodeOS on its own. On first boot with network, the Bitcoin node
# (Core or Knots) and the DATUM gateway are installed and started.
#
#   ############################################################
#   #  WARNING: the resulting ISO WIPES the target machine's   #
#   #  first disk without asking. It is an appliance installer.#
#   ############################################################
#
# Run on any Debian/Ubuntu machine (the Proxmox host or a VM works):
#   bash deploy/build-iso.sh [--binary dist/nodeosd-linux-amd64] [--out dist/nodeos-installer-amd64.iso]
#                            [--password nodeos] [--keyboard us|de|ch] [--prune 20000]
#                            [--node-impl core|knots]
#                            [--iso /path/to/debian-13.x.0-amd64-netinst.iso]
#
# Needs: xorriso curl openssl  (apt install -y xorriso curl openssl)
# The Debian netinst ISO (~0.7 GB) is downloaded and checksum-verified unless
# --iso is given. Works for BIOS/SeaBIOS and UEFI boot.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

BINARY="$REPO_ROOT/dist/nodeosd-linux-amd64"
OUT="$REPO_ROOT/dist/nodeos-installer-amd64.iso"
PASSWORD="nodeos"
KEYBOARD="us"
PRUNE=20000
NODE_IMPL="core"
SRC_ISO=""
MIRROR="https://cdimage.debian.org/debian-cd/current/amd64/iso-cd"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --binary) BINARY="$2"; shift 2 ;;
    --out) OUT="$2"; shift 2 ;;
    --password) PASSWORD="$2"; shift 2 ;;
    --keyboard) KEYBOARD="$2"; shift 2 ;;
    --prune) PRUNE="$2"; shift 2 ;;
    --node-impl) NODE_IMPL="$2"; shift 2 ;;
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

# ---------- get the Debian netinst ISO ----------

if [[ -z "$SRC_ISO" ]]; then
  log "resolving current Debian 13 netinst ISO"
  SUMS="$(curl -fsSL "$MIRROR/SHA256SUMS")"
  ISO_NAME="$(echo "$SUMS" | grep -o 'debian-13[0-9.]*-amd64-netinst\.iso' | head -1)"
  [[ -n "$ISO_NAME" ]] || { echo "could not find netinst ISO in $MIRROR/SHA256SUMS" >&2; exit 1; }
  SRC_ISO="$REPO_ROOT/dist/$ISO_NAME"
  if [[ ! -f "$SRC_ISO" ]]; then
    log "downloading $ISO_NAME (~0.7 GB)"
    mkdir -p "$REPO_ROOT/dist"
    curl -fL -o "$SRC_ISO" "$MIRROR/$ISO_NAME"
  fi
  log "verifying checksum"
  (cd "$(dirname "$SRC_ISO")" && echo "$SUMS" | grep "  $ISO_NAME\$" | sha256sum --check -)
fi
[[ -f "$SRC_ISO" ]] || { echo "ISO not found: $SRC_ISO" >&2; exit 1; }

# ---------- unpack and inject ----------

log "unpacking ISO"
xorriso -osirrox on -indev "$SRC_ISO" -extract / "$WORK/iso" >/dev/null 2>&1
chmod -R u+w "$WORK/iso"

log "injecting NodeOS payload + preseed"
mkdir -p "$WORK/iso/nodeos"
cp "$BINARY" "$WORK/iso/nodeos/nodeosd-linux-amd64"
cp "$SCRIPT_DIR/install.sh" "$WORK/iso/nodeos/install.sh"

# runs inside the installed system (chroot) at the end of the installation
cat > "$WORK/iso/nodeos/target-setup.sh" <<EOF
#!/bin/bash
# NodeOS target setup — executed by the Debian installer via in-target.
set -e
bash /opt/nodeos/install.sh --binary /opt/nodeos/nodeosd-linux-amd64 --no-start
cat > /etc/systemd/system/nodeos-firstboot.service <<'UNIT'
[Unit]
Description=NodeOS first boot: install Bitcoin node + DATUM Gateway
After=network-online.target
Wants=network-online.target
ConditionPathExists=!/var/lib/nodeos/.firstboot-done

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/bin/bash /opt/nodeos/install.sh --binary /opt/nodeos/nodeosd-linux-amd64 --with-bitcoind --node-impl $NODE_IMPL --with-datum --prune $PRUNE
ExecStartPost=/usr/bin/touch /var/lib/nodeos/.firstboot-done

[Install]
WantedBy=multi-user.target
UNIT
mkdir -p /etc/systemd/system/multi-user.target.wants
ln -sf ../nodeos-firstboot.service /etc/systemd/system/multi-user.target.wants/nodeos-firstboot.service
EOF
chmod 0755 "$WORK/iso/nodeos/target-setup.sh"

PASSHASH="$(openssl passwd -6 "$PASSWORD")"
cat > "$WORK/iso/nodeos/preseed.cfg" <<EOF
# NodeOS unattended install (Debian 13). WIPES the first disk.
d-i debian-installer/locale string en_US.UTF-8
d-i keyboard-configuration/xkb-keymap select $KEYBOARD

d-i netcfg/choose_interface select auto
d-i netcfg/get_hostname string nodeos
d-i netcfg/get_domain string local
d-i netcfg/hostname string nodeos

d-i mirror/country string manual
d-i mirror/http/hostname string deb.debian.org
d-i mirror/http/directory string /debian
d-i mirror/http/proxy string

d-i passwd/root-login boolean false
d-i passwd/user-fullname string NodeOS
d-i passwd/username string nodeos
d-i passwd/user-password-crypted password $PASSHASH

d-i clock-setup/utc boolean true
d-i time/zone string UTC
d-i clock-setup/ntp boolean true

# partitioning: single disk, everything in one partition, no questions
d-i partman-auto/method string regular
d-i partman-auto/choose_recipe select atomic
d-i partman-lvm/device_remove_lvm boolean true
d-i partman-md/device_remove_md boolean true
d-i partman-partitioning/confirm_write_new_label boolean true
d-i partman/choose_partition select finish
d-i partman/confirm boolean true
d-i partman/confirm_nooverwrite boolean true

d-i apt-setup/non-free-firmware boolean true
tasksel tasksel/first multiselect standard, ssh-server
# avahi + libnss-mdns: the box is reachable as http://nodeos.local
d-i pkgsel/include string curl ca-certificates avahi-daemon libnss-mdns
popularity-contest popularity-contest/participate boolean false

d-i grub-installer/only_debian boolean true
d-i grub-installer/bootdev string default

d-i preseed/late_command string \\
  mkdir -p /target/opt/nodeos; \\
  cp /cdrom/nodeos/nodeosd-linux-amd64 /cdrom/nodeos/install.sh /cdrom/nodeos/target-setup.sh /target/opt/nodeos/; \\
  in-target bash /opt/nodeos/target-setup.sh

# power off when done so the installer ISO can be removed — prevents an
# install loop when the stick/ISO is still attached on next boot
d-i finish-install/reboot_in_progress note
d-i debian-installer/exit/poweroff boolean true
EOF

# ---------- boot menus: auto-start the preseeded install ----------

BOOT_ARGS="auto=true priority=critical preseed/file=/cdrom/nodeos/preseed.cfg"

log "patching UEFI boot menu (grub)"
cat > "$WORK/iso/boot/grub/grub.cfg" <<EOF
set timeout=3
set default=0
menuentry "Install NodeOS  (WIPES the first disk!)" {
    linux    /install.amd/vmlinuz $BOOT_ARGS --- quiet
    initrd   /install.amd/initrd.gz
}
menuentry "Debian installer (manual, expert)" {
    linux    /install.amd/vmlinuz
    initrd   /install.amd/initrd.gz
}
EOF

log "patching BIOS boot menu (isolinux)"
if [[ -d "$WORK/iso/isolinux" ]]; then
  # Replace Debian's whole menu tree (menu.cfg pulls in "Graphical install"
  # as menu default) with a self-contained config: NodeOS is the default and
  # boots automatically after 3 s — same behaviour as the UEFI grub menu.
  cat > "$WORK/iso/isolinux/isolinux.cfg" <<EOF
ui vesamenu.c32
prompt 0
timeout 30

menu title NodeOS Installer
menu tabmsg Press ENTER to start now, TAB to edit
label nodeos
	menu label Install NodeOS  (WIPES the first disk!)
	menu default
	kernel /install.amd/vmlinuz
	append $BOOT_ARGS vga=788 initrd=/install.amd/initrd.gz --- quiet
label expert
	menu label Debian installer (manual, expert)
	kernel /install.amd/vmlinuz
	append vga=788 initrd=/install.amd/initrd.gz ---
EOF
fi

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
 NodeOS installer ISO ready (Debian 13 based):
   $OUT  ($SIZE)
   sha256: $SHA

 Write it to a USB stick (>= 1 GB), e.g.:
   Linux:   dd if=$OUT of=/dev/sdX bs=4M status=progress oflag=sync
   Windows: Rufus or balenaEtcher

 Boot the target machine from the stick (BIOS or UEFI).
 !! The first disk is WIPED automatically, no questions asked !!
 The machine POWERS OFF when the install is done — remove the stick
 (or detach the ISO in Proxmox), then power it back on.

 After installation:
   web UI:  https://nodeos.local/  (self-signed cert — accept the warning)
            or http://nodeos.local/ / http://<machine-ip>/
   login:   nodeos / $PASSWORD   (SSH; change it with: passwd)
   The web UI asks you to set its own admin password on first visit.
   Bitcoin $NODE_IMPL installs itself on first boot with network (prune=$PRUNE MiB).

 The installer needs a network connection (netinst downloads packages).
 Tip: test the ISO in a Proxmox VM first (upload as ISO, boot a fresh VM).
==========================================================================
EOF
