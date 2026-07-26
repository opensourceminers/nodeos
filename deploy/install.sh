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
#   --with-bitcoind    install Bitcoin Core + systemd service, wire it to NodeOS
#   --prune MIB        bitcoind prune target in MiB (0 = full node, default 0)
#   --listen ADDR      nodeosd listen address (default :80)
#   --bitcoin-version V  Bitcoin Core version (default 29.0)
#   --no-start         install everything but do not start services

set -euo pipefail

BINARY=""
FROM_SOURCE=0
WITH_BITCOIND=0
PRUNE=0
LISTEN=":80"
BITCOIN_VERSION="29.0"
NO_START=0
GO_VERSION="1.26.5"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --binary) BINARY="$2"; shift 2 ;;
    --from-source) FROM_SOURCE=1; shift ;;
    --with-bitcoind) WITH_BITCOIND=1; shift ;;
    --prune) PRUNE="$2"; shift 2 ;;
    --listen) LISTEN="$2"; shift 2 ;;
    --bitcoin-version) BITCOIN_VERSION="$2"; shift 2 ;;
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
mkdir -p /etc/nodeos /var/lib/nodeos
chown nodeos:nodeos /var/lib/nodeos

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
  "alerts": { "temp_max_c": 70 }
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

# ---------- optional: Bitcoin Core ----------

if [[ $WITH_BITCOIND -eq 1 ]]; then
  log "installing Bitcoin Core $BITCOIN_VERSION"
  id -u bitcoin >/dev/null 2>&1 || useradd --system --home-dir /var/lib/bitcoind --shell /usr/sbin/nologin bitcoin
  mkdir -p /etc/bitcoin /var/lib/bitcoind
  chown bitcoin:bitcoin /var/lib/bitcoind
  chmod 750 /var/lib/bitcoind

  TARBALL="bitcoin-${BITCOIN_VERSION}-${BTC_ARCH}.tar.gz"
  cd /tmp
  curl -fsSLO "https://bitcoincore.org/bin/bitcoin-core-${BITCOIN_VERSION}/${TARBALL}"
  curl -fsSLO "https://bitcoincore.org/bin/bitcoin-core-${BITCOIN_VERSION}/SHA256SUMS"
  sha256sum --check --ignore-missing SHA256SUMS
  # NOTE: checksum only. Verifying the SHA256SUMS.asc signatures is on the
  # roadmap; do it manually for production installs.
  tar -xzf "$TARBALL"
  install -m 0755 "bitcoin-${BITCOIN_VERSION}/bin/bitcoind" "bitcoin-${BITCOIN_VERSION}/bin/bitcoin-cli" /usr/local/bin/
  rm -rf "bitcoin-${BITCOIN_VERSION}" "$TARBALL" SHA256SUMS

  if [[ ! -f /etc/bitcoin/bitcoin.conf ]]; then
    cat > /etc/bitcoin/bitcoin.conf <<EOF
# Managed by NodeOS installer — safe to edit.
server=1
prune=$PRUNE
dbcache=1024
rpcbind=127.0.0.1
rpcallowip=127.0.0.1
# cookie readable by the bitcoin group (nodeos is a member)
rpccookieperms=group
EOF
    chown root:bitcoin /etc/bitcoin/bitcoin.conf
    chmod 640 /etc/bitcoin/bitcoin.conf
  fi

  cat > /etc/systemd/system/bitcoind.service <<'EOF'
[Unit]
Description=Bitcoin Core daemon
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
EOF

  usermod -aG bitcoin nodeos
  systemctl daemon-reload
  systemctl enable bitcoind
  [[ $NO_START -eq 1 ]] || systemctl restart bitcoind
fi

# ---------- start ----------

systemctl daemon-reload
systemctl enable nodeosd
[[ $NO_START -eq 1 ]] || systemctl restart nodeosd

IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
PORT="${LISTEN##*:}"
[[ "$PORT" == "80" ]] && URL="http://${IP:-<vm-ip>}/" || URL="http://${IP:-<vm-ip>}:${PORT}/"

log "done."
log "web UI:        $URL"
log "config:        /etc/nodeos/config.json"
log "logs:          journalctl -u nodeosd -f"
[[ $WITH_BITCOIND -eq 1 ]] && log "bitcoind logs: journalctl -u bitcoind -f (initial sync takes hours/days; prune=$PRUNE)"
log "NodeOS scans your subnet for Bitaxe/NerdAxe/NerdQAxe miners at startup."
log "SECURITY: no auth yet — keep this on a trusted LAN/VLAN."
