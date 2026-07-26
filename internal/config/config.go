// Package config holds the static bootstrap configuration for nodeosd.
// Runtime-mutable state (registered miners, pool settings) lives in the
// store package; this file is only read at startup.
package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type Bitcoind struct {
	// RPCURL is the JSON-RPC endpoint of bitcoind, e.g. "http://127.0.0.1:8332".
	RPCURL string `json:"rpc_url"`
	// RPCUser/RPCPass take precedence over CookieFile when set.
	RPCUser    string `json:"rpc_user"`
	RPCPass    string `json:"rpc_pass"`
	CookieFile string `json:"cookie_file"`
	// ConfFile is the bitcoin.conf NodeOS reads settings from and asks the
	// privileged helper to edit.
	ConfFile string `json:"conf_file"`
}

type Pool struct {
	StratumURL   string `json:"stratum_url"`
	StratumPort  int    `json:"stratum_port"`
	StratumUser  string `json:"stratum_user"`
	FallbackURL  string `json:"fallback_url"`
	FallbackPort int    `json:"fallback_port"`
	FallbackUser string `json:"fallback_user"`
}

type Alerts struct {
	// TempMaxC triggers a temperature alert when a miner's chip temp exceeds it.
	TempMaxC float64 `json:"temp_max_c"`
}

// TLS configures the HTTPS listener. A self-signed certificate is generated
// on first start unless CertFile/KeyFile point at custom ones. HTTP on the
// main listen address stays available (LAN scripts, miners' simplicity).
type TLS struct {
	Enabled  bool   `json:"enabled"`
	Listen   string `json:"listen"`
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
}

// Auth configures web UI authentication. Disabled is meant for development
// (`--no-auth`); production installs always require a password.
type Auth struct {
	Disabled bool `json:"disabled"`
}

// Update configures self-updating from GitHub releases.
type Update struct {
	// Repo is the "owner/name" whose releases carry nodeosd binaries.
	Repo string `json:"repo"`
}

// NodeSoftware holds the default versions offered when installing or
// switching the Bitcoin node implementation.
type NodeSoftware struct {
	CoreVersion  string `json:"core_version"`
	KnotsVersion string `json:"knots_version"`
}

// Work configures the solo-mining work engine (DATUM Gateway supervision).
// Runtime-mutable settings (enabled, payout address, mode) live in the store;
// these are the static paths and ports.
type Work struct {
	// BinaryPath is the datum_gateway executable to supervise.
	BinaryPath string `json:"binary_path"`
	// StratumPort is where the gateway serves work to the miners.
	StratumPort int `json:"stratum_port"`
	// APIPort is the gateway's own HTTP dashboard/API port.
	APIPort int `json:"api_port"`
	// AdvertiseHost overrides the auto-detected LAN address that miners are
	// pointed at when the fleet switches to the engine.
	AdvertiseHost string `json:"advertise_host"`
	// OceanHost/OceanPort is the upstream DATUM pool for pooled mode.
	OceanHost string `json:"ocean_host"`
	OceanPort int    `json:"ocean_port"`
}

type Config struct {
	Listen          string   `json:"listen"`
	DataDir         string   `json:"data_dir"`
	ScanCIDR        string   `json:"scan_cidr"`
	PollIntervalSec int      `json:"poll_interval_sec"`
	Demo            bool     `json:"demo"`
	DemoMiners      int      `json:"demo_miners"`
	Bitcoind        Bitcoind `json:"bitcoind"`
	Pool            Pool     `json:"pool"`
	Alerts          Alerts   `json:"alerts"`
	Work            Work     `json:"work"`
	TLS             TLS      `json:"tls"`
	Auth            Auth     `json:"auth"`
	Update          Update   `json:"update"`
	NodeSoftware    NodeSoftware `json:"node_software"`
}

func Default() Config {
	return Config{
		Listen:          ":8080",
		DataDir:         "./data",
		ScanCIDR:        "",
		PollIntervalSec: 10,
		Demo:            false,
		DemoMiners:      6,
		Bitcoind: Bitcoind{
			RPCURL:     "http://127.0.0.1:8332",
			CookieFile: "/var/lib/bitcoind/.cookie",
			ConfFile:   "/etc/bitcoin/bitcoin.conf",
		},
		Alerts: Alerts{TempMaxC: 70},
		Work: Work{
			BinaryPath:  "/usr/local/bin/datum_gateway",
			StratumPort: 23334,
			APIPort:     7152,
			OceanHost:   "datum-beta1.mine.ocean.xyz",
			OceanPort:   28915,
		},
		TLS:    TLS{Enabled: true, Listen: ":443"},
		Update: Update{Repo: "opensourceminers/nodeos"},
		NodeSoftware: NodeSoftware{
			CoreVersion:  "29.0",
			KnotsVersion: "29.3.knots20260508",
		},
	}
}

// Load reads the config file at path on top of defaults. A missing file is
// not an error — the defaults are returned so the daemon can start bare.
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.PollIntervalSec < 2 {
		cfg.PollIntervalSec = 2
	}
	if cfg.DemoMiners <= 0 {
		cfg.DemoMiners = 6
	}
	return cfg, nil
}
