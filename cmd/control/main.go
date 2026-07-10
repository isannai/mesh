package main

// cmd/control — the Control Center as its OWN binary. control is PINNED to the
// control (console + forward) role: it ignores any `mode` in the config (there
// is none anymore) and always runs control.New(...). The binary IS the role.
// (Replaced the old mode-switched cmd/proxy, now retired — the station role
// lives in its own cmd/station binary.)
//
// This is the executable an installed `control` app mesh launches —
// artifacts/addon/apps/control/bin/control.exe, resolved from mesh.json `bin`
// and started with `-config conf/control.json` (cwd = the app folder). isannd
// owns the public listener (mesh.json isannd.servers, :6443) and reverse-proxies
// to the backend control listens on (:8080); control serves the console SPA +
// the /node/<id>/* forward on that backend.

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/isannai/mesh/pkg/control"
	"github.com/daesob/http3proxy/pkg/tunnel"
)

func main() {
	configFile := flag.String("config", "", "path to JSON config file (e.g., conf/control.json)")
	flag.Parse()

	if *configFile == "" {
		log.Fatal("[control] --config required (e.g. -config conf/control.json)")
	}
	cfg, err := tunnel.LoadConfig(*configFile)
	if err != nil {
		log.Fatalf("[control] load config: %v", err)
	}
	log.Printf("[control] config loaded from %s", *configFile)

	// Pinned: control is always the control (console + /node forward) role,
	// whatever the config's `mode` says. A split binary makes mode vestigial. The
	// on-wire RV role string + node-id prefix are already "control" / "C:"
	// (pkg/control/rendezvous.go); the deeper internal-naming cleanup (Go types,
	// URL paths, the web console's "Broker" label) is the remaining Phase 3 work.
	cfg.Mode = tunnel.ModeControl

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("[control] signal received (%s), shutting down", sig)
		cancel()
		// Force-exit backstop: if graceful shutdown hangs on a stuck listener /
		// isannd tcp-pipe connection (Windows), bail after 5s. Mirrors cmd/station.
		go func() {
			<-time.After(5 * time.Second)
			log.Printf("[control] graceful shutdown timed out, forcing exit")
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
		log.Printf("[control] isann anchor not found (%v) — using cwd %s", ferr, cwd)
		root = cwd
		iann = &tunnel.IannConfig{Version: "1.0"}
	}

	base := tunnel.NewBase(cfg, root, iann)
	log.Printf("[control] control role  listen=%s isannd=%s", cfg.ListenAddr, cfg.OutboundGateway.URL())

	if err := control.New(base).Run(ctx); err != nil {
		log.Fatal(err)
	}
}
