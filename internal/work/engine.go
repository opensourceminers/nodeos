// Package work supervises the solo-mining work engine: a DATUM Gateway
// process that builds block templates from the local bitcoind and serves
// stratum work to the fleet. The engine owns the process lifecycle (start,
// health check, crash backoff) and the "magic moment": once the node is
// synced and the gateway healthy, it can point the whole fleet at itself,
// keeping the previous pool as per-device fallback.
package work

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"nodeos/internal/alerts"
	"nodeos/internal/config"
	"nodeos/internal/fleet"
	"nodeos/internal/node"
	"nodeos/internal/store"
)

type State string

const (
	StateDisabled    State = "disabled"
	StateWaitingNode State = "waiting_node"
	StateStarting    State = "starting"
	StateRunning     State = "running"
	StateBackoff     State = "backoff"
	StateError       State = "error"
)

const (
	tickInterval   = 3 * time.Second
	backoffInitial = 5 * time.Second
	backoffMax     = 2 * time.Minute
	// syncedProgress is the verification progress treated as "synced"; GBT
	// itself refuses to serve templates while bitcoind considers itself in IBD.
	syncedProgress = 0.9995
	logKeep        = 200
)

// FleetControl is the slice of fleet.Manager the engine needs.
type FleetControl interface {
	Pool() config.Pool
	SetPool(config.Pool)
	ApplyPool(ctx context.Context, hosts []string) []fleet.ApplyResult
}

type Engine struct {
	work    config.Work
	btc     config.Bitcoind
	demo    bool
	dataDir string

	nodeStatus func() node.Status
	fleet      FleetControl
	store      *store.Store
	feed       *alerts.Feed

	mu        sync.Mutex
	settings  store.WorkSettings
	state     State
	detail    string
	switched  bool // fleet currently pointed at the engine
	autoDone  bool // auto-switch already fired this enable cycle
	cmd       *exec.Cmd
	mock      net.Listener
	startedAt time.Time
	healthyAt time.Time
	restarts  int
	lastExit  string
	nextStart time.Time
	backoff   time.Duration

	logMu    sync.Mutex
	logLines []string
	logBuf   []byte
}

func NewEngine(cfg config.Config, st *store.Store, feed *alerts.Feed, fc FleetControl, nodeStatus func() node.Status) *Engine {
	e := &Engine{
		work:       cfg.Work,
		btc:        cfg.Bitcoind,
		demo:       cfg.Demo,
		dataDir:    cfg.DataDir,
		nodeStatus: nodeStatus,
		fleet:      fc,
		store:      st,
		feed:       feed,
		state:      StateDisabled,
	}
	e.settings = st.Get().Work
	if e.settings.Mode == "" {
		e.settings.Mode = "solo"
	}
	return e
}

// Run drives the engine until ctx is cancelled; the supervised process is
// stopped on the way out.
func (e *Engine) Run(ctx context.Context) {
	t := time.NewTicker(tickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			e.mu.Lock()
			e.stopLocked()
			e.mu.Unlock()
			return
		case <-t.C:
			e.tick(ctx)
		}
	}
}

func (e *Engine) tick(ctx context.Context) {
	e.mu.Lock()
	s := e.settings
	if !s.Enabled {
		e.stopLocked()
		e.state, e.detail = StateDisabled, "work engine is off"
		e.mu.Unlock()
		return
	}

	ns := e.nodeStatus()
	ready := e.demo || (ns.Available && !ns.IBD && ns.Progress >= syncedProgress)
	if !ready {
		e.stopLocked()
		if !ns.Available {
			e.detail = "waiting for bitcoind (unreachable)"
		} else {
			e.detail = fmt.Sprintf("waiting for node sync: %.2f %%", ns.Progress*100)
		}
		e.state = StateWaitingNode
		e.mu.Unlock()
		return
	}

	if !e.runningLocked() {
		if time.Now().Before(e.nextStart) {
			e.state = StateBackoff
			e.detail = fmt.Sprintf("restarting in %s (%s)", time.Until(e.nextStart).Round(time.Second), e.lastExit)
			e.mu.Unlock()
			return
		}
		if err := e.startLocked(); err != nil {
			e.state, e.detail = StateError, err.Error()
			e.nextStart = time.Now().Add(30 * time.Second)
			e.mu.Unlock()
			return
		}
		e.state, e.detail = StateStarting, "process started, waiting for stratum port"
	}
	port := e.work.StratumPort
	wasRunning := e.state == StateRunning
	e.mu.Unlock()

	healthy := dialOK(port)

	e.mu.Lock()
	if healthy {
		if !wasRunning {
			e.feed.Add(alerts.Info, "work_engine_up", "",
				fmt.Sprintf("Work engine is serving templates from your node on port %d", port))
		}
		e.state = StateRunning
		e.detail = e.runningDetail()
		if e.healthyAt.IsZero() {
			e.healthyAt = time.Now()
		}
		if time.Since(e.healthyAt) > time.Minute {
			e.backoff = 0 // stable: reset crash backoff
		}
	} else {
		e.healthyAt = time.Time{}
		if wasRunning || time.Since(e.startedAt) > 30*time.Second {
			e.state, e.detail = StateStarting, "stratum port not answering"
		}
	}
	doAuto := e.state == StateRunning && s.AutoSwitch && !e.switched && !e.autoDone
	if doAuto {
		e.autoDone = true
	}
	e.mu.Unlock()

	// If the user re-pointed the fleet manually, drop the switched flag so the
	// UI reflects reality (but never auto-override their choice).
	cur := e.fleet.Pool()
	e.mu.Lock()
	if e.switched && !e.poolPointsAtEngineLocked(cur) {
		e.switched = false
	}
	e.mu.Unlock()

	if doAuto {
		if err := e.SwitchToEngine(ctx); err != nil {
			e.feed.Add(alerts.Warning, "work_engine_switch_failed", "",
				fmt.Sprintf("Could not point the fleet at the work engine: %v", err))
		}
	}
}

func (e *Engine) runningDetail() string {
	if e.settings.Mode == "ocean" {
		return "pooled via OCEAN DATUM — templates built by your node"
	}
	return "pure solo — a found block pays your address directly"
}

// ---- process lifecycle (all *Locked helpers expect e.mu held) ----

func (e *Engine) runningLocked() bool { return e.cmd != nil || e.mock != nil }

func (e *Engine) startLocked() error {
	if e.demo {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", e.work.StratumPort))
		if err != nil {
			return fmt.Errorf("demo listener: %w", err)
		}
		e.mock = ln
		go acceptAndClose(ln)
		e.startedAt = time.Now()
		e.appendLog("[demo] simulated work engine listening on :" + fmt.Sprint(e.work.StratumPort))
		return nil
	}

	if _, err := os.Stat(e.work.BinaryPath); err != nil {
		return fmt.Errorf("datum_gateway not found at %s — install it with install.sh --with-datum", e.work.BinaryPath)
	}
	cfgBytes, err := datumConfigJSON(e.settings, e.work, e.btc)
	if err != nil {
		return err
	}
	cfgPath := filepath.Join(e.dataDir, "datum_gateway_config.json")
	if err := os.WriteFile(cfgPath, cfgBytes, 0o600); err != nil {
		return fmt.Errorf("write gateway config: %w", err)
	}

	cmd := exec.Command(e.work.BinaryPath, "-c", cfgPath)
	lw := &logWriter{e: e}
	cmd.Stdout = lw
	cmd.Stderr = lw
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start datum_gateway: %w", err)
	}
	e.cmd = cmd
	e.startedAt = time.Now()
	e.healthyAt = time.Time{}
	go e.wait(cmd)
	return nil
}

// wait reaps the process and schedules a restart unless the exit was ours.
func (e *Engine) wait(cmd *exec.Cmd) {
	err := cmd.Wait()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cmd != cmd {
		return // stopLocked already disowned it: deliberate shutdown
	}
	e.cmd = nil
	if err != nil {
		e.lastExit = err.Error()
	} else {
		e.lastExit = "exited cleanly"
	}
	e.restarts++
	if e.backoff == 0 {
		e.backoff = backoffInitial
	} else if e.backoff < backoffMax {
		e.backoff *= 2
	}
	e.nextStart = time.Now().Add(e.backoff)
	e.feed.Add(alerts.Warning, "work_engine_exit", "",
		fmt.Sprintf("Work engine exited (%s) — restarting in %s", e.lastExit, e.backoff))
}

func (e *Engine) stopLocked() {
	if e.mock != nil {
		e.mock.Close()
		e.mock = nil
	}
	if e.cmd != nil {
		cmd := e.cmd
		e.cmd = nil // disown so wait() doesn't schedule a restart
		if cmd.Process != nil {
			cmd.Process.Signal(syscall.SIGTERM)
			go func() {
				time.Sleep(5 * time.Second)
				cmd.Process.Kill()
			}()
		}
	}
}

func dialOK(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// acceptAndClose keeps the demo listener draining so health checks succeed.
func acceptAndClose(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		c.Close()
	}
}

// ---- fleet switching ----

// AdvertiseHost is the address miners are pointed at: config override first,
// then the machine's primary private IPv4.
func (e *Engine) AdvertiseHost() string {
	if e.work.AdvertiseHost != "" {
		return e.work.AdvertiseHost
	}
	if e.demo {
		return "127.0.0.1"
	}
	return lanIP()
}

func lanIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok {
				if ip4 := ipnet.IP.To4(); ip4 != nil && ip4.IsPrivate() {
					return ip4.String()
				}
			}
		}
	}
	return ""
}

func (e *Engine) poolPointsAtEngineLocked(p config.Pool) bool {
	host := e.AdvertiseHost()
	return host != "" && p.StratumURL == host && p.StratumPort == e.work.StratumPort
}

// SwitchToEngine points the fleet at the engine: current pool becomes the
// per-device fallback and is remembered for SwitchToExternal.
func (e *Engine) SwitchToEngine(ctx context.Context) error {
	e.mu.Lock()
	if e.state != StateRunning {
		e.mu.Unlock()
		return fmt.Errorf("work engine is not running (state: %s)", e.state)
	}
	s := e.settings
	host := e.AdvertiseHost()
	port := e.work.StratumPort
	e.mu.Unlock()
	if host == "" {
		return fmt.Errorf("cannot determine this machine's LAN address; set work.advertise_host in the config")
	}

	cur := e.fleet.Pool()
	e.mu.Lock()
	if e.poolPointsAtEngineLocked(cur) {
		e.switched = true
		e.mu.Unlock()
		return nil
	}
	e.switched = true
	e.mu.Unlock()

	if cur.StratumURL != "" {
		cp := cur
		e.store.Update(func(st *store.State) { st.ExternalPool = &cp })
	}
	e.fleet.SetPool(config.Pool{
		StratumURL:   host,
		StratumPort:  port,
		StratumUser:  s.PayoutAddress + ".{worker}",
		FallbackURL:  cur.StratumURL,
		FallbackPort: cur.StratumPort,
		FallbackUser: cur.StratumUser,
	})
	e.feed.Add(alerts.Party, "work_engine_switched", "",
		fmt.Sprintf("Fleet is switching to YOUR node: %s:%d (previous pool kept as fallback)", host, port))
	go func() {
		actx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		e.fleet.ApplyPool(actx, nil)
	}()
	return nil
}

// SwitchToExternal restores the pool that was active before SwitchToEngine.
func (e *Engine) SwitchToExternal(ctx context.Context) error {
	ext := e.store.Get().ExternalPool
	if ext == nil {
		return fmt.Errorf("no previous pool remembered — set one under Settings → Pool")
	}
	e.mu.Lock()
	e.switched = false
	e.autoDone = true // don't immediately switch back again
	e.mu.Unlock()
	e.fleet.SetPool(*ext)
	e.feed.Add(alerts.Info, "work_engine_switched_back", "",
		fmt.Sprintf("Fleet is switching back to %s:%d", ext.StratumURL, ext.StratumPort))
	go func() {
		actx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		e.fleet.ApplyPool(actx, nil)
	}()
	return nil
}

// ---- settings & status ----

func (e *Engine) Settings() store.WorkSettings {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.settings
}

// UpdateSettings validates, persists and applies new settings. Disabling the
// engine switches the fleet back to the remembered external pool.
func (e *Engine) UpdateSettings(ctx context.Context, s store.WorkSettings) error {
	if s.Mode == "" {
		s.Mode = "solo"
	}
	if s.Mode != "solo" && s.Mode != "ocean" {
		return fmt.Errorf("mode must be \"solo\" or \"ocean\"")
	}
	if s.Enabled {
		if err := ValidatePayoutAddress(s.PayoutAddress); err != nil {
			return err
		}
	}

	e.mu.Lock()
	old := e.settings
	e.settings = s
	if s.Enabled != old.Enabled || (s.AutoSwitch && !old.AutoSwitch) {
		e.autoDone = false // new enable cycle: auto-switch may fire again
	}
	restart := e.runningLocked() && (s.PayoutAddress != old.PayoutAddress || s.Mode != old.Mode)
	if restart || !s.Enabled {
		e.stopLocked()
		e.nextStart = time.Time{}
		e.backoff = 0
	}
	wasSwitched := e.switched
	e.mu.Unlock()

	if err := e.store.Update(func(st *store.State) { st.Work = s }); err != nil {
		return err
	}
	if !s.Enabled && wasSwitched {
		return e.SwitchToExternal(ctx)
	}
	return nil
}

type StatusView struct {
	State       State              `json:"state"`
	Detail      string             `json:"detail,omitempty"`
	Settings    store.WorkSettings `json:"settings"`
	Endpoint    string             `json:"endpoint,omitempty"`
	Switched    bool               `json:"switched"`
	Restarts    int                `json:"restarts"`
	UptimeS     int64              `json:"uptime_s"`
	LastExit    string             `json:"last_exit,omitempty"`
	Mock        bool               `json:"mock"`
	BinaryFound bool               `json:"binary_found"`
}

func (e *Engine) Status() StatusView {
	e.mu.Lock()
	defer e.mu.Unlock()
	v := StatusView{
		State:    e.state,
		Detail:   e.detail,
		Settings: e.settings,
		Switched: e.switched,
		Restarts: e.restarts,
		LastExit: e.lastExit,
		Mock:     e.mock != nil,
	}
	if host := e.AdvertiseHost(); host != "" {
		v.Endpoint = fmt.Sprintf("%s:%d", host, e.work.StratumPort)
	}
	if e.runningLocked() {
		v.UptimeS = int64(time.Since(e.startedAt).Seconds())
	}
	if e.demo {
		v.BinaryFound = true
	} else {
		_, err := os.Stat(e.work.BinaryPath)
		v.BinaryFound = err == nil
	}
	return v
}

// ---- log capture ----

func (e *Engine) appendLog(line string) {
	e.logMu.Lock()
	e.logLines = append(e.logLines, line)
	if len(e.logLines) > logKeep {
		e.logLines = e.logLines[len(e.logLines)-logKeep:]
	}
	e.logMu.Unlock()
}

func (e *Engine) LogTail(n int) []string {
	e.logMu.Lock()
	defer e.logMu.Unlock()
	if n <= 0 || n > len(e.logLines) {
		n = len(e.logLines)
	}
	return append([]string(nil), e.logLines[len(e.logLines)-n:]...)
}

// logWriter splits process output into lines for the ring buffer.
type logWriter struct{ e *Engine }

func (w *logWriter) Write(p []byte) (int, error) {
	e := w.e
	e.logMu.Lock()
	e.logBuf = append(e.logBuf, p...)
	for {
		i := -1
		for j, b := range e.logBuf {
			if b == '\n' {
				i = j
				break
			}
		}
		if i < 0 {
			break
		}
		line := string(e.logBuf[:i])
		e.logBuf = e.logBuf[i+1:]
		if line != "" {
			e.logLines = append(e.logLines, line)
			if len(e.logLines) > logKeep {
				e.logLines = e.logLines[len(e.logLines)-logKeep:]
			}
		}
	}
	e.logMu.Unlock()
	return len(p), nil
}
