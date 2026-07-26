# Build NodeOS release binaries (Linux amd64 + arm64) from Windows.
# NodeOS ships as a Linux appliance; there is no Windows build target.
# Local dev/testing on Windows: go run ./cmd/nodeosd --demo --listen 127.0.0.1:8080
param([switch]$Bundle)

$ErrorActionPreference = "Stop"
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Error "Go not found. Install it (winget install GoLang.Go) and retry."
    exit 1
}

New-Item -ItemType Directory -Force dist | Out-Null
go vet ./...

$env:CGO_ENABLED = "0"
$env:GOOS = "linux"
foreach ($arch in "amd64", "arm64") {
    $env:GOARCH = $arch
    go build -trimpath -ldflags "-s -w" -o "dist/nodeosd-linux-$arch" ./cmd/nodeosd
    Write-Host "built dist/nodeosd-linux-$arch"
}
Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED

if ($Bundle) {
    tar -czf dist/nodeos-bundle.tar.gz -C dist nodeosd-linux-amd64 nodeosd-linux-arm64 -C ../deploy install.sh
    Write-Host "built dist/nodeos-bundle.tar.gz"
}
