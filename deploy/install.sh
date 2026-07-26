#!/usr/bin/env bash
# NodeOS installer for Debian 12/13 and Ubuntu 22.04+ (amd64/arm64).
# Self-contained: needs only this script plus a nodeosd binary (or --from-source).
#
#   sudo bash install.sh --binary ./nodeosd-linux-amd64 [--with-bitcoind] [--prune 20000]
#   sudo bash install.sh --from-source          # builds inside the VM (installs Go)
#
# Flags:
#   --binary PATH      path to a prebuilt nodeosd binary (default: auto-detect next to script)
#   --from-source      build nodeosd from the repo this script lives in
#   --with-bitcoind    install a Bitcoin node + systemd service, wire it to NodeOS
#   --node-impl I      node implementation: core (default) or knots
#   --with-datum       build & install OCEAN's DATUM Gateway (solo-mining work engine)
#   --prune MIB        bitcoind prune target in MiB (0 = full node, default 0)
#   --listen ADDR      nodeosd listen address (default :80)
#   --bitcoin-version V  Bitcoin Core version (default 29.0)
#   --knots-version V  Bitcoin Knots version (default 29.3.knots20260508)
#   --login-password P console/SSH password for the nodeos user when the
#                      account has none yet (default: nodeos — change it!)
#   --no-start         install everything but do not start services

set -euo pipefail

BINARY=""
FROM_SOURCE=0
WITH_BITCOIND=0
WITH_DATUM=0
DATUM_REF="master"
PRUNE=0
LISTEN=":80"
BITCOIN_VERSION="29.0"
KNOTS_VERSION="29.3.knots20260508"
NODE_IMPL="core"
LOGIN_PASSWORD="nodeos"
NO_START=0
GO_VERSION="1.26.5"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --binary) BINARY="$2"; shift 2 ;;
    --from-source) FROM_SOURCE=1; shift ;;
    --with-bitcoind) WITH_BITCOIND=1; shift ;;
    --with-datum) WITH_DATUM=1; shift ;;
    --datum-ref) DATUM_REF="$2"; shift 2 ;;
    --prune) PRUNE="$2"; shift 2 ;;
    --listen) LISTEN="$2"; shift 2 ;;
    --bitcoin-version) BITCOIN_VERSION="$2"; shift 2 ;;
    --knots-version) KNOTS_VERSION="$2"; shift 2 ;;
    --node-impl) NODE_IMPL="$2"; shift 2 ;;
    --login-password) LOGIN_PASSWORD="$2"; shift 2 ;;
    --no-start) NO_START=1; shift ;;
    *) echo "unknown flag: $1" >&2; exit 1 ;;
  esac
done

[[ $EUID -eq 0 ]] || { echo "run as root (sudo bash install.sh ...)" >&2; exit 1; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
case "$(uname -m)" in
  x86_64)  ARCH=amd64;  BTC_ARCH=x86_64-linux-gnu ;;
  aarch64) ARCH=arm64;  BTC_ARCH=aarch64-linux-gnu ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

log() { echo -e "\033[1;34m[nodeos]\033[0m $*"; }

# ---------- locate or build the binary ----------

if [[ $FROM_SOURCE -eq 1 ]]; then
  REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
  [[ -f "$REPO_ROOT/go.mod" ]] || { echo "--from-source: no go.mod found at $REPO_ROOT" >&2; exit 1; }
  if ! command -v go >/dev/null 2>&1; then
    log "installing Go $GO_VERSION to /usr/local/go"
    curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${ARCH}.tar.gz" -o /tmp/go.tgz
    rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tgz && rm /tmp/go.tgz
    export PATH="/usr/local/go/bin:$PATH"
  fi
  log "building nodeosd from source"
  (cd "$REPO_ROOT" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /tmp/nodeosd ./cmd/nodeosd)
  BINARY=/tmp/nodeosd
elif [[ -z "$BINARY" ]]; then
  for cand in "$SCRIPT_DIR/nodeosd-linux-$ARCH" "$SCRIPT_DIR/../dist/nodeosd-linux-$ARCH" "$SCRIPT_DIR/nodeosd"; do
    [[ -f "$cand" ]] && BINARY="$cand" && break
  done
  [[ -n "$BINARY" ]] || { echo "no nodeosd binary found; pass --binary PATH or --from-source" >&2; exit 1; }
fi
[[ -f "$BINARY" ]] || { echo "binary not found: $BINARY" >&2; exit 1; }

# ---------- nodeos user, dirs, binary, config ----------

log "installing nodeosd from $BINARY"
id -u nodeos >/dev/null 2>&1 || useradd --system --home-dir /var/lib/nodeos --shell /usr/sbin/nologin nodeos
# the bitcoin group exists from the start so nodeosd.service can reference it
# (SupplementaryGroups) even when the node is installed later via the web UI
getent group bitcoin >/dev/null || groupadd --system bitcoin
usermod -aG bitcoin nodeos
# journal access for support bundles (journalctl -u nodeosd/bitcoind)
getent group systemd-journal >/dev/null && usermod -aG systemd-journal nodeos
mkdir -p /etc/nodeos /var/lib/nodeos
chown nodeos:nodeos /var/lib/nodeos

# ---------- console/SSH login as nodeos ----------
# The appliance must be reachable on the machine itself: user nodeos with a
# login shell. The password is only set when the account has none — an
# existing password (ISO/cloud-init installs) is never overwritten.
if [[ "$(getent passwd nodeos | cut -d: -f7)" == */nologin ]]; then
  usermod -s /bin/bash nodeos
fi
SHADOW_HASH="$(getent shadow nodeos | cut -d: -f2)"
if [[ -z "$SHADOW_HASH" || "$SHADOW_HASH" == "!"* || "$SHADOW_HASH" == "*" ]]; then
  echo "nodeos:${LOGIN_PASSWORD}" | chpasswd
  log "console/SSH login enabled: nodeos / ${LOGIN_PASSWORD}  — CHANGE IT (passwd)"
fi
getent group sudo >/dev/null && usermod -aG sudo nodeos

install -m 0755 "$BINARY" /usr/local/bin/nodeosd

if [[ ! -f /etc/nodeos/config.json ]]; then
  cat > /etc/nodeos/config.json <<EOF
{
  "listen": "$LISTEN",
  "data_dir": "/var/lib/nodeos",
  "scan_cidr": "",
  "poll_interval_sec": 10,
  "demo": false,
  "bitcoind": {
    "rpc_url": "http://127.0.0.1:8332",
    "rpc_user": "",
    "rpc_pass": "",
    "cookie_file": "/var/lib/bitcoind/.cookie"
  },
  "pool": {
    "stratum_url": "public-pool.io",
    "stratum_port": 21496,
    "stratum_user": "REPLACE_WITH_YOUR_BTC_ADDRESS.nodeos"
  },
  "alerts": { "temp_max_c": 70 },
  "tls": { "enabled": true, "listen": ":443", "cert_file": "", "key_file": "" },
  "work": {
    "binary_path": "/usr/local/bin/datum_gateway",
    "stratum_port": 23334,
    "api_port": 7152,
    "advertise_host": ""
  }
}
EOF
  log "wrote /etc/nodeos/config.json (edit pool.stratum_user!)"
else
  log "/etc/nodeos/config.json exists — keeping it"
fi

cat > /etc/systemd/system/nodeosd.service <<'EOF'
[Unit]
Description=NodeOS control plane (miner fleet + Bitcoin node manager)
Documentation=https://github.com/opensourceminers/nodeos
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=nodeos
Group=nodeos
# read access to bitcoind's RPC cookie, even when the node is installed later,
# plus the journal for support bundles
SupplementaryGroups=bitcoin systemd-journal
ExecStart=/usr/local/bin/nodeosd --config /etc/nodeos/config.json
Restart=always
RestartSec=3
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/var/lib/nodeos
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
EOF

# ---------- mDNS: reachable as http(s)://<hostname>.local ----------

if command -v apt-get >/dev/null 2>&1; then
  log "installing avahi (mDNS), smartmontools, sudo, openssh-server"
  export DEBIAN_FRONTEND=noninteractive
  apt-get install -y -qq avahi-daemon libnss-mdns smartmontools sudo openssh-server >/dev/null 2>&1 \
    || log "package install partially failed (non-fatal)"
  systemctl enable --now ssh >/dev/null 2>&1 || true
  usermod -aG sudo nodeos 2>/dev/null || true
  mkdir -p /etc/avahi/services
  cat > /etc/avahi/services/nodeos.service <<'EOF'
<?xml version="1.0" standalone='no'?>
<!DOCTYPE service-group SYSTEM "avahi-service.dtd">
<service-group>
  <name replace-wildcards="yes">NodeOS on %h</name>
  <service><type>_http._tcp</type><port>80</port></service>
  <service><type>_https._tcp</type><port>443</port></service>
</service-group>
EOF
  systemctl enable --now avahi-daemon >/dev/null 2>&1 || true
fi

# ---------- SMART collector (root timer -> smart.json for nodeosd) ----------

log "installing SMART collector"
cat > /usr/local/bin/nodeos-smart <<'EOF'
#!/usr/bin/env bash
# Writes condensed SMART data where the unprivileged nodeosd can read it.
set -u
OUT=/var/lib/nodeos/health/smart.json
mkdir -p /var/lib/nodeos/health
command -v smartctl >/dev/null || exit 0
DISKS=""
while read -r dev _; do
  [ -n "$dev" ] || continue
  J="$(smartctl -H -A -i --json=c "$dev" 2>/dev/null)" || true
  [ -n "$J" ] || continue
  DISKS="${DISKS:+$DISKS,}$J"
done < <(smartctl --scan | awk '{print $1}')
printf '{"generated_unix":%s,"disks":[%s]}\n' "$(date +%s)" "$DISKS" > "$OUT.tmp"
mv "$OUT.tmp" "$OUT"
chown nodeos:nodeos "$OUT" 2>/dev/null || true
EOF
chmod 0755 /usr/local/bin/nodeos-smart

cat > /etc/systemd/system/nodeos-smart.service <<'EOF'
[Unit]
Description=NodeOS SMART collector

[Service]
Type=oneshot
ExecStart=/usr/local/bin/nodeos-smart
EOF

cat > /etc/systemd/system/nodeos-smart.timer <<'EOF'
[Unit]
Description=NodeOS SMART collector (every 30 min)

[Timer]
OnBootSec=2min
OnUnitActiveSec=30min

[Install]
WantedBy=timers.target
EOF
systemctl daemon-reload
systemctl enable --now nodeos-smart.timer >/dev/null 2>&1 || true
/usr/local/bin/nodeos-smart || true

# ---------- privileged admin helper (node install/switch, self-update) ----------
# nodeosd runs unprivileged (NoNewPrivileges); privileged operations go
# through a command-file queue in /var/lib/nodeos/admin that a root-owned
# systemd path unit watches. This helper validates and executes the commands.

log "installing nodeos-admin helper"
cat > /usr/local/bin/nodeos-admin <<'HELPER'
#!/usr/bin/env bash
# NodeOS privileged helper. Runs as root, triggered by nodeos-admin.path.
# Commands (one arg per line in <id>.cmd):
#   node-install <core|knots> <version> <prune-MiB>
#   self-update <version>
set -u
QUEUE=/var/lib/nodeos/admin
STAGED=/var/lib/nodeos/staged/nodeosd

log() { echo "[$(date +%H:%M:%S)] $*"; }

ensure_node_base() {
  getent group bitcoin >/dev/null || groupadd --system bitcoin
  id -u bitcoin >/dev/null 2>&1 || useradd --system -g bitcoin --home-dir /var/lib/bitcoind --shell /usr/sbin/nologin bitcoin
  mkdir -p /etc/bitcoin /var/lib/bitcoind
  chown bitcoin:bitcoin /var/lib/bitcoind
  chmod 750 /var/lib/bitcoind
  id -u nodeos >/dev/null 2>&1 && usermod -aG bitcoin nodeos
  if [[ ! -f /etc/bitcoin/bitcoin.conf ]]; then
    cat > /etc/bitcoin/bitcoin.conf <<'CONF'
# Managed by NodeOS — safe to edit.
server=1
prune=0
dbcache=1024
rpcbind=127.0.0.1
rpcallowip=127.0.0.1
# cookie readable by the bitcoin group (nodeos is a member)
rpccookieperms=group
CONF
    chown root:bitcoin /etc/bitcoin/bitcoin.conf
    chmod 640 /etc/bitcoin/bitcoin.conf
  fi
  if [[ ! -f /etc/systemd/system/bitcoind.service ]]; then
    cat > /etc/systemd/system/bitcoind.service <<'UNIT'
[Unit]
Description=Bitcoin daemon (managed by NodeOS)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=bitcoin
Group=bitcoin
ExecStart=/usr/local/bin/bitcoind -conf=/etc/bitcoin/bitcoin.conf -datadir=/var/lib/bitcoind
Restart=on-failure
RestartSec=10
TimeoutStopSec=600
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/var/lib/bitcoind
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
UNIT
  fi
}

set_prune() {
  local prune="$1"
  if grep -q '^prune=' /etc/bitcoin/bitcoin.conf; then
    sed -i "s/^prune=.*/prune=$prune/" /etc/bitcoin/bitcoin.conf
  else
    echo "prune=$prune" >> /etc/bitcoin/bitcoin.conf
  fi
}

node_install() {
  local impl="$1" version="$2" prune="$3"
  case "$impl" in core|knots) ;; *) log "invalid impl: $impl"; return 1 ;; esac
  [[ "$version" =~ ^[0-9]+\.[0-9]+(\.[0-9]+)?(\.knots[0-9]{8})?$ ]] || { log "invalid version: $version"; return 1; }
  [[ "$prune" =~ ^[0-9]+$ ]] || { log "invalid prune: $prune"; return 1; }
  local btc_arch
  case "$(uname -m)" in
    x86_64)  btc_arch=x86_64-linux-gnu ;;
    aarch64) btc_arch=aarch64-linux-gnu ;;
    *) log "unsupported architecture"; return 1 ;;
  esac
  local url sums
  if [[ "$impl" == core ]]; then
    [[ "$version" != *knots* ]] || { log "core version must not contain 'knots'"; return 1; }
    url="https://bitcoincore.org/bin/bitcoin-core-$version/bitcoin-$version-$btc_arch.tar.gz"
    sums="https://bitcoincore.org/bin/bitcoin-core-$version/SHA256SUMS"
  else
    [[ "$version" == *knots* ]] || { log "knots version must look like 29.3.knots20260508"; return 1; }
    local major="${version%%.*}"
    url="https://bitcoinknots.org/files/${major}.x/$version/bitcoin-$version-$btc_arch.tar.gz"
    sums="https://bitcoinknots.org/files/${major}.x/$version/SHA256SUMS"
  fi
  local tmp
  tmp="$(mktemp -d)" || return 1
  log "downloading bitcoin $impl $version ($btc_arch)"
  ( set -e
    cd "$tmp"
    curl -fsSLO "$url"
    curl -fsSL "$sums" -o SHA256SUMS
    # NOTE: checksum only; GPG signature verification is on the roadmap.
    sha256sum --check --ignore-missing SHA256SUMS
    tar -xzf "bitcoin-$version-$btc_arch.tar.gz"
    install -m 0755 "bitcoin-$version/bin/bitcoind" "bitcoin-$version/bin/bitcoin-cli" /usr/local/bin/
  ) || { log "download/verify/install failed"; rm -rf "$tmp"; return 1; }
  rm -rf "$tmp"
  ensure_node_base
  set_prune "$prune"
  systemctl daemon-reload
  systemctl enable bitcoind >/dev/null 2>&1
  if [[ "${NODEOS_ADMIN_NO_START:-0}" != 1 ]]; then
    log "restarting bitcoind"
    systemctl restart bitcoind || {
      log "bitcoind failed to start — check: journalctl -u bitcoind"
      log "note: switching a pruned node to prune=0 requires re-downloading the chain"
      return 1
    }
  fi
  log "installed: $(/usr/local/bin/bitcoind --version 2>/dev/null | head -1)"
}

self_update() {
  [[ -f "$STAGED" ]] || { log "no staged binary at $STAGED"; return 1; }
  log "installing staged nodeosd and restarting"
  install -m 0755 "$STAGED" /usr/local/bin/nodeosd
  rm -f "$STAGED"
  systemctl restart nodeosd
}

process_queue() {
  shopt -s nullglob
  local cmd id
  for cmd in "$QUEUE"/*.cmd; do
    id="$(basename "$cmd" .cmd)"
    mapfile -t lines < "$cmd"
    mv "$cmd" "$QUEUE/$id.run"
    {
      case "${lines[0]:-}" in
        node-install) node_install "${lines[1]:-}" "${lines[2]:-}" "${lines[3]:-}" ;;
        self-update)  self_update ;;
        *) log "unknown command: ${lines[0]:-<empty>}"; false ;;
      esac
    } >> "$QUEUE/$id.log" 2>&1 && touch "$QUEUE/$id.done" || touch "$QUEUE/$id.fail"
    rm -f "$QUEUE/$id.run"
    chown nodeos:nodeos "$QUEUE/$id".* 2>/dev/null || true
  done
}

case "${1:-}" in
  process-queue) process_queue ;;
  run) shift
    case "${1:-}" in
      node-install) shift; node_install "$@" ;;
      self-update)  self_update ;;
      *) echo "usage: nodeos-admin run {node-install|self-update} ..." >&2; exit 1 ;;
    esac ;;
  *) echo "usage: nodeos-admin {process-queue|run ...}" >&2; exit 1 ;;
esac
HELPER
chmod 0755 /usr/local/bin/nodeos-admin

mkdir -p /var/lib/nodeos/admin /var/lib/nodeos/staged
chown nodeos:nodeos /var/lib/nodeos/admin /var/lib/nodeos/staged
touch /var/lib/nodeos/admin/.helper-ready

cat > /etc/systemd/system/nodeos-admin.service <<'EOF'
[Unit]
Description=NodeOS privileged helper (processes admin command queue)

[Service]
Type=oneshot
ExecStart=/usr/local/bin/nodeos-admin process-queue
EOF

cat > /etc/systemd/system/nodeos-admin.path <<'EOF'
[Unit]
Description=Watch the NodeOS admin command queue

[Path]
PathExistsGlob=/var/lib/nodeos/admin/*.cmd

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now nodeos-admin.path >/dev/null 2>&1 || true

# ---------- optional: Bitcoin node (Core or Knots) ----------

if [[ $WITH_BITCOIND -eq 1 ]]; then
  case "$NODE_IMPL" in
    core)  NODE_VERSION="$BITCOIN_VERSION" ;;
    knots) NODE_VERSION="$KNOTS_VERSION" ;;
    *) echo "--node-impl must be core or knots" >&2; exit 1 ;;
  esac
  log "installing Bitcoin ${NODE_IMPL} ${NODE_VERSION} (prune $PRUNE MiB)"
  NODEOS_ADMIN_NO_START=$NO_START /usr/local/bin/nodeos-admin run node-install "$NODE_IMPL" "$NODE_VERSION" "$PRUNE"
fi

# ---------- optional: DATUM Gateway (work engine) ----------

if [[ $WITH_DATUM -eq 1 ]]; then
  log "building OCEAN DATUM Gateway ($DATUM_REF) from source"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq --no-install-recommends \
    git cmake pkgconf build-essential \
    libcurl4-openssl-dev libjansson-dev libmicrohttpd-dev libsodium-dev
  rm -rf /tmp/datum_gateway
  git clone --depth 1 --branch "$DATUM_REF" \
    https://github.com/OCEAN-xyz/datum_gateway /tmp/datum_gateway
  (cd /tmp/datum_gateway && cmake . && make -j"$(nproc)")
  install -m 0755 /tmp/datum_gateway/datum_gateway /usr/local/bin/datum_gateway
  rm -rf /tmp/datum_gateway
  log "datum_gateway installed — enable the work engine in the NodeOS web UI (Node tab)"
  # nodeosd supervises the gateway itself (no systemd unit): it regenerates
  # the config with fresh RPC cookie credentials on every start.
fi

# ---------- console branding ----------

log "installing NodeOS console banner"
cat > /usr/local/bin/nodeos-banner <<'EOF'
#!/bin/sh
# Regenerates /etc/issue (pre-login console screen) with access info.
IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
[ -n "$IP" ] || IP="(no network yet)"
VER="$(/usr/local/bin/nodeosd --version 2>/dev/null || echo NodeOS)"
{
cat <<'ART'

  ███╗   ██╗ ██████╗ ██████╗ ███████╗     ██████╗ ███████╗
  ████╗  ██║██╔═══██╗██╔══██╗██╔════╝    ██╔═══██╗██╔════╝
  ██╔██╗ ██║██║   ██║██║  ██║█████╗      ██║   ██║███████╗
  ██║╚██╗██║██║   ██║██║  ██║██╔══╝      ██║   ██║╚════██║
  ██║ ╚████║╚██████╔╝██████╔╝███████╗    ╚██████╔╝███████║
  ╚═╝  ╚═══╝ ╚═════╝ ╚═════╝ ╚══════╝     ╚═════╝ ╚══════╝
        Bitcoin mining & node control plane

ART
printf '  %s\n\n' "$VER"
printf '  Web UI:   https://%s.local/  or  http://%s/\n' "$(hostname)" "$IP"
printf '  SSH:      ssh nodeos@%s\n' "$IP"
printf '  Login:    nodeos  (default password: nodeos - change it with: passwd)\n\n'
} > /etc/issue
# refresh the login prompt on tty1 unless someone is working there
who 2>/dev/null | grep -q tty1 || systemctl try-restart getty@tty1.service 2>/dev/null || true
EOF
chmod 0755 /usr/local/bin/nodeos-banner

cat > /etc/systemd/system/nodeos-banner.service <<'EOF'
[Unit]
Description=NodeOS console banner (login screen info)
After=network-online.target nodeosd.service
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/nodeos-banner

[Install]
WantedBy=multi-user.target
EOF

# post-login banner (motd)
mkdir -p /etc/update-motd.d
cat > /etc/update-motd.d/10-nodeos <<'EOF'
#!/bin/sh
IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
NSTAT="$(systemctl is-active nodeosd 2>/dev/null || echo unknown)"
BSTAT="$(systemctl is-active bitcoind 2>/dev/null || echo not-installed)"
cat <<'ART'

  ███╗   ██╗ ██████╗ ██████╗ ███████╗     ██████╗ ███████╗
  ████╗  ██║██╔═══██╗██╔══██╗██╔════╝    ██╔═══██╗██╔════╝
  ██╔██╗ ██║██║   ██║██║  ██║█████╗      ██║   ██║███████╗
  ██║╚██╗██║██║   ██║██║  ██║██╔══╝      ██║   ██║╚════██║
  ██║ ╚████║╚██████╔╝██████╔╝███████╗    ╚██████╔╝███████║
  ╚═╝  ╚═══╝ ╚═════╝ ╚═════╝ ╚══════╝     ╚═════╝ ╚══════╝

ART
printf '  Web UI:    https://%s.local/  or  http://%s/\n' "$(hostname)" "$IP"
printf '  Services:  nodeosd %s · bitcoind %s\n' "$NSTAT" "$BSTAT"
printf '  Logs:      journalctl -u nodeosd -f\n\n'
EOF
chmod 0755 /etc/update-motd.d/10-nodeos
# silence Ubuntu's default motd noise (ads, help text); harmless if absent
chmod -x /etc/update-motd.d/10-help-text /etc/update-motd.d/50-motd-news \
         /etc/update-motd.d/88-esm-announce /etc/update-motd.d/91-contract-ua-esm-status \
         2>/dev/null || true
systemctl disable --now motd-news.timer >/dev/null 2>&1 || true
systemctl enable nodeos-banner >/dev/null 2>&1 || true
/usr/local/bin/nodeos-banner || true

# ---------- start ----------

systemctl daemon-reload
systemctl enable nodeosd
[[ $NO_START -eq 1 ]] || systemctl restart nodeosd

IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
PORT="${LISTEN##*:}"
[[ "$PORT" == "80" ]] && URL="http://${IP:-<vm-ip>}/" || URL="http://${IP:-<vm-ip>}:${PORT}/"

log "done."
log "web UI:        https://$(hostname).local/  (self-signed — accept the browser warning)"
log "               $URL"
log "config:        /etc/nodeos/config.json"
log "logs:          journalctl -u nodeosd -f"
[[ $WITH_BITCOIND -eq 1 ]] && log "bitcoind logs: journalctl -u bitcoind -f (initial sync takes hours/days; prune=$PRUNE)"
log "NodeOS scans your subnet for Bitaxe/NerdAxe/NerdQAxe miners at startup."
log "First visit to the web UI asks you to set the admin password."
log "HTTPS uses a self-signed certificate — your browser warns once; that is expected."
