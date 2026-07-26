# Review: "OSM Node OS" architecture proposal (July 2026)

An external (GPT-generated) architecture prompt proposed a ground-up
"OSM Node OS". This document maps it against the working NodeOS prototype
(v0.3.0) and records what we adopt, defer, or reject — so we evolve the
running product instead of restarting.

## Already built (the proposal asks for things we have)

| Proposal | Status in NodeOS |
|---|---|
| Go backend, single API, UI never touches system directly | ✅ `nodeosd`, everything via `/api/*` |
| Miner discovery/fleet/pool as core, not app | ✅ subnet scan, fleet dashboard, pool switchboard |
| "Mine on your own node" journey | ✅ work engine + auto-switch (DATUM), 2 clicks |
| Bitcoin Core **and** Knots, pruned or full | ✅ installer flags + web UI switching |
| Local auth, sessions, CSRF-resistant cookies | ✅ PBKDF2 + HttpOnly/SameSite=Strict |
| Signed/verified updates without apt | ✅ (partially) GitHub releases, SHA256-verified, root-helper install. Image signing later |
| No general apps (no Plex/Nextcloud) | ✅ position held since day 1 |
| Debian base | ✅ install.sh targets Debian; ISO now Debian 13 netinst (was Ubuntu) |
| No cloud dependency, no telemetry | ✅ |

## Adopt now (cheap, real value)

- **Debian 13 everywhere** — done in this commit: appliance ISO is now
  trixie netinst + preseed (~0.7 GB instead of 2.7 GB Ubuntu), Proxmox test
  VM uses the trixie cloud image.
- **`http://nodeos.local`** — avahi + libnss-mdns preseeded on the appliance.
- Soon (next milestones): SMART/system health in the API, support bundle,
  factory reset, HTTPS (nodeosd native TLS — no Caddy needed), nftables on
  the appliance.

## Adopt later (right idea, wrong time)

- **Immutable A/B image, recovery partition, boot counting, image signing.**
  Matches our own roadmap ("appliance image"). Do it *after* the current
  stack is validated on real hardware — an A/B image of unvalidated software
  hardens the wrong thing. Evaluate `systemd-sysupdate`+`systemd-boot`
  (x86/UEFI, systemd-native) vs RAUC (better Pi story) when we get there.
  The proposal's partition layout (EFI/OS-A/OS-B/Recovery/DATA) is a good
  starting point; `/data/*` layout can be adopted then.
- **App manifests + registry.** Matches our plugin roadmap. Only needed when
  third-party services (mempool, BTCPay…) arrive.

## Rejected (with reasons)

- **Docker for everything + runtime abstraction interface.** First-party
  services (bitcoind, DATUM, electrs) stay native systemd: smaller RAM
  footprint on small boxes, direct cookie/file access, one less privileged
  daemon, and systemd hardening we already use. Containers make sense for
  *third-party* apps later — podman rootless, not Docker, and we will not
  pre-build an abstraction layer for a runtime swap we may never do.
- **React/Next.js frontend.** Next.js means running an SSR Node server on an
  appliance — wrong tool. The no-build vanilla SPA works, is fast on weak
  hardware and keeps contributor friction near zero. Revisit a framework
  only if UI complexity genuinely outgrows it.
- **Caddy reverse proxy.** nodeosd can terminate TLS itself; one binary
  fewer, no config split. mDNS gives us the nice hostname.
- **NetworkManager.** Server appliance → systemd-networkd (or the netinst
  default ifupdown) is leaner. NM is a desktop tool.
- **Restart from a skeleton repo.** The proposal says "build the repo
  skeleton first". We have a tested, working v0.3.0 — evolution beats
  rewrite. Monorepo restructuring is churn without user value.
- **x86-64 only.** Go cross-compiles arm64 for free and Pi-class boxes are
  half the audience; we keep shipping both.
- **SSH disabled by default** — correct for the final appliance, wrong for
  the prototype phase (it is our only recovery path until the recovery
  image exists). Revisit with the A/B image.
