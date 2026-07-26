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
	"regexp"
	"strings"
	"sync"
	"time"

	"nodeos/internal/admin"
	"nodeos/internal/alerts"
	"nodeos/internal/auth"
	"nodeos/internal/config"
	"nodeos/internal/fleet"
	"nodeos/internal/health"
	"nodeos/internal/node"
	"nodeos/internal/store"
	"nodeos/internal/support"
	"nodeos/internal/update"
	"nodeos/internal/work"
	"nodeos/web"
)

// Deps bundles everything the HTTP layer serves.
type Deps struct {
	Cfg     config.Config
	Version string
	Fleet   *fleet.Manager
	Node    *node.Client
	Feed    *alerts.Feed
	Engine  *work.Engine
	Auth    *auth.Manager
	Admin   *admin.Client
	Update  *update.Checker
	Health  *health.Monitor
	// ConfigPath is included (redacted) in support bundles.
	ConfigPath string
}

type Server struct {
	cfg     config.Config
	version string
	started time.Time
	fleet   *fleet.Manager
	node    *node.Client
	feed    *alerts.Feed
	engine  *work.Engine
	auth    *auth.Manager
	admin   *admin.Client
	update  *update.Checker
	health  *health.Monitor
	cfgPath string

	sseMu   sync.Mutex
	sseSubs map[chan []byte]struct{}
}

func New(d Deps) *Server {
	s := &Server{
		cfg: d.Cfg, version: d.Version, started: time.Now(),
		fleet: d.Fleet, node: d.Node, feed: d.Feed, engine: d.Engine,
		auth: d.Auth, admin: d.Admin, update: d.Update,
		health: d.Health, cfgPath: d.ConfigPath,
		sseSubs: map[chan []byte]struct{}{},
	}
	d.Fleet.OnTick(func() { s.broadcast("snapshot", s.statusPayload(60)) })
	d.Feed.OnAlert(func(a alerts.Alert) { s.broadcast("alert", a) })
	return s
}

// publicAPI lists the endpoints reachable without a session.
var publicAPI = map[string]bool{
	"/api/auth/state": true,
	"/api/auth/login": true,
	"/api/auth/setup": true,
}

// withAuth gates every /api/* route behind the session cookie; the static
// SPA shell stays public (it contains no data).
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && !publicAPI[r.URL.Path] {
			if !s.auth.Authenticated(r) {
				httpErr(w, 401, fmt.Errorf("not authenticated"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

var (
	coreVersionRe  = regexp.MustCompile(`^\d+\.\d+(\.\d+)?$`)
	knotsVersionRe = regexp.MustCompile(`^\d+\.\d+(\.\d+)?\.knots\d{8}$`)
)

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// ---- auth ----

	mux.HandleFunc("GET /api/auth/state", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"setup_required": s.auth.SetupRequired(),
			"authenticated":  s.auth.Authenticated(r),
			"disabled":       s.auth.Disabled(),
		})
	})
	mux.HandleFunc("POST /api/auth/setup", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Password string `json:"password"`
		}
		if err := decode(r, &req); err != nil {
			httpErr(w, 400, err)
			return
		}
		if !s.auth.SetupRequired() {
			httpErr(w, 409, fmt.Errorf("password already set — log in instead"))
			return
		}
		if err := s.auth.SetPassword(req.Password); err != nil {
			httpErr(w, 400, err)
			return
		}
		if err := s.auth.Login(w, req.Password); err != nil {
			httpErr(w, 500, err)
			return
		}
		s.feed.Add(alerts.Info, "auth_setup", "", "Admin password set — the web UI is now protected.")
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Password string `json:"password"`
		}
		if err := decode(r, &req); err != nil {
			httpErr(w, 400, err)
			return
		}
		if s.auth.SetupRequired() {
			httpErr(w, 409, fmt.Errorf("no password set yet — create one first"))
			return
		}
		if err := s.auth.Login(w, req.Password); err != nil {
			httpErr(w, 401, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		s.auth.Logout(w, r)
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/auth/password", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Current string `json:"current"`
			New     string `json:"new"`
		}
		if err := decode(r, &req); err != nil {
			httpErr(w, 400, err)
			return
		}
		if !s.auth.CheckPassword(req.Current) {
			httpErr(w, 401, fmt.Errorf("current password is wrong"))
			return
		}
		if err := s.auth.SetPassword(req.New); err != nil {
			httpErr(w, 400, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})

	// ---- node software (Core/Knots, pruning) ----

	mux.HandleFunc("GET /api/node/setup", func(w http.ResponseWriter, r *http.Request) {
		nst := s.node.Status()
		impl := "unknown"
		if nst.Available {
			if strings.Contains(nst.Subversion, "Knots") {
				impl = "knots"
			} else if strings.Contains(nst.Subversion, "Satoshi") {
				impl = "core"
			}
		}
		writeJSON(w, map[string]any{
			"helper_available": s.admin.Available(),
			"impl":             impl,
			"subversion":       nst.Subversion,
			"pruned":           nst.Pruned,
			"core_version":     s.cfg.NodeSoftware.CoreVersion,
			"knots_version":    s.cfg.NodeSoftware.KnotsVersion,
			"job":              s.admin.Current(),
		})
	})
	mux.HandleFunc("POST /api/node/setup", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Impl    string `json:"impl"`    // "core" | "knots"
			Version string `json:"version"` // empty = default for impl
			Prune   int    `json:"prune"`   // MiB; 0 = full node
		}
		if err := decode(r, &req); err != nil {
			httpErr(w, 400, err)
			return
		}
		switch req.Impl {
		case "core":
			if req.Version == "" {
				req.Version = s.cfg.NodeSoftware.CoreVersion
			}
			if !coreVersionRe.MatchString(req.Version) {
				httpErr(w, 400, fmt.Errorf("bad Core version %q (want e.g. 29.0)", req.Version))
				return
			}
		case "knots":
			if req.Version == "" {
				req.Version = s.cfg.NodeSoftware.KnotsVersion
			}
			if !knotsVersionRe.MatchString(req.Version) {
				httpErr(w, 400, fmt.Errorf("bad Knots version %q (want e.g. 29.3.knots20260508)", req.Version))
				return
			}
		default:
			httpErr(w, 400, fmt.Errorf(`impl must be "core" or "knots"`))
			return
		}
		if req.Prune < 0 || (req.Prune > 0 && req.Prune < 550) {
			httpErr(w, 400, fmt.Errorf("prune must be 0 (full node) or at least 550 MiB"))
			return
		}
		job, err := s.admin.Start("node-install", req.Impl, req.Version, fmt.Sprint(req.Prune))
		if err != nil {
			httpErr(w, 409, err)
			return
		}
		s.feed.Add(alerts.Info, "node_install", "",
			fmt.Sprintf("Installing Bitcoin %s %s (prune %d MiB)…",
				map[string]string{"core": "Core", "knots": "Knots"}[req.Impl], req.Version, req.Prune))
		writeJSON(w, job)
	})

	// ---- self-update from GitHub releases ----

	mux.HandleFunc("GET /api/update", func(w http.ResponseWriter, r *http.Request) {
		info, err := s.update.Check(r.Context())
		if err != nil {
			httpErr(w, 502, err)
			return
		}
		writeJSON(w, info)
	})
	mux.HandleFunc("POST /api/update/apply", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		job, err := s.update.Apply(ctx)
		if err != nil {
			httpErr(w, 502, err)
			return
		}
		s.feed.Add(alerts.Info, "self_update", "",
			"Update downloaded and verified — installing and restarting nodeosd…")
		writeJSON(w, job)
	})

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

	mux.HandleFunc("GET /api/work", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"status": s.engine.Status(),
			"log":    s.engine.LogTail(100),
		})
	})
	mux.HandleFunc("PUT /api/work", func(w http.ResponseWriter, r *http.Request) {
		var req store.WorkSettings
		if err := decode(r, &req); err != nil {
			httpErr(w, 400, err)
			return
		}
		if err := s.engine.UpdateSettings(r.Context(), req); err != nil {
			httpErr(w, 400, err)
			return
		}
		writeJSON(w, s.engine.Status())
	})
	mux.HandleFunc("POST /api/work/switch", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Target string `json:"target"` // "engine" | "external"
		}
		if err := decode(r, &req); err != nil {
			httpErr(w, 400, err)
			return
		}
		var err error
		switch req.Target {
		case "engine":
			err = s.engine.SwitchToEngine(r.Context())
		case "external":
			err = s.engine.SwitchToExternal(r.Context())
		default:
			httpErr(w, 400, fmt.Errorf(`target must be "engine" or "external"`))
			return
		}
		if err != nil {
			httpErr(w, 409, err)
			return
		}
		writeJSON(w, s.engine.Status())
	})

	mux.HandleFunc("GET /api/system", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.health.Last())
	})
	mux.HandleFunc("GET /api/support/bundle", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition",
			fmt.Sprintf(`attachment; filename="nodeos-support-%s.tar.gz"`, time.Now().Format("20060102-150405")))
		if err := support.Write(w, support.Sources{
			Version:    s.version,
			Status:     func() any { return s.statusPayload(0) },
			WorkLog:    func() []string { return s.engine.LogTail(200) },
			Health:     func() any { return s.health.Last() },
			ConfigPath: s.cfgPath,
		}); err != nil {
			// headers already sent; log path is the best we can do
			fmt.Fprintf(w, "\nbundle error: %v\n", err)
		}
	})

	mux.HandleFunc("GET /api/events", s.handleSSE)

	// Embedded UI. Serve index.html for "/" and let the FS handle assets.
	sub, err := fs.Sub(web.Files, ".")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /", http.FileServerFS(sub))

	return s.withAuth(mux)
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
		"work":      s.engine.Status(),
		"system":    s.health.Last(),
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
