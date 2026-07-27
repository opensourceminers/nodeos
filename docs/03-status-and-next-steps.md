# NodeOS — status and what comes next

Working document, updated 2026-07-27 (v0.4.1).

## Where we are

Everything below has been exercised on a real Debian 13 machine, not just
in unit tests:

| Area | State |
|---|---|
| Appliance ISO (Debian 13 preseed) | boots and installs unattended (BIOS + UEFI) |
| Bitcoin node — Core or Knots, pruned or full | installs, switches, verified |
| Work engine (DATUM) — solo mining on your own node | real gateway serving stratum work from own templates |
| Fleet: discovery, pool switching, tuning presets | verified with simulated devices |
| Firmware rollouts (canary → verify → fleet) | tested against scripted fake devices |
| Services (CLN, electrs, mempool, BTCPay) via Podman Quadlets | electrs installed end to end through the root helper |
| Auth, HTTPS (self-signed), mDNS `nodeos.local` | verified |
| System health + SMART + support bundle | verified |
| Self-update from GitHub releases | staged binary installed by the helper, version flipped, verified |

## The one thing that has never been tested: real mining hardware

No Bitaxe/NerdQAxe has ever talked to this software. Everything miner-side
is exercised against simulators that we wrote, which means they encode our
assumptions, not the devices' behaviour. The highest-value next step is not
a feature — it is one afternoon with real hardware:

1. Install from the ISO on the mining LAN.
2. Watch discovery find the devices (mDNS/scan, model detection).
3. Point the fleet at the node ("Mine on your own node") and confirm shares
   arrive at the DATUM gateway.
4. Roll out a firmware update to a single device and let the canary logic
   run for real.
5. Run `deploy/regtest-smoketest.sh` for the node-side chain.

Expect surprises in: device API dialects across firmware versions, how long
a device really takes to come back after OTA, and stratum reconnect
behaviour when the gateway restarts.

## Known gaps, ranked

1. **Real hardware validation** (above).
2. **Mainnet work engine.** The engine has only ever run against regtest.
   Mainnet adds: hours-long IBD before it can start, real template sizes,
   and the OCEAN/DATUM pool handshake.
3. **Repo is private → self-update cannot work for users.** The updater
   uses the public GitHub API; while the repo is private it reports "no
   releases found". Publishing the repo (or at least the releases) is a
   prerequisite for shipping to anyone.
4. **Services run with `Network=host`.** Simple and it works, but a
   compromised container sees localhost — including bitcoind's RPC port and
   nodeosd itself. Acceptable while the catalog is first-party only;
   revisit before any third-party service is allowed in.
5. **No release signing.** Updates are SHA256-verified against the release,
   which protects against corruption but not against a compromised GitHub
   account. Signing (and verifying in the helper) is the fix.
6. **`internal/lightning` has no tests.** Thin HTTP client, lower risk.
7. **Self-signed TLS warning** on every first visit. A device-CA install
   flow would remove it.

## What not to do next

- Do not add more services to the catalog before hardware validation. Each
  one adds support surface for a product nobody has run on real gear yet.
- Do not start the immutable A/B image work yet (see
  `02-osm-prompt-review.md`): hardening an unvalidated stack hardens the
  wrong thing.
- Do not rename the product mid-development. Decide the name once, right
  before going public, with a trademark check.

## Suggested order

1. Hardware afternoon (needs miners; blocks everything else in honesty terms).
2. Decide: publish the repo → self-update becomes real for users.
3. Mainnet soak: one box, real node, real miners, one week, watch alerts.
4. Then either release signing or the A/B image, depending on whether
   trust or reliability is the bigger worry at that point.
