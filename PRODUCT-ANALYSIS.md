# NodeOS — Product & Technical Architecture Analysis

*Working title: NodeOS (project "OpenSourceNode"). Status: pre-code product analysis. Date: 2026-07-25.*

---

## 0. Positions Taken (Assumptions Challenged)

The brief asks to challenge every assumption. These are the calls this document makes, up front:

1. **Do not build an operating system.** "Bitcoin OS" is the marketing name, not the engineering plan. Start9 spent years building a custom Linux distro and package manager and paid for it in velocity; Umbrel is "just" Docker on Debian and shipped circles around everyone. The product is a **control plane**: one daemon + one UI that runs on any Linux, on a Pi image, or in Docker — including *alongside* an existing Umbrel or Start9 box. That is also the adoption strategy: nobody reformats their node on day one.

2. **Mining-first, node-second.** Every competitor is node-first with mining as an afterthought. The unowned, fast-growing, passionately evangelical niche is **open-source ASIC fleet management**. The node is the foundation (solo mining against *your own* templates is the whole point), but the wedge into the market is the miner, not the node.

3. **No app store of everything.** Umbrel's moat is breadth (media servers, AI, smart home). Competing there is unwinnable and invites shitcoin pressure. NodeOS ships a small set of first-party, Bitcoin-only services and a *typed* extension system (device drivers, pool adapters, automations) — not a general app platform.

4. **Lightning is an app, not the core.** LSPs and mobile wallets have absorbed the "casual Lightning" use case. Routing-node operators are a tiny, well-served niche (Umbrel/Start9/RaspiBlitz + ThunderHub/RTL). Ship LN support later as an optional service; do not build the product around it.

5. **No "Bitcoin AI assistant" as a headline feature.** The useful 10% of that idea is **explainable diagnostics**: every alert and every metric can answer "what does this mean and what should I do?" in plain language. That can be rule-based first and model-assisted later. A chatbot on the dashboard is a demo, not a product.

6. **Hashrate virtualization is research, not roadmap.** Splitting a fleet across solo/pool/syndicate targets is genuinely novel and eventually valuable, but non-custodial payout coordination is unsolved. Flag it as an R&D track; do not promise it.

7. **Honesty is a feature.** Solo mining is a lottery. Every competitor and pool dashboard buries this. A product that says "at 5 TH/s your expected time to a block is ~34 years — here is exactly what that means" earns the trust that everything else is built on.

---

## 1. Vision

> **Every watt you own, working for Bitcoin — through one screen you actually trust.**

NodeOS is the operating layer for people who *run* Bitcoin rather than merely use it: node runners, solo miners, home miners heating their houses, and small farms. One system scales from a single Bitaxe on a bookshelf to a node plus two hundred ASICs in a container — same software, same UI, same guarantees.

"If Apple designed it" means, concretely:

- **Fewer decisions, better defaults.** Plug in a miner, it appears, it mines against your own node. No stratum URLs to copy, no ports to memorize.
- **It never breaks.** Atomic A/B updates with automatic rollback. A failed update is a non-event.
- **Honest numbers.** Real odds, real efficiency, real heat output. No vanity metrics.

Open source. Bitcoin only. No tokens, no shitcoins, no custody.

---

## 2. Product Strategy

### The wedge: own the open-source mining fleet

The open-source ASIC ecosystem (Bitaxe, NerdAxe, NerdQAxe, and successors) has shipped hundreds of thousands of devices, and its management story in 2026 is: *open a separate AxeOS browser tab per device, or write your own scripts against the REST API*. People run 5–20+ devices this way. There is no fleet product. Meanwhile the sovereignty stack matured underneath them — DATUM for own-template solo mining, ESP-Miner v2.14+ with native Stratum V2 — but wiring it together is a weekend of config-file archaeology (Start9's forum is full of "Bitaxe keeps falling back to the wrong pool" threads).

**Land:** the Bitaxe owner. Auto-discovery → fleet dashboard → one-click solo mining against their own node, in under a minute.

**Expand:** remote access and alerting → energy/heat automation → multi-site fleets and small farms → node services (Electrum server, mempool explorer) for the same install base.

**Distribution is the strategy:** with OpenSourceMiners backing, the goal is that every open-source miner vendor *ships* NodeOS as the recommended companion — the way every Bitaxe listing today says "point it at Public Pool." Becoming the blessed default in the vendor ecosystem is worth more than any marketing.

**Run-alongside adoption:** because NodeOS is a daemon, not a distro, an Umbrel owner installs it in Docker next to their existing node and points it at their existing `bitcoind`. Switching cost on day one: zero. The full image (Pi 5 / x86) is for new installs and dedicated boxes.

### What "winning" looks like

Not "Umbrel but Bitcoin-only" (that's myNode, and it's a one-person project with a dated UI). Winning = *the default control plane for self-sovereign hashrate*, measured in: devices under management, and % of managed hashrate mining on self-built templates.

---

## 3. Market Analysis

### Segments

| Segment | Size (order of magnitude) | Current tooling | Pain |
|---|---|---|---|
| Open-source ASIC owners | 100k+ devices shipped, growing fast | Per-device AxeOS tabs, DIY scripts, community dashboards | No fleet view, manual firmware, fragile solo-mining setup |
| Node runners | ~50–100k active sovereign-node installs (Umbrel/Start9/myNode/RaspiBlitz/DIY) | Mature | Mining is an afterthought; trust/verifiability gaps |
| Home miners / heat reuse | Growing niche (S9-as-heater, hydro units, Loki/Heatbit-style) | Almost nothing open | No tariff/solar automation, no thermostat integration |
| Small farms (10–500 ASICs) | Thousands of operations | Foreman (SaaS, per-miner fees), Braiins tooling (Antminer-centric), spreadsheets | Underserved between hobby and enterprise; SaaS fees; closed |
| Vendors / integrators (B2B) | Dozens (OSMU ecosystem, D-Central, Solo Satoshi, bitronics, …) | None to bundle | Want a hub product to sell alongside hardware |

### Tailwinds

- **Post-halving economics** push hobby mining toward heat-reuse framing ("free heat + lottery ticket"), which needs automation software.
- **Sovereignty narrative is now practical:** DATUM (own block templates) and Stratum V2 (job negotiation; ~25% of major pools have SV2-compatible infrastructure, seven majors representing ~75% of hashrate joined the SV2 working group in May 2026) make "home hashrate matters for decentralization" real instead of rhetorical.
- **Hardware costs falling**, open designs proliferating — the device zoo grows, which *increases* the value of one manager for all of them.

### Honest sizing

This is a niche. Tens of thousands of active installs within two years is success, not failure — because the niche is growing, evangelical, and has no incumbent. Demand is Bitcoin-price-cyclical; the project must be structured (small team, low burn, grant-friendly) to survive a bear market. See Risks.

---

## 4. Competitor Analysis

| | Umbrel | Start9 | myNode | Citadel | RaspiBlitz | Foreman/Braiins farm tools | AxeOS (ESP-Miner) |
|---|---|---|---|---|---|---|---|
| Focus | General home server | Sovereign self-hosting | Bitcoin node | — (abandoned) | Hacker node toolbox | Industrial farms | Single device |
| Mining support | Checkbox app | Community packages, fiddly | Minimal | — | Minimal | Antminer-centric / SaaS | Excellent, but one device at a time |
| Fleet concept | No | No | No | — | No | Yes ($/miner SaaS) | No |
| Update model | Best-effort | Verified, custom OS | Best-effort | — | Manual | n/a | Per-device manual |
| Openness | Source-available core, licensing criticized | Fully FOSS (MIT), principled | Partly paid/closed | — | FOSS | Closed/SaaS | FOSS |
| Weakness to exploit | Bitcoin is now a side feature; no verifiability; shitcoin-adjacent app store | Velocity tax of custom OS; UX friction; mining an afterthought | One-person bus factor, dated | Dead — its users need a home | Expert-only | Price + closed + overkill for <500 miners | Screams for a fleet layer above it |

**Key readings:**

- **Umbrel** won ease-of-use and breadth, and pivoted accordingly (media/AI/smart-home apps, Tailscale built in, $549 hardware). It will not out-deepen anyone on mining — depth contradicts its breadth strategy. Its Bitcoin-maximalist early adopters are exactly the users a Bitcoin-only OS can win back.
- **Start9** is right about verifiability and wrong about how much OS to build. Steal the principle (signed, reproducible, atomic) with boring off-the-shelf tech (RAUC A/B images, sigstore), not a bespoke package manager.
- **The real competitor is "nothing":** the Bitaxe owner's current stack is browser tabs + a Public Pool dashboard. The bar to clear is *that*, which is why time-to-value (60 seconds) is the metric that matters.

---

## 5. Unique Selling Proposition

**"Your miners and your node are one system."**

1. **Zero-config sovereign mining:** plug in any ESP-Miner-family device → auto-discovered → mining against *your* node's block templates (DATUM/SV2) with pool failover — one click, no stratum URLs.
2. **Fleet-native:** the dashboard is a fleet from day one; one device or two hundred, same UX. Bulk firmware updates, tuning profiles, thermal policies.
3. **Honest numbers:** expected-time-to-block, real efficiency (J/TH measured, not spec-sheet), heat output in watts, tariff-aware cost. Trust as differentiation.
4. **Never breaks:** A/B atomic updates + automatic rollback; signed, reproducible builds.
5. **Sovereign remote access:** no accounts, no cloud dependency — key-based access via embedded WireGuard or Nostr-keyed relay (self-hostable).
6. **Bitcoin only, forever:** structural, not cosmetic — there is no general app store to pollute.

---

## 6. Target Customers

In priority order:

1. **The Bitaxe collector** (2–10 devices, technical hobbyist): wants the lottery, the learning, the dashboard to show friends. First and loudest adopter; lives on OSMU Discord/Telegram and Twitter/Nostr.
2. **The heater** (1–5 larger units heating a space): wants silence rules, thermostat/solar/tariff automation, "how much did my heating cost net of sats" reporting.
3. **The small farmer** (10–500 mixed ASICs): wants uptime alerts, remote multi-site view, no per-miner SaaS fees. Pays for support/enterprise features.
4. **The node runner** (Umbrel/Start9 refugee or DIY): wants Bitcoin-only, verifiable software, and a reason to point hashrate at his own templates.
5. **The developer/vendor**: wants an API/SDK to build drivers, automations, and bundled products on.

Beginner ≠ target #1 at launch. The Apple-simple experience is built *for* the hobbyist first; true beginners arrive via vendor bundles in phase 3.

---

## 7. Feature Priorities

### P0 — MVP (defines the product)
- `bitcoind` lifecycle management (install, prune/full, **assumeutxo fast-sync**)
- Miner auto-discovery (mDNS + subnet scan) for ESP-Miner family (Bitaxe, NerdAxe, NerdQAxe)
- Fleet dashboard: hashrate, temps, shares, best difficulty, uptime, power
- Integrated solo-mining work engine backed by the local node (DATUM Gateway integration; SV2 next), with configurable pool failover
- **Payout-address hygiene:** payouts derived from a user xpub (fresh address rotation, watch-only — NodeOS never holds keys)
- Bulk firmware updates with staged rollout (update 1 device, verify, then the rest)
- Atomic self-update (A/B) with rollback; signed releases
- Onboarding magic: miners start on a fallback pool immediately, **auto-switch to your own node when it finishes syncing**

### P1 — Retention (first year)
- Alerting (device down, temp, hashrate drop, *block found*) via push/Nostr/Telegram/webhook
- Remote access: embedded WireGuard; optional self-hostable relay
- Energy automation: tariff windows, solar-surplus signal, thermostat mode — **via Home Assistant integration**, not by rebuilding HA
- Tuning profiles: frequency/voltage presets per model, quiet mode, thermal throttle policies
- Stratum V2 endpoint (SRI-based) as ESP-Miner v2.14+ adoption grows
- Electrum server (electrs) + mempool explorer as optional one-click services
- Mobile PWA with push notifications
- **The Block Ceremony:** when a managed fleet finds a block, the product handles the biggest moment of that user's Bitcoin life perfectly — verification, provenance record, shareable artifact. Costs little, markets itself.

### P2 — Expansion
- Multi-site / multi-controller fleets; roles & audit log (enterprise)
- Driver SDK for third-party ASIC families; pool-adapter SDK
- Anonymous opt-in fleet benchmarking ("your Gamma runs 4% under fleet median at 490 MHz") — a data flywheel nobody else can build
- Lightning node as optional service
- HA controller pairs for farms

### Explicitly not built (and why)
- **General app store** — breadth is Umbrel's game; it dilutes focus and invites non-Bitcoin content.
- **Custom Linux distro / package manager** — velocity killer; RAUC + Debian base does the job.
- **Own public pool** — integrate Public Pool/ckpool/OCEAN; running pool infrastructure is a different business with different liabilities.
- **Wallet with keys / custody of any kind** — watch-only xpub is the boundary. Non-negotiable risk cut.
- **AI chatbot** — explainable diagnostics instead (see §0.5).
- **Anything non-Bitcoin** — structural, see USP.

---

## 8. System Architecture

```
┌─────────────────────────── nodeosd (single binary) ───────────────────────────┐
│                                                                                │
│  Web UI (embedded)   REST+WS API   Auth (keys/passkeys)                        │
│  ────────────────────────────────────────────────────────                      │
│  Fleet Manager      Discovery (mDNS/scan)   Update Manager (self + firmware)   │
│  Work Engine glue   Alert Engine            Automation hooks (HA/webhooks)     │
│  ────────────────────────────────────────────────────────                      │
│  Supervisor: bitcoind │ datum-gateway │ SV2 pool (SRI) │ electrs │ …            │
│  State: SQLite        Metrics: embedded TSDB (Prometheus-compatible)           │
└────────────────────────────────────────────────────────────────────────────────┘
        │ LAN                                    │ optional
   ESP-Miner devices (AxeOS REST / SV2)     Relay (self-hostable) ⇄ Mobile PWA
```

Principles:

- **One daemon, one binary, monolith.** No microservices, no Kubernetes, no message broker. SQLite for state, embedded Prometheus-compatible metrics store. Boring wins on a Pi.
- **Supervise, don't rewrite.** `bitcoind`, DATUM Gateway, `electrs` are mature external processes under NodeOS supervision (health checks, log capture, config generation). The SV2 pool role builds on SRI crates. NodeOS's own code is the control plane: discovery, fleet, updates, alerts, UI.
- **Three deployment forms, one codebase:** (1) flashable image (Pi 5/x86, Debian base, RAUC A/B partitions), (2) single binary on any Linux, (3) Docker/Compose. Form 2 and 3 can attach to an *existing* `bitcoind` — the run-alongside path.
- **Controller/agent for scale:** a farm site runs a lightweight agent; one controller aggregates sites. Same binary, different mode. The controller↔agent protocol is versioned and documented — publish it as an open spec so it can become the de-facto standard.
- **Remote access without accounts:** embedded WireGuard for direct access; optional relay (Nostr-key authenticated, end-to-end encrypted, self-hostable, we run one as a convenience/paid service).

---

## 9. Technology Stack

| Layer | Choice | Why |
|---|---|---|
| Core daemon | **Rust** | SRI (Stratum V2 reference) is Rust — the deepest integration surface we have; single static binary; the reliability story ("miner-grade") is credible; one toolchain for core + data plane |
| Work generation | DATUM Gateway (supervised C binary) now; SRI-based SV2 pool role next | Don't rewrite what OCEAN maintains; SV2 is the future the ecosystem just committed to |
| Web UI | SvelteKit (static, embedded in binary) | Light enough for a Pi; no separate web server |
| State | SQLite (+ Litestream-style backup) | Zero-ops, atomic snapshots |
| Metrics | Embedded Prometheus-compatible store | Grafana-compatible for power users, invisible for everyone else |
| OS image | Debian stable + RAUC A/B | Boring, auditable, atomic |
| Updates/signing | Reproducible builds, sigstore/minisign, staged rollout channels | Steal Start9's principle with commodity tech |
| API | REST + WebSocket, OpenAPI-generated SDKs (TS/Python/Rust) | The SDK is generated, not hand-maintained |

Anti-choices: no Kubernetes, no Electron desktop app (PWA), no GraphQL, no plugin-store microservice sprawl, no custom package format.

---

## 10. Security Architecture

**Threat model first:** the realistic adversaries are (a) opportunistic attackers on exposed remote-access ports, (b) supply-chain compromise of updates, (c) compromised IoT-grade miner firmware moving laterally, (d) physical theft of the box. *Not* in scope: nation-state targeting; custody attacks (there are no keys to steal — by design).

- **No custody, ever.** Payouts to addresses derived from a user-supplied xpub (watch-only, rotating). The most effective security feature is the absence of the asset.
- **Signed, reproducible, atomic updates** with staged channels (canary → stable) and automatic rollback on failed health checks. Firmware updates to miners follow the same staged pattern (1 device → verify → fleet).
- **Miners are untrusted devices.** ESP32-class firmware is IoT-grade; NodeOS treats the miner LAN segment as hostile — the controller is the security boundary, guides users to VLAN isolation, and never forwards miner-originated traffic.
- **Remote access = keys, not passwords:** passkeys/WebAuthn locally, WireGuard or Nostr-key-authenticated relay remotely; E2E encrypted; relay sees ciphertext only.
- **No telemetry by default.** Benchmarking flywheel is opt-in, anonymized, and documented.
- Secrets in OS keyring/TPM where available; single-purpose OS image with minimal packages; security.txt + disclosure policy from day one.

---

## 11. Plugin System

Not an app store — **typed extension points** with narrow contracts:

1. **Device drivers** (highest value): add support for a new ASIC family by implementing a small trait/interface (discover, poll, tune, update). This is how the community absorbs the hardware zoo without core-team burnout.
2. **Pool adapters:** normalize per-pool stats/APIs.
3. **Automations:** event-driven hooks (webhook/Home Assistant/scripts) on a documented event bus.
4. **Services** (later, curated): optional supervised processes like electrs, mempool, LN — first-party quality bar, signed, Bitcoin-only by review policy.

Mechanism: out-of-process plugins speaking gRPC over a unix socket (or WASM for drivers if the sandbox proves ergonomic) — crash isolation, language freedom, capability-scoped permissions. A curated, signed registry; "Bitcoin only" enforced editorially, which is honest — technical enforcement of ideology is theater.

---

## 12. UI/UX Concept

**One screen: "Your operation."** Fleet grid (live hashrate/temp per device), node status, blocks-found history, and the honest headline stat: *expected time to block at current fleet hashrate*, framed correctly ("this is a lottery; here's your ticket count").

Principles:

- **Defaults over settings.** Every setting that exists must justify itself; tuning lives behind a "workshop" mode.
- **Every number is explainable.** Click any metric → plain-language explanation + what action it suggests. This *is* the "AI assistant," done credibly.
- **Progressive disclosure:** beginner sees fleet + node health; developer opens the same screen's API inspector.
- **Onboarding in three steps:** flash/install → scan finds miners → paste xpub. Mining starts immediately (fallback pool), flips automatically to own-node solo when sync completes — the product's signature magic moment.
- **Mobile:** PWA with push (block found, device down, temp). Native apps only if PWA push proves insufficient on iOS.
- Dark-mode-first, dense-but-calm; the aesthetic benchmark is a great instrument panel, not a SaaS marketing dashboard.

---

## 13. Development Roadmap

**Phase 0 — Proof (months 1–2).** Read-only: discovery + AxeOS polling + fleet dashboard, attach to existing bitcoind. Ship to 20 OSMU community testers. *Kill criterion: if discovery/UX doesn't wow Bitaxe owners in 60 seconds, revisit the wedge.*

**Phase 1 — MVP (months 3–6).** Everything in §14. Public beta, vendor conversations start now (bundling is a launch channel, not an afterthought).

**Phase 2 — Retention (months 6–12).** Alerts, remote access + relay, energy automation via HA, SV2 endpoint, firmware staged rollouts, PWA, Block Ceremony, electrs/mempool services. First paid offering (relay/support).

**Phase 3 — Scale (months 12–24).** Controller/agent multi-site, driver + pool SDKs, benchmarking flywheel, enterprise features (roles, audit, SSO), HA pairs, LN service. Vendor-bundled installs become the top acquisition channel.

---

## 14. MVP Definition

**A Bitaxe owner with zero infrastructure reaches "mining on my own node" in under an hour of wall-clock time and under 5 minutes of attention.**

In: bitcoind management with assumeutxo; ESP-Miner discovery; fleet dashboard; DATUM-backed solo mining with pool failover; xpub payout rotation; bulk firmware update; A/B self-update; signed releases; runs on Pi 5, x86, Docker; attach-to-existing-node mode.

Out (MVP): Lightning, app store/plugins, remote relay, energy automation, multi-site, SV2 (unless SRI integration turns out cheap), mobile app, marketplace, everything in §7 "not built."

Success metrics: time-to-first-share < 60 s after discovery; 1,000 active installs; ≥1 vendor bundling commitment; unsolicited community drivers/PRs appearing.

---

## 15. Monetization Strategy

Reality check: FOSS Bitcoin infrastructure is not venture-scale SaaS. Structure for sustainability, not blitzscaling:

1. **Vendor partnerships** (nearest-term): bundling deals, co-branded images, integration fees from hardware sellers in the OSMU orbit.
2. **Managed relay subscription:** remote access + push notifications without self-hosting a relay. Self-hostable version always free — the payment is convenience, not lock-in.
3. **Enterprise fleet tier** (open-core, thin): multi-site controller, SSO/roles/audit, SLA support contracts for small farms. Flat pricing, explicitly *not* per-miner (that's the Foreman resentment we inherit users from).
4. **Grants/sponsorships:** OpenSats, HRF, Spiral, corporate sponsors — credible because of the SV2/decentralization angle.
5. **Never:** app-store rents, tokens, ads, selling data, custody yield anything.

License: core under MIT or Apache-2.0 (vendor-friendly, OSMU-aligned); the enterprise tier proprietary or BSL — decide before first external contribution, not after.

---

## 16. Risks

| Risk | Severity | Mitigation |
|---|---|---|
| Bitcoin-cyclical demand; bear market kills momentum | High | Small team, low burn, grant funding, heat-reuse framing works in bear markets |
| Solo-mining disillusionment (nobody wins for months) | High | Honesty UX as brand; pool/failover as first-class; heat + learning as co-equal value props |
| ESP-Miner/AxeOS ships its own fleet feature | Medium-high | Build *with* the maintainers, upstream patches, OSMU alignment — be the blessed companion, not a competitor |
| Umbrel adds a mining app | Medium | Their DNA is breadth; depth (drivers, tuning, DATUM/SV2, staged firmware) is years of focused work. Speed matters anyway. |
| Hardware zoo maintenance burden | Medium | Driver SDK + community ownership per family; support matrix kept honest |
| DATUM/upstream dependency changes (license, direction) | Medium | Supervised-process integration keeps coupling loose; SRI-based own path as successor |
| Team bus factor (the myNode failure mode) | Medium | ≥2 maintainers per subsystem from phase 2; boring tech lowers contribution bar |
| Regulatory (mining) | Low | No custody, no pool operation, no KYC surface; ship software |

The single biggest product risk: **shipping a node OS with mining bolted on** — becoming the fifth Umbrel. The wedge discipline in §2 is the mitigation; if a feature doesn't serve someone who owns a miner or runs a node *for* miners, it waits.

---

## 17. Long-Term Vision (5 Years)

- **The default:** every open-source ASIC ships with a QR code that ends in a NodeOS fleet dashboard. "Point it at your NodeOS" replaces "point it at Public Pool."
- **Decentralization that shows up on-chain:** a meaningful share of network templates built by SV2 job negotiation on hardware NodeOS manages. The north-star metric: **% of network hashrate mining on self-built templates through a NodeOS instance.**
- **The heating category:** miner-as-heater becomes a normal appliance decision; NodeOS is the thermostat-integrated brain, with tariff/solar automation making home mining rational in most climates.
- **An open standard:** the controller/agent fleet protocol is adopted by other tools — winning by being the spec, not just the app.
- **Research bearing fruit:** syndicate/split solo mining (the "shared lottery ticket") solved non-custodially, if at all — shipped only when it can be done without trust.

The five-year test from the brief — *"the OS every Bitcoiner will want to install"* — is won at the moment a Bitaxe buyer's unboxing experience is: plug it in, open NodeOS, and thirty seconds later watch their own node hand their own miner its first job.
