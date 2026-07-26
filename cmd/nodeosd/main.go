// nodeosd is the NodeOS control-plane daemon: miner discovery, fleet
// telemetry, pool management and bitcoind status behind one web UI.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"nodeos/internal/alerts"
	"nodeos/internal/config"
	"nodeos/internal/fleet"
	"nodeos/internal/node"
	"nodeos/internal/server"
	"nodeos/internal/sim"
	"nodeos/internal/store"
	"nodeos/internal/work"
)

const version = "0.2.0"

func main() {
	var (
		cfgPath  = flag.String("config", "", "path to config.json (optional)")
		listen   = flag.String("listen", "", "listen address, e.g. :8080 (overrides config)")
		dataDir  = flag.String("data", "", "data directory (overrides config)")
		demo     = flag.Bool("demo", false, "start simulated miners (overrides config)")
		scanCIDR = flag.String("scan-cidr", "", "subnet to scan, e.g. 192.168.1.0/24 (overrides config)")
		noScan   = flag.Bool("no-scan", false, "skip the automatic discovery scan at startup")
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

	// zero-click discovery on real installs
	if !cfg.Demo && !*noScan {
		if cidr, err := fm.StartScan(cfg.ScanCIDR); err == nil {
			log.Printf("discovery: scanning %s", cidr)
		} else {
			log.Printf("discovery: %v", err)
		}
	}

	srv := &http.Server{Addr: cfg.Listen, Handler: server.New(cfg, version, fm, nc, feed, eng).Handler()}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	log.Printf("web UI ready on %s", cfg.Listen)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("http: %v", err)
	}
	log.Print("nodeosd stopped")
}
