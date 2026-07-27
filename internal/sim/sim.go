// Package sim runs simulated AxeOS miners as real localhost HTTP servers.
// Demo mode registers them like physical devices, so the whole fleet path —
// discovery client, polling, pool apply, restart — is exercised end to end.
package sim

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"sync"
	"time"
)

type model struct {
	name     string
	asic     string
	baseGH   float64
	watts    float64
	asicCnt  int
	smallCnt int
}

var models = []model{
	{"Bitaxe Ultra", "BM1366", 500, 15, 1, 894},
	{"Bitaxe Supra", "BM1368", 700, 18, 1, 1276},
	{"Bitaxe Gamma", "BM1370", 1200, 20, 1, 2040},
	{"NerdQAxe++", "BM1370", 4800, 76, 4, 8160},
}

type simMiner struct {
	mu       sync.Mutex
	host     string
	model    model
	started  time.Time
	rng      *rand.Rand
	hashGH   float64
	temp     float64
	accepted int64
	rejected int64
	bestDiff float64
	bestSess float64
	freq     float64
	coreV    float64
	flashing bool
	version  string
	stratum  struct {
		URL, User         string
		Port              int
		FbURL, FbUser     string
		FbPort            int
	}
}

// StartFleet launches n simulated miners on 127.0.0.1 and returns their
// host:port addresses.
func StartFleet(n int) ([]string, error) {
	hosts := make([]string, 0, n)
	for i := 0; i < n; i++ {
		m := models[i%len(models)]
		s := &simMiner{
			model:   m,
			started: time.Now(),
			rng:     rand.New(rand.NewSource(int64(i)*7919 + 42)),
			hashGH:  m.baseGH,
			temp:    52 + float64(i%5)*2,
			freq:    490,
			coreV:   1150,
			version: "v2.14.1-sim",
		}
		s.stratum.URL = "public-pool.io"
		s.stratum.Port = 21496
		s.stratum.User = "bc1qdemo.nodeos-sim"

		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return hosts, err
		}
		s.host = ln.Addr().String()
		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/system/info", s.handleInfo)
		mux.HandleFunc("PATCH /api/system", s.handlePatch)
		mux.HandleFunc("POST /api/system/restart", s.handleRestart)
		mux.HandleFunc("POST /api/system/OTA", s.handleOTA)
		mux.HandleFunc("POST /api/system/OTAWWW", s.handleOTA)
		srv := &http.Server{Handler: mux}
		go srv.Serve(ln)
		go s.tick()
		hosts = append(hosts, s.host)
	}
	return hosts, nil
}

func (s *simMiner) tick() {
	t := time.NewTicker(2 * time.Second)
	for range t.C {
		s.mu.Lock()
		// hashrate: random walk around base, ±8%
		drift := (s.rng.Float64() - 0.5) * 0.04 * s.model.baseGH
		s.hashGH += drift
		if s.hashGH < s.model.baseGH*0.92 {
			s.hashGH = s.model.baseGH * 0.92
		}
		if s.hashGH > s.model.baseGH*1.08 {
			s.hashGH = s.model.baseGH * 1.08
		}
		// temp follows frequency loosely
		target := 50 + (s.freq-400)/10 + s.rng.Float64()*4
		s.temp += (target - s.temp) * 0.2
		// shares arrive roughly proportional to hashrate
		if s.rng.Float64() < 0.9 {
			s.accepted += 1 + int64(s.rng.Intn(3))
		}
		if s.rng.Float64() < 0.02 {
			s.rejected++
		}
		// occasionally a new best diff
		if s.rng.Float64() < 0.05 {
			d := s.rng.Float64() * 5e6
			if d > s.bestSess {
				s.bestSess = d
			}
			if d > s.bestDiff {
				s.bestDiff = d
			}
		}
		s.mu.Unlock()
	}
}

func fmtDiff(d float64) string {
	switch {
	case d >= 1e12:
		return fmt.Sprintf("%.2fT", d/1e12)
	case d >= 1e9:
		return fmt.Sprintf("%.2fG", d/1e9)
	case d >= 1e6:
		return fmt.Sprintf("%.2fM", d/1e6)
	case d >= 1e3:
		return fmt.Sprintf("%.1fk", d/1e3)
	default:
		return fmt.Sprintf("%.0f", d)
	}
}

func (s *simMiner) handleInfo(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.flashing {
		// a flashing device is simply unreachable
		http.Error(w, "flashing", http.StatusServiceUnavailable)
		return
	}
	_, portStr, _ := net.SplitHostPort(s.host)
	info := map[string]any{
		"power":              s.model.watts * (0.95 + 0.1*s.rng.Float64()),
		"voltage":            5150.0,
		"current":            2100.0,
		"temp":               s.temp,
		"vrTemp":             s.temp - 8,
		"hashRate":           s.hashGH,
		"bestDiff":           fmtDiff(s.bestDiff),
		"bestSessionDiff":    fmtDiff(s.bestSess),
		"isUsingFallbackStratum": 0,
		"stratumURL":         s.stratum.URL,
		"stratumPort":        s.stratum.Port,
		"stratumUser":        s.stratum.User,
		"fallbackStratumURL": s.stratum.FbURL,
		"fallbackStratumPort": s.stratum.FbPort,
		"fallbackStratumUser": s.stratum.FbUser,
		"sharesAccepted":     s.accepted,
		"sharesRejected":     s.rejected,
		"uptimeSeconds":      int64(time.Since(s.started).Seconds()),
		"ASICModel":          s.model.asic,
		"asicCount":          s.model.asicCnt,
		"smallCoreCount":     s.model.smallCnt,
		"hostname":           fmt.Sprintf("sim-%s-%s", s.model.asic, portStr),
		"macAddr":            fmt.Sprintf("02:AX:E0:00:00:%s", portStr[len(portStr)-2:]),
		"ssid":               "nodeos-lab",
		"wifiStatus":         "Connected!",
		"frequency":          s.freq,
		"coreVoltage":        s.coreV,
		"coreVoltageActual":  s.coreV - 7,
		"fanspeed":           35 + (s.temp-50)*2,
		"fanrpm":             3000 + (s.temp-50)*120,
		"autofanspeed":       1,
		"overheat_mode":      0,
		"version":            s.version,
		"boardVersion":       "204",
		"runningPartition":   "ota_0",
		"freeHeap":           190000,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

func (s *simMiner) handlePatch(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := fields["stratumURL"].(string); ok {
		s.stratum.URL = v
	}
	if v, ok := fields["stratumPort"].(float64); ok {
		s.stratum.Port = int(v)
	}
	if v, ok := fields["stratumUser"].(string); ok {
		s.stratum.User = v
	}
	if v, ok := fields["fallbackStratumURL"].(string); ok {
		s.stratum.FbURL = v
	}
	if v, ok := fields["fallbackStratumPort"].(float64); ok {
		s.stratum.FbPort = int(v)
	}
	if v, ok := fields["fallbackStratumUser"].(string); ok {
		s.stratum.FbUser = v
	}
	if v, ok := fields["frequency"].(float64); ok {
		s.freq = v
	}
	if v, ok := fields["coreVoltage"].(float64); ok {
		s.coreV = v
	}
	w.WriteHeader(http.StatusOK)
}

// handleOTA emulates a firmware flash: accept the image, go dark for a few
// seconds, come back with a bumped version. Lets the staged rollout —
// including the canary verification — be tested without risking hardware.
func (s *simMiner) handleOTA(w http.ResponseWriter, r *http.Request) {
	n, _ := io.Copy(io.Discard, io.LimitReader(r.Body, 64<<20))
	if n < 1024 {
		http.Error(w, "image too small", http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
	go func() {
		s.mu.Lock()
		s.flashing = true
		s.mu.Unlock()
		time.Sleep(12 * time.Second) // reboot window
		s.mu.Lock()
		s.flashing = false
		s.started = time.Now()
		s.version = "v2.15.0-sim"
		s.mu.Unlock()
	}()
}

func (s *simMiner) handleRestart(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.started = time.Now()
	s.bestSess = 0
	s.accepted = 0
	s.rejected = 0
	s.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}
