# Deploy NodeOS to a running Debian VM from a Windows workstation.
#   .\deploy\push-to-vm.ps1 -VmIp 192.168.1.50 [-User nodeos] [-WithBitcoind] [-Prune 20000]
param(
    [Parameter(Mandatory)][string]$VmIp,
    [string]$User = "nodeos",
    [switch]$WithBitcoind,
    [int]$Prune = 0
)

$root = Split-Path $PSScriptRoot -Parent
$bin = Join-Path $root "dist\nodeosd-linux-amd64"
if (-not (Test-Path $bin)) {
    Write-Error "dist\nodeosd-linux-amd64 missing - run .\build.ps1 first"
    exit 1
}

scp $bin (Join-Path $PSScriptRoot "install.sh") "${User}@${VmIp}:/tmp/"
if ($LASTEXITCODE -ne 0) { exit 1 }

$flags = "--binary /tmp/nodeosd-linux-amd64"
if ($WithBitcoind) { $flags += " --with-bitcoind --prune $Prune" }

ssh "${User}@${VmIp}" "sudo bash /tmp/install.sh $flags"
if ($LASTEXITCODE -eq 0) {
    Write-Host "`nNodeOS installed - open http://${VmIp}/" -ForegroundColor Green
}
