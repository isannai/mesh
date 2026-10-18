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
// Without a group assignment it idles: no directory fetch, no firing, one log line.
// That is the expected state for a node that merely has the mesh installed.

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/isannai/mesh/pkg/probe"
	"github.com/isannai/mesh/pkg/setup"
)

// options are the command line. They are defined on a FlagSet handed in, so the
// help text can be checked against the same list (help_test.go).
type options struct {
	configPath *string
	once       *bool
	report     *int
	shotID     *int64
	node       *string
	epoch      *int64
	detail     *bool
	slotsN     *int
	version    *bool
}

func defineFlags(fs *flag.FlagSet) *options {
	return &options{
		configPath: fs.String("config", "", "config file (default: $ISANN_MESH_CONFIG, else conf/probe.json)"),
		once:       fs.Bool("once", false, "run a single refresh + fire round and exit"),
		// --report reads the database and exits, touching no network. It exists
		// because a node has no sqlite3 on PATH, and "install a sqlite client" is a
		// poor answer to "did my probe fire".
		report: fs.Int("report", 0, "print the last N shots, the queue and the uptime anchors, then exit"),
		shotID: fs.Int64("id", 0, "print only the shot with this id (the ID column of --report), then exit"),
		node:   fs.String("node", "", "print only the shots of nodes whose id contains this (a prefix such as 0xbada8be8 is enough), then exit"),
		epoch:  fs.Int64("epoch", 0, "print only the shots fired in this epoch (the slot, the EPOCH column of --report), then exit"),
		detail: fs.Bool("a", false, "with --report, --id, --node or --epoch: under each shot, the question, the expected answer and the answer received"),
		// --slots draws image orders and prints them without touching anything. The
		// slot table is the one place where a mistake is invisible at runtime — a
		// caption whose alternatives came from the subject's OWN category asks CLIP
		// to tell a fox from a wolf, which fails honest nodes and looks like a
		// model problem. Eyeballing the draw is how that gets caught.
		slotsN: fs.Int("slots", 0, "draw N image orders (prompt + CLIP checks) and exit"),
		// isannd asks the installed binary for this (`isann version` shows it), so the
		// answer is the file on disk, however it got there.
		version: fs.Bool("version", false, "print the version this binary was built as and exit"),
	}
}

// checkOptions refuses option values that would otherwise fall through to the
// bottom of main and START THE PROBER. Every read option is "0 / empty means not
// given", so `--report 0`, `--id 0`, `--epoch -1`, `--slots 0`, `--node=` or a
// lone `-a` used to skip every branch above and run the prober in the foreground
// (with a database and an isannd it then really fires). Asking to read
// something and getting a second prober is the wrong answer; say what is wrong.
func checkOptions(fs *flag.FlagSet, o *options) error {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if set["report"] && *o.report < 1 {
		return fmt.Errorf("--report needs a number of 1 or more, got %d", *o.report)
	}
	if set["id"] && *o.shotID < 1 {
		return fmt.Errorf("--id needs a shot number of 1 or more, got %d", *o.shotID)
	}
	if set["epoch"] && *o.epoch < 1 {
		return fmt.Errorf("--epoch needs an epoch number of 1 or more, got %d", *o.epoch)
	}
	if set["slots"] && *o.slotsN < 1 {
		return fmt.Errorf("--slots needs a number of 1 or more, got %d", *o.slotsN)
	}
	if set["node"] && strings.TrimSpace(*o.node) == "" {
		return fmt.Errorf("--node needs some text of a node id (a prefix such as 0xbada8be8 is enough)")
	}
	if set["a"] && !set["report"] && !set["id"] && !set["node"] && !set["epoch"] {
		return fmt.Errorf("-a only adds detail to --report, --id, --node or --epoch")
	}
	return nil
}

func main() {
	opt := defineFlags(flag.CommandLine)
	flag.Usage = func() { fmt.Fprint(os.Stderr, helpText) }
	flag.Parse()
	if err := checkOptions(flag.CommandLine, opt); err != nil {
		fmt.Fprintf(os.Stderr, "probe: %v (try: probe.exe help)\n", err)
		os.Exit(2)
	}

	// `probe.exe help` and -h / --help print the same text, and need no config.
	if flag.Arg(0) == "help" {
		fmt.Print(helpText)
		return
	}
	if flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "probe: unknown command %q (try: probe.exe help)\n", flag.Arg(0))
		os.Exit(2)
	}

	if *opt.version {
		fmt.Println("probe", setup.ProbeVersion)
		return
	}

	if *opt.slotsN > 0 {
		probe.PrintImageOrders(*opt.slotsN)
		return
	}

	log.SetFlags(log.LstdFlags)

	cfg, err := probe.LoadConfig(*opt.configPath)
	if err != nil {
		log.Fatalf("[probe] config: %v", err)
	}

	// Report BEFORE New(): New asks isannd for this node's identity, and a
	// read-only look at the database should work whether or not the daemon is up.
	if *opt.report > 0 || *opt.shotID > 0 || *opt.node != "" || *opt.epoch > 0 {
		// 🔴 DO NOT CREATE A DATABASE TO LOOK AT IT. conf/probe.json and db/probe.db
		// are found relative to the current folder, so run from anywhere else the
		// report opened a new, empty db/probe.db there and answered "nothing has
		// been fired yet": a wrong answer, and a stray file left behind.
		if _, err := os.Stat(cfg.DB); err != nil {
			log.Fatalf("[probe] no database at %s (looked up from the current folder). "+
				"Run this in the probe folder, the one holding conf and db", cfg.DB)
		}
		store, err := probe.OpenStore(cfg.DB)
		if err != nil {
			log.Fatalf("[probe] open %s: %v", cfg.DB, err)
		}
		defer store.Close()
		out, err := store.Report(probe.ReportOptions{
			Limit: *opt.report, ShotID: *opt.shotID, Node: *opt.node, Epoch: *opt.epoch, Detail: *opt.detail,
		})
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

	log.Printf("[probe] isannd=%s db=%s refresh=%ds fire=%ds window=+%s..-%s of slot",
		cfg.NodeBridgeAddr, cfg.DB, cfg.RefreshSec, cfg.FireIntervalSec,
		cfg.FireLead(), cfg.FireTail())
	// Printed rather than left to be discovered: which nodes write the
	// questions and which judge the images is the setting most likely to be
	// wrong, and its symptom otherwise is a silent fallback to arithmetic.
	log.Printf("[probe] generators=%v clips=%v", cfg.GeneratorNames(), cfg.ClipNames())

	if *opt.once {
		p.Once()
		return
	}
	p.Run()
}
