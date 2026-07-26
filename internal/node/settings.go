package node

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Setting describes one bitcoin.conf option NodeOS exposes. The schema is
// served to the UI, which renders the form generically — adding an option
// here is all it takes to make it configurable.
type Setting struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"` // int | bool | enum
	Unit     string   `json:"unit,omitempty"`
	Min      int64    `json:"min,omitempty"`
	Max      int64    `json:"max,omitempty"`
	Options  []string `json:"options,omitempty"`
	Default  string   `json:"default"`
	Help     string   `json:"help"`
	Group    string   `json:"group"`
	Reindex  bool     `json:"reindex,omitempty"`  // change forces a resync/reindex
	KnotsOnly bool    `json:"knots_only,omitempty"`
}

// Schema is the curated set of options NodeOS manages. Anything outside this
// list stays untouched in bitcoin.conf — power users keep full control of the
// file, we only own these keys.
func Schema() []Setting {
	return []Setting{
		// ---- storage ----
		{
			Key: "prune", Label: "Pruning", Type: "int", Unit: "MiB", Min: 0, Max: 8_000_000,
			Default: "0", Group: "Storage",
			Help: "0 keeps the full chain (~700 GB). Any other value is the target size for block storage; 20000 (20 GiB) is a good choice for a mining node. Lowering from full to pruned is instant; going from pruned back to full re-downloads the chain.",
		},
		{
			Key: "dbcache", Label: "Database cache", Type: "int", Unit: "MB", Min: 4, Max: 65536,
			Default: "450", Group: "Storage",
			Help: "Memory for the UTXO cache. More cache means a much faster initial sync — 2000–4000 is worth it while syncing if the machine has the RAM to spare.",
		},
		{
			Key: "txindex", Label: "Full transaction index", Type: "bool", Default: "0",
			Group: "Storage", Reindex: true,
			Help: "Lets the node look up any transaction by ID (needed by some explorers and wallets). Costs ~40 GB and a full reindex. Cannot be combined with pruning.",
		},
		{
			Key: "blockfilterindex", Label: "Block filter index", Type: "bool", Default: "0",
			Group: "Storage", Reindex: true,
			Help: "Builds compact block filters (BIP158) so light wallets can sync privately against your node. Costs a few GB.",
		},
		{
			Key: "coinstatsindex", Label: "Coin statistics index", Type: "bool", Default: "0",
			Group: "Storage", Reindex: true,
			Help: "Precomputes UTXO set statistics (supply, UTXO count). Nice for dashboards, not required for mining.",
		},

		// ---- network ----
		{
			Key: "maxconnections", Label: "Maximum connections", Type: "int", Min: 8, Max: 1000,
			Default: "125", Group: "Network",
			Help: "Upper limit of simultaneous peer connections. Lower it on a small box or a metered line.",
		},
		{
			Key: "listen", Label: "Accept inbound connections", Type: "bool", Default: "1",
			Group: "Network",
			Help: "Serve blocks to other nodes. Requires port 8333 to be reachable from the internet to have any effect.",
		},
		{
			Key: "maxuploadtarget", Label: "Upload limit", Type: "int", Unit: "MiB/day", Min: 0, Max: 1_000_000,
			Default: "0", Group: "Network",
			Help: "0 means unlimited. Blocks of the last 24 h and transaction relay are never throttled, so a limit does not hurt your own mining.",
		},
		{
			Key: "onlynet", Label: "Restrict network", Type: "enum",
			Options: []string{"", "onion", "ipv4", "ipv6"}, Default: "", Group: "Network",
			Help: "Empty means use every available network. Set to onion to talk to peers exclusively over Tor (Tor must be installed and reachable).",
		},
		{
			Key: "proxy", Label: "SOCKS5 proxy", Type: "string", Default: "", Group: "Network",
			Help: "Route peer traffic through a proxy, e.g. 127.0.0.1:9050 for a local Tor daemon.",
		},

		// ---- mempool & relay policy ----
		{
			Key: "maxmempool", Label: "Mempool size", Type: "int", Unit: "MB", Min: 5, Max: 32768,
			Default: "300", Group: "Mempool",
			Help: "Larger mempool means more fee-paying transactions to choose from when your node builds a block template.",
		},
		{
			Key: "mempoolexpiry", Label: "Mempool expiry", Type: "int", Unit: "hours", Min: 1, Max: 8760,
			Default: "336", Group: "Mempool",
			Help: "How long an unconfirmed transaction stays in the mempool.",
		},
		{
			Key: "minrelaytxfee", Label: "Minimum relay fee", Type: "string", Unit: "BTC/kvB",
			Default: "0.00001", Group: "Mempool",
			Help: "Transactions paying less are neither relayed nor mined by your templates.",
		},
		{
			Key: "blockmaxweight", Label: "Block template weight", Type: "int", Unit: "WU", Min: 4000, Max: 4_000_000,
			Default: "3996000", Group: "Mempool",
			Help: "Maximum weight of blocks your node builds. Only lower this if you know exactly why.",
		},

		// ---- Knots-specific policy ----
		{
			Key: "datacarrier", Label: "Relay OP_RETURN data", Type: "bool", Default: "1",
			Group: "Policy (Knots)", KnotsOnly: true,
			Help: "Bitcoin Knots lets you refuse to relay and mine data-carrier outputs. Turning this off is a policy choice, not a consensus rule — other nodes may still relay them.",
		},
		{
			Key: "datacarriersize", Label: "Max data-carrier size", Type: "int", Unit: "bytes", Min: 0, Max: 100000,
			Default: "83", Group: "Policy (Knots)", KnotsOnly: true,
			Help: "Upper size for OP_RETURN payloads your node accepts.",
		},
		{
			Key: "permitbaremultisig", Label: "Permit bare multisig", Type: "bool", Default: "1",
			Group: "Policy (Knots)", KnotsOnly: true,
			Help: "Bare multisig outputs are one common way of embedding arbitrary data in the chain.",
		},
	}
}

// ReadConf parses a bitcoin.conf into a key→value map. Section headers such
// as [main] are ignored: NodeOS only manages global keys.
func ReadConf(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out, sc.Err()
}

// Values returns the effective value of every schema key: what the config
// file says, else the documented default.
func Values(confPath string) (map[string]string, error) {
	conf, err := ReadConf(confPath)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, s := range Schema() {
		if v, ok := conf[s.Key]; ok {
			out[s.Key] = v
		} else {
			out[s.Key] = s.Default
		}
	}
	return out, nil
}

// ValidateSettings checks a set of key/value pairs against the schema and
// returns them normalised. Unknown keys are rejected — the helper applies
// only what passes here (and validates again on its side).
func ValidateSettings(in map[string]string) (map[string]string, error) {
	schema := map[string]Setting{}
	for _, s := range Schema() {
		schema[s.Key] = s
	}
	out := map[string]string{}
	for k, v := range in {
		s, ok := schema[k]
		if !ok {
			return nil, fmt.Errorf("unknown setting %q", k)
		}
		v = strings.TrimSpace(v)
		switch s.Type {
		case "int":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("%s: not a number", s.Label)
			}
			if n < s.Min || (s.Max > 0 && n > s.Max) {
				return nil, fmt.Errorf("%s: must be between %d and %d", s.Label, s.Min, s.Max)
			}
			out[k] = strconv.FormatInt(n, 10)
		case "bool":
			switch v {
			case "0", "1":
				out[k] = v
			case "true":
				out[k] = "1"
			case "false":
				out[k] = "0"
			default:
				return nil, fmt.Errorf("%s: must be 0 or 1", s.Label)
			}
		case "enum":
			valid := false
			for _, o := range s.Options {
				if v == o {
					valid = true
				}
			}
			if !valid {
				return nil, fmt.Errorf("%s: %q is not an allowed value", s.Label, v)
			}
			out[k] = v
		case "string":
			// conservative: no whitespace, no shell metacharacters, no newlines
			if strings.ContainsAny(v, " \t\n\r;|&$`'\"\\") {
				return nil, fmt.Errorf("%s: contains invalid characters", s.Label)
			}
			if len(v) > 120 {
				return nil, fmt.Errorf("%s: too long", s.Label)
			}
			out[k] = v
		default:
			return nil, fmt.Errorf("%s: unsupported type", s.Label)
		}
	}
	if err := crossCheck(out); err != nil {
		return nil, err
	}
	return out, nil
}

// crossCheck rejects combinations bitcoind itself would refuse to start with,
// so the user sees the problem in the form instead of a dead node.
func crossCheck(v map[string]string) error {
	prune, hasPrune := v["prune"]
	if hasPrune && prune != "0" {
		if n, err := strconv.ParseInt(prune, 10, 64); err == nil && n > 0 && n < 550 {
			return fmt.Errorf("Pruning: the smallest allowed target is 550 MiB (or 0 for a full node)")
		}
		for _, idx := range []string{"txindex", "coinstatsindex"} {
			if v[idx] == "1" {
				return fmt.Errorf("%s cannot be enabled on a pruned node — set pruning to 0 first", idx)
			}
		}
	}
	return nil
}
