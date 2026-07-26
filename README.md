# NodeOS

**The operating layer for people who *run* Bitcoin — nodes, solo miners, home
mining fleets.** One daemon, one UI: plug in a Bitaxe, it appears
automatically and mines against your own node.

> Status: **prototype v0.1.0** — fleet management, discovery, pool control and
> node monitoring work today. Work-engine supervision (DATUM/Stratum V2),
> auth, updates and the flashable appliance image are next.
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
- **Honest solo odds** — expected time to block and daily chance computed from
  *your* fleet hashrate and *real* network difficulty
- **Alerts** — miner offline/online, over-temperature, and the
  share-≥-network-difficulty "possible block!" event
- **Demo mode** — simulated fleet (real HTTP devices in-process) to try the UI
  without hardware: `nodeosd --demo`
- **REST API + SSE** — everything the UI does is `/api/*`; automation-friendly

## Quickstart

### A. Proxmox (recommended for testing)

On the Proxmox host:

```bash
bash deploy/proxmox-create-vm.sh --bridge vmbr0 --storage local-lvm
```

This creates a Debian 12 cloud-init VM. Then, from your workstation
(repo root, after `./build.sh` or `.\build.ps1`):

```bash
scp dist/nodeosd-linux-amd64 deploy/install.sh nodeos@<VM-IP>:/tmp/
ssh nodeos@<VM-IP> "sudo bash /tmp/install.sh --binary /tmp/nodeosd-linux-amd64 --with-bitcoind --prune 20000"
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

### C. Developer / demo mode (any OS)

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
`GET /api/node` · `GET /api/alerts` · `GET /api/events` (SSE)

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
internal/sim       simulated miners (demo mode)
internal/server    REST API + SSE + embedded UI
web/               dashboard (vanilla JS, no build step)
deploy/            installer, Proxmox VM script
```

## Security notes (prototype!)

- **No authentication yet.** Run on a trusted LAN/VLAN only, never expose to
  the internet. Auth (passkeys) + WireGuard remote access are next.
- NodeOS holds no keys and no funds; payout addresses live on the pool/miner.
- Installer verifies Bitcoin Core SHA256 checksums; GPG signature verification
  is still manual.

## Roadmap (next)

1. **Work engine**: supervise DATUM Gateway / SRI Stratum-V2 endpoint so
   miners solo-mine against the local node with pool failover — the
   auto-switch-when-synced magic moment
2. Auth (passkeys) + HTTPS
3. Firmware updates with staged rollout (1 device → verify → fleet)
4. **Appliance image**: preinstalled Pi 5 / x86 image (Debian base, A/B
   partitions via RAUC, signed updates) — the Umbrel/StartOS-style install path
5. Alerts via push/Nostr/Telegram; energy automation via Home Assistant

## License

TBD — MIT or Apache-2.0 recommended (see PRODUCT-ANALYSIS.md §15). Decide
before accepting external contributions.
