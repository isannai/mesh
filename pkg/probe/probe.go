package probe

// probe.go — the faucet prober: config, the two loops, and the idle default.
//
// The prober asks public nodes a question and records what comes back, so a
// node can later prove it was doing real work. It talks to nothing but its own
// isannd over loopback — discovery, NAT traversal and the HTTP/3 hop to the
// target all happen there, exactly as they do for `isann infer --nodes <id>`.
//
// IDLE IS THE DEFAULT. Without a group assignment it fetches nothing and
// fires nothing. The mesh is meant to be installed widely and appointed
// selectively, so "installed but not appointed" has to be a quiet, cheap state.
//
// Scope: submit and collect. Scoring the answers comes next; every answer is
// stored verbatim so it can be scored retroactively instead of re-fired.

import (
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/isannai/mesh/pkg/rvnodes"
)

// Config is the prober's settings. Every field has a working default, so an
// absent config file still runs.
type Config struct {
	// NodeBridgeAddr is the local isannd. Empty falls back to ISANN_NODE_URL
	// (injected by the mesh runtime) and then to the loopback default.
	NodeBridgeAddr string `json:"node_bridge_addr"`
	// RefreshSec is how often the node directory is re-read. Membership
	// changes slowly and every fetch costs a round trip.
	RefreshSec int `json:"refresh_sec"`
	// FireIntervalSec is how often the prober checks who has come due. It is a
	// polling cadence, not a rate: what actually gets fired is decided by the
	// schedule below, so a shorter interval only means a due node is served
	// sooner, never that more shots go out.
	FireIntervalSec int `json:"fire_interval_sec"`
	// ScheduleSec is the uptime each shot requires, in SECONDS. The n-th shot
	// comes due once the node has been continuously present that long — see
	// schedule.go. The list length IS the daily cap.
	//
	// Seconds because the same field has to be readable at both ends: the real
	// ladder is hours (3600 = 1h), but a smoke test wants ten seconds, and
	// writing ten seconds in hours is 0.00277.
	ScheduleSec []float64 `json:"schedule_sec"`
	// ScheduleHours is the superseded spelling, read only to migrate configs
	// already in the field. ScheduleSec wins when both are set.
	ScheduleHours []float64 `json:"schedule_hours,omitempty"`
	// TextDeadline is the base answer allowance for a TEXT question, in seconds,
	// scaled up for larger declared models so a bigger machine is not penalised
	// for being one.
	//
	// Named for the track it belongs to, to pair with ImageDeadline. The old
	// spelling said what it waited FOR while its sibling said what it was ABOUT,
	// so the two read like unrelated settings when they are the same knob for
	// the two halves of the prober.
	TextDeadline int `json:"text_deadline_sec"`
	// ResponseDeadline is the superseded spelling, read only to migrate configs
	// already in the field. TextDeadline wins when both are set.
	ResponseDeadline int `json:"response_deadline_sec,omitempty"`
	// Generators are the nodes that write geography/animal/colour questions, in
	// round-robin order. A prober is a small machine and need not host a model
	// itself, so this is normally an allied node.
	//
	// 🔴 ABSENT AND EMPTY MEAN DIFFERENT THINGS, which is why it is a pointer:
	//
	//	absent  → ["this"]   the old behaviour, kept for configs already deployed
	//	[]      → no generation at all; arithmetic only, chosen deliberately
	//
	// Encoding/json cannot tell those apart in a plain slice — both arrive as
	// len 0 — and collapsing them would make "I explicitly turned generation
	// off" silently restart it against a node with no engine.
	//
	// Items are "<node>" or "<node>/<service>"; "" / "this" / "local" / "self"
	// all mean this node. Not a probe target either way: arithmetic is written
	// in code, and image probes are a separate track with their own validator.
	Generators *[]string `json:"generators,omitempty"`
	// GeneratorService is the service name used for entries that do not name
	// one. Allies usually run the same engine, so this carries the common case.
	GeneratorService string `json:"generator_service"`
	// QuestionGeneratorService is the superseded single-service form. Read only
	// to migrate configs already in the field — GeneratorService wins when both
	// are set.
	QuestionGeneratorService string `json:"question_generator_service,omitempty"`
	// Clips are the CLIP validators that judge image probes, same rules as
	// Generators. Absent or empty both mean the image track is NOT run:
	// unlike questions there is no code-generated fallback, and firing at an
	// image node with no judge waiting only burns someone else's GPU.
	Clips *[]string `json:"clips,omitempty"`
	// ClipService is the service name for clip entries that do not name one.
	ClipService string `json:"clip_service"`
	// ImageSizes overrides the resolutions an image order may ask for. Left
	// empty — the normal case — the target's own declared architecture picks:
	// 512x512 for an sd15-class model, 1024x1024 for SDXL. See imageParams.
	//
	// It exists as an operator escape hatch for a fleet whose declared
	// architecture does not match what the hardware can actually render.
	//
	// 🔴 Only sizes SD actually handles. It produces garbage or refuses outright
	// on arbitrary dimensions, and that would read as an honest node failing.
	//
	// There is no step knob, deliberately. The engine manifest declares
	// `steps` with a default and the prober simply omits the field, so the
	// engine's own default applies — see imageRun.
	ImageSizes []string `json:"image_sizes"`
	// ImageDeadline is how long a node has to draw a picture, in seconds.
	//
	// Much longer than the text allowance and configurable rather than fixed:
	// generation time is dominated by the card, and a 4GB GPU spilling to
	// system memory takes minutes on the same 512x512 that a 12GB card does in
	// half a minute. A cap tuned to the fast machine records the slow one as a
	// timeout, which measures our impatience rather than the node.
	ImageDeadline int `json:"image_deadline_sec"`
	// DeferMax is how many rounds an image shot waits for the node's queue to
	// clear before going out anyway.
	//
	// It exists to bound politeness, not to enforce it. Ordering into an empty
	// queue is what makes the measured time mean something, but a node that is
	// busy all day is a node with customers — the exact node the faucet is for —
	// and waiting for it to be idle would exclude it permanently. So the wait
	// ends. The padded measurement that produces is harmless: queueing can only
	// make a time slower, and capability is judged on a node's FASTEST shot of
	// the day, so a padded one loses to its own clean ones.
	//
	// 0 or negative means "never defer" — fire on the first attempt every time.
	DeferMax int `json:"defer_max"`
	// FireAtSelf lets the prober fire at its OWN node.
	//
	// Off by default, and for a reason that is not arbitrary: a prober signs the
	// tickets, so firing at itself means signing tickets for a machine it
	// controls. That is not a measurement under any reading.
	//
	// It exists because a single-PC setup has nowhere else to aim. isannd
	// short-circuits a self-reference to in-process serving (isSelfNodeRef), so
	// the whole submit → collect → judge path still runs — only the RV lookup
	// and the NAT hop are skipped, and neither is what the image track is
	// exercising. Turn it on to try the pipeline; leave it off in production.
	FireAtSelf bool   `json:"fire_at_self"`
	DB         string `json:"db"`
	// QueueLowWater is the refill threshold: when a category has fewer than
	// this many unused questions left, another batch is generated. Questions
	// are consumed once and discarded (that is what makes a cache attack
	// impossible), so the queue drains continuously.
	QueueLowWater int `json:"queue_low_water"`
	// ObservationDays is how long hourly directory snapshots are kept. The
	// eventual "was this node public all day" rule reads them, so it outlives
	// the 15-day claim window.
	ObservationDays int `json:"observation_days"`
	// QuestionFanout is how many nodes are asked the SAME question in one
	// round — always in different /24s (see FireRound).
	//
	// It trades question supply against cross-checking. Higher means fewer
	// questions to generate and more answers to compare; lower means the
	// opposite. The re-check panel wants a majority from at least 3-4 distinct
	// networks, so this should not go below about 4.
	QuestionFanout int `json:"question_fanout"`
}

// defaultIsanndURL is the node-bridge's loopback address.
const defaultIsanndURL = "http://127.0.0.1:8443"

// DefaultConfig returns the settings the prober runs with when nothing is set.
func DefaultConfig() Config {
	return Config{
		RefreshSec:      3600,
		FireIntervalSec: 60,
		// 3 · 6 · 9 · 12 · 15 hours, written out so the file that ships is the
		// same shape an operator edits.
		ScheduleSec:      []float64{10800, 21600, 32400, 43200, 54000},
		TextDeadline:     30,
		GeneratorService: "llm-api",
		ClipService:      "clip-api",
		// 🔴 ZERO means "derive it from the node's own average", which is what
		// imageDeadline was written to do. It used to be seeded with 300 here,
		// and that seed made the derivation dead code — the override branch is
		// checked first, so every node got a flat five minutes however slow it
		// had told us it was. A 4GB card reporting avg_job_sec 370 timed out on
		// every shot while drawing every picture correctly.
		ImageDeadline: 0,
		// Ten rounds, which at the default one-minute cadence is ten minutes.
		// Sized against the job, not the clock: one SD picture takes 1.5 to 2.5
		// minutes, so a handful of rounds is barely one job length and would
		// give up before a genuinely short queue had drained. Ten covers several
		// jobs while staying negligible against a schedule whose steps are hours
		// apart.
		DeferMax: 10,
		// 🔴 ImageSizes stays EMPTY by default. Seeding it here overrode the
		// architecture logic entirely — imageParams checks the config first, so
		// a default value meant every node got the same list no matter what it
		// declared, and the sd15/SDXL split never ran at all.
		ImageSizes:      nil,
		DB:              "db/probe.db",
		QueueLowWater:   10,
		ObservationDays: 30,
		QuestionFanout:  5,
	}
	// Generators / Clips stay nil here on purpose. Their defaults differ from
	// "empty" and are applied in LoadConfig, after the file has had its say —
	// see defaultGenerators / defaultClips.
}

// Schedule resolves the uptime ladder in seconds.
//
// ScheduleSec wins; `schedule_hours` is honoured only when the new key was left
// alone, so a config mid-migration is not steered by the field being replaced.
func (c Config) Schedule() []float64 {
	if len(c.ScheduleSec) > 0 {
		return c.ScheduleSec
	}
	if len(c.ScheduleHours) > 0 {
		return hoursToSec(c.ScheduleHours)
	}
	return nil // parseScheduleSec falls back to DefaultSchedule
}

// defaultGenerators is what an ABSENT `generators` means: this node, as before.
// An empty list is not this — that is the operator saying "no generation".
func defaultGenerators() []string { return []string{selfNode} }

// defaultClips is what an absent `clips` means: none. The image track needs a
// judge and there is no local fallback for one, so it stays off until named.
func defaultClips() []string { return nil }

// generatorEntries returns the parsed question-writer pool.
func (c Config) generatorEntries() []poolEntry {
	items := defaultGenerators()
	if c.Generators != nil {
		items = *c.Generators
	}
	return parsePool(items, c.GeneratorService)
}

// clipEntries returns the parsed validator pool.
func (c Config) clipEntries() []poolEntry {
	items := defaultClips()
	if c.Clips != nil {
		items = *c.Clips
	}
	return parsePool(items, c.ClipService)
}

// GeneratorNames / ClipNames render the resolved pools as "<node>/<service>"
// for the boot log. Resolved, not raw: the point is to show what the defaults
// and the "this"/"local"/"self" spellings actually turned into, which is where
// a misconfiguration hides.
func (c Config) GeneratorNames() []string { return describePool(c.generatorEntries()) }
func (c Config) ClipNames() []string      { return describePool(c.clipEntries()) }

func describePool(entries []poolEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.String())
	}
	return out
}

// LoadConfig reads path, filling anything absent with a default.
//
// A missing file is fine — the defaults are the intended settings, and the file
// exists to override them rather than to enumerate them.
func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		path = os.Getenv("ISANN_MESH_CONFIG")
	}
	if path == "" {
		path = filepath.Join("conf", "probe.json")
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return cfg, err
	}
	if err == nil {
		// The seeded default is cleared before unmarshalling so that a file
		// carrying only the superseded `schedule_hours` is not overruled by the
		// default sitting in ScheduleSec. Restored below if the file set
		// neither.
		seeded := cfg.ScheduleSec
		cfg.ScheduleSec = nil
		if err := json.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("parse %s: %w", path, err)
		}
		if len(cfg.ScheduleSec) == 0 && len(cfg.ScheduleHours) == 0 {
			cfg.ScheduleSec = seeded
		}
	}
	// Absorb the superseded single-service key. Configs are already deployed
	// with it, and ignoring it would quietly move an operator's generation to
	// whatever the default happens to be. It only applies when the new key was
	// left at its default — an explicit generator_service always wins.
	//
	// The old key's "empty means arithmetic only" spelling is NOT carried over;
	// that statement now belongs to `generators: []`. An operator who had it
	// empty ends up attempting generation against this node, failing, and being
	// told so in the log — the same arithmetic-only end state, reached loudly.
	if s := strings.TrimSpace(cfg.QuestionGeneratorService); s != "" &&
		cfg.GeneratorService == DefaultConfig().GeneratorService {
		cfg.GeneratorService = s
	}

	// Mesh config also arrives as environment variables, and those win: the
	// mesh runtime is what an operator edits through `isann mesh config`.
	applyEnvOverrides(&cfg)

	// Absorb the superseded deadline spelling, AFTER the environment so a node
	// still setting PROBE_RESPONSE_DEADLINE_SEC is carried over too. Dropping
	// either silently would hand an operator's tuned value back to the default
	// and make every large model start timing out.
	if cfg.ResponseDeadline > 0 && cfg.TextDeadline == DefaultConfig().TextDeadline {
		cfg.TextDeadline = cfg.ResponseDeadline
	}

	if cfg.NodeBridgeAddr == "" {
		cfg.NodeBridgeAddr = strings.TrimSpace(os.Getenv("ISANN_NODE_URL"))
	}
	if cfg.NodeBridgeAddr == "" {
		cfg.NodeBridgeAddr = defaultIsanndURL
	}
	if cfg.FireIntervalSec < 1 {
		cfg.FireIntervalSec = 1
	}
	if cfg.RefreshSec < 60 {
		cfg.RefreshSec = 60
	}
	// Below 2 there is no sharing at all and the question supply becomes the
	// binding constraint again; the re-check panel wants 3-4 distinct networks.
	if cfg.QuestionFanout < 2 {
		cfg.QuestionFanout = 2
	}
	return cfg, nil
}

func applyEnvOverrides(cfg *Config) {
	for name, dst := range map[string]*string{
		"PROBE_GENERATOR_SERVICE": &cfg.GeneratorService,
		"PROBE_CLIP_SERVICE":      &cfg.ClipService,
		"PROBE_DB":                &cfg.DB,
	} {
		if v := os.Getenv(name); v != "" {
			*dst = v
		}
	}
	// Superseded name, still honoured so a mesh config carrying it keeps working.
	if v := os.Getenv("PROBE_QUESTION_GENERATOR_SERVICE"); v != "" {
		cfg.GeneratorService = v
	}
	// The list forms arrive comma-separated, because a mesh env var is one
	// string. "none" is how a list is emptied through the environment — an
	// empty env var is indistinguishable from an unset one, so it cannot mean
	// "[]" the way the JSON spelling can.
	for name, dst := range map[string]**[]string{
		"PROBE_GENERATORS": &cfg.Generators,
		"PROBE_CLIPS":      &cfg.Clips,
	} {
		v := strings.TrimSpace(os.Getenv(name))
		if v == "" {
			continue
		}
		var items []string
		if !strings.EqualFold(v, "none") {
			for _, part := range strings.Split(v, ",") {
				if p := strings.TrimSpace(part); p != "" {
					items = append(items, p)
				}
			}
		}
		if items == nil {
			items = []string{}
		}
		*dst = &items
	}
	for name, dst := range map[string]*int{
		"PROBE_REFRESH_SEC":           &cfg.RefreshSec,
		"PROBE_FIRE_INTERVAL_SEC":     &cfg.FireIntervalSec,
		"PROBE_TEXT_DEADLINE_SEC":     &cfg.TextDeadline,
		"PROBE_RESPONSE_DEADLINE_SEC": &cfg.ResponseDeadline, // superseded; folded in by LoadConfig
		"PROBE_QUESTION_FANOUT":       &cfg.QuestionFanout,
		"PROBE_IMAGE_DEADLINE_SEC":    &cfg.ImageDeadline,
		"PROBE_DEFER_MAX":             &cfg.DeferMax,
	} {
		if v := os.Getenv(name); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				*dst = n
			}
		}
	}
	// 🔴 Only "true"/"1" turns it ON, and nothing turns it off. A prober firing
	// at its own node signs tickets for itself, which is the one shape a faucet
	// must not have — so the environment may enable it for a single-node bench
	// but a typo'd value must never silently do so.
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PROBE_FIRE_AT_SELF"))) {
	case "true", "1", "yes":
		cfg.FireAtSelf = true
	}
}

// Prober holds the running state.
type Prober struct {
	cfg   Config
	store *Store
	firer *Firer
	gen   *Generator
	http  *http.Client
	rng   *rand.Rand

	// validator judges image probes. Disabled unless `clips` names one — there
	// is no local fallback for judging, and firing at an image node with no
	// judge waiting only burns someone else's GPU.
	validator *Validator
	// exclude is the set of node addresses never fired at: this node and its
	// helpers. Computed once — the config does not change under a running
	// process, and neither does this node's identity.
	exclude map[string]bool

	// self is this node's address, and signKey the hardware-derived key behind
	// it. The probe signs with the NODE IDENTITY key, never a wallet key —
	// design G22: this signature is worth one free inference, not money.
	self    string
	signKey *ecdsa.PrivateKey

	assign     Assignment
	hasAssign  bool
	targets    []Target
	imgTargets []Target
	idleLogged bool

	// schedule is the uptime ladder; present is how much of today each node has
	// been up for, totalled across gaps. Both recomputed on refresh.
	schedule []time.Duration
	present  map[string]time.Duration

	// metrics is the RV's volatile per-service view, refreshed alongside the
	// directory. It answers how long this node typically takes (imageDeadline),
	// which /v1/nodes cannot. nil until the first successful poll, and a failed
	// poll leaves the previous one rather than blanking it — stale numbers beat
	// no numbers for a sizing decision.
	//
	// 🔴 It does NOT decide whether a node is busy right now. One SD picture
	// outlasts the heartbeat interval, so by the time this reads "idle" the
	// queue has turned over. That reading comes from the node itself, per shot,
	// in holdBusy.
	metrics *rvnodes.Metrics

	// deferrals counts consecutive rounds we held off firing at a (node,
	// service) because its queue was busy. Cleared the moment a shot goes out —
	// the count then belongs to that shot's row.
	//
	// In memory on purpose: it measures how long we have been waiting, and a
	// prober restart already resets the uptime anchors it waits alongside.
	// Persisting one without the other would be the inconsistent half.
	deferrals map[string]int
	deferMax  int

	// openShots is the (node, service) set with a picture still on order, so a
	// second is never sent on top of it. Rebuilt each round from the database
	// rather than tracked incrementally: the database is the record, and a
	// prober that restarted mid-flight has to see the shots it left behind.
	openShots map[string]bool

	// refilling guards the background question generator so only one round
	// runs at a time. Atomic because Refresh (one goroutine) starts it and the
	// generator goroutine clears it.
	refilling atomic.Bool
	// genRetryAt is when a prober in the arithmetic fallback may try a writer
	// again, and genBackoff is the gap it will use. Refills otherwise ride the
	// directory refresh, which is hourly in production — far too long to sit on
	// arithmetic after a cold start.
	//
	// Both are touched only from the firing goroutine. genFailures is not, which
	// is why it is atomic: refillQueue writes it from a background goroutine.
	genRetryAt time.Time
	genBackoff time.Duration

	// 🔴 ATOMIC BECAUSE TWO GOROUTINES TOUCH IT. refillQueue runs in the
	// background (startRefill) and writes it; the firing loop reads it to decide
	// whether to retry. It was a plain int while only the background goroutine
	// looked at it, and the retry is what made that unsafe.
	//
	// genFailures counts consecutive generation failures. After a few, the
	// prober stops asking every hour: an engine that is not there will not be
	// there next hour either, and the log line is the same one every time.
	genFailures atomic.Int32
}

// roundStats tallies one firing round for the summary line.
//
// Atomic because the members of a /24 group fire concurrently — they have to,
// since a farm sharing one GPU passes serial probes and fails simultaneous ones.
type roundStats struct {
	fired     atomic.Int64
	answered  atomic.Int64
	pass      atomic.Int64
	fail      atomic.Int64
	truncated atomic.Int64
	queueFull atomic.Int64
	refused   atomic.Int64
	timeout   atomic.Int64
}

// String renders the tally, omitting whatever did not happen. A clean round
// reads "3 fired, 3 answered (3 pass)" rather than a row of zeros.
func (s *roundStats) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d fired, %d answered", s.fired.Load(), s.answered.Load())
	var parts []string
	for _, p := range []struct {
		n     int64
		label string
	}{
		{s.pass.Load(), "pass"},
		{s.fail.Load(), "fail"},
		{s.truncated.Load(), "truncated"},
		{s.queueFull.Load(), "queue full"},
		{s.refused.Load(), "refused"},
		{s.timeout.Load(), "timeout"},
	} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.label))
		}
	}
	if len(parts) > 0 {
		fmt.Fprintf(&b, " (%s)", strings.Join(parts, ", "))
	}
	return b.String()
}

// record folds one shot's outcome into the tally: fired here and settled here.
func (s *roundStats) record(outcome, verdict string) {
	s.fired.Add(1)
	s.settle(outcome, verdict)
}

// settle folds in an outcome for a shot fired in an EARLIER round.
//
// 🔴 It must not touch the fired count. An image shot is counted when it goes
// out and settled whenever its picture turns up, which is usually a different
// round — adding to `fired` here would count that one shot twice and make the
// daily tally drift upward all day.
func (s *roundStats) settle(outcome, verdict string) {
	switch outcome {
	case OutcomeAnswered:
		s.answered.Add(1)
	case OutcomeQueueFull:
		s.queueFull.Add(1)
	case OutcomeTimeout:
		s.timeout.Add(1)
	default:
		s.refused.Add(1)
	}
	switch verdict {
	case VerdictPass:
		s.pass.Add(1)
	case VerdictFail:
		s.fail.Add(1)
	case VerdictTruncated:
		s.truncated.Add(1)
	}
}

// genFailureLimit is how many consecutive failed generation rounds it takes
// before the prober stops trying. math needs no engine, so falling back to it
// is a working state rather than a degraded one worth retrying at.
const genFailureLimit = 3

// New builds a Prober and opens its database.
//
// The signing key is NOT opened here: which key to open is decided by the
// appointment, and the appointment is not known until the first refresh.
func New(cfg Config) (*Prober, error) {
	store, err := OpenStore(cfg.DB)
	if err != nil {
		return nil, err
	}
	gens, clips := cfg.generatorEntries(), cfg.clipEntries()
	client := &http.Client{Timeout: 20 * time.Second}
	return &Prober{
		cfg:       cfg,
		store:     store,
		firer:     NewFirer(cfg.NodeBridgeAddr),
		gen:       NewGenerator(cfg.NodeBridgeAddr, gens),
		validator: NewValidator(cfg.NodeBridgeAddr, clips),
		http:      client,
		rng:       rand.New(rand.NewSource(time.Now().UnixNano())),
		schedule:  parseScheduleSec(cfg.Schedule()),
		present:   map[string]time.Duration{},
		deferrals: map[string]int{},
		deferMax:  cfg.DeferMax,
		openShots: map[string]bool{},
		// Derived, not asked. This used to be a call to isannd's /info; the
		// same address comes out of the hardware, so the round trip bought
		// nothing but a way to fail at startup. An error here leaves the set
		// smaller and the prober may waste a shot on itself, which Refresh
		// then reports properly.
		exclude: excluded(selfIfExcluded(cfg, selfAddressOrEmpty())),
	}, nil
}

func (p *Prober) Close() error { return p.store.Close() }

// Run drives the two loops until interrupted.
//
// Two cadences, not one. The directory refreshes hourly because node membership
// changes slowly and every fetch costs a round trip; firing happens by the
// minute because spreading shots across the day is what makes them evidence of
// being CONTINUOUSLY online rather than of having been alive once.
func (p *Prober) Run() {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	p.Refresh()

	refresh := time.NewTicker(time.Duration(p.cfg.RefreshSec) * time.Second)
	fire := time.NewTicker(time.Duration(p.cfg.FireIntervalSec) * time.Second)
	defer refresh.Stop()
	defer fire.Stop()

	for {
		select {
		case <-stop:
			log.Printf("[probe] shutting down")
			return
		case <-refresh.C:
			p.Refresh()
		case <-fire.C:
			p.FireRound()
		}
	}
}

// Once runs a single refresh, refill and fire round to completion.
//
// 🔴 It cannot just be Refresh + FireRound. Refresh starts the question refill
// in the BACKGROUND (see startRefill), which is right for the long-running loop
// and wrong here: the caller returns as soon as FireRound does, so the generator
// goroutine is killed mid-call and the round fires from a queue that was never
// filled. On a fresh database that is EVERY question, and the run looks like a
// working prober that simply had nothing to ask.
func (p *Prober) Once() {
	p.Refresh()
	// Poll rather than share a channel: refills are started from two places
	// (every Refresh) and only this one waits, so a WaitGroup would have to be
	// reset on each round for a case that happens once per process.
	for p.refilling.Load() {
		time.Sleep(200 * time.Millisecond)
	}
	p.FireRound()
}

// Refresh re-reads the assignment, the directory and the question queue.
//
// The assignment is checked FIRST and everything else is skipped without one.
// An unappointed prober must not poll the RV: that would cost a round trip per
// hour per idle node, for a list it has no right to act on.
func (p *Prober) Refresh() {
	// Who we are. Hardware-derived, so this is also the address the RV knows
	// this node by and the one an operator writes into faucet.json's `probers`.
	key, self, err := NodeSigningKey()
	if err != nil {
		log.Printf("[probe] %v — idle", err)
		p.targets, p.hasAssign = nil, false
		return
	}
	p.signKey, p.self = key, self

	// This slot's groups. Absent is the ORDINARY state for a node that runs the
	// mesh but was not named a prober, so it is logged once per transition
	// rather than once per poll.
	assign, ok, err := FetchAssignment(p.cfg.NodeBridgeAddr, self)
	if err != nil {
		log.Printf("[probe] %v", err)
		return // keep the previous assignment: one flaky poll is not a retirement
	}
	if !ok {
		if !p.idleLogged {
			log.Printf("[probe] not a prober this slot — idle (the RV names its probers in faucet.json; this node is %s)", self)
			p.idleLogged = true
		}
		p.assign, p.hasAssign, p.targets = Assignment{}, false, nil
		return
	}
	if assign.Epoch != p.assign.Epoch {
		log.Printf("[probe] epoch %d: %d group(s) to check, root %s (rv=%s)",
			assign.Epoch, len(assign.Groups), assign.Root, assign.RV)
	}
	p.assign, p.hasAssign, p.idleLogged = assign, true, false

	nodes, err := rvnodes.Fetch(p.cfg.NodeBridgeAddr, true)
	if err != nil {
		log.Printf("[probe] %v", err)
		return
	}
	// The volatile view, polled with the directory. A failure here is not fatal
	// to the round: firing still works, it just falls back to the floor
	// deadline and skips the busy check. Blanking p.metrics on error would turn
	// one flaky poll into a round that mis-sizes every deadline.
	if m, merr := rvnodes.FetchMetrics(p.cfg.NodeBridgeAddr); merr != nil {
		log.Printf("[probe] metrics: %v (using previous)", merr)
	} else {
		p.metrics = m
	}

	now := time.Now()
	if err := p.store.RecordObservations(observationsOf(nodes), now); err != nil {
		log.Printf("[probe] record observations: %v", err)
	}
	// 🔴 The ASSIGNMENT decides who, the DIRECTORY decides how.
	//
	// The assignment carries addresses and nothing else, so it cannot say which
	// service to aim at or whether the node has one ready. eligible() still
	// filters for a ready text service — the RV cannot know that, it only knows
	// who was online at the boundary.
	// 🔴 Both counts are kept. Reporting only the final number makes two very
	// different failures look identical: "the directory has nobody I can fire
	// at" (no ready text service) versus "everyone ready belongs to another
	// prober's group". The fix lives in a different place for each.
	fireable := eligible(nodes, p.exclude)
	p.targets = p.assignedTargets(fireable)
	// Image targets only when there is a judge. Ordering a picture with no
	// validator to look at it burns someone else's GPU for a result that gets
	// thrown away — see image.go.
	if p.validator.Enabled() {
		p.imgTargets = p.assignedTargets(imageTargets(nodes, p.exclude))
	} else {
		p.imgTargets = nil
	}

	// Time present today, recomputed from the observation history this poll just
	// extended. Derived from the table rather than kept in memory so a prober
	// restart does not reset everyone's day.
	if sightings, err := p.store.SightingsToday(dayStart(now)); err != nil {
		log.Printf("[probe] sightings: %v", err)
	} else {
		p.present = presenceFrom(sightings)
	}
	log.Printf("[probe] directory: %d nodes, %d fireable, %d assigned, %d image (epoch %d)",
		len(nodes), len(fireable), len(p.targets), len(p.imgTargets), p.assign.Epoch)
	if len(fireable) == 0 && len(nodes) > 1 {
		// Nothing to do, and the reason is upstream of the faucet entirely: a
		// node qualifies only with a text service reporting server_ready, and
		// the llama engine does not report it today. Said once here because
		// the assignment looks healthy while nothing is ever fired.
		log.Printf("[probe] no node advertises a ready text service - nothing can be fired at (server_ready)")
	}

	p.startRefill()
	p.prune(now)
}

// startRefill tops the question queue up in the BACKGROUND.
//
// 🔴 Not inline. Generating a batch means a real inference call on this node's
// own engine, which can take a minute when the engine is cold — and Refresh
// shares its goroutine with the firing loop, so a slow generation would stall
// firing for as long as it ran. The prober would look alive and quietly stop
// probing.
//
// One refill at a time: a second one starting while the first is still waiting
// on the engine would queue work behind it for no benefit. If the previous
// round is still going, this one is skipped and the next hour picks it up.
// genRetryEvery is the FIRST gap a prober stuck on arithmetic waits before
// trying a writer again. It doubles from there — see nextGenBackoff.
//
// A minute, matching the fire tick: the case this exists for is a boot that
// raced the hole punch, and that resolves in seconds.
const genRetryEvery = time.Minute

// nextGenBackoff doubles the retry gap, capped at the refresh period.
//
// The cap is what keeps a dead writer from being expensive: once the gap
// reaches the directory refresh, retrying costs exactly what the old
// refresh-driven refill cost, and the fallback has stopped being a special
// case. Below the cap the sequence is 1, 2, 4, 8… minutes, so a transient
// failure is picked up almost immediately and a permanent one fades out.
func nextGenBackoff(current, refresh time.Duration) time.Duration {
	if refresh < genRetryEvery {
		refresh = genRetryEvery // a very short refresh is still the ceiling
	}
	next := genRetryEvery
	if current > 0 {
		next = current * 2
	}
	if next > refresh {
		next = refresh
	}
	return next
}

func (p *Prober) startRefill() {
	if !p.refilling.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer p.refilling.Store(false)
		p.refillQueue()
	}()
}

// refillQueue tops up any category running low.
func (p *Prober) refillQueue() {
	depth, err := p.store.QueueDepth()
	if err != nil {
		log.Printf("[probe] queue depth: %v", err)
		return
	}
	now := time.Now()

	// Code-written categories first: they need no engine, cannot fail, and
	// cannot run dry, so the queue is never empty even when every writer is
	// unreachable.
	for _, spec := range codeSpecs {
		if depth[spec.cat] >= p.cfg.QueueLowWater {
			continue
		}
		if _, err := p.store.AddQuestions(FillCode(spec, batchTarget, p.rng), now); err != nil {
			log.Printf("[probe] refill %s: %v", spec.cat, err)
		}
	}
	// Everything below needs a writer — this node or an ally. With none the
	// prober runs on arithmetic alone, which is not so much a degraded mode as
	// a narrower one: math is half the intended mix anyway, and it is the only
	// category whose answers are certain, so it never needs a re-check panel.
	if !p.gen.HasEngine() {
		if p.genFailures.Load() == 0 {
			log.Printf("[probe] no question writers configured (`generators`) — arithmetic questions only")
			p.genFailures.Store(genFailureLimit) // say it once, not every hour
		}
		return
	}
	// 🔴 NO EARLY RETURN ON genFailures. It used to stop trying here, and that
	// was a one-way door: the counter only clears on a success, and a success
	// needs an attempt. One bad minute pinned the prober to arithmetic until
	// somebody restarted it.
	//
	// The failure that triggered it is not even a broken engine. The prober
	// refills the moment it boots, before isannd has dialled the writer's node,
	// so the first call gets a 502 from an unfinished hole punch. Three of those
	// and the day was over.
	//
	// genFailures now gates the LOG, not the attempt: the retry costs one round
	// trip per refresh (hourly in production) and buys back the whole category
	// mix as soon as the peer answers.
	failed := false
	for _, spec := range genSpecs {
		if depth[spec.cat] >= p.cfg.QueueLowWater {
			continue
		}
		qs, err := p.gen.FillCategory(spec.cat)
		if err != nil {
			log.Printf("[probe] refill %s: %v", spec.cat, err)
			failed = true
			continue
		}
		added, err := p.store.AddQuestions(qs, now)
		if err != nil {
			log.Printf("[probe] store %s batch: %v", spec.cat, err)
			continue
		}
		// Duplicates are worth saying out loud: a batch that is nearly all
		// repeats means the topic's answer space is running dry, and the queue
		// will start leaning on arithmetic whether or not anyone notices.
		if added < len(qs) {
			log.Printf("[probe] %s: %d new, %d already asked", spec.cat, added, len(qs)-added)
		}
		// One success clears the count. Announced when it ends a fallback, so the
		// recovery is as visible in the log as the failure was.
		if p.genFailures.Load() >= genFailureLimit {
			log.Printf("[probe] question generation recovered — %s questions are back", spec.cat)
		}
		p.genFailures.Store(0)
	}
	if failed {
		n := p.genFailures.Add(1)
		// Said once, on the crossing. Repeating it every retry would bury the
		// round lines, and the state it describes does not change until a
		// success clears the counter — which is logged in its own right.
		if n == genFailureLimit {
			log.Printf("[probe] question generation has failed %d times — arithmetic only until a writer answers (check `generators`: %v)",
				n, p.gen.Writers())
		}
	}
}

// prune drops history past its usefulness.
func (p *Prober) prune(now time.Time) {
	cutoff := now.AddDate(0, 0, -p.cfg.ObservationDays)
	if n, err := p.store.PruneObservations(cutoff); err != nil {
		log.Printf("[probe] prune observations: %v", err)
	} else if n > 0 {
		log.Printf("[probe] pruned %d observations", n)
	}
	if _, err := p.store.PruneConsumed(cutoff); err != nil {
		log.Printf("[probe] prune questions: %v", err)
	}
}

// FireRound fires this round's share of the day's work.
//
// HOW QUESTIONS ARE SHARED — the two rules point in opposite directions, and
// both matter:
//
//	within one /24    DIFFERENT questions. Nodes behind one connection may be
//	                  one machine wearing several hats; giving them the same
//	                  question lets it generate once and copy the answer, which
//	                  is exactly the shortcut the simultaneous burst exists to
//	                  expose.
//	across /24s       THE SAME question. Unrelated networks answering the same
//	                  thing at the same moment is a free cross-check, and it is
//	                  what the eventual re-check panel needs: a majority drawn
//	                  from DIFFERENT /24s, so one owner's machines cannot form
//	                  their own majority.
//
// So a round draws only as many questions as the largest group has members —
// the i-th node of every group gets question i. That is also what makes the
// question supply affordable: 50,000 shots a day would need 50,000 questions
// one-per-shot, but a few thousand this way.
func (p *Prober) FireRound() {
	now := time.Now()

	// One line per round, so a working prober is not silent. Declared up here
	// because every stage reports into it — collection included, which is why a
	// round can show more answers than it fired shots.
	var stats roundStats

	// 🔴 COLLECTION RUNS FIRST AND IS NOT GATED. A picture already ordered has
	// to be fetched whether or not we may fire again: the assignment could
	// have expired, the directory could have come back empty, and neither
	// changes the fact that a node drew something for us and the station will
	// drop it after DoneTTL. Gating this behind the fire checks would strand
	// those rows open forever — and an open row keeps its node ineligible.
	// 🔴 COLD START RECOVERY. The prober refills the moment it boots, before
	// isannd has dialled the writer's node, so the first call meets an
	// unfinished hole punch and comes back 502. Refills otherwise only happen on
	// the directory refresh, so that one-second failure would cost a full
	// refresh period — an hour in production — of arithmetic-only questions.
	//
	// While in the fallback, try again on the fire tick instead. It costs one
	// round trip a minute at worst, the log for it is already suppressed, and it
	// turns "an hour" into "the next minute".
	if p.genFailures.Load() < genFailureLimit {
		p.genBackoff = 0 // out of the fallback: the next cold start starts fresh
	} else if p.gen.HasEngine() && now.After(p.genRetryAt) {
		// 🔴 DOUBLING, NOT A FIXED MINUTE. A boot that raced the hole punch
		// recovers on the first retry, so the first gap wants to be short. A
		// writer that is genuinely gone would otherwise cost a dial, a punch
		// attempt and a timeout every minute for as long as it stays down —
		// 1440 of them a day, each one asking the RV to coordinate a punch to a
		// node that is not there.
		//
		// The ceiling is the refresh period, so a dead writer settles back to
		// exactly the cadence it had before any of this existed.
		p.genBackoff = nextGenBackoff(p.genBackoff, time.Duration(p.cfg.RefreshSec)*time.Second)
		p.genRetryAt = now.Add(p.genBackoff)
		p.startRefill()
	}

	p.collectRound(now, &stats)
	defer func() {
		if stats.fired.Load() > 0 || stats.answered.Load() > 0 {
			log.Printf("[probe] round: %s", &stats)
		}
	}()

	if !p.hasAssign || (len(p.targets) == 0 && len(p.imgTargets) == 0) {
		return
	}
	if p.assign.Stale(now) {
		// The slot turned over mid-round. Targets computed under the old
		// assignment carry the old root, and a node that has already moved on
		// would reject every one of them.
		return
	}

	counts, err := p.store.ShotCountsToday(dayStart(now))
	if err != nil {
		log.Printf("[probe] shot counts: %v", err)
		return
	}

	// No random draw and no budget: a node is here because it EARNED the shot
	// by staying up past the next threshold. Load spreads on its own, since
	// nodes anchor at whatever time they happened to connect.

	// 🔴 The two tracks are independent. An earlier version returned here when
	// no text node was due, which silently took the image round with it — a
	// node serving only sd-api was then never fired at, and the log said
	// nothing at all because the summary line lives past this point.
	due := dueTargets(p.targets, p.present, counts, p.schedule)
	groups := groupBySlash24(due)
	if len(groups) > 0 {
		p.fireTextGroups(groups, now, &stats)
	}

	p.fireImageRound(now, counts, &stats)
}

// collectRound asks after every picture still on order and rebuilds the open set.
//
// 🔴 THE OPEN SET IS REBUILT FROM THE DATABASE, not carried in memory. A prober
// that restarted mid-flight has orders outstanding it knows nothing about, and
// an in-memory set would let it fire again on top of them — the queue growth
// this whole arrangement exists to stop. The database is the record.
//
// Failures are per shot. One node being unreachable must not stop the others
// from being collected.
func (p *Prober) collectRound(now time.Time, stats *roundStats) {
	open, err := p.store.PendingShots()
	if err != nil {
		// 🔴 Fail CLOSED for firing: without the list we cannot tell which nodes
		// already have a picture on order, and firing blind is what stacks jobs
		// on a slow node. The previous set stands for this round.
		log.Printf("[probe] pending shots: %v — not firing at image nodes this round", err)
		return
	}

	var images []Shot
	for _, sh := range open {
		if sh.QuestionID != 0 {
			// A text shot. Those are collected inline in the same round they are
			// fired; one left open here is the residue of a restart or a crash,
			// and its answer is long gone from the station.
			if e := p.store.FailShot(sh.ID, now, OutcomeSkipped); e != nil {
				log.Printf("[probe] close orphaned text shot: %v", e)
			}
			continue
		}
		images = append(images, sh)
	}

	// 🔴 CONCURRENT, because one unreachable node must not spend the round. Each
	// peek carries a 15s timeout, so a handful of dead nodes collected in series
	// would outlast the firing interval and the next round would be skipped
	// outright — the prober would go quiet for a reason nothing reports.
	var (
		mu   sync.Mutex
		next = map[string]bool{}
		wg   sync.WaitGroup
		sem  = make(chan struct{}, maxConcurrentImages)
	)
	for _, sh := range images {
		wg.Add(1)
		go func(sh Shot) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if p.collectImageShot(sh, now, stats) {
				return // settled, one way or another
			}
			mu.Lock()
			next[shotKey(sh.NodeID, sh.Service)] = true
			mu.Unlock()
		}(sh)
	}
	wg.Wait()
	p.openShots = next
}

// fireTextGroups runs the text half of a round.
//
// HOW QUESTIONS ARE SHARED — the two rules point in opposite directions, and
// both matter:
//
//	within one /24    DIFFERENT questions. Nodes behind one connection may be
//	                  one machine wearing several hats; giving them the same
//	                  question lets it generate once and copy the answer.
//	across /24s       THE SAME question. Unrelated networks answering the same
//	                  thing at the same moment is a free cross-check.
func (p *Prober) fireTextGroups(groups [][]Target, now time.Time, stats *roundStats) {

	questions := p.takeQuestions(questionsNeeded(groups, p.cfg.QuestionFanout), now)
	if len(questions) == 0 {
		log.Printf("[probe] question queue is empty — skipping this round")
		return
	}

	// Groups run under a concurrency limit; the members of one group do NOT.
	// 🔴 A group has to go out together — a farm of registrations sharing one
	// GPU answers serial probes fine and fails simultaneous ones, so throttling
	// inside a group would undo the measurement. Throttling ACROSS groups is
	// free: they are unrelated networks and nothing is learned by hitting them
	// at the same instant.
	sem := make(chan struct{}, maxConcurrentGroups)
	var wg sync.WaitGroup
	shot := 0 // running index across the whole round, assigned before launching
	for _, g := range groups {
		// Question i goes to shot i, i+N, i+2N … where N is the number of
		// questions. Consecutive shots are the members of one group, so within
		// a group every node gets a different question, while the same question
		// lands on nodes from N-apart groups — different /24s by construction.
		picked := make([]Question, len(g))
		for i := range g {
			picked[i] = questions[(shot+i)%len(questions)]
		}
		shot += len(g)

		wg.Add(1)
		go func(g []Target, qs []Question) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			var inner sync.WaitGroup
			for i, t := range g {
				inner.Add(1)
				go func(t Target, q Question) {
					defer inner.Done()
					p.fireOne(t, q, stats)
				}(t, qs[i])
			}
			inner.Wait()
		}(g, picked)
	}
	wg.Wait()
}

// questionsNeeded is how many distinct questions this round has to draw.
//
// Two constraints, and the LARGER wins:
//
//	shots / fanout   the sharing target — each question should reach about
//	                 `fanout` nodes, no more
//	widest group     🔴 a hard floor. With fewer questions than the widest
//	                 group has members, the round-robin wraps and two nodes in
//	                 the SAME /24 get the same question — which is exactly the
//	                 shortcut a shared GPU needs (generate once, answer twice).
//	                 Getting this wrong costs the sybil test, silently.
func questionsNeeded(groups [][]Target, fanout int) int {
	if fanout < 1 {
		fanout = 1
	}
	shots, widest := 0, 0
	for _, g := range groups {
		shots += len(g)
		if len(g) > widest {
			widest = len(g)
		}
	}
	n := (shots + fanout - 1) / fanout // round up
	if n < widest {
		n = widest
	}
	if n < 1 {
		n = 1
	}
	return n
}

// maxConcurrentGroups bounds how many /24 groups are in flight at once, so a
// large budget spreads over the round instead of opening every socket at the
// same moment.
const maxConcurrentGroups = 16

// takeQuestions draws n questions for this round, sampling categories by weight.
//
// A category that has run dry falls back to arithmetic, which is generated in
// code and therefore always available. That keeps a missing engine from
// stopping the round — at the cost of shifting the mix toward math, which the
// refill path logs.
func (p *Prober) takeQuestions(n int, now time.Time) []Question {
	if n < 1 {
		n = 1
	}
	out := make([]Question, 0, n)
	for len(out) < n {
		q, ok, err := p.store.TakeQuestion(pickCategory(p.rng), now)
		if err != nil {
			log.Printf("[probe] take question: %v", err)
			break
		}
		if !ok {
			if q, ok, err = p.store.TakeQuestion(CatMath, now); err != nil || !ok {
				break // even arithmetic is gone; the refill will restock it
			}
		}
		out = append(out, q)
	}
	return out
}

// fireOne runs one question through one node, submit to result.
func (p *Prober) fireOne(t Target, q Question, stats *roundStats) {
	now := time.Now()

	sh := Shot{
		FiredAt:     now,
		NodeID:      t.Node.ID,
		NodeAddr:    t.Node.Addr,
		Slash24:     t.Slash24,
		Engine:      t.Service.Engine,
		Service:     t.Service.Name,
		Model:       t.Service.Model,
		ModelHash:   t.Service.ModelHash,
		QuestionID:  q.ID,
		AssignEpoch: p.assign.Epoch,
		AssignRoot:  p.assign.Root,
	}

	res, serr := p.firer.Submit(t, q)
	sh.JobID = res.JobID
	sh.SubmitStatus = res.Status
	sh.Outcome = res.Outcome
	if sh.Outcome == "" {
		sh.Outcome = OutcomeRefused
	}
	id, err := p.store.RecordShot(sh)
	if err != nil {
		log.Printf("[probe] record shot: %v", err)
		return
	}
	if serr != nil || res.Outcome != OutcomeSubmitted {
		stats.record(sh.Outcome, "")
		if serr != nil {
			log.Printf("[probe] %s: %v", short(t.Node.ID), serr)
		}
		return
	}

	fetched, ferr := p.firer.Collect(t.Node.ID, res.JobID, p.deadlineFor(t))
	end := time.Now()
	if ferr != nil || fetched.Outcome != OutcomeAnswered {
		outcome := fetched.Outcome
		if outcome == "" {
			outcome = OutcomeRefused
		}
		if err := p.store.FailShot(id, end, outcome); err != nil {
			log.Printf("[probe] fail shot: %v", err)
		}
		stats.record(outcome, "")
		if ferr != nil {
			log.Printf("[probe] %s: %v", short(t.Node.ID), ferr)
		}
		return
	}

	// Scored here, in the same write as the answer. The raw text is kept
	// alongside the verdict either way: it is what makes a disputed question
	// re-examinable without firing at the node again.
	verdict := Score(q, fetched.Answer, fetched.CompletionTokens)
	stats.record(OutcomeAnswered, verdict)

	// Observed latency, not the node's generation time — the two differ by
	// however long the polling loop waited before asking. Stored as an upper
	// bound and labelled as such in the schema.
	if err := p.store.CompleteShot(id, end, end.Sub(now).Milliseconds(),
		fetched.PromptTokens, fetched.CompletionTokens, fetched.Answer, OutcomeAnswered, verdict); err != nil {
		log.Printf("[probe] complete shot: %v", err)
	}
	p.noteTicket(t.Node.ID, verdict, end)
	switch verdict {
	case VerdictFail:
		log.Printf("[probe] %s answered %q, expected %q (%s)",
			short(t.Node.ID), trim(fetched.Answer), q.Draft, q.Category)
	case VerdictTruncated:
		// Our cap, not the node's mistake. Logged loudly because a run of these
		// means probeMaxTokens is set too low for the answers being asked for.
		log.Printf("[probe] %s hit the %d-token cap: %q (expected %q) — not scored",
			short(t.Node.ID), probeMaxTokens, trim(fetched.Answer), q.Draft)
	}
}

// noteTicket says a node just earned one of the day's tickets.
//
// 🔴 It only SAYS so. The ticket is the passing row itself, already written by
// the caller, and the count is derived from the rows whenever anyone asks
// (PassesToday). What a ticket is worth is decided when the voucher is signed,
// so nothing here needs to know a rate.
//
// A failed count is not worth failing over: this line is for the operator
// watching the log, and the rows carry the fact regardless.
func (p *Prober) noteTicket(nodeID, verdict string, at time.Time) {
	if verdict != VerdictPass {
		return
	}
	counts, err := p.store.PassesToday(dayStart(at))
	if err != nil {
		log.Printf("[probe] %s earned a ticket", short(nodeID))
		return
	}
	log.Printf("[probe] %s earned a ticket (%d/%d today)",
		short(nodeID), counts[nodeID], len(p.schedule))
}

// trim shortens an answer for a log line.
func trim(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}

// deadlineFor scales the response allowance to the declared model.
//
// A fixed cap would penalise a node for running a bigger model, which is the
// opposite of what the probe is meant to reward.
func (p *Prober) deadlineFor(t Target) time.Duration {
	base := time.Duration(p.cfg.TextDeadline) * time.Second
	m := strings.ToLower(t.Service.Model)
	switch {
	case strings.Contains(m, "70b"), strings.Contains(m, "72b"):
		return base * 3
	case strings.Contains(m, "30b"), strings.Contains(m, "32b"), strings.Contains(m, "34b"):
		return base * 2
	}
	return base
}

// short trims a node id for log lines.
func short(id string) string {
	if len(id) > 12 {
		return id[:12] + "…"
	}
	return id
}
