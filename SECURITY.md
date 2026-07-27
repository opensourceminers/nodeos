# Security

NodeOS runs a Bitcoin node, supervises mining hardware and manages system
services on the machine it is installed on. This documents its security
posture and how to harden it. Found an issue? Email
security@opensourceminers.de (please don't open a public issue first).

## Posture

- **No custody, ever.** NodeOS holds no keys, no wallet and no funds. Mining
  payouts go to the address you configure on the pool or in the work engine;
  NodeOS only writes it into stratum/gateway configuration. Bitcoin Core's
  wallet is not used, and the Lightning service (when installed) keeps its own
  keys in its own volume — NodeOS never reads them.
- **The daemon is unprivileged.** `nodeosd` runs as the `nodeos` system user
  with `NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome` and a
  writable-path allowlist. It has no sudo rights and cannot escalate.
- **Privileged work goes through a validating helper.** Installing or
  switching the Bitcoin node, installing services and applying self-updates
  are performed by `/usr/local/bin/nodeos-admin`, triggered by a systemd path
  unit watching a command queue. The helper re-validates every argument
  against strict allowlists before acting. A fully compromised `nodeosd`
  cannot run arbitrary commands as root.
- **Container services are allowlisted, not free-form.** The service catalog
  is compiled into the binary (Bitcoin-only, no third-party registry).
  `nodeosd` merely stages Quadlet unit files; the root helper copies them into
  a root-only directory, then validates every single line: pinned image
  digests/tags from an allowlist, volume paths confined to
  `/var/lib/nodeos-services`, and a rejection list for `Privileged`,
  `PodmanArgs`, `Mount`, `AddCapability`, `User`, `Sysctl` and any host-side
  `ExecStart`. Units are installed only from the validated copy, so the
  contents cannot be swapped in between (no time-of-check/time-of-use gap),
  and symlinked staged units are refused. See
  `deploy/test-service-validation.sh` — an adversarial suite that must keep
  passing.
- **Downloads are verified.** Bitcoin Core/Knots and NodeOS self-updates are
  checked against the publisher's SHA256SUMS before installation; a mismatch
  aborts before anything is staged. Firmware images are only accepted from
  the configured ESP-Miner release repository.
- **No cloud, no telemetry.** NodeOS talks to your node, your miners and —
  only when you ask it to — GitHub (updates, firmware lists) and the vendor
  download servers. Nothing is reported anywhere.

## Network exposure & auth

- **The web UI requires a password.** On first visit you set an admin
  password; it is stored as a PBKDF2-HMAC-SHA256 hash (210k iterations,
  per-install salt), never in plain text. Sessions are random tokens in
  HttpOnly, SameSite=Strict cookies held in memory — restarting the daemon
  logs everyone out. Every `/api/*` route except login/setup requires a valid
  session.
- **HTTPS with a self-signed certificate** is served on `:443` (generated on
  first start, SANs for the hostname, `<hostname>.local` and LAN IPs). Your
  browser warns once — that is expected for a LAN appliance. Plain HTTP stays
  on `:80` for scripts on the same network.
- **Do not expose NodeOS to the internet.** It is designed for a trusted
  LAN/VLAN. There is no rate limiting on login beyond a fixed delay, no 2FA
  yet, and the TLS certificate is not one a browser trusts. Use WireGuard or
  an authenticating reverse proxy for remote access.
- **The stratum/work engine listens on the LAN** so miners can reach it. It
  speaks Stratum V1 (what the hardware speaks) and accepts unauthenticated
  connections by design — treat it like any other mining endpoint and keep it
  off the public internet.
- **Container services run with `Network=host`** for simplicity. A
  compromised service container therefore sees localhost, including the node's
  RPC port. This is acceptable while the catalog is first-party and pinned;
  it will be revisited before any third-party service is allowed in.
- **The `nodeos` OS account ships with a default password** (`nodeos`) so the
  appliance is reachable by console/SSH after an unattended install. **Change
  it with `passwd` on first login**, or pass `--login-password` to the
  installer.

## Support bundles

`Settings → Download support bundle` collects status, system health, the work
engine log, a journal excerpt and your configuration **with every
secret-looking value redacted** (passwords, RPC credentials, tokens). It never
contains wallet data, private keys or the admin password hash. Redaction is
covered by a test that fails if a credential leaks. Still, read a bundle
before sharing it publicly.

## Known limitations

Honest list of what is *not* yet protected:

- **Releases are not signed.** Updates are SHA256-verified against the GitHub
  release, which protects against corruption and tampering in transit — but
  not against a compromised release pipeline. Signing is planned.
- **Bitcoin Core/Knots signature verification is checksum-only.** The
  installer verifies SHA256SUMS but does not yet check the maintainers' GPG
  signatures. Verify manually for high-value deployments.
- **No secure boot / disk encryption** in the current appliance image. Anyone
  with physical access to the disk has everything on it.
- **No passkeys/2FA** for the web UI yet.

## Reporting

Email **security@opensourceminers.de**. Please include what you found, how to
reproduce it and how you would exploit it. We will confirm receipt, keep you
updated, and credit you in the release notes unless you prefer otherwise.
Please give us a reasonable window to ship a fix before publishing.
