# OpenSourceNode — Product Analysis & Technical Architecture

**Status:** Draft v1 · July 2026
**Author:** Lead Architecture (working document, no code yet)
**Working title:** OpenSourceNode / "OpenOS" — final product name TBD

---

## Executive Summary

Every existing "node in a box" product — Umbrel, Start9, myNode, Citadel — is a
variation of the same idea: a Linux box that runs Bitcoin Core plus an app store.
That category is crowded, commoditized, and increasingly drifting away from
Bitcoin (Umbrel now markets itself as a general home server; its OS is no longer
FOSS).

Meanwhile a second world has exploded that none of them serve: **open-source
mining**. Hundreds of thousands of Bitaxes, NerdAxes, NerdQAxes and refurbished
ASICs are running in homes, garages and small farms — managed one browser tab
at a time, with no fleet software, no node integration, no energy automation,
and no first-class solo-mining stack.

**The opportunity is not a better Umbrel. It is the layer nobody owns: the
operating system that unifies the node, the miners, and the energy around
them.** The wedge is one sentence:

> **Plug in your miners. They find your node. Your node is your pool.**

Zero-config miner discovery, a built-in self-hosted stratum/solo-mining engine
backed by your own node, DATUM/Ocean integration for pooled-but-sovereign
mining, fleet management from 1 to 1,000 devices, and heat/energy automation —
wrapped in an Apple-grade UI and licensed MIT.

Everything else in this document derives from that wedge.

---

# Part I — Product Analysis (First Task)

## 1. What problems do Umbrel, Start9, myNode and Citadel have?

### Umbrel
- **No longer open source.** umbrelOS moved to the PolyForm Noncommercial
  license (source-available, not FOSS). For the Bitcoin audience this is a real
  wound, and a standing invitation for a truly open competitor.
- **Strategic drift.** Umbrel now positions itself as a "personal home server"
  — Plex, Nextcloud, AI apps. Bitcoin is one app among many. Bitcoiners feel
  it; the focus is gone.
- **Docker-compose spaghetti.** Every app is its own container stack. On a
  4 GB Raspberry Pi, 5–6 apps exhaust the machine. Inter-app integration is
  brittle (apps talk to each other via hardcoded container hostnames).
- **Shallow control.** The GUI exposes only basic settings. Real configuration
  (bitcoin.conf, lnd.conf) means SSH and hand-editing files that the update
  system may overwrite.
- **App-store bottleneck.** Security patches to upstream apps wait for Umbrel
  to repackage them.
- **Fragile storage story.** SD-card corruption and USB-SSD dropouts still
  cause data loss on Pi deployments; no A/B updates, no real recovery path.
- **Zero mining story.** DATUM exists as a community app; there is no
  discovery, no fleet view, no tuning, no telemetry.

### Start9 (StartOS)
- **Right values, punishing execution.** MIT-licensed and sovereignty-focused —
  but StartOS 0.4.0 is a ground-up rewrite that spent years in beta, with users
  reporting bricked upgrades and failing backups. The package SDK changed
  incompatibly; the service catalog perpetually lags.
- **Complexity as a feature.** Tor-first networking is philosophically pure and
  practically miserable for beginners (slow, flaky, confusing).
- **Small team, huge surface.** They build the OS, the SDK, the registry, the
  hardware, and now a router. Everything is 80 % finished.
- **No mining story at all** beyond a community DATUM package.

### myNode
- **One-man project with a paywall.** The "Premium" tier gates features of what
  is mostly upstream open-source software. Trust discount in the community.
- **Dated UX.** Server-rendered pages that feel like 2018; no real API, no
  mobile experience, no fleet or multi-device concept.
- **Bus factor of one.** Slow releases, no ecosystem, no third-party developer
  story.

### Citadel
- Effectively **stalled**. An Umbrel fork that never escaped its origin;
  development activity has dwindled to near zero. Its only lasting contribution
  is proof that a fork without a differentiated thesis dies.

### Category-wide failures (all four)
1. **Mining does not exist** in their world view — the single fastest-growing
   segment of home Bitcoin infrastructure.
2. **Single-box thinking.** No concept of multiple devices, sites, or fleets.
3. **The UI is a veneer**, not a system. Under the paint it's Docker + shell
   scripts; when something breaks, the abstraction evaporates and the beginner
   is stranded.
4. **Backups treat the node as a pet.** Losing the box means days of resync and
   often lost channel state. Nobody has made recovery boring.
5. **No real API/SDK.** None of them can be scripted, automated, or embedded in
   a bigger workflow without reverse-engineering.
6. **Update anxiety.** Updates are the most dangerous thing users do. No A/B
   partitions, no automatic rollback, no staged rollouts.

## 2. What are users currently missing?

- **A fleet view.** Anyone with more than one Bitaxe manages N browser tabs.
  The AxeOS HTTP API exists precisely so someone builds this — nobody has, at
  product quality.
- **Node-backed mining without a PhD.** Getting a Bitaxe to solo-mine against
  *your own* node today means: run bitcoind, compile/configure public-pool or a
  DATUM gateway, open ports, hand-type stratum URLs into each miner. Every step
  is a dropout point.
- **Energy & heat integration.** Miners are heaters and demand-response
  devices. Users bolt together Home Assistant, smart plugs and cron scripts.
  Nothing native exists.
- **Boring recovery.** "My SSD died" should be a 30-minute story (new disk,
  restore manifest, resync in background), not a lost weekend.
- **Remote access that isn't a science project.** Tor is slow, port-forwarding
  is dangerous, Tailscale is a third-party dependency. Users want a phone app
  that just shows their node and miners, securely.
- **Honesty about health.** Users discover problems (fan dying, pool
  unreachable, disk filling, thermal throttling) days late. No proactive
  diagnostics anywhere in the category.

## 3. What could become a unique selling point?

Ranked by defensibility:

1. **"Your node is your pool."** Integrated stratum engine + DATUM gateway +
   miner auto-discovery. One switch flips a fleet between self-hosted solo,
   Ocean/DATUM, and any external pool with failover. Nobody has this; it is
   squarely in OpenSourceMiners' credibility zone, and it is *hard to retrofit*
   onto Umbrel's architecture.
2. **Fleet-first design.** The data model starts at "N devices, M sites", not
   "one box". Scales from one Bitaxe to a container of S19s.
3. **Actually open source (MIT/Apache-2.0), forever.** Umbrel handed us this
   USP by relicensing. Make irrevocability a governance promise, not a vibe.
4. **Unbreakable updates.** Immutable OS, A/B partitions, automatic rollback.
   "You cannot brick this" is a feature normal users can feel.
5. **Energy intelligence.** Solar-surplus mining, price-aware curtailment
   (dynamic tariffs), heat-reuse scheduling — native, not duct-taped.

## 4. Which features would make users switch?

| Audience | Switch trigger |
|---|---|
| Bitaxe owners (largest, most underserved) | One dashboard for all miners, auto-discovery, firmware updates in bulk, best-share leaderboard, solo mining against own node in 2 clicks |
| Umbrel refugees | True FOSS license, migration importer (reads an Umbrel disk, adopts chain data + LND state), faster and lighter runtime |
| Start9 sympathizers | Same sovereignty values, working software, sane remote access |
| Small farms (10–500 ASICs) | Site/fleet management, alerting, per-device power/efficiency analytics, curtailment API — at $0 instead of enterprise-SaaS pricing |
| Developers | A real documented API + SDK; everything in the UI is available via gRPC/REST |

**Chain-data adoption is the killer migration feature:** attach an existing
drive with a synced chain (from Umbrel, Core on a laptop, anything), and the
system verifies and adopts it instead of resyncing for a week.

## 5. Which Bitcoin workflows are currently painful?

- Initial block download (days, opaque, kills first impressions).
- Pointing a miner at your own node (see above — the flagship pain).
- Lightning channel backup/restore (still terrifying for non-experts).
- Watching miners: no alerts when a device drops, overheats, or a pool dies.
- Updating firmware on many miners (one device at a time, by hand).
- Moving a node to new hardware.
- Verifying that what you run is what was released (reproducibility, signatures).
- Explaining any of this to a normal person.

## 6. Revolutionary (not incremental) ideas

- **The Pool Switchboard.** Treat pools like WiFi networks: a picker with
  live latency/fee/health per pool, per-miner or per-group assignment,
  automatic failover chains (Own node → Ocean/DATUM → public-pool). Includes
  a *lottery scheduler*: "mine solo for 1 hour a day, pooled otherwise."
- **Energy Router.** Miners as controllable loads: integrate dynamic
  electricity prices, PV surplus (via MQTT/Home Assistant/Shelly), and thermal
  targets ("keep the office at 21 °C"). The OS decides when and how hard to
  mine. This makes home mining *economically rational* in Europe.
- **Boring Recovery.** A signed, encrypted "identity capsule" (config, wallet
  descriptors, channel backups, miner inventory — a few MB) synced to a phone
  and/or user-chosen targets. New hardware + capsule = full restore; chain data
  is treated as cache, re-adoptable or re-syncable.
- **Fleet Mesh.** Multiple OpenSourceNode boxes (home + parents' house +
  workshop) form one logical fleet with E2E-encrypted sync — one app shows all
  sites. This is "multi-node cluster" done for humans, not Kubernetes.
- **Diagnostics engine, not AI chatbot.** A rule-based health system with
  causal explanations ("Miner 'garage-3' is throttling: intake temp 41 °C,
  fan RPM 40 % below fleet median → likely dust") and one-tap remediations.
  An optional local LLM can *narrate* diagnoses; it must never be the
  diagnostic mechanism. (See "What not to build.")

### What we should NOT build (challenged assumptions)

- **A general-purpose app store.** That is Umbrel's trap: 100 mediocre apps,
  infinite maintenance, diluted identity. We ship a *curated* Bitcoin-only
  catalog (~15 first-party-quality services) plus an open plugin SDK so the
  long tail lives in third-party repos at their maintainers' risk.
- **Our own mining pool (hosted).** Custody of payouts = regulatory and trust
  nightmare. We integrate pools; we never operate one.
- **An "AI-first" assistant as headline feature.** LLMs guessing at node
  problems erode exactly the trust this product must earn. Deterministic
  diagnostics first; LLM as optional presentation layer.
- **A Linux distribution from scratch.** Base on Debian stable with an
  immutable image pipeline (below). Kernel and driver maintenance would
  consume the whole team.
- **Tor-first networking.** Offer Tor; default to modern encrypted tunnels.
  Start9 proved purity loses to usability.
- **Our own hardware, initially.** Partner with existing vendors (Bitaxe
  ecosystem shops) for certified bundles; revisit first-party hardware after
  product-market fit.
- **Altcoin anything. Ever.** Bitcoin-only is a filter, a brand, and a
  security-surface reduction all at once.

---

# Part II — Deliverables

## 1. Vision

**Every satoshi of home and small-scale Bitcoin infrastructure — nodes,
miners, and the energy that feeds them — runs on one open operating system
that a beginner can install in ten minutes and a farm can script against.**

"If Apple designed a Bitcoin operating system": opinionated defaults, zero
jargon on the surface, immense depth one layer down, and hardware/software
that feel like one product. Unlike Apple: MIT-licensed, self-hosted,
verifiable, forever.

## 2. Product Strategy

1. **Win the miners first.** The Bitaxe/open-ASIC community is underserved,
   passionate, evangelical, and aligned with OpenSourceMiners. A superb fleet
   + solo-mining experience creates word-of-mouth no node-only feature can.
2. **The node comes free with the miner story.** To solo-mine sovereignly you
   need a node — so the product makes running one effortless and *purposeful*.
   ("Your node built this block template" is the best node-marketing ever.)
3. **Expand to services.** Once the box is trusted (node + mining), layer on
   Electrum server, mempool explorer, Lightning, BTCPay — curated, first-party
   quality.
4. **Then scale up-market.** The same fleet model, priced/support-tiered for
   small farms and hosting operators (enterprise features, not enterprise
   lock-in).
5. **Community as moat.** Upstream contributions to ESP-Miner/AxeOS, DATUM,
   public-pool; become the reference platform the OSMU ecosystem recommends by
   default.

## 3. Market Analysis

- **Node-in-a-box:** O(100k) active installs across Umbrel/Start9/myNode/
  RaspiBlitz. Growing slowly; commoditized; differentiation now happens on
  license and reliability, not features.
- **Open-source mining:** the breakout segment. Bitaxe-class devices have
  shipped in the hundreds of thousands; ESP-Miner v2.14 (June 2026) added
  native Stratum V2; an ecosystem of vendors (Solo Satoshi, D-Central, PCBWay
  clones) ships devices with *no fleet software*. NerdQAxe++ and multi-chip
  boards push home hashrate into TH/s territory where management actually
  matters.
- **Small farms:** refurbished S19s at low power prices, heat-reuse startups,
  container operators. Tooling today: Braiins/Vnish firmware + spreadsheets, or
  $/miner/month SaaS (Foreman, HiveOS ASIC) that is closed and altcoin-tainted.
- **Macro tailwinds:** post-2024-halving margin pressure → efficiency tooling
  demand; Stratum V2 + DATUM normalize sovereign template construction;
  European energy prices make smart curtailment genuinely valuable; hardware
  (Pi 5 / N100 mini-PCs) makes capable home servers cheap.
- **TAM honesty:** this is a passion-market beachhead (~low millions of
  potential users), not a unicorn SaaS market. Strategy and monetization are
  sized accordingly (see §15).

## 4. Competitor Analysis

| | License | Focus | Mining | Fleet | API/SDK | Updates | Verdict |
|---|---|---|---|---|---|---|---|
| **Umbrel** | PolyForm NC (not FOSS) | drifting to general home server | app-only (DATUM community app) | ✗ | ✗ (private) | in-place, no rollback | Polished veneer, wrong license, wrong direction |
| **Start9** | MIT | sovereignty server | ✗ | ✗ | SDK in flux (0.4.0 rewrite) | improving, rocky | Right values, unreliable execution |
| **myNode** | mixed + paywall | BTC node | ✗ | ✗ | ✗ | scripted in-place | Aging one-man product |
| **Citadel** | FOSS | Umbrel fork | ✗ | ✗ | ✗ | — | Effectively dead |
| **RaspiBlitz** | MIT | power users | ✗ | ✗ | scripts | manual | Great lab, not a product |
| **Braiins OS / Vnish** | partly closed | ASIC firmware | ✓ (device-level) | thin | device API | ✓ | Firmware, not an OS; complement not competitor |
| **HiveOS / Foreman** | closed SaaS | farm mgmt | ✓ | ✓ | ✓ | ✓ | Fleet done, but closed, paid, altcoin-adjacent, cloud-dependent |
| **AxeOS/ESP-Miner** | GPL | single-device firmware | ✓ | API only | ✓ | ✓ | Our substrate and ally, not competitor |

**The empty quadrant: open-source × fleet × node-integrated.** HiveOS proves
fleet demand; Umbrel proves UX demand; nobody combines them under a free
license with a node at the center.

## 5. Unique Selling Proposition

> **The only operating system where your miners and your node are one system.**

Concretely, five promises on the homepage:

1. **Plug in a miner, see it in 10 seconds.** (Auto-discovery, zero config.)
2. **Solo-mine against your own node in 2 clicks.** (Built-in stratum engine +
   DATUM.)
3. **Manage 1 or 1,000 miners the same way.** (Fleet-first, sites, groups,
   bulk firmware.)
4. **You cannot brick it.** (A/B updates, automatic rollback, boring recovery.)
5. **MIT licensed. Bitcoin only. Forever.** (Governance-backed.)

## 6. Target Customers

1. **"Tab-fatigued Tinkerer" (beachhead).** Owns 2–20 Bitaxe-class miners and
   maybe an Umbrel. Technical enough to flash firmware, tired of glue scripts.
   Buys in week one.
2. **"Sovereign Beginner."** First node, maybe first miner as a gadget/heater.
   Needs the 10-minute install and the diagnostics engine. Largest long-term
   pool.
3. **"Garage Farm Operator."** 20–500 ASICs, spreadsheet-managed, margin-
   sensitive. Needs alerting, efficiency analytics, curtailment. Will pay for
   support/enterprise features.
4. **"Builder."** Develops Bitcoin apps/plugins; needs the SDK, stable APIs,
   CI images. Small in number, decisive for ecosystem gravity.
5. Explicit non-targets (v1): industrial farms >1 MW, hosting resellers,
   anyone wanting altcoins or a media server.

## 7. Feature Priorities

**P0 — the wedge (MVP, see §14)**
- Immutable OS image (x86-64 + Pi 5), 10-minute guided install, A/B updates
- Bitcoin Core managed service; chain-data adoption/import; assumeutxo-
  accelerated sync
- Miner auto-discovery (mDNS + subnet scan; AxeOS/ESP-Miner API, cgminer API)
- Fleet dashboard: live hashrate, temps, power, best shares; groups & bulk
  actions; bulk AxeOS firmware update
- **Strathub**: built-in stratum server (solo against own node), pool
  switchboard with failover chains, DATUM gateway integration
- Alerting (push via app/ntfy/email): miner down, temp, node issues, disk
- Identity-capsule backup & restore

**P1 — trust & expansion**
- Electrum server (electrs/Fulcrum), mempool explorer, wallet pairing flows
- Mobile app (monitor + alerts + remote access via built-in WireGuard/relay)
- Energy Router v1: schedules, dynamic-tariff API, MQTT/Home Assistant, smart-
  plug curtailment; thermal targets
- Plugin SDK + curated catalog (BTCPay, LNbits, Nostr relay, join-market etc.)
- Lightning (LND or CLN, pick one first) with automated channel-state backup

**P2 — scale**
- Fleet Mesh (multi-box, multi-site, one pane of glass, E2E-encrypted)
- Advanced farm analytics (efficiency curves, J/TH per device, ROI, heat maps)
- Stratum V2 end-to-end (miners already support it since ESP-Miner 2.14)
- High availability for critical services; enterprise RBAC/audit log
- Marketplace for third-party plugin repos with signing & review tiers

**Deliberately later/never:** general app store (never), hosted pool (never),
VM hosting (never), altcoins (never), AI assistant beyond diagnostics
narration (later, optional, local-only).

## 8. System Architecture

```
┌─────────────────────────── Devices ────────────────────────────┐
│ Bitaxe / NerdQAxe (AxeOS API, Stratum)   S19s (cgminer API)    │
│ Smart plugs / PV inverter (MQTT)         Phone app             │
└──────────────┬─────────────────────────────────┬───────────────┘
               │ LAN (mDNS, HTTP, Stratum)       │ WireGuard / relay
┌──────────────▼─────────────────────────────────▼───────────────┐
│                     opend  (single Go daemon)                  │
│  ┌───────────┐ ┌───────────┐ ┌──────────┐ ┌────────────────┐   │
│  │ Discovery │ │ Fleet Mgr │ │ Strathub │ │ Service Superv.│   │
│  └───────────┘ └───────────┘ └──────────┘ └────────────────┘   │
│  ┌───────────┐ ┌───────────┐ ┌──────────┐ ┌────────────────┐   │
│  │ Energy    │ │ Diagnostics│ │ Backup  │ │ Plugin Host    │   │
│  └───────────┘ └───────────┘ └──────────┘ └────────────────┘   │
│        Event bus (NATS-style, in-proc) · SQLite · TSDB         │
│        One public API: gRPC + REST gateway + WS events         │
└──────┬──────────────┬───────────────┬──────────────┬───────────┘
       │systemd units │               │OCI (podman)  │
┌──────▼─────┐ ┌──────▼─────┐ ┌───────▼────┐ ┌───────▼────────┐
│ bitcoind   │ │ electrs    │ │ DATUM gw   │ │ 3rd-party      │
│ (native)   │ │ (native)   │ │ (native)   │ │ plugins (sand- │
└────────────┘ └────────────┘ └────────────┘ │ boxed containers)│
                                             └────────────────┘
┌────────────────────────────────────────────────────────────────┐
│ Immutable base OS: Debian-stable image (mkosi), A/B partitions │
│ RAUC updates + auto-rollback · data partition (LUKS optional)  │
└────────────────────────────────────────────────────────────────┘
```

**Key decisions (each simpler than the incumbent's choice):**

- **One supervisor daemon (`opend`), declarative state.** The user's intent
  (services enabled, pool chains, groups, schedules) lives in one versioned
  SQLite-backed config; `opend` reconciles reality against it. No
  docker-compose file soup; recovery = replay the declaration.
- **First-party services run native (systemd), not in containers.** bitcoind,
  electrs, DATUM, strathub are single static binaries; containers add nothing
  but RAM overhead here. Containers (rootless podman) are reserved for
  *third-party plugins*, where isolation genuinely matters. This alone halves
  Umbrel's footprint.
- **Strathub is a first-class subsystem**, not an app: Go stratum server
  deriving work from the local bitcoind (`getblocktemplate`) for pure solo
  mode, plus proxy/failover mode multiplexing upstream pools (Ocean/DATUM,
  public-pool, Braiins…), plus per-device routing. Stratum V1 today, V2 as it
  matures on pool side.
- **Fleet model in the core schema from day one:** `site → device-group →
  device`, even when there's exactly one of each. Fleet Mesh (P2) syncs this
  tree between boxes; it is not a bolt-on.
- **Everything through one API.** The web UI is a client of the same
  gRPC/REST/WebSocket API third parties get. No private endpoints — this keeps
  us honest and makes the SDK real.
- **Telemetry:** embedded Prometheus-compatible TSDB with downsampling
  (miner samples every 5 s, retained tiered); `/metrics` exposed for users who
  bring Grafana.

## 9. Technology Stack

| Layer | Choice | Why (and why not the alternative) |
|---|---|---|
| Base OS | Debian stable, image-built with **mkosi**, immutable, A/B via **RAUC** | Boring, huge contributor pool, systemd-native. NixOS is more elegant but shrinks the contributor base and complicates plugin authorship; buildroot too spartan. |
| Core daemon | **Go** | Single static binary, superb concurrency for stratum/telemetry, big Bitcoin ecosystem (lnd, btcd wire types), fastest contributor onboarding. Rust reserved for hot paths if profiling ever demands it — don't pay the velocity tax up front. |
| Node services | bitcoind, electrs (Fulcrum optional later), DATUM gateway, LND *or* CLN (one, later) | Upstream, unpatched, reproducible builds verified against upstream signatures. |
| Stratum engine | own Go implementation (`strathub`) | public-pool is TypeScript/Node — fine as reference, wrong runtime for an appliance core. A Go SV1 server + GBT integration is a small, well-understood component. |
| DB / state | SQLite (WAL) + embedded TSDB | One file to back up; no Postgres to babysit. |
| API | gRPC + grpc-gateway REST + WebSocket events; OpenAPI published | One schema, three access styles. |
| Web UI | **SvelteKit** SPA served by opend | Small bundle, fast on a Pi, pleasant contributor DX. React acceptable fallback if hiring dictates. |
| Mobile | React Native (or Flutter) thin client on the same API; push via self-hostable ntfy + optional hosted relay | The app is a viewport, not a second brain. |
| Remote access | Built-in WireGuard; optional zero-knowledge rendezvous relay (our hosted service, self-hostable); Tor as opt-in | Fast by default, sovereign by option. |
| Plugin runtime | rootless podman + declarative manifest | See §11. |
| Updates | RAUC bundles signed via **TUF**-style root; staged rollout channels (stable/beta/edge) | Auto-rollback on failed health check after boot. |

## 10. Security Architecture

- **Immutable root, verified boot chain** where hardware allows (x86 Secure
  Boot with our shim; Pi as best-effort). Rootfs is read-only, dm-verity
  hashed; only the data partition is writable (optionally LUKS-encrypted, key
  in TPM where present or passphrase/keyfile).
- **Signed everything.** OS bundles, first-party binaries, and plugin
  manifests are signed; update metadata follows TUF to survive repo
  compromise. Reproducible builds published with third-party attestation
  invited.
- **Least-privilege by construction.** Every first-party service runs as its
  own user with systemd hardening (seccomp, no-new-privileges, private tmp,
  RO filesystem). Plugins get *capability scopes* (see §11) instead of raw
  socket access — a plugin never talks to bitcoind's RPC directly, only to
  opend's mediated, scope-checked API.
- **LAN pairing model.** First device pairing happens on-LAN via QR/short
  code (SPAKE2); remote access only via keys established during pairing. No
  passwords over WAN, WebAuthn/passkeys for browser login, optional TOTP.
- **Network posture:** no inbound ports required (relay/WireGuard are
  outbound); stratum exposed on LAN only unless explicitly opened; per-plugin
  network policies (default: no egress).
- **Secrets:** single encrypted keystore (age-based) inside the identity
  capsule; wallet seeds never touch plugins; hardware-wallet-first flows for
  anything holding value.
- **Threat model documented publicly** (evil housemate, stolen disk, malicious
  plugin, compromised update server, hostile network) with explicit
  mitigations per threat. This document *is* marketing to our audience.

## 11. Plugin System

- **Manifest-first.** A plugin = OCI image + `plugin.yaml`: metadata,
  version constraints, requested scopes (`bitcoin.rpc.readonly`,
  `fleet.read`, `events.subscribe:miner.*`, `storage:2GB`,
  `net.egress:host=api.example.com`), UI entry, health check, backup hooks.
- **Scopes are the whole trick.** Plugins call opend's API with a scoped
  token; opend proxies to bitcoind/LND/fleet as allowed. Users see a
  permissions sheet at install ("This plugin can read your node's chain state.
  It cannot spend funds or see miners."). Apple-grade legibility, capability-
  based security.
- **UI embedding:** plugin web UIs render in sandboxed iframes with a
  postMessage bridge for theme/auth/navigation — no style leakage, no DOM
  access, works with any framework the plugin author likes.
- **Distribution tiers:** (1) *Core catalog* — first-party maintained, ~15
  services, our quality bar; (2) *Verified* — third-party, manifest-reviewed
  and signed; (3) *Community repos* — user-added URLs, loud warnings. We never
  become the app-store janitor for tier 3.
- **SDK:** `opensdk` CLI scaffolds a plugin, runs a local dev harness
  (mock opend with fake fleet/regtest node), validates manifests, and builds
  reproducibly. CI templates included. Target: first plugin in under an hour.

## 12. UI/UX Concept

- **One screen to rule them all: "Home".** Node health, fleet hashrate,
  best share ever (gamification the mining community already loves), energy
  cost today, alerts. Glanceable on phone and 4k monitor alike.
- **Progressive disclosure, three layers:** (1) glanceable status with plain-
  language states ("Catching up — about 14 hours left"), (2) control surfaces
  (pool switchboard as a drag-to-reorder failover list; tuning as three
  presets — Quiet / Balanced / Max — with a "custom" door), (3) raw depth
  (full configs, logs, metrics, terminal) — present, never required.
- **The Pool Switchboard** is the signature interaction: cards for each pool
  (latency, fee, last block, status), drag miners/groups onto pools, reorder
  failover. It should demo like magic and screenshot like a poster.
- **Setup as theater:** discovery animation ("Found: Bitaxe Gamma · 1.2 TH/s")
  during onboarding turns config into delight; IBD progress framed honestly
  with what already works meanwhile.
- **Copy rules:** no jargon on layer 1 (never "IBD", "GBT", "vardiff"); every
  error names the next action; German + English at launch (community
  languages via Weblate).
- **Design system:** dark-first (it lives in garages and on OLED phones),
  high-contrast, WCAG AA, real data-viz discipline for hashrate/temp charts.
  No dashboard-clipart, no fake precision.

## 13. Development Roadmap

**Phase 0 — Foundation (months 0–3)** · team ~4
Image pipeline (mkosi+RAUC, x86-64 + Pi 5), opend skeleton (API, event bus,
service supervision), bitcoind managed + chain adoption, minimal web UI,
signed A/B updates working end-to-end. *Exit: a stranger installs in 10 min
and survives a power-pull during update.*

**Phase 1 — The Wedge (months 3–7)**
Discovery, fleet dashboard, bulk firmware, strathub solo mode, pool
switchboard + failover, DATUM integration, alerting, identity-capsule backup.
**Public beta at month ~6** aimed at the Bitaxe community; ship with 3 vendor
partners preinstalling it.

**Phase 2 — Trust & Depth (months 7–14)**
Electrum server + mempool + wallet pairing, mobile app + relay, Energy Router
v1, Plugin SDK + first 5 catalog plugins, Lightning (one implementation),
migration importer from Umbrel. **1.0 at month ~12–14** — the "you cannot
brick it" release.

**Phase 3 — Scale (months 14–24)**
Fleet Mesh multi-site, farm analytics, Stratum V2 E2E, RBAC/audit, verified
plugin tier + marketplace, enterprise support offering.

Cadence: 6-week release trains on `stable`, weekly `beta`, nightly `edge`;
public roadmap; every release with reproducible-build hashes.

## 14. MVP Definition

**One sentence:** *A signed, unbrickable OS image for Pi 5/x86 that runs a
full Bitcoin node, auto-discovers every open-source miner on the LAN, shows
them on one dashboard, and lets the user point the whole fleet at their own
node — solo or via DATUM — in two clicks.*

**In:** guided install; bitcoind + chain adoption; discovery (AxeOS/ESP-Miner
+ cgminer API); fleet dashboard (hashrate/temp/power/best-share, groups, bulk
restart + bulk AxeOS firmware update); strathub solo mode; pool switchboard
with one failover chain; DATUM gateway; push/ntfy alerts (device down,
overheat, node stalled, disk); identity-capsule backup/restore; A/B updates;
web UI (DE/EN).

**Out (explicitly):** Lightning, Electrum server, mobile app (web is
responsive), plugins/SDK, Energy Router, Fleet Mesh, Tor, marketplace,
migration importer, any AI.

**MVP success criteria:** 1,000 weekly-active installs within 90 days of
public beta; median install-to-first-share < 30 min on synced node; < 1 %
failed-update rate; NPS from Bitaxe community forums that we'd frame.

## 15. Monetization Strategy

Principle: **the OS is free and MIT forever; money comes from convenience and
scale, never from gating sovereignty.** (Umbrel's relicensing shows what
poisoning the well costs.)

1. **Hosted relay & push subscription** (~€3–5/mo): zero-knowledge remote
   access + push notifications without self-hosting a relay. Fully optional,
   self-hostable alternative documented. (Tailscale-style economics.)
2. **Certified hardware bundles** with ecosystem vendors (rev-share): "works
   out of the box" boxes and miner bundles under partner brands.
3. **Farm tier** (per-site flat fee, not per-miner): SSO/RBAC, audit log,
   long-retention analytics, SLA support. Features that only orgs need —
   individuals never hit the paywall.
4. **Support contracts & integration services** for vendors/operators.
5. **Grants/sponsorship** (OpenSats, Human Rights Foundation, OSMU backers,
   hardware vendors) for protocol-level work (Stratum V2, DATUM hardening).
6. Non-goals: ads, telemetry sales, token/points anything, custody, pool fees.

## 16. Risks

| Risk | Likelihood | Mitigation |
|---|---|---|
| Umbrel/Start9 bolt on mining after our beta | Medium | Fleet-first data model & strathub are architectural, hard to retrofit; move fast in the Bitaxe community; license moat vs Umbrel |
| Bitaxe org / OSMU ships its own fleet tool | Medium | Don't compete — co-opt: upstream PRs, make them design partners, aim to be the *official* recommendation |
| Solo-mining hype cools (variance disappointment) | Medium | Energy Router + DATUM make mining rational beyond lottery dreams; node/services keep the box valuable |
| Team burns out on OS maintenance (drivers, kernels) | High if careless | Debian-stable base, two hardware targets only at start, certified-hardware program instead of "runs on anything" |
| Update system bricks devices early → trust never recovers | Low-Med | A/B + auto-rollback before *any* public release; staged rollouts; chaos testing (power-pull CI rig) |
| Funding gap before Farm tier revenue | Medium | Grants + vendor partnerships early; keep core team ≤6 through Phase 2 |
| Regulatory noise around mining (EU energy reporting) | Low-Med | Energy Router is the answer, not the victim: reporting/curtailment features turn compliance into a feature |
| Key-person risk (small team, deep systems knowledge) | Medium | Docs-as-code, reproducible builds, public architecture decision records from day one |
| Security incident in a plugin tars the platform | Medium | Scope model + tiered catalog + default-deny egress; incident-response playbook published in advance |

## 17. Long-term Vision (5 Years)

**2031: OpenSourceNode is the default substrate for sovereign Bitcoin
infrastructure — the thing you flash before you plug in anything Bitcoin.**

- **Every open-source miner ships with it recommended on the box.** The
  certification mark ("Runs on OpenSourceNode") is what "Works with HomeKit"
  is for smart homes.
- **The home energy loop is closed:** PV-aware, tariff-aware, thermally
  integrated mining makes the space heater that pays rent a mainstream
  European product category — and our Energy Router is its brain.
- **Fleet Mesh grows up:** families and communities run multi-site
  infrastructure — node redundancy, watchtowers for each other's Lightning
  channels, shared block-template sanity checking — without any cloud.
- **Stratum V2 + DATUM everywhere** means template construction is genuinely
  decentralized; tens of thousands of our boxes each build their own blocks.
  That is a measurable contribution to Bitcoin's censorship resistance — the
  mission metric that matters more than MAUs.
- **The plugin ecosystem is the Bitcoin service layer:** BTCPay for the shop,
  Nostr relay for the community, Fedimint/ecash gateways, inheritance-vault
  tooling — installed by normal people because the permission sheet made it
  legible.
- **Possible endgames:** first-party reference hardware (once volume
  justifies it); a foundation holding the trademark + signing root with
  irrevocable-license bylaws; the farm tier quietly funding everything.

The five-year test is one sentence: *when someone says "I'm getting into
Bitcoin, what do I buy?", the answer ends with "…and it all runs on
OpenSourceNode."*

---

## Appendix A — Open questions for the team

1. Product name & trademark check ("OpenSourceNode" is descriptive but long —
   candidates worth exploring; keep "powered by OpenSourceMiners" badge).
2. LND vs CLN as the single first Lightning implementation (P1 decision).
3. Hosted relay jurisdiction & zero-knowledge design review.
4. Hardware targets beyond Pi 5 / x86-64 mini-PC (RISC-V worth watching, not
   worth blocking on).
5. Governance: when to create the foundation vs company-owned start.

## Appendix B — Sources consulted (July 2026)

- Umbrel licensing & limitations: [blockdyor Umbrel review](https://blockdyor.com/umbrel-review/), [Knowing Bitcoin review](https://knowingbitcoin.com/umbrel-review-2026/), [Umbrel vs Start9](https://thebitcoinhole.com/vs/umbrel-vs-start9)
- StartOS 0.4.0 rewrite/beta state: [Start9 community](https://community.start9.com/t/start-os-0-4-0-timeline/4106), [start-os GitHub](https://github.com/Start9Labs/start-os)
- Bitaxe/AxeOS ecosystem & v2.14 (Stratum V2): [ESP-Miner releases](https://github.com/bitaxeorg/ESP-Miner/releases), [AxeOS guide](https://www.solosatoshi.com/axeos-guide/), [AxeOS API](https://d-central.tech/axeos-advanced-configuration-api-overclocking-network-setup/)
- DATUM/Ocean & solo pools: [DATUM protocol](https://d-central.tech/datum-protocol/), [Ocean guide](https://d-central.tech/ocean-mining-pool-guide/), [Public Pool review](https://blockdyor.com/public-pool-review/), [DATUM on Start9](https://bitronics.store/blogs/knowledge-base/set-up-your-own-datum-solo-mining-pool-on-a-start9-node)
- Stratum V1/V2 landscape: [Stratum matrix](https://d-central.tech/data/stratum-protocol-matrix/)
