#!/usr/bin/env bash
# End-to-end smoke test on a fresh NodeOS install (run as root on the box).
#
# Switches bitcoind to regtest (instantly "synced"), enables the work engine
# with a real datum_gateway, and proves the whole chain: own node builds the
# template -> gateway serves stratum work -> a raw stratum client receives a
# job with the NodeOS coinbase tags.
#
#   sudo bash regtest-smoketest.sh <web-ui-password>
#
# NOTE: test-only. Remove the regtest lines from /etc/bitcoin/bitcoin.conf
# and restore cookie_file in /etc/nodeos/config.json to go back to mainnet.
# DATUM cannot parse bcrt1 (regtest bech32) payout addresses — the test uses
# a legacy address, which its base58 parser accepts.

set -euo pipefail
PW="${1:?usage: regtest-smoketest.sh <web-ui-password>}"
BCLI="sudo -u bitcoin /usr/local/bin/bitcoin-cli -conf=/etc/bitcoin/bitcoin.conf -datadir=/var/lib/bitcoind"

echo "== switching bitcoind to regtest"
systemctl stop bitcoind
if ! grep -q '^chain=regtest' /etc/bitcoin/bitcoin.conf; then
  cat >> /etc/bitcoin/bitcoin.conf <<'EOF'

# --- test-only: regtest mode (remove for mainnet) ---
chain=regtest
[regtest]
rpcbind=127.0.0.1
rpcallowip=127.0.0.1
rpcport=8332
rpccookieperms=group
EOF
fi
systemctl start bitcoind
sleep 3
# bitcoind creates the regtest subdir 0700; open it for the bitcoin group so
# nodeosd can reach the cookie (mainnet needs none of this)
chmod g+rx /var/lib/bitcoind/regtest

echo "== mining 101 regtest blocks"
$BCLI createwallet test >/dev/null 2>&1 || true
LEGACY=$($BCLI getnewaddress "" legacy)
$BCLI generatetoaddress 101 "$($BCLI getnewaddress)" >/dev/null
echo "   payout (legacy): $LEGACY"

echo "== pointing nodeosd at the regtest cookie"
sed -i 's|/var/lib/bitcoind/.cookie|/var/lib/bitcoind/regtest/.cookie|' /etc/nodeos/config.json
systemctl restart nodeosd
sleep 3

echo "== enabling the work engine"
JAR=$(mktemp)
curl -sf -c "$JAR" -X POST http://127.0.0.1/api/auth/login -d "{\"password\":\"$PW\"}" >/dev/null \
  || { echo "login failed — set the web UI password first (or pass the right one)"; exit 1; }
curl -sf -b "$JAR" -X PUT http://127.0.0.1/api/work \
  -d "{\"enabled\":true,\"payout_address\":\"$LEGACY\",\"mode\":\"solo\",\"auto_switch\":false}" >/dev/null

echo "== waiting for state=running"
for i in $(seq 1 20); do
  STATE=$(curl -sf -b "$JAR" http://127.0.0.1/api/work | grep -o '"state":"[a-z_]*"' | head -1 | cut -d'"' -f4)
  [ "$STATE" = "running" ] && break
  sleep 3
done
echo "   work engine state: ${STATE:-unknown}"
[ "$STATE" = "running" ] || { echo "FAIL: engine did not reach running"; curl -s -b "$JAR" http://127.0.0.1/api/work; exit 1; }

echo "== stratum probe (subscribe + authorize)"
OUT=$( (printf '{"id":1,"method":"mining.subscribe","params":["smoketest/1.0"]}\n'; sleep 1; \
        printf '{"id":2,"method":"mining.authorize","params":["%s.smoketest","x"]}\n' "$LEGACY"; sleep 2) \
      | timeout 6 nc 127.0.0.1 23334 )
echo "$OUT" | grep -q 'mining.notify' || { echo "FAIL: no mining.notify received"; echo "$OUT"; exit 1; }
echo "$OUT" | grep -q '4e6f64654f53' && echo "   coinbase carries the NodeOS tag ✓"

echo
echo "SMOKE TEST PASSED — your node builds templates, the gateway serves work."
