// Package server exposes the nodeosd REST API, a Server-Sent-Events stream
// for live updates, and the embedded web UI.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"net/http"
	"sync"
	"time"

	"nodeos/internal/alerts"
	"nodeos/internal/config"
	"nodeos/internal/fleet"
	"nodeos/internal/node"
	"nodeos/web"
)

type Server struct {
	cfg     config.Config
	version string
	started time.Time
	fleet   *fleet.Manager
	node    *node.Client
	feed    *alerts.Feed

	sseMu   sync.Mutex
	sseSubs map[chan []byte]struct{}
}

func New(cfg config.Config, version string, fm *fleet.Manager, nc *node.Client, feed *alerts.Feed) *Server {
	s := &Server{
		cfg: cfg, version: version, started: time.Now(),
		fleet: fm, node: nc, feed: feed,
		sseSubs: map[chan []byte]struct{}{},
	}
	fm.OnTick(func() { s.broadcast("snapshot", s.statusPayload(60)) })
	feed.OnAlert(func(a alerts.Alert) { s.broadcast("alert", a) })
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.statusPayload(60))
	})
	mux.HandleFunc("GET /api/miners", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.fleet.Miners(360))
	})
	mux.HandleFunc("POST /api/miners", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Host string `json:"host"`
			Name string `json:"name"`
		}
		if err := decode(r, &req); err != nil {
			httpErr(w, 400, err)
			return
		}
		miner, err := s.fleet.AddMiner(req.Host, "manual", req.Name)
		if err != nil {
			httpErr(w, 400, err)
			return
		}
		writeJSON(w, miner)
	})
	mux.HandleFunc("DELETE /api/miners/{host}", func(w http.ResponseWriter, r *http.Request) {
		if !s.fleet.RemoveMiner(r.PathValue("host")) {
			httpErr(w, 404, fmt.Errorf("unknown miner"))
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/miners/{host}/restart", func(w http.ResponseWriter, r *http.Request) {
		if err := s.fleet.RestartMiner(r.Context(), r.PathValue("host")); err != nil {
			httpErr(w, 502, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/miners/{host}/rename", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name string `json:"name"`
		}
		if err := decode(r, &req); err != nil {
			httpErr(w, 400, err)
			return
		}
		if !s.fleet.RenameMiner(r.PathValue("host"), req.Name) {
			httpErr(w, 404, fmt.Errorf("unknown miner"))
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("PATCH /api/miners/{host}", func(w http.ResponseWriter, r *http.Request) {
		var fields map[string]any
		if err := decode(r, &fields); err != nil {
			httpErr(w, 400, err)
			return
		}
		if err := s.fleet.PatchMiner(r.Context(), r.PathValue("host"), fields); err != nil {
			httpErr(w, 502, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})

	mux.HandleFunc("GET /api/pool", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.fleet.Pool())
	})
	mux.HandleFunc("PUT /api/pool", func(w http.ResponseWriter, r *http.Request) {
		var p config.Pool
		if err := decode(r, &p); err != nil {
			httpErr(w, 400, err)
			return
		}
		if p.StratumURL == "" || p.StratumPort <= 0 || p.StratumUser == "" {
			httpErr(w, 400, fmt.Errorf("stratum_url, stratum_port and stratum_user are required"))
			return
		}
		s.fleet.SetPool(p)
		writeJSON(w, p)
	})
	mux.HandleFunc("POST /api/pool/apply", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Hosts []string `json:"hosts"`
		}
		_ = decode(r, &req) // empty body = all online miners
		// Applying takes ~1s per miner; run with an independent timeout so
		// large fleets don't get cut off by the request context.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		writeJSON(w, s.fleet.ApplyPool(ctx, req.Hosts))
	})

	mux.HandleFunc("GET /api/node", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.node.Status())
	})
	mux.HandleFunc("GET /api/alerts", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.feed.List())
	})

	mux.HandleFunc("POST /api/scan", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			CIDR string `json:"cidr"`
		}
		_ = decode(r, &req)
		cidr, err := s.fleet.StartScan(req.CIDR)
		if err != nil {
			httpErr(w, 400, err)
			return
		}
		writeJSON(w, map[string]string{"scanning": cidr})
	})
	mux.HandleFunc("GET /api/scan", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.fleet.ScanStatus())
	})

	mux.HandleFunc("GET /api/events", s.handleSSE)

	// Embedded UI. Serve index.html for "/" and let the FS handle assets.
	sub, err := fs.Sub(web.Files, ".")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /", http.FileServerFS(sub))

	return mux
}

// ---- status payload ----

type soloStats struct {
	NetworkDifficulty float64 `json:"network_difficulty"`
	// ExpectedSeconds is the statistical expectation for this fleet to find
	// a block at current difficulty. Zero when unknown.
	ExpectedSeconds float64 `json:"expected_seconds"`
	// OddsPerDay is the probability (0..1) of finding a block in 24h.
	OddsPerDay float64 `json:"odds_per_day"`
}

func (s *Server) statusPayload(histSamples int) map[string]any {
	sum := s.fleet.Summary()
	nst := s.node.Status()

	var solo soloStats
	solo.NetworkDifficulty = nst.Difficulty
	if nst.Difficulty > 0 && sum.TotalHashGH > 0 {
		hashesPerBlock := nst.Difficulty * math.Pow(2, 32)
		solo.ExpectedSeconds = hashesPerBlock / (sum.TotalHashGH * 1e9)
		solo.OddsPerDay = 1 - math.Exp(-86400/solo.ExpectedSeconds)
	}

	return map[string]any{
		"version":   s.version,
		"demo":      s.cfg.Demo,
		"uptime_s":  int64(time.Since(s.started).Seconds()),
		"time":      time.Now().Unix(),
		"fleet":     sum,
		"node":      nst,
		"solo":      solo,
		"miners":    s.fleet.Miners(histSamples),
		"scan":      s.fleet.ScanStatus(),
	}
}

// ---- SSE ----

func (s *Server) broadcast(event string, payload any) {
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	msg := []byte("event: " + event + "\ndata: " + string(b) + "\n\n")
	s.sseMu.Lock()
	for ch := range s.sseSubs {
		select {
		case ch <- msg:
		default: // slow client: drop frame rather than block the fleet loop
		}
	}
	s.sseMu.Unlock()
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpErr(w, 500, fmt.Errorf("streaming unsupported"))
		return
	}
	ch := make(chan []byte, 16)
	s.sseMu.Lock()
	s.sseSubs[ch] = struct{}{}
	s.sseMu.Unlock()
	defer func() {
		s.sseMu.Lock()
		delete(s.sseSubs, ch)
		s.sseMu.Unlock()
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// initial snapshot so the UI paints immediately
	b, _ := json.Marshal(s.statusPayload(60))
	fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", b)
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case msg := <-ch:
			w.Write(msg)
			flusher.Flush()
		}
	}
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func decode(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}
