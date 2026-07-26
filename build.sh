#!/usr/bin/env bash
# Build NodeOS release binaries (Linux amd64 + arm64).
set -euo pipefail
cd "$(dirname "$0")"

command -v go >/dev/null || { echo "Go not found — install go 1.26+" >&2; exit 1; }

mkdir -p dist
go vet ./...

for arch in amd64 arm64; do
  CGO_ENABLED=0 GOOS=linux GOARCH=$arch \
    go build -trimpath -ldflags "-s -w" -o "dist/nodeosd-linux-$arch" ./cmd/nodeosd
  echo "built dist/nodeosd-linux-$arch"
done

if [[ "${1:-}" == "--bundle" ]]; then
  tar -czf dist/nodeos-bundle.tar.gz -C dist nodeosd-linux-amd64 nodeosd-linux-arm64 -C ../deploy install.sh
  echo "built dist/nodeos-bundle.tar.gz"
fi
