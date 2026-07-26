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
		},
		Alerts: Alerts{TempMaxC: 70},
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
