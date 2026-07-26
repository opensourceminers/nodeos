# NodeOS

**The operating layer for people who *run* Bitcoin — nodes, solo miners, home
mining fleets.** One daemon, one UI: plug in a Bitaxe, it appears
automatically and mines against your own node.

> Status: **prototype v0.3.0** — fleet management, discovery, pool control,
> node monitoring, the DATUM work engine (solo mining against your own node,
> with auto-switch), a login-protected web UI, Core/Knots selection with
> pruning, and self-updates from GitHub releases work today. Firmware updates
> and the flashable appliance image are next.
> Product thinking and full roadmap: [PRODUCT-ANALYSIS.md](PRODUCT-ANALYSIS.md).

Bitcoin only. No shitcoins. No custody — NodeOS never touches private keys.

## What works in this prototype

- **Miner auto-discovery** — subnet scan finds every ESP-Miner-family device
  (Bitaxe, NerdAxe, NerdQAxe, …); runs automatically at startup, manual add by IP
- **Fleet dashboard** — live hashrate/temp/power/shares/best-diff per device and
  fleet-wide, 1-hour history chart, SSE live updates
- **Pool management** — set stratum + fallback once, push to the whole fleet
  with one click (staged apply + restart per device)
- **Bitcoin Core integration** — sync progress, difficulty, peers, mempool;
  installer can set up bitcoind (cookie auth, pruned or full)
- **Work engine (solo mining)** — supervises [OCEAN's DATUM
  Gateway](https://github.com/OCEAN-xyz/datum_gateway): your node builds the
  block templates, your miners do the work. Two modes: *pure solo* (a found
  block pays your address) or *OCEAN pooled* (steady payouts, self-built
  templates). **Auto-switch**: once the node is synced and the gateway
  healthy, the whole fleet is pointed at your node — previous pool kept as
  per-device fallback, every miner gets its own worker name
  (`bc1q….{worker}`). Crash supervision with backoff, health checks, log
  capture, one-click switch back.
- **Honest solo odds** — expected time to block and daily chance computed from
  *your* fleet hashrate and *real* network difficulty
- **Alerts** — miner offline/online, over-temperature, and the
  share-≥-network-difficulty "possible block!" event
- **Login-protected web UI** — first visit asks you to set an admin password
  (PBKDF2-hashed, HttpOnly session cookie); change it under Settings.
  `--no-auth` for development only.
- **HTTPS + `nodeos.local`** — a self-signed certificate (SANs for hostname,
  `.local` name and LAN IPs) is generated on first start and served on
  `:443`; avahi/mDNS makes the box reachable as `https://<hostname>.local/`.
  HTTP on `:80` stays available for LAN scripts.
- **Core or Knots, pruned or full** — pick the node implementation and prune
  target at install (`--node-impl knots --prune 20000`) or switch later in
  the web UI (Node tab). Chain data is kept when switching; downloads are
  checksum-verified against the vendor's SHA256SUMS.
- **Self-updates from GitHub** — Settings → Updates checks the repo's
  releases, downloads the binary, verifies it against the release's
  SHA256SUMS and installs it via the root helper (service restarts, UI
  reconnects). Requires the repo/releases to be publicly readable.
- **Demo mode** — simulated fleet (real HTTP devices in-process) to try the UI
  without hardware: `nodeosd --demo`
- **REST API + SSE** — everything the UI does is `/api/*`; automation-friendly

## Quickstart

### A. Proxmox (recommended for testing)

On the Proxmox host:

```bash
bash deploy/proxmox-create-vm.sh --bridge vmbr0 --storage local-lvm
```

This creates a Debian 13 cloud-init VM. Then, from your workstation
(repo root, after `./build.sh` or `.\build.ps1`):

```bash
scp dist/nodeosd-linux-amd64 deploy/install.sh nodeos@<VM-IP>:/tmp/
ssh nodeos@<VM-IP> "sudo bash /tmp/install.sh --binary /tmp/nodeosd-linux-amd64 --with-bitcoind --with-datum --prune 20000"
```

Open `http://<VM-IP>/`. **The VM's bridge must sit on the same L2 network as
your miners** or discovery can't see them (a routed subnet works via the scan
CIDR field in the UI).

Windows users: `.\deploy\push-to-vm.ps1 -VmIp <VM-IP> -WithBitcoind -Prune 20000`
does the scp+ssh dance for you.

### B. Any Debian/Ubuntu box or VM (Pi 5, x86, existing homelab)

```bash
git clone <this-repo> && cd nodeos
sudo bash deploy/install.sh --from-source --with-bitcoind --prune 20000
```

`--from-source` installs Go inside the machine and builds there — no
workstation needed. Already have a node (Umbrel, Start9, DIY)? Skip
`--with-bitcoind` and point `bitcoind.rpc_*` in `/etc/nodeos/config.json` at
your existing node: NodeOS runs *alongside*, nothing gets reformatted.

### C. Appliance installer ISO (USB stick or Proxmox)

Build a bootable ISO that installs NodeOS unattended — the Umbrel-style
install path. **Debian 13 (trixie) based**, preseeded, works on BIOS and
UEFI; the installed box is reachable as `http://nodeos.local` via mDNS.
Build it on any Debian/Ubuntu machine (a VM or the Proxmox host; needs
`xorriso curl openssl` and the repo + `dist/` binary):

```bash
bash deploy/build-iso.sh --keyboard ch --prune 20000 --node-impl knots
# → dist/nodeos-installer-amd64.iso  (~0.8 GB, netinst-based)
```

**The ISO wipes the target machine's first disk without asking.** After the
unattended install the machine powers off — remove the stick / detach the
ISO, power on, done: `http://<machine-ip>/`, login `nodeos` (password set at
build time, default `nodeos` — change it). Bitcoin Core installs itself on
first boot with network.

Test it in Proxmox first: upload the ISO (storage → ISO Images → Upload),
create a fresh VM booting from it (2 cores / 4 GB / ≥64 GB disk, bridge on
the miner LAN), start — it installs itself, powers off, detach ISO, boot.

### D. Developer / demo mode (any OS)

```bash
go run ./cmd/nodeosd --demo --listen 127.0.0.1:8080
```

→ http://127.0.0.1:8080 with a simulated 6-miner fleet.

## Configuration (`/etc/nodeos/config.json`)

| Key | Default | Meaning |
|---|---|---|
| `listen` | `:80` (installed) / `:8080` (dev) | HTTP listen address |
| `data_dir` | `/var/lib/nodeos` | state.json location |
| `scan_cidr` | auto-detected /24 | subnet for discovery scans |
| `poll_interval_sec` | `10` | fleet polling cadence |
| `demo`, `demo_miners` | `false`, `6` | simulated fleet |
| `bitcoind.rpc_url` | `http://127.0.0.1:8332` | bitcoind JSON-RPC |
| `bitcoind.cookie_file` | `/var/lib/bitcoind/.cookie` | cookie auth (or set `rpc_user`/`rpc_pass`) |
| `pool.*` | Public Pool | initial stratum settings pushed via "Apply to fleet" |
| `alerts.temp_max_c` | `70` | over-temperature threshold |

## API

`GET /api/status` · `GET/POST /api/miners` · `DELETE /api/miners/{host}` ·
`POST /api/miners/{host}/restart` · `PATCH /api/miners/{host}` (tuning) ·
`GET/PUT /api/pool` · `POST /api/pool/apply` · `POST|GET /api/scan` ·
`GET /api/node` · `GET/POST /api/node/setup` (Core/Knots + prune) ·
`GET/PUT /api/work` · `POST /api/work/switch` ·
`GET /api/update` · `POST /api/update/apply` ·
`/api/auth/{state,setup,login,logout,password}` ·
`GET /api/alerts` · `GET /api/events` (SSE)

All endpoints except the auth ones require a session cookie (log in via the
web UI, or script it: `curl -c jar -X POST .../api/auth/login -d
'{"password":"…"}'`).

## Architecture

Single static Go binary (`nodeosd`), no external dependencies, stdlib only.
Web UI embedded in the binary. State = one JSON file; telemetry = in-memory
ring buffers (1 h). bitcoind runs as its own systemd service, supervised
config via the installer. Details and rationale: [PRODUCT-ANALYSIS.md](PRODUCT-ANALYSIS.md) §8–9.

```
cmd/nodeosd        entrypoint
internal/axeos     ESP-Miner/AxeOS REST client
internal/fleet     discovery, polling, history, pool apply
internal/node      bitcoind JSON-RPC status
internal/work      work engine: DATUM gateway supervision + fleet auto-switch
internal/auth      admin password (PBKDF2) + session cookies
internal/admin     bridge to the root helper (command-file queue)
internal/update    GitHub release check + staged self-update
internal/sim       simulated miners (demo mode)
internal/server    REST API + SSE + embedded UI
web/               dashboard (vanilla JS, no build step)
deploy/            installer, admin helper, ISO builder, Proxmox VM script
```

Privileged operations (installing/switching bitcoind, self-update) never run
inside nodeosd: it writes a command file to `/var/lib/nodeos/admin/`, a
root-owned systemd **path unit** picks it up and runs `nodeos-admin`, which
validates the arguments, executes, and reports back via log/marker files.
nodeosd keeps full systemd hardening (`NoNewPrivileges`, `ProtectSystem`).

## Security notes (prototype!)

- The web UI is password-protected (set on first visit) and served over
  HTTPS with a self-signed certificate (browser warns once). Keep the box on
  a trusted LAN/VLAN anyway; passkeys + WireGuard remote access are next.
- NodeOS holds no keys and no funds; payout addresses live on the pool/miner.
- Bitcoin Core/Knots and self-update downloads are SHA256-verified; GPG/
  release signature verification is still manual and on the roadmap.
- Self-update needs the GitHub repo (or at least its releases) to be public;
  while the repo is private the check reports "no releases found".

## Roadmap (next)

1. Verify the DATUM work engine + node switching on real hardware (Proxmox
   VM + Bitaxe fleet); wire `blocknotify` for instant new-block templates
   (polling fallback works today)
2. Passkeys; release signing for self-updates; optional device-CA install
   flow (no more browser warning)
3. Firmware updates with staged rollout (1 device → verify → fleet)
4. **Appliance image**: preinstalled Pi 5 / x86 image (Debian base, A/B
   partitions via RAUC, signed updates) — the Umbrel/StartOS-style install path
5. Alerts via push/Nostr/Telegram; energy automation via Home Assistant
6. Stratum V2 (SRI) as an alternative work-engine backend

## License

TBD — MIT or Apache-2.0 recommended (see PRODUCT-ANALYSIS.md §15). Decide
before accepting external contributions.
