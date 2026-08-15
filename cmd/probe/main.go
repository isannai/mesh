package main

// cmd/probe — the faucet prober as its own binary. The binary IS the role.
//
// This is the executable an installed `probe` app mesh launches —
// artifacts/addon/meshes/probe/bin/probe.exe, resolved from mesh.json `bin` and
// started with `-config conf/probe.json` (cwd = the app folder).
//
// Unlike station and control it opens NO public listener: mesh.json declares no
// `isannd.servers`, because the prober only dials out. Everything it needs —
// the node directory, the target nodes themselves — it reaches through the
// local isannd node-bridge, which does the RV lookup, the hole punch and the
// HTTP/3 hop.
//
// Without an appointment it idles: no directory fetch, no firing, one log line.
// That is the expected state for a node that merely has the mesh installed.

import (
	"flag"
	"log"

	"github.com/isannai/mesh/pkg/probe"
)

func main() {
	configPath := flag.String("config", "", "config file (default: $ISANN_MESH_CONFIG, else conf/probe.json)")
	once := flag.Bool("once", false, "run a single refresh + fire round and exit")
	flag.Parse()

	log.SetFlags(log.LstdFlags)

	cfg, err := probe.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("[probe] config: %v", err)
	}
	p, err := probe.New(cfg)
	if err != nil {
		log.Fatalf("[probe] %v", err)
	}
	defer p.Close()

	log.Printf("[probe] isannd=%s db=%s schedule=%vh fire=%ds",
		cfg.NodeBridgeAddr, cfg.DB, cfg.ScheduleHours, cfg.FireIntervalSec)

	if *once {
		p.Refresh()
		p.FireRound()
		return
	}
	p.Run()
}
