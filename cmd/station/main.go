package main

// cmd/station — the inference station as its OWN binary. station is PINNED to
// the station (inference-serving) role: it ignores any `mode` in the config
// (there is none anymore) and always runs station.New(...). The binary IS the
// role. (This replaced the old mode-switched cmd/proxy binary, now retired —
// the control role lives in its own cmd/control binary.)
//
// This is the executable an installed `station` app mesh launches —
// artifacts/addon/apps/station/bin/station.exe, resolved from mesh.json `bin`
// and started with `-config conf/station.json` (cwd = the app folder). isannd
// owns the public listener (mesh.json isannd.servers) and reverse-proxies to the
// backend station listens on (:8090); station itself just serves that backend.

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/isannai/mesh/pkg/station"
	"github.com/daesob/http3proxy/pkg/tunnel"
)

func main() {
	configFile := flag.String("config", "", "path to JSON config file (e.g., conf/station.json)")
	flag.Parse()

	if *configFile == "" {
		log.Fatal("[station] --config required (e.g. -config conf/station.json)")
	}
	cfg, err := tunnel.LoadConfig(*configFile)
	if err != nil {
		log.Fatalf("[station] load config: %v", err)
	}
	log.Printf("[station] config loaded from %s", *configFile)

	// Pinned: station is always the station (inference-serving) role, whatever the
	// config's `mode` says. A split binary makes mode vestigial — the executable
	// is the role. The on-wire RV role string + node-id prefix are already
	// "station" / "S:" (pkg/station/rendezvous.go — buildRegisterMsg / ping). What
	// remains for the Phase 3 cleanup is the deeper internal naming — Go types
	// (*Provider), /provider/* URL paths (see
	// docs/confirm/20260703/provider-broker-rename-impact.md).
	cfg.Mode = tunnel.ModeStation

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("[station] signal received (%s), shutting down", sig)
		cancel()
		// Force-exit backstop: if graceful shutdown hangs on a stuck listener /
		// isannd tcp-pipe connection (Windows), bail after 5s. Mirrors cmd/control.
		go func() {
			<-time.After(5 * time.Second)
			log.Printf("[station] graceful shutdown timed out, forcing exit")
			os.Exit(1)
		}()
	}()

	// Locate the isann anchor by walking up from the executable dir; fall back to
	// cwd (dev / `go run`, or an app folder without an anchor).
	startDir := ""
	if exe, err := os.Executable(); err == nil {
		startDir = filepath.Dir(exe)
	}
	root, iann, ferr := tunnel.FindRoot(startDir)
	if ferr != nil {
		cwd, _ := os.Getwd()
		log.Printf("[station] isann anchor not found (%v) — using cwd %s", ferr, cwd)
		root = cwd
		iann = &tunnel.IannConfig{Version: "1.0"}
	}

	base := tunnel.NewBase(cfg, root, iann)
	log.Printf("[station] station role  listen=%s isannd=%s", cfg.ListenAddr, cfg.OutboundGateway.URL())

	if err := station.New(base).Run(ctx); err != nil {
		log.Fatal(err)
	}
}
