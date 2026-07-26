// Package fleet is the core of nodeosd: it discovers AxeOS-family miners on
// the LAN, polls them, keeps short-term telemetry history, raises alerts and
// pushes pool configuration to the whole fleet.
package fleet

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"nodeos/internal/alerts"
	"nodeos/internal/axeos"
	"nodeos/internal/config"
	"nodeos/internal/store"
)

const (
	historyLen     = 360 // at 10s polling: 1 hour
	offlineAfter   = 3   // consecutive failed polls before a miner is offline
	scanConcurrency = 64
)

type Sample struct {
	T      int64   `json:"t"` // unix seconds
	HashGH float64 `json:"h"`
	TempC  float64 `json:"c"`
	PowerW float64 `json:"p"`
}

type Miner struct {
	Host      string      `json:"host"`
	Name      string      `json:"name,omitempty"`
	Source    string      `json:"source"`
	Online    bool        `json:"online"`
	FirstSeen time.Time   `json:"first_seen"`
	LastSeen  time.Time   `json:"last_seen"`
	LastError string      `json:"last_error,omitempty"`
	Info      *axeos.Info `json:"info,omitempty"`

	failCount int
	history   [historyLen]Sample
	histIdx   int
	histCount int
	// bestDiffSeen tracks the highest parsed bestSessionDiff so the block
	// candidate alert fires only once per new record.
	bestDiffSeen float64
}

func (m *Miner) appendSample(s Sample) {
	m.history[m.histIdx] = s
	m.histIdx = (m.histIdx + 1) % historyLen
	if m.histCount < historyLen {
		m.histCount++
	}
}

// History returns up to n most recent samples, oldest first.
func (m *Miner) History(n int) []Sample {
	if n > m.histCount {
		n = m.histCount
	}
	out := make([]Sample, 0, n)
	start := (m.histIdx - n + historyLen*2) % historyLen
	for i := 0; i < n; i++ {
		out = append(out, m.history[(start+i)%historyLen])
	}
	return out
}

type ScanStatus struct {
	Running  bool      `json:"running"`
	CIDR     string    `json:"cidr,omitempty"`
	Scanned  int       `json:"scanned"`
	Total    int       `json:"total"`
	Found    []string  `json:"found"`
	LastEnd  time.Time `json:"last_end,omitzero"`
}

type Manager struct {
	cfg    config.Config
	client *axeos.Client
	store  *store.Store
	feed   *alerts.Feed

	mu     sync.RWMutex
	miners map[string]*Miner
	pool   config.Pool

	scanMu sync.Mutex
	scan   ScanStatus

	// onTick is invoked after every poll round (used for SSE broadcasts).
	onTick func()

	// networkDifficulty is fed from the node client for block-candidate checks.
	diffMu            sync.Mutex
	networkDifficulty float64
}

func NewManager(cfg config.Config, st *store.Store, feed *alerts.Feed) *Manager {
	m := &Manager{
		cfg:    cfg,
		client: axeos.NewClient(3 * time.Second),
		store:  st,
		feed:   feed,
		miners: map[string]*Miner{},
	}
	state := st.Get()
	m.pool = state.Pool
	if m.pool.StratumURL == "" {
		m.pool = cfg.Pool // seed from config on first run
	}
	for _, pm := range state.Miners {
		if pm.Source == "sim" {
			continue // sim miners are re-created per run, ports change
		}
		m.miners[pm.Host] = &Miner{Host: pm.Host, Name: pm.Name, Source: pm.Source, FirstSeen: time.Now()}
	}
	return m
}

func (m *Manager) OnTick(fn func()) { m.onTick = fn }

func (m *Manager) SetNetworkDifficulty(d float64) {
	m.diffMu.Lock()
	m.networkDifficulty = d
	m.diffMu.Unlock()
}

func (m *Manager) getNetworkDifficulty() float64 {
	m.diffMu.Lock()
	defer m.diffMu.Unlock()
	return m.networkDifficulty
}

// Run starts the poll loop; blocks until ctx is cancelled.
func (m *Manager) Run(ctx context.Context) {
	interval := time.Duration(m.cfg.PollIntervalSec) * time.Second
	m.pollAll(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.pollAll(ctx)
		}
	}
}

func (m *Manager) pollAll(ctx context.Context) {
	m.mu.RLock()
	hosts := make([]string, 0, len(m.miners))
	for h := range m.miners {
		hosts = append(hosts, h)
	}
	m.mu.RUnlock()

	var wg sync.WaitGroup
	for _, host := range hosts {
		wg.Add(1)
		go func(host string) {
			defer wg.Done()
			m.pollOne(ctx, host)
		}(host)
	}
	wg.Wait()
	if m.onTick != nil {
		m.onTick()
	}
}

func (m *Manager) pollOne(ctx context.Context, host string) {
	cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	info, err := m.client.GetInfo(cctx, host)

	m.mu.Lock()
	defer m.mu.Unlock()
	miner, ok := m.miners[host]
	if !ok {
		return // removed while polling
	}
	if err != nil {
		miner.failCount++
		miner.LastError = err.Error()
		if miner.Online && miner.failCount >= offlineAfter {
			miner.Online = false
			m.feed.Add(alerts.Critical, "miner_offline", host,
				fmt.Sprintf("%s is unreachable", miner.Label()))
		}
		return
	}
	wasOffline := !miner.Online && miner.histCount > 0
	miner.failCount = 0
	miner.LastError = ""
	miner.Online = true
	miner.LastSeen = time.Now()
	if miner.FirstSeen.IsZero() {
		miner.FirstSeen = miner.LastSeen
	}
	miner.Info = info
	miner.appendSample(Sample{
		T: time.Now().Unix(), HashGH: info.HashRate, TempC: info.Temp, PowerW: info.Power,
	})
	if wasOffline {
		m.feed.Add(alerts.Info, "miner_online", host,
			fmt.Sprintf("%s is back online", miner.Label()))
	}
	if info.Temp >= m.cfg.Alerts.TempMaxC && info.Temp > 0 {
		m.feed.Add(alerts.Warning, "temp_high", host,
			fmt.Sprintf("%s chip temp %.0f°C (limit %.0f°C)", miner.Label(), info.Temp, m.cfg.Alerts.TempMaxC))
	}
	// Block candidate: a session-best share at or above network difficulty
	// would have solved a block on whatever work the device was mining.
	if d := axeos.ParseDiff(string(info.BestSessionDiff)); d > miner.bestDiffSeen {
		miner.bestDiffSeen = d
		if nd := m.getNetworkDifficulty(); nd > 0 && d >= nd {
			m.feed.Add(alerts.Party, "block_candidate", host,
				fmt.Sprintf("%s found a share ≥ network difficulty — possible BLOCK! Check your pool/node now.", miner.Label()))
		}
	}
}

func (m *Miner) Label() string {
	if m.Name != "" {
		return m.Name
	}
	if m.Info != nil && m.Info.Hostname != "" {
		return m.Info.Hostname
	}
	return m.Host
}

// ---- registry ----

func (m *Manager) AddMiner(host, source, name string) (*Miner, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil, fmt.Errorf("empty host")
	}
	m.mu.Lock()
	if existing, ok := m.miners[host]; ok {
		m.mu.Unlock()
		return existing, nil
	}
	miner := &Miner{Host: host, Source: source, Name: name, FirstSeen: time.Now()}
	m.miners[host] = miner
	m.mu.Unlock()

	m.persist()
	go m.pollOne(context.Background(), host)
	return miner, nil
}

func (m *Manager) RemoveMiner(host string) bool {
	m.mu.Lock()
	_, ok := m.miners[host]
	delete(m.miners, host)
	m.mu.Unlock()
	if ok {
		m.persist()
	}
	return ok
}

func (m *Manager) RenameMiner(host, name string) bool {
	m.mu.Lock()
	miner, ok := m.miners[host]
	if ok {
		miner.Name = name
	}
	m.mu.Unlock()
	if ok {
		m.persist()
	}
	return ok
}

func (m *Manager) persist() {
	m.mu.RLock()
	pms := make([]store.PersistedMiner, 0, len(m.miners))
	for _, miner := range m.miners {
		if miner.Source == "sim" {
			continue
		}
		pms = append(pms, store.PersistedMiner{Host: miner.Host, Source: miner.Source, Name: miner.Name})
	}
	pool := m.pool
	m.mu.RUnlock()
	sort.Slice(pms, func(i, j int) bool { return pms[i].Host < pms[j].Host })
	if err := m.store.Update(func(s *store.State) {
		s.Miners = pms
		s.Pool = pool
	}); err != nil {
		log.Printf("store: %v", err)
	}
}

// ---- snapshots for the API ----

type MinerView struct {
	Host      string      `json:"host"`
	Name      string      `json:"name,omitempty"`
	Label     string      `json:"label"`
	Source    string      `json:"source"`
	Online    bool        `json:"online"`
	LastSeen  time.Time   `json:"last_seen,omitzero"`
	LastError string      `json:"last_error,omitempty"`
	Info      *axeos.Info `json:"info,omitempty"`
	History   []Sample    `json:"history"`
}

type Summary struct {
	Count        int     `json:"count"`
	Online       int     `json:"online"`
	TotalHashGH  float64 `json:"total_hash_gh"`
	TotalPowerW  float64 `json:"total_power_w"`
	AvgTempC     float64 `json:"avg_temp_c"`
	BestDiff     float64 `json:"best_diff"`
	BestDiffStr  string  `json:"best_diff_str"`
	SharesAcc    int64   `json:"shares_accepted"`
	SharesRej    int64   `json:"shares_rejected"`
	EfficiencyJT float64 `json:"efficiency_j_th"` // joules per TH
}

func (m *Manager) Miners(histSamples int) []MinerView {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]MinerView, 0, len(m.miners))
	for _, miner := range m.miners {
		out = append(out, MinerView{
			Host: miner.Host, Name: miner.Name, Label: miner.Label(),
			Source: miner.Source, Online: miner.Online,
			LastSeen: miner.LastSeen, LastError: miner.LastError,
			Info: miner.Info, History: miner.History(histSamples),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out
}

func (m *Manager) Summary() Summary {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var s Summary
	var tempSum float64
	var tempN int
	for _, miner := range m.miners {
		s.Count++
		if !miner.Online || miner.Info == nil {
			continue
		}
		s.Online++
		s.TotalHashGH += miner.Info.HashRate
		s.TotalPowerW += miner.Info.Power
		s.SharesAcc += miner.Info.SharesAccepted
		s.SharesRej += miner.Info.SharesRejected
		if miner.Info.Temp > 0 {
			tempSum += miner.Info.Temp
			tempN++
		}
		if d := axeos.ParseDiff(string(miner.Info.BestDiff)); d > s.BestDiff {
			s.BestDiff = d
			s.BestDiffStr = string(miner.Info.BestDiff)
		}
	}
	if tempN > 0 {
		s.AvgTempC = tempSum / float64(tempN)
	}
	if s.TotalHashGH > 0 {
		s.EfficiencyJT = s.TotalPowerW / (s.TotalHashGH / 1000.0)
	}
	return s
}

// ---- pool management ----

func (m *Manager) Pool() config.Pool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.pool
}

func (m *Manager) SetPool(p config.Pool) {
	m.mu.Lock()
	m.pool = p
	m.mu.Unlock()
	m.persist()
}

type ApplyResult struct {
	Host  string `json:"host"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// ApplyPool pushes the current pool settings to the given hosts (all online
// miners when hosts is empty), then restarts each device. Devices are
// updated sequentially with a small stagger so a bad config doesn't take the
// whole fleet down at once.
func (m *Manager) ApplyPool(ctx context.Context, hosts []string) []ApplyResult {
	pool := m.Pool()
	if len(hosts) == 0 {
		for _, mv := range m.Miners(0) {
			if mv.Online {
				hosts = append(hosts, mv.Host)
			}
		}
	}
	fields := map[string]any{
		"stratumURL":  pool.StratumURL,
		"stratumPort": pool.StratumPort,
		"stratumUser": pool.StratumUser,
	}
	if pool.FallbackURL != "" {
		fields["fallbackStratumURL"] = pool.FallbackURL
		fields["fallbackStratumPort"] = pool.FallbackPort
		fields["fallbackStratumUser"] = pool.FallbackUser
	}
	results := make([]ApplyResult, 0, len(hosts))
	for i, host := range hosts {
		if i > 0 {
			time.Sleep(500 * time.Millisecond)
		}
		res := ApplyResult{Host: host, OK: true}
		cctx, cancel := context.WithTimeout(ctx, 6*time.Second)
		err := m.client.PatchSystem(cctx, host, fields)
		if err == nil {
			err = m.client.Restart(cctx, host)
		}
		cancel()
		if err != nil {
			res.OK = false
			res.Error = err.Error()
			m.feed.Add(alerts.Warning, "pool_apply_failed", host, fmt.Sprintf("pool apply failed: %v", err))
		}
		results = append(results, res)
	}
	m.feed.Add(alerts.Info, "pool_applied", "",
		fmt.Sprintf("Pool settings pushed to %d miner(s): %s:%d", len(hosts), pool.StratumURL, pool.StratumPort))
	return results
}

// PatchMiner forwards a whitelisted set of tuning fields to one device.
func (m *Manager) PatchMiner(ctx context.Context, host string, fields map[string]any) error {
	allowed := map[string]bool{
		"hostname": true, "frequency": true, "coreVoltage": true,
		"fanspeed": true, "autofanspeed": true, "flipscreen": true, "invertfanpolarity": true,
	}
	clean := map[string]any{}
	for k, v := range fields {
		if allowed[k] {
			clean[k] = v
		}
	}
	if len(clean) == 0 {
		return fmt.Errorf("no allowed fields in patch")
	}
	cctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	return m.client.PatchSystem(cctx, host, clean)
}

func (m *Manager) RestartMiner(ctx context.Context, host string) error {
	cctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	return m.client.Restart(cctx, host)
}

// ---- discovery ----

// autoCIDR guesses the LAN /24 from the first private, non-loopback IPv4.
func autoCIDR() string {
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
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil || !ip4.IsPrivate() {
				continue
			}
			return fmt.Sprintf("%d.%d.%d.0/24", ip4[0], ip4[1], ip4[2])
		}
	}
	return ""
}

func (m *Manager) ScanStatus() ScanStatus {
	m.scanMu.Lock()
	defer m.scanMu.Unlock()
	cp := m.scan
	cp.Found = append([]string(nil), m.scan.Found...)
	return cp
}

// StartScan probes every host in cidr (default: config, else auto-detected
// /24) for an AxeOS API and registers hits. Runs in the background; progress
// is available via ScanStatus.
func (m *Manager) StartScan(cidr string) (string, error) {
	if cidr == "" {
		cidr = m.cfg.ScanCIDR
	}
	if cidr == "" {
		cidr = autoCIDR()
	}
	if cidr == "" {
		return "", fmt.Errorf("could not determine subnet; pass a CIDR like 192.168.1.0/24")
	}
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return "", fmt.Errorf("bad CIDR %q: %w", cidr, err)
	}
	if prefix.Bits() < 22 {
		return "", fmt.Errorf("refusing to scan more than a /22 (%s)", cidr)
	}

	m.scanMu.Lock()
	if m.scan.Running {
		m.scanMu.Unlock()
		return cidr, fmt.Errorf("scan already running")
	}
	m.scan = ScanStatus{Running: true, CIDR: cidr}
	m.scanMu.Unlock()

	go m.runScan(prefix)
	return cidr, nil
}

func (m *Manager) runScan(prefix netip.Prefix) {
	var ips []string
	for addr := prefix.Masked().Addr(); prefix.Contains(addr); addr = addr.Next() {
		ips = append(ips, addr.String())
	}
	m.scanMu.Lock()
	m.scan.Total = len(ips)
	m.scanMu.Unlock()

	client := axeos.NewClient(1500 * time.Millisecond)
	sem := make(chan struct{}, scanConcurrency)
	var wg sync.WaitGroup
	for _, ip := range ips {
		wg.Add(1)
		sem <- struct{}{}
		go func(ip string) {
			defer wg.Done()
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			info, err := client.GetInfo(ctx, ip)
			cancel()
			m.scanMu.Lock()
			m.scan.Scanned++
			m.scanMu.Unlock()
			if err != nil || !info.IsMiner() {
				return
			}
			m.scanMu.Lock()
			m.scan.Found = append(m.scan.Found, ip)
			m.scanMu.Unlock()
			if _, err := m.AddMiner(ip, "scan", ""); err == nil {
				m.feed.Add(alerts.Info, "miner_discovered", ip,
					fmt.Sprintf("Discovered %s (%s) at %s", info.Hostname, info.ASICModel, ip))
			}
		}(ip)
	}
	wg.Wait()
	m.scanMu.Lock()
	m.scan.Running = false
	m.scan.LastEnd = time.Now()
	m.scanMu.Unlock()
	if m.onTick != nil {
		m.onTick()
	}
}
