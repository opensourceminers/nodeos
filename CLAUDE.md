# Repository conventions

## Git identity — read this first on a fresh clone

Commits **must** be authored as:

```
othervice <291600513+othervice@users.noreply.github.com>
```

The history was rewritten on 2026-07-27 to remove a private email address, so
every clone made before that date has diverged. On such a clone, before doing
any work:

```bash
git fetch origin && git reset --hard origin/main
git config user.email "291600513+othervice@users.noreply.github.com"
git config user.name "othervice"
```

Without the second line the next commit puts the private address back into
the public repository. Verify with `git log --format='%an <%ae>' -1`.

Do not add co-author trailers or tooling attribution to commit messages.

## Build, test, run

```bash
go build ./... && go vet ./... && go test ./...
GOOS=linux GOARCH=amd64 go vet ./...   # health/ uses syscall.Statfs — cross-check
go run ./cmd/nodeosd --demo --no-auth --listen 127.0.0.1:8080
```

`internal/health` and `internal/services` behave differently per platform;
always cross-vet for linux before pushing from macOS or Windows.

Shell code ships to real machines — `bash -n deploy/*.sh` before committing,
and run `bash deploy/test-service-validation.sh deploy/install.sh` when
touching the service allowlists (it must report 0 failures).

## Where the trust boundary is

`nodeosd` is unprivileged. Anything requiring root (installing the Bitcoin
node, installing container services, self-update) is queued as a command file
in `/var/lib/nodeos/admin/` and executed by `nodeos-admin` (see
`deploy/install.sh`), which **re-validates every argument and every unit line**
against allowlists. When adding a service to `internal/services`, the image,
volume paths and unit keys must also be allowlisted there — the test in
`internal/services/services_test.go` parses the installer's regexes and fails
if catalog and helper drift apart. Never widen an allowlist to make a feature
work without understanding what it lets through.

## Verifying changes for real

macOS unit tests do not prove much for an appliance. A Debian VM is the
minimum bar for anything touching the installer, systemd units or the helper:

```bash
limactl start template://debian-13 --name nodeos-test --vm-type=vz --memory 4 --disk 20
limactl shell nodeos-test -- sudo bash <repo>/deploy/install.sh --from-source --with-bitcoind --with-datum
sudo bash deploy/regtest-smoketest.sh <web-ui-password>   # full solo-mining chain on regtest
```

Two regtest quirks, both test-only: bitcoind creates `regtest/` as 0700 (needs
`chmod g+rx` for the cookie), and DATUM cannot parse `bcrt1` payout addresses
— use a legacy address.

## Product line

Bitcoin only. No general-purpose apps, no altcoins, no custody, no cloud
dependency, no telemetry. See `docs/` for the product analysis, the review of
alternative architecture proposals (and why they were rejected), and the
current status with open gaps.
