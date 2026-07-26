package work

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"nodeos/internal/alerts"
	"nodeos/internal/config"
	"nodeos/internal/fleet"
	"nodeos/internal/node"
	"nodeos/internal/store"
)

// ---- datum config generation ----

func TestDatumConfigCookieAuth(t *testing.T) {
	dir := t.TempDir()
	cookie := filepath.Join(dir, ".cookie")
	if err := os.WriteFile(cookie, []byte("__cookie__:s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := datumConfigJSON(
		store.WorkSettings{Enabled: true, PayoutAddress: "bc1qtestaddress0123456789abcdef", Mode: "solo"},
		config.Default().Work,
		config.Bitcoind{RPCURL: "http://127.0.0.1:8332", CookieFile: cookie},
	)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["bitcoind"]["rpcuser"] != "__cookie__" || cfg["bitcoind"]["rpcpassword"] != "s3cret" {
		t.Fatalf("cookie credentials not applied: %v", cfg["bitcoind"])
	}
	if cfg["bitcoind"]["notify_fallback"] != true {
		t.Fatal("notify_fallback must be true (no blocknotify wiring)")
	}
	if cfg["datum"]["pool_host"] != "" {
		t.Fatalf("solo mode must leave pool_host empty, got %v", cfg["datum"]["pool_host"])
	}
	if cfg["datum"]["pooled_mining_only"] != false {
		t.Fatal("pooled_mining_only must be false so solo keeps mining without a pool")
	}
}

func TestDatumConfigOceanMode(t *testing.T) {
	b, err := datumConfigJSON(
		store.WorkSettings{Enabled: true, PayoutAddress: "bc1qtestaddress0123456789abcdef", Mode: "ocean"},
		config.Default().Work,
		config.Bitcoind{RPCURL: "http://127.0.0.1:8332", RPCUser: "u", RPCPass: "p"},
	)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["datum"]["pool_host"] != "datum-beta1.mine.ocean.xyz" {
		t.Fatalf("ocean mode pool_host wrong: %v", cfg["datum"]["pool_host"])
	}
	if cfg["datum"]["pool_port"] != float64(28915) {
		t.Fatalf("ocean mode pool_port wrong: %v", cfg["datum"]["pool_port"])
	}
}

func TestDatumConfigRequiresAddress(t *testing.T) {
	_, err := datumConfigJSON(store.WorkSettings{Enabled: true}, config.Default().Work,
		config.Bitcoind{RPCUser: "u", RPCPass: "p"})
	if err == nil {
		t.Fatal("expected error for missing payout address")
	}
}

func TestValidatePayoutAddress(t *testing.T) {
	good := []string{
		"bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4",
		"1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
		"3J98t1WpEZ73CNmQviecrnyiWrnqRhWNLy",
		"tb1qw508d6qejxtdg4y5r3zarvary0c5xw7kxpjzsx",
	}
	for _, a := range good {
		if err := ValidatePayoutAddress(a); err != nil {
			t.Errorf("%s rejected: %v", a, err)
		}
	}
	bad := []string{"", "hello", "bc1q short", "xq" + strings.Repeat("y", 30), strings.Repeat("b", 100)}
	for _, a := range bad {
		if err := ValidatePayoutAddress(a); err == nil {
			t.Errorf("%q accepted, want error", a)
		}
	}
}

// ---- engine state machine ----

type fakeFleet struct {
	mu      sync.Mutex
	pool    config.Pool
	applies int
}

func (f *fakeFleet) Pool() config.Pool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pool
}
func (f *fakeFleet) SetPool(p config.Pool) {
	f.mu.Lock()
	f.pool = p
	f.mu.Unlock()
}
func (f *fakeFleet) ApplyPool(ctx context.Context, hosts []string) []fleet.ApplyResult {
	f.mu.Lock()
	f.applies++
	f.mu.Unlock()
	return nil
}
func (f *fakeFleet) applyCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.applies
}

func testEngine(t *testing.T, cfg config.Config, nodeSt node.Status, ff *fakeFleet) *Engine {
	t.Helper()
	cfg.DataDir = t.TempDir()
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	return NewEngine(cfg, st, alerts.NewFeed(), ff, func() node.Status { return nodeSt })
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestEngineDisabledByDefault(t *testing.T) {
	e := testEngine(t, config.Default(), node.Status{}, &fakeFleet{})
	e.tick(context.Background())
	if s := e.Status(); s.State != StateDisabled {
		t.Fatalf("state = %s, want disabled", s.State)
	}
}

func TestEngineWaitsForNode(t *testing.T) {
	cfg := config.Default()
	e := testEngine(t, cfg, node.Status{Available: true, IBD: true, Progress: 0.42}, &fakeFleet{})
	if err := e.UpdateSettings(context.Background(), store.WorkSettings{
		Enabled: true, PayoutAddress: "bc1qtestaddress0123456789abcdef", Mode: "solo",
	}); err != nil {
		t.Fatal(err)
	}
	e.tick(context.Background())
	if s := e.Status(); s.State != StateWaitingNode {
		t.Fatalf("state = %s, want waiting_node", s.State)
	}
}

func TestEngineDemoLifecycleAndAutoSwitch(t *testing.T) {
	cfg := config.Default()
	cfg.Demo = true
	cfg.Work.StratumPort = freePort(t)
	ff := &fakeFleet{pool: config.Pool{StratumURL: "public-pool.io", StratumPort: 21496, StratumUser: "bc1qold.worker"}}
	e := testEngine(t, cfg, node.Status{}, ff)

	if err := e.UpdateSettings(context.Background(), store.WorkSettings{
		Enabled: true, PayoutAddress: "bc1qtestaddress0123456789abcdef", Mode: "solo", AutoSwitch: true,
	}); err != nil {
		t.Fatal(err)
	}

	// first tick starts the mock listener, second sees it healthy and switches
	e.tick(context.Background())
	e.tick(context.Background())

	s := e.Status()
	if s.State != StateRunning {
		t.Fatalf("state = %s (%s), want running", s.State, s.Detail)
	}
	if !s.Switched {
		t.Fatal("auto-switch did not fire")
	}
	p := ff.Pool()
	if p.StratumURL != "127.0.0.1" || p.StratumPort != cfg.Work.StratumPort {
		t.Fatalf("fleet pool not pointed at engine: %+v", p)
	}
	if p.StratumUser != "bc1qtestaddress0123456789abcdef.{worker}" {
		t.Fatalf("stratum user wrong: %s", p.StratumUser)
	}
	if p.FallbackURL != "public-pool.io" || p.FallbackPort != 21496 {
		t.Fatalf("previous pool not kept as fallback: %+v", p)
	}
	// external pool remembered for switch-back
	if ext := e.store.Get().ExternalPool; ext == nil || ext.StratumURL != "public-pool.io" {
		t.Fatalf("external pool not remembered: %+v", ext)
	}

	// switch back restores the previous pool
	if err := e.SwitchToExternal(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p := ff.Pool(); p.StratumURL != "public-pool.io" {
		t.Fatalf("switch back failed: %+v", p)
	}
	if e.Status().Switched {
		t.Fatal("switched flag still set after switch back")
	}

	// disabling stops the mock listener
	if err := e.UpdateSettings(context.Background(), store.WorkSettings{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	e.tick(context.Background())
	if s := e.Status(); s.State != StateDisabled || s.UptimeS != 0 {
		t.Fatalf("state after disable = %s uptime %d, want disabled/0", s.State, s.UptimeS)
	}
}

func TestEngineAutoSwitchFiresOncePerEnableCycle(t *testing.T) {
	cfg := config.Default()
	cfg.Demo = true
	cfg.Work.StratumPort = freePort(t)
	ff := &fakeFleet{pool: config.Pool{StratumURL: "public-pool.io", StratumPort: 21496, StratumUser: "u"}}
	e := testEngine(t, cfg, node.Status{}, ff)
	if err := e.UpdateSettings(context.Background(), store.WorkSettings{
		Enabled: true, PayoutAddress: "bc1qtestaddress0123456789abcdef", Mode: "solo", AutoSwitch: true,
	}); err != nil {
		t.Fatal(err)
	}
	e.tick(context.Background())
	e.tick(context.Background())
	if !e.Status().Switched {
		t.Fatal("expected switch")
	}
	// user takes manual control: engine must notice and never override
	ff.SetPool(config.Pool{StratumURL: "solo.ckpool.org", StratumPort: 3333, StratumUser: "u"})
	e.tick(context.Background())
	if e.Status().Switched {
		t.Fatal("switched flag should drop after manual pool change")
	}
	e.tick(context.Background())
	if got := ff.Pool().StratumURL; got != "solo.ckpool.org" {
		t.Fatalf("engine overrode the user's manual pool choice: %s", got)
	}
}

func TestEngineCrashBackoff(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("needs /bin/sh")
	}
	cfg := config.Default()
	cfg.Work.StratumPort = freePort(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "crasher.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho boom >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg.Work.BinaryPath = script
	ff := &fakeFleet{}
	e := testEngine(t, cfg, node.Status{Available: true, IBD: false, Progress: 1}, ff)
	if err := e.UpdateSettings(context.Background(), store.WorkSettings{
		Enabled: true, PayoutAddress: "bc1qtestaddress0123456789abcdef", Mode: "solo",
	}); err != nil {
		t.Fatal(err)
	}
	// engine config needs rpc creds for the datum config file
	e.btc = config.Bitcoind{RPCURL: "http://127.0.0.1:8332", RPCUser: "u", RPCPass: "p"}

	e.tick(context.Background())
	deadline := time.Now().Add(3 * time.Second)
	for e.Status().Restarts == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	s := e.Status()
	if s.Restarts == 0 {
		t.Fatal("crash was not recorded")
	}
	e.tick(context.Background())
	if s := e.Status(); s.State != StateBackoff {
		t.Fatalf("state = %s, want backoff", s.State)
	}
	if tail := e.LogTail(10); len(tail) == 0 || !strings.Contains(strings.Join(tail, "\n"), "boom") {
		t.Fatalf("process stderr not captured: %v", tail)
	}
}
