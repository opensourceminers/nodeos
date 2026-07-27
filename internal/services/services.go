// Package services manages NodeOS's curated Bitcoin services: a compiled-in
// catalog (no third-party registry, Bitcoin only), rendered into Podman
// Quadlet units that systemd supervises like any other service.
//
// Trust boundary: nodeosd (unprivileged) renders unit files into a staging
// directory and enqueues an install job; the root helper re-validates every
// line against strict allowlists (images, volume paths, keys) before copying
// anything into /etc/containers/systemd. nodeosd can therefore be fully
// compromised without gaining the ability to run an arbitrary container.
package services

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Requirement flags surfaced in the UI before installing.
type Requires struct {
	FullNode bool `json:"full_node,omitempty"` // pruned node won't do
	TxIndex  bool `json:"tx_index,omitempty"`
	Disk     int  `json:"disk_gb,omitempty"` // rough extra disk need
}

type Service struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Tagline     string   `json:"tagline"`
	Description string   `json:"description"`
	Requires    Requires `json:"requires"`
	// Port the service answers on (health checks); WebPath non-empty means
	// it has a browser UI reachable at http://<host>:<Port><WebPath>.
	Port    int    `json:"port"`
	WebPath string `json:"web_path,omitempty"`
	Planned bool   `json:"planned,omitempty"` // in the catalog, not installable yet

	// units to render: filename (without dir) → content template.
	// @@RPCPASS@@ is substituted by the root helper at install time.
	units map[string]string
}

// unit filenames double as systemd unit names: nodeos-svc-<x>.container
// becomes nodeos-svc-<x>.service.
func (s *Service) UnitNames() []string {
	var out []string
	for f := range s.units {
		out = append(out, strings.TrimSuffix(f, ".container")+".service")
	}
	return out
}

const dataRoot = "/var/lib/nodeos-services"

// Catalog is the curated service list. Images are pinned to specific tags;
// the root helper additionally enforces an image allowlist.
func Catalog() []*Service {
	return []*Service{
		{
			ID:      "lightning",
			Name:    "Core Lightning",
			Tagline: "Lightning node (CLN)",
			Description: "Runs a Core Lightning node against your Bitcoin node. RPC and data stay in " +
				dataRoot + "/lightning.",
			Requires: Requires{FullNode: true, Disk: 2},
			Port:     9735,
			units: map[string]string{
				"nodeos-svc-lightning.container": `[Unit]
Description=NodeOS service: Core Lightning
After=network-online.target bitcoind.service
Wants=network-online.target

[Container]
Image=docker.io/elementsproject/lightningd:v26.06.6
ContainerName=nodeos-svc-lightning
Network=host
Volume=` + dataRoot + `/lightning:/root/.lightning
Exec=--network=bitcoin --bitcoin-rpcconnect=127.0.0.1 --bitcoin-rpcport=8332 --bitcoin-rpcuser=nodeossvc --bitcoin-rpcpassword=@@RPCPASS@@ --bind-addr=0.0.0.0:9735 --log-level=info

[Service]
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
`,
			},
		},
		{
			ID:      "electrs",
			Name:    "Electrum Server",
			Tagline: "electrs — connect your own wallets",
			Description: "Indexes the chain so hardware and desktop wallets (Sparrow, Electrum) can use " +
				"your node instead of someone else's server.",
			Requires: Requires{FullNode: true, Disk: 120},
			Port:     50001,
			units: map[string]string{
				"nodeos-svc-electrs.container": `[Unit]
Description=NodeOS service: Electrum server (electrs)
After=network-online.target bitcoind.service
Wants=network-online.target

[Container]
Image=docker.io/getumbrel/electrs:v0.10.9
ContainerName=nodeos-svc-electrs
Network=host
Volume=` + dataRoot + `/electrs:/data
Environment=ELECTRS_DB_DIR=/data
Environment=ELECTRS_DAEMON_RPC_ADDR=127.0.0.1:8332
Environment=ELECTRS_DAEMON_P2P_ADDR=127.0.0.1:8333
Environment=ELECTRS_ELECTRUM_RPC_ADDR=0.0.0.0:50001
Environment=ELECTRS_AUTH=nodeossvc:@@RPCPASS@@
Environment=ELECTRS_LOG_FILTERS=INFO

[Service]
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
`,
			},
		},
		{
			ID:      "mempool",
			Name:    "Mempool Explorer",
			Tagline: "the mempool.space UI on your own node",
			Description: "Block and fee explorer served entirely from your node — three containers " +
				"(database, API, web) supervised as one service.",
			Requires: Requires{Disk: 5},
			Port:     3006,
			WebPath:  "/",
			units: map[string]string{
				"nodeos-svc-mempool-db.container": `[Unit]
Description=NodeOS service: Mempool database
After=network-online.target

[Container]
Image=docker.io/library/mariadb:11.4
ContainerName=nodeos-svc-mempool-db
Network=host
Volume=` + dataRoot + `/mempool/db:/var/lib/mysql
Environment=MARIADB_DATABASE=mempool
Environment=MARIADB_USER=mempool
Environment=MARIADB_PASSWORD=mempool-local
Environment=MARIADB_RANDOM_ROOT_PASSWORD=yes
Exec=--bind-address=127.0.0.1 --port=3307

[Service]
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
`,
				"nodeos-svc-mempool-api.container": `[Unit]
Description=NodeOS service: Mempool API
After=network-online.target nodeos-svc-mempool-db.service bitcoind.service
Wants=nodeos-svc-mempool-db.service

[Container]
Image=docker.io/mempool/backend:v3.0.1
ContainerName=nodeos-svc-mempool-api
Network=host
Volume=` + dataRoot + `/mempool/cache:/backend/cache
Environment=MEMPOOL_BACKEND=none
Environment=CORE_RPC_HOST=127.0.0.1
Environment=CORE_RPC_PORT=8332
Environment=CORE_RPC_USERNAME=nodeossvc
Environment=CORE_RPC_PASSWORD=@@RPCPASS@@
Environment=DATABASE_ENABLED=true
Environment=DATABASE_HOST=127.0.0.1
Environment=DATABASE_PORT=3307
Environment=DATABASE_DATABASE=mempool
Environment=DATABASE_USERNAME=mempool
Environment=DATABASE_PASSWORD=mempool-local
Environment=MEMPOOL_HTTP_PORT=8999
Environment=MEMPOOL_CACHE_DIR=/backend/cache

[Service]
Restart=on-failure
RestartSec=15

[Install]
WantedBy=multi-user.target
`,
				"nodeos-svc-mempool-web.container": `[Unit]
Description=NodeOS service: Mempool web UI
After=network-online.target nodeos-svc-mempool-api.service
Wants=nodeos-svc-mempool-api.service

[Container]
Image=docker.io/mempool/frontend:v3.0.1
ContainerName=nodeos-svc-mempool-web
Network=host
Environment=FRONTEND_HTTP_PORT=3006
Environment=BACKEND_MAINNET_HTTP_HOST=127.0.0.1
Environment=BACKEND_MAINNET_HTTP_PORT=8999

[Service]
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
`,
			},
		},
		{
			ID:      "btcpay",
			Name:    "BTCPay Server",
			Tagline: "accept Bitcoin payments",
			Description: "Self-hosted payment processor: database, NBXplorer and BTCPay as one " +
				"service. Works on a pruned node; uses Core Lightning automatically when that " +
				"service is installed.",
			Requires: Requires{Disk: 5},
			Port:     23000,
			WebPath:  "/",
			units: map[string]string{
				"nodeos-svc-btcpay-db.container": `[Unit]
Description=NodeOS service: BTCPay database (PostgreSQL)
After=network-online.target

[Container]
Image=docker.io/library/postgres:17.10
ContainerName=nodeos-svc-btcpay-db
Network=host
Volume=` + dataRoot + `/btcpay/db:/var/lib/postgresql/data
Environment=POSTGRES_USER=btcpay
Environment=POSTGRES_PASSWORD=btcpay-local
# maintenance DB named after the user: NBXplorer and BTCPay both connect to
# it (Npgsql default) when creating their own databases on first start
Environment=POSTGRES_DB=btcpay
Exec=-p 5433 -c listen_addresses=127.0.0.1

[Service]
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
`,
				"nodeos-svc-btcpay-nbx.container": `[Unit]
Description=NodeOS service: NBXplorer (chain indexer for BTCPay)
After=network-online.target nodeos-svc-btcpay-db.service bitcoind.service
Wants=nodeos-svc-btcpay-db.service

[Container]
Image=docker.io/nicolasdorier/nbxplorer:2.6.9
ContainerName=nodeos-svc-btcpay-nbx
Network=host
Volume=` + dataRoot + `/btcpay/nbxplorer:/datadir
Environment=NBXPLORER_DATADIR=/datadir
Environment=NBXPLORER_NETWORK=mainnet
Environment=NBXPLORER_CHAINS=btc
Environment=NBXPLORER_BTCRPCURL=http://127.0.0.1:8332/
Environment=NBXPLORER_BTCRPCUSER=nodeossvc
Environment=NBXPLORER_BTCRPCPASSWORD=@@RPCPASS@@
Environment=NBXPLORER_BTCNODEENDPOINT=127.0.0.1:8333
Environment=NBXPLORER_BIND=127.0.0.1:24444
Environment=NBXPLORER_NOAUTH=1
Environment=NBXPLORER_POSTGRES=Host=127.0.0.1;Port=5433;Database=nbxplorer;Username=btcpay;Password=btcpay-local

[Service]
Restart=on-failure
RestartSec=15

[Install]
WantedBy=multi-user.target
`,
				"nodeos-svc-btcpay-web.container": `[Unit]
Description=NodeOS service: BTCPay Server
After=network-online.target nodeos-svc-btcpay-nbx.service
Wants=nodeos-svc-btcpay-nbx.service

[Container]
Image=docker.io/btcpayserver/btcpayserver:2.4.1
ContainerName=nodeos-svc-btcpay-web
Network=host
Volume=` + dataRoot + `/btcpay/btcpay:/datadir
Volume=` + dataRoot + `/lightning:/etc/lightning
Environment=BTCPAY_DATADIR=/datadir
Environment=BTCPAY_NETWORK=mainnet
Environment=BTCPAY_CHAINS=btc
Environment=BTCPAY_BIND=0.0.0.0:23000
Environment=BTCPAY_ROOTPATH=/
Environment=BTCPAY_BTCEXPLORERURL=http://127.0.0.1:24444/
Environment=BTCPAY_POSTGRES=Host=127.0.0.1;Port=5433;Database=btcpayserver;Username=btcpay;Password=btcpay-local
Environment=BTCPAY_BTCLIGHTNING=type=clightning;server=unix:///etc/lightning/bitcoin/lightning-rpc

[Service]
Restart=on-failure
RestartSec=15

[Install]
WantedBy=multi-user.target
`,
			},
		},
	}
}

func ByID(id string) *Service {
	for _, s := range Catalog() {
		if s.ID == id {
			return s
		}
	}
	return nil
}

// ---------- staging ----------

// Stage writes the service's unit files into <dataDir>/services-staging/<id>/
// for the root helper to validate and install.
func Stage(dataDir string, s *Service) error {
	if s.Planned {
		return fmt.Errorf("%s is not installable yet", s.Name)
	}
	dir := filepath.Join(dataDir, "services-staging", s.ID)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, content := range s.units {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// ---------- status ----------

type UnitStatus struct {
	Unit   string `json:"unit"`
	Active string `json:"active"` // active | inactive | failed | activating | …
}

type Status struct {
	ID        string       `json:"id"`
	Installed bool         `json:"installed"`
	Running   bool         `json:"running"`
	Degraded  bool         `json:"degraded"` // installed, but not every unit is active
	Units     []UnitStatus `json:"units,omitempty"`
}

type Manager struct {
	mu      sync.Mutex
	cached  []Status
	fetched time.Time
}

func NewManager() *Manager { return &Manager{} }

// StatusAll reports install/running state for every catalog entry. Results
// are cached briefly — it shells out to systemctl and runs on every SSE tick.
func (m *Manager) StatusAll() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if time.Since(m.fetched) < 5*time.Second && m.cached != nil {
		return m.cached
	}
	var out []Status
	for _, s := range Catalog() {
		st := Status{ID: s.ID}
		if s.Planned {
			out = append(out, st)
			continue
		}
		allActive := true
		anyInstalled := false
		for _, unit := range s.UnitNames() {
			if _, err := os.Stat("/etc/containers/systemd/" + strings.TrimSuffix(unit, ".service") + ".container"); err != nil {
				allActive = false
				continue
			}
			anyInstalled = true
			state := unitState(unit)
			st.Units = append(st.Units, UnitStatus{Unit: unit, Active: state})
			if state != "active" && state != "activating" {
				allActive = false
			}
		}
		st.Installed = anyInstalled
		st.Running = anyInstalled && allActive
		st.Degraded = anyInstalled && !allActive
		out = append(out, st)
	}
	m.cached = out
	m.fetched = time.Now()
	return out
}

// Invalidate drops the cache (called right after install/ctl jobs are queued
// so the UI picks up state changes quickly).
func (m *Manager) Invalidate() {
	m.mu.Lock()
	m.cached = nil
	m.mu.Unlock()
}

func unitState(unit string) string {
	out, err := exec.Command("systemctl", "is-active", unit).Output()
	state := strings.TrimSpace(string(out))
	if state == "" && err != nil {
		return "inactive"
	}
	return state
}

// Logs returns the recent journal of every unit of a service; nodeosd is in
// the systemd-journal group, so this needs no privileges.
func Logs(s *Service, lines int) string {
	args := []string{"-n", fmt.Sprint(lines), "--no-pager", "-o", "short-iso"}
	for _, u := range s.UnitNames() {
		args = append(args, "-u", u)
	}
	out, err := exec.Command("journalctl", args...).CombinedOutput()
	if err != nil && len(out) == 0 {
		return fmt.Sprintf("journalctl: %v", err)
	}
	return string(out)
}
