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
	"fmt"
	"log"

	"github.com/isannai/mesh/pkg/probe"
)

func main() {
	configPath := flag.String("config", "", "config file (default: $ISANN_MESH_CONFIG, else conf/probe.json)")
	once := flag.Bool("once", false, "run a single refresh + fire round and exit")
	// -report reads the database and exits, touching no network. It exists
	// because a node has no sqlite3 on PATH, and "install a sqlite client" is a
	// poor answer to "did my probe fire".
	report := flag.Int("report", 0, "print the last N shots, the queue and the uptime anchors, then exit")
	// -slots draws image orders and prints them without touching anything. The
	// slot table is the one place where a mistake is invisible at runtime — a
	// caption whose alternatives came from the subject's OWN category asks CLIP
	// to tell a fox from a wolf, which fails honest nodes and looks like a
	// model problem. Eyeballing the draw is how that gets caught.
	slotsN := flag.Int("slots", 0, "draw N image orders (prompt + CLIP checks) and exit")
	flag.Parse()

	if *slotsN > 0 {
		probe.PrintImageOrders(*slotsN)
		return
	}

	log.SetFlags(log.LstdFlags)

	cfg, err := probe.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("[probe] config: %v", err)
	}

	// Report BEFORE New(): New asks isannd for this node's identity, and a
	// read-only look at the database should work whether or not the daemon is up.
	if *report > 0 {
		store, err := probe.OpenStore(cfg.DB)
		if err != nil {
			log.Fatalf("[probe] open %s: %v", cfg.DB, err)
		}
		defer store.Close()
		out, err := store.Report(*report)
		if err != nil {
			log.Fatalf("[probe] report: %v", err)
		}
		fmt.Print(out)
		return
	}

	p, err := probe.New(cfg)
	if err != nil {
		log.Fatalf("[probe] %v", err)
	}
	defer p.Close()

	log.Printf("[probe] isannd=%s db=%s schedule=%vs fire=%ds",
		cfg.NodeBridgeAddr, cfg.DB, cfg.Schedule(), cfg.FireIntervalSec)
	// Printed rather than left to be discovered: which nodes write the
	// questions and which judge the images is the setting most likely to be
	// wrong, and its symptom otherwise is a silent fallback to arithmetic.
	log.Printf("[probe] generators=%v clips=%v", cfg.GeneratorNames(), cfg.ClipNames())

	if *once {
		p.Once()
		return
	}
	p.Run()
}
