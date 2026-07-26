// Package health collects system vitals (CPU, memory, disks, temperature,
// SMART) and raises alerts on threshold transitions. All readers tolerate
// missing sources (macOS dev machines, containers) by returning zero values.
package health

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"nodeos/internal/alerts"
)

type Disk struct {
	Mount   string  `json:"mount"`
	TotalB  uint64  `json:"total_b"`
	FreeB   uint64  `json:"free_b"`
	UsedPct float64 `json:"used_pct"`
}

// SmartDisk condenses one smartctl -j report (written by the root-side
// nodeos-smart timer; see deploy/install.sh).
type SmartDisk struct {
	Device       string `json:"device"`
	Model        string `json:"model,omitempty"`
	Passed       *bool  `json:"passed,omitempty"` // nil = unknown
	TempC        int    `json:"temp_c,omitempty"`
	PowerOnHours int    `json:"power_on_hours,omitempty"`
	WearPct      int    `json:"wear_pct,omitempty"` // NVMe percentage_used
}

type Snapshot struct {
	Load1     float64     `json:"load1"`
	Load5     float64     `json:"load5"`
	Load15    float64     `json:"load15"`
	CPUCount  int         `json:"cpu_count"`
	MemTotalB uint64      `json:"mem_total_b"`
	MemAvailB uint64      `json:"mem_avail_b"`
	UptimeS   int64       `json:"uptime_s"`
	CPUTempC  float64     `json:"cpu_temp_c"`
	Disks     []Disk      `json:"disks"`
	Smart     []SmartDisk `json:"smart"`
	SmartAge  int64       `json:"smart_age_s,omitempty"` // seconds since smart.json was generated
	CheckedAt time.Time   `json:"checked_at"`
}

// Collect gathers a snapshot. root is "/" in production and a temp dir in
// tests; dataDir is where smart.json and the data disk live.
func Collect(root, dataDir string) Snapshot {
	s := Snapshot{CheckedAt: time.Now(), CPUCount: cpuCount(root)}
	s.Load1, s.Load5, s.Load15 = loadavg(root)
	s.MemTotalB, s.MemAvailB = meminfo(root)
	s.UptimeS = uptime(root)
	s.CPUTempC = cpuTemp(root)
	s.Disks = disks(dataDir)
	s.Smart, s.SmartAge = smart(filepath.Join(dataDir, "health", "smart.json"))
	return s
}

func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func loadavg(root string) (l1, l5, l15 float64) {
	f := strings.Fields(readFile(filepath.Join(root, "proc/loadavg")))
	if len(f) >= 3 {
		l1, _ = strconv.ParseFloat(f[0], 64)
		l5, _ = strconv.ParseFloat(f[1], 64)
		l15, _ = strconv.ParseFloat(f[2], 64)
	}
	return
}

func cpuCount(root string) int {
	n := 0
	for _, line := range strings.Split(readFile(filepath.Join(root, "proc/cpuinfo")), "\n") {
		if strings.HasPrefix(line, "processor") {
			n++
		}
	}
	return n
}

func meminfo(root string) (total, avail uint64) {
	for _, line := range strings.Split(readFile(filepath.Join(root, "proc/meminfo")), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		kb, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			total = kb * 1024
		case "MemAvailable:":
			avail = kb * 1024
		}
	}
	return
}

func uptime(root string) int64 {
	f := strings.Fields(readFile(filepath.Join(root, "proc/uptime")))
	if len(f) >= 1 {
		if v, err := strconv.ParseFloat(f[0], 64); err == nil {
			return int64(v)
		}
	}
	return 0
}

// cpuTemp returns the hottest thermal zone in °C (0 when unavailable).
func cpuTemp(root string) float64 {
	zones, _ := filepath.Glob(filepath.Join(root, "sys/class/thermal/thermal_zone*/temp"))
	var max float64
	for _, z := range zones {
		if v, err := strconv.ParseFloat(strings.TrimSpace(readFile(z)), 64); err == nil {
			if c := v / 1000.0; c > max && c < 150 {
				max = c
			}
		}
	}
	return max
}

func disks(dataDir string) []Disk {
	var out []Disk
	seen := map[string]bool{}
	for _, m := range []string{"/", dataDir, "/var/lib/bitcoind"} {
		var st syscall.Statfs_t
		if err := syscall.Statfs(m, &st); err != nil {
			continue
		}
		// portable same-filesystem detection (syscall.Fsid's field name
		// differs across platforms): identical statfs vitals => same fs
		key := fmt.Sprintf("%d-%d-%d-%d", st.Blocks, st.Bfree, st.Files, st.Bsize)
		if seen[key] {
			continue
		}
		seen[key] = true
		total := st.Blocks * uint64(st.Bsize)
		free := st.Bavail * uint64(st.Bsize)
		if total == 0 {
			continue
		}
		out = append(out, Disk{
			Mount: m, TotalB: total, FreeB: free,
			UsedPct: 100 * float64(total-free) / float64(total),
		})
	}
	return out
}

// smart parses the report the root-side timer writes:
// {"generated_unix": 123, "disks": [<raw smartctl -j output>, ...]}
func smart(path string) ([]SmartDisk, int64) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, 0
	}
	var report struct {
		GeneratedUnix int64             `json:"generated_unix"`
		Disks         []map[string]any  `json:"disks"`
	}
	if err := json.Unmarshal(b, &report); err != nil {
		return nil, 0
	}
	var out []SmartDisk
	for _, raw := range report.Disks {
		d := SmartDisk{}
		if dev, ok := raw["device"].(map[string]any); ok {
			d.Device, _ = dev["name"].(string)
		}
		d.Model, _ = raw["model_name"].(string)
		if ss, ok := raw["smart_status"].(map[string]any); ok {
			if p, ok := ss["passed"].(bool); ok {
				d.Passed = &p
			}
		}
		if t, ok := raw["temperature"].(map[string]any); ok {
			if c, ok := t["current"].(float64); ok {
				d.TempC = int(c)
			}
		}
		if p, ok := raw["power_on_time"].(map[string]any); ok {
			if h, ok := p["hours"].(float64); ok {
				d.PowerOnHours = int(h)
			}
		}
		if n, ok := raw["nvme_smart_health_information_log"].(map[string]any); ok {
			if w, ok := n["percentage_used"].(float64); ok {
				d.WearPct = int(w)
			}
		}
		if d.Device != "" || d.Model != "" {
			out = append(out, d)
		}
	}
	var age int64
	if report.GeneratedUnix > 0 {
		age = int64(time.Since(time.Unix(report.GeneratedUnix, 0)).Seconds())
	}
	return out, age
}

// ---- monitor: cached snapshot + threshold alerts ----

const (
	diskWarnPct  = 90.0
	diskClearPct = 85.0
	tempWarnC    = 80.0
	tempClearC   = 75.0
)

type Monitor struct {
	dataDir string
	feed    *alerts.Feed

	mu     sync.Mutex
	last   Snapshot
	active map[string]bool // alert condition currently firing
}

func NewMonitor(dataDir string, feed *alerts.Feed) *Monitor {
	m := &Monitor{dataDir: dataDir, feed: feed, active: map[string]bool{}}
	m.last = Collect("/", dataDir)
	return m
}

func (m *Monitor) Last() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.last
}

func (m *Monitor) Run(ctx context.Context) {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s := Collect("/", m.dataDir)
			m.mu.Lock()
			m.last = s
			m.mu.Unlock()
			m.check(s)
		}
	}
}

// transition fires the alert only when the condition newly starts; hysteresis
// is handled by the caller passing separate set/clear conditions.
func (m *Monitor) transition(key string, set, clear bool, level alerts.Level, msg string) {
	m.mu.Lock()
	was := m.active[key]
	if set && !was {
		m.active[key] = true
	}
	if clear && was {
		m.active[key] = false
	}
	now := m.active[key]
	m.mu.Unlock()
	if now && !was {
		m.feed.Add(level, "system_"+key, "", msg)
	}
}

func (m *Monitor) check(s Snapshot) {
	for _, d := range s.Disks {
		key := "disk_" + d.Mount
		m.transition(key, d.UsedPct >= diskWarnPct, d.UsedPct < diskClearPct, alerts.Warning,
			fmt.Sprintf("Disk %s is %.0f%% full — the node stops syncing when it runs out", d.Mount, d.UsedPct))
	}
	if s.CPUTempC > 0 {
		m.transition("cpu_temp", s.CPUTempC >= tempWarnC, s.CPUTempC < tempClearC, alerts.Warning,
			fmt.Sprintf("CPU temperature %.0f °C — check airflow/dust", s.CPUTempC))
	}
	for _, sd := range s.Smart {
		if sd.Passed != nil {
			m.transition("smart_"+sd.Device, !*sd.Passed, *sd.Passed, alerts.Critical,
				fmt.Sprintf("SMART reports FAILING for %s (%s) — back up and replace this disk", sd.Device, sd.Model))
		}
	}
}
