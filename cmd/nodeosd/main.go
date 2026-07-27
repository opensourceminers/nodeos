// nodeosd is the NodeOS control-plane daemon: miner discovery, fleet
// telemetry, pool management and bitcoind status behind one web UI.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"nodeos/internal/admin"
	"nodeos/internal/alerts"
	"nodeos/internal/auth"
	"nodeos/internal/config"
	"nodeos/internal/fleet"
	"nodeos/internal/health"
	"nodeos/internal/node"
	"nodeos/internal/server"
	"nodeos/internal/services"
	"nodeos/internal/sim"
	"nodeos/internal/store"
	"nodeos/internal/tlscert"
	"nodeos/internal/update"
	"nodeos/internal/work"
)

const version = "0.3.0"

func main() {
	var (
		cfgPath  = flag.String("config", "", "path to config.json (optional)")
		listen   = flag.String("listen", "", "listen address, e.g. :8080 (overrides config)")
		dataDir  = flag.String("data", "", "data directory (overrides config)")
		demo     = flag.Bool("demo", false, "start simulated miners (overrides config)")
		scanCIDR = flag.String("scan-cidr", "", "subnet to scan, e.g. 192.168.1.0/24 (overrides config)")
		noScan   = flag.Bool("no-scan", false, "skip the automatic discovery scan at startup")
		noAuth   = flag.Bool("no-auth", false, "disable web UI authentication (development only)")
		showVer  = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVer {
		fmt.Println("NodeOS nodeosd v" + version)
		return
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	if *dataDir != "" {
		cfg.DataDir = *dataDir
	}
	if *demo {
		cfg.Demo = true
	}
	if *scanCIDR != "" {
		cfg.ScanCIDR = *scanCIDR
	}
	if *noAuth {
		cfg.Auth.Disabled = true
	}

	log.Printf("NodeOS nodeosd v%s starting (listen %s, data %s, demo %v)",
		version, cfg.Listen, cfg.DataDir, cfg.Demo)

	st, err := store.Open(cfg.DataDir)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	feed := alerts.NewFeed()
	fm := fleet.NewManager(cfg, st, feed)
	nc := node.NewClient(cfg.Bitcoind)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.Demo {
		hosts, err := sim.StartFleet(cfg.DemoMiners)
		if err != nil {
			log.Fatalf("sim: %v", err)
		}
		for _, h := range hosts {
			fm.AddMiner(h, "sim", "")
		}
		log.Printf("demo mode: %d simulated miners started", len(hosts))
		feed.Add(alerts.Info, "demo", "", "Demo mode: fleet below is simulated. Disable demo mode to manage real hardware.")
	}

	// bitcoind status loop; feeds network difficulty to the fleet for
	// block-candidate detection.
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			stt := nc.Refresh(ctx)
			if stt.Available {
				fm.SetNetworkDifficulty(stt.Difficulty)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	// fleet poll loop
	go fm.Run(ctx)

	// solo-mining work engine (DATUM gateway supervision + fleet auto-switch)
	eng := work.NewEngine(cfg, st, feed, fm, nc.Status)
	go eng.Run(ctx)

	authm := auth.New(st, cfg.Auth.Disabled)
	if cfg.Auth.Disabled {
		log.Print("WARNING: web UI authentication is DISABLED")
	}
	adm := admin.New(cfg.DataDir)
	upd := update.New(cfg.Update.Repo, version, cfg.DataDir, adm)

	// system health: cached vitals + threshold alerts
	hm := health.NewMonitor(cfg.DataDir, feed)
	go hm.Run(ctx)

	// zero-click discovery on real installs
	if !cfg.Demo && !*noScan {
		if cidr, err := fm.StartScan(cfg.ScanCIDR); err == nil {
			log.Printf("discovery: scanning %s", cidr)
		} else {
			log.Printf("discovery: %v", err)
		}
	}

	handler := server.New(server.Deps{
		Cfg: cfg, Version: version, Fleet: fm, Node: nc, Feed: feed,
		Engine: eng, Auth: authm, Admin: adm, Update: upd,
		Health: hm, Store: st, Services: services.NewManager(), ConfigPath: *cfgPath,
	}).Handler()

	srv := &http.Server{Addr: cfg.Listen, Handler: handler}

	// HTTPS with a self-signed certificate; failure to bind (e.g. no
	// privileges in dev) degrades to HTTP-only with a warning.
	var tlsSrv *http.Server
	if cfg.TLS.Enabled {
		certPath, keyPath := cfg.TLS.CertFile, cfg.TLS.KeyFile
		var err error
		if certPath == "" || keyPath == "" {
			certPath, keyPath, err = tlscert.Ensure(cfg.DataDir, nil)
		}
		if err == nil {
			var tc *tls.Config
			if tc, err = tlscert.Config(certPath, keyPath); err == nil {
				tlsSrv = &http.Server{Addr: cfg.TLS.Listen, Handler: handler, TLSConfig: tc}
				go func() {
					log.Printf("https ready on %s (self-signed)", cfg.TLS.Listen)
					if err := tlsSrv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
						log.Printf("WARNING: https listener failed (%v) — continuing HTTP-only", err)
					}
				}()
			}
		}
		if err != nil {
			log.Printf("WARNING: TLS setup failed (%v) — continuing HTTP-only", err)
		}
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
		if tlsSrv != nil {
			tlsSrv.Shutdown(shutdownCtx)
		}
	}()

	log.Printf("web UI ready on %s", cfg.Listen)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("http: %v", err)
	}
	log.Print("nodeosd stopped")
}
