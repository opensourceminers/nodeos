// datum.go generates the configuration file for OCEAN's DATUM Gateway, the
// external process the work engine supervises. Format reference:
// https://github.com/OCEAN-xyz/datum_gateway/blob/master/doc/example_datum_gateway_config.json
package work

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"nodeos/internal/config"
	"nodeos/internal/store"
)

// rpcCredentials resolves bitcoind RPC credentials the same way the node
// package does: explicit user/pass wins, otherwise the cookie file. DATUM has
// no cookie support of its own, so the cookie is inlined into its config —
// which means the config must be regenerated on every gateway start (the
// cookie rotates when bitcoind restarts).
func rpcCredentials(b config.Bitcoind) (user, pass string, err error) {
	if b.RPCUser != "" {
		return b.RPCUser, b.RPCPass, nil
	}
	if b.CookieFile == "" {
		return "", "", fmt.Errorf("no bitcoind rpc credentials configured")
	}
	raw, err := os.ReadFile(b.CookieFile)
	if err != nil {
		return "", "", fmt.Errorf("read cookie: %w", err)
	}
	parts := strings.SplitN(strings.TrimSpace(string(raw)), ":", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("malformed cookie file %s", b.CookieFile)
	}
	return parts[0], parts[1], nil
}

// datumConfigJSON renders a complete datum_gateway config for the current
// settings. Mode "ocean" points the gateway at OCEAN's DATUM pool (pooled
// payouts, self-built templates); mode "solo" leaves the pool host empty for
// non-pooled mining where a found block pays the payout address directly.
func datumConfigJSON(s store.WorkSettings, w config.Work, b config.Bitcoind) ([]byte, error) {
	if s.PayoutAddress == "" {
		return nil, fmt.Errorf("payout address is required")
	}
	user, pass, err := rpcCredentials(b)
	if err != nil {
		return nil, err
	}
	datum := map[string]any{
		"pool_pass_workers":    true,
		"pool_pass_full_users": true,
		// false = keep serving miners from local templates when the pool is
		// unreachable (or, in solo mode, always).
		"pooled_mining_only": false,
	}
	if s.Mode == "ocean" {
		datum["pool_host"] = w.OceanHost
		datum["pool_port"] = w.OceanPort
	} else {
		datum["pool_host"] = ""
	}
	cfg := map[string]any{
		"bitcoind": map[string]any{
			"rpcurl":      b.RPCURL,
			"rpcuser":     user,
			"rpcpassword": pass,
			// poll for new blocks so no blocknotify wiring is required
			"notify_fallback": true,
		},
		"stratum": map[string]any{
			"listen_port": w.StratumPort,
		},
		"mining": map[string]any{
			"pool_address":          s.PayoutAddress,
			"coinbase_tag_primary":  "NodeOS",
			"coinbase_tag_secondary": "solo",
		},
		"api": map[string]any{
			"listen_port":    w.APIPort,
			"admin_password": "",
			"modify_conf":    false,
		},
		"logger": map[string]any{
			"log_to_console":    true,
			"log_to_file":       false,
			"log_level_console": 2,
		},
		"datum": datum,
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// ValidatePayoutAddress is a light plausibility check, not full address
// validation — bitcoind rejects a bad address the moment DATUM asks it for a
// template, and the engine surfaces that error.
func ValidatePayoutAddress(addr string) error {
	addr = strings.TrimSpace(addr)
	if len(addr) < 26 || len(addr) > 90 {
		return fmt.Errorf("payout address has an implausible length")
	}
	for _, r := range addr {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return fmt.Errorf("payout address contains invalid characters")
		}
	}
	ok := false
	for _, p := range []string{"bc1", "1", "3", "tb1", "bcrt1", "m", "n", "2"} {
		if strings.HasPrefix(addr, p) {
			ok = true
			break
		}
	}
	if !ok {
		return fmt.Errorf("payout address does not look like a Bitcoin address")
	}
	return nil
}
