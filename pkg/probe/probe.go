package probe

// probe.go — the faucet prober: config, the two loops, and the idle default.
//
// The prober asks public nodes a question and records what comes back, so a
// node can later prove it was doing real work. It talks to nothing but its own
// isannd over loopback — discovery, NAT traversal and the HTTP/3 hop to the
// target all happen there, exactly as they do for `isann infer --nodes <id>`.
//
// IDLE IS THE DEFAULT. Without an active appointment it fetches nothing and
// fires nothing. The mesh is meant to be installed widely and appointed
// selectively, so "installed but not appointed" has to be a quiet, cheap state.
//
// Scope: submit and collect. Scoring the answers comes next; every answer is
// stored verbatim so it can be scored retroactively instead of re-fired.

import (
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
	// ScheduleHours is the uptime each shot requires, in hours. The n-th shot
	// comes due once the node has been continuously present that long — see
	// schedule.go. The list length IS the daily cap.
	ScheduleHours []float64 `json:"schedule_hours"`
	// ResponseDeadline is the base answer allowance in seconds, scaled up for
	// larger declared models so a bigger machine is not penalised for being one.
	ResponseDeadline int `json:"response_deadline_sec"`
	// QuestionGeneratorService is THIS node's own text service, used to write
	// geography/animal/colour questions in batches. It is not a probe target
	// and has nothing to do with what gets fired at: arithmetic is generated in
	// code, and image probes are a separate track with their own validator.
	// Empty ⇒ arithmetic only.
	QuestionGeneratorService string `json:"question_generator_service"`
	DB                       string `json:"db"`
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
	QuestionFanout int          `json:"question_fanout"`
	Signer         SignerConfig `json:"signer"`
}

// defaultIsanndURL is the node-bridge's loopback address.
const defaultIsanndURL = "http://127.0.0.1:8443"

// DefaultConfig returns the settings the prober runs with when nothing is set.
func DefaultConfig() Config {
	return Config{
		RefreshSec:               3600,
		FireIntervalSec:          60,
		ScheduleHours:            []float64{1, 3, 5, 8, 13},
		ResponseDeadline:         30,
		QuestionGeneratorService: "llm-api",
		DB:                       "probe.db",
		QueueLowWater:            10,
		ObservationDays:          30,
		QuestionFanout:           5,
	}
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
		if err := json.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	// Mesh config also arrives as environment variables, and those win: the
	// mesh runtime is what an operator edits through `isann mesh config`.
	applyEnvOverrides(&cfg)

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
		"PROBE_QUESTION_GENERATOR_SERVICE": &cfg.QuestionGeneratorService,
		"PROBE_DB":                         &cfg.DB,
	} {
		if v := os.Getenv(name); v != "" {
			*dst = v
		}
	}
	for name, dst := range map[string]*int{
		"PROBE_REFRESH_SEC":           &cfg.RefreshSec,
		"PROBE_FIRE_INTERVAL_SEC":     &cfg.FireIntervalSec,
		"PROBE_RESPONSE_DEADLINE_SEC": &cfg.ResponseDeadline,
		"PROBE_QUESTION_FANOUT":       &cfg.QuestionFanout,
	} {
		if v := os.Getenv(name); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				*dst = n
			}
		}
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

	signer    Signer
	hasSigner bool

	appt       Appointment
	hasAppt    bool
	targets    []Target
	idleLogged bool

	// schedule is the uptime ladder; anchors is each node's continuity start,
	// both recomputed on refresh.
	schedule []time.Duration
	anchors  map[string]time.Time

	// refilling guards the background question generator so only one round
	// runs at a time. Atomic because Refresh (one goroutine) starts it and the
	// generator goroutine clears it.
	refilling atomic.Bool
	// genFailures counts consecutive generation failures. After a few, the
	// prober stops asking every hour: an engine that is not there will not be
	// there next hour either, and the log line is the same one every time.
	genFailures int
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
	return &Prober{
		cfg:      cfg,
		store:    store,
		firer:    NewFirer(cfg.NodeBridgeAddr),
		gen:      NewGenerator(cfg.NodeBridgeAddr, cfg.QuestionGeneratorService),
		http:     &http.Client{Timeout: 20 * time.Second},
		rng:      rand.New(rand.NewSource(time.Now().UnixNano())),
		schedule: parseSchedule(cfg.ScheduleHours),
		anchors:  map[string]time.Time{},
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

// Refresh re-reads the appointment, the directory and the question queue.
//
// The appointment is checked FIRST and everything else is skipped without one.
// An unappointed prober must not poll the RV: that would cost a round trip per
// hour per idle node, for a list it has no right to act on.
func (p *Prober) Refresh() {
	appt, ok, err := FetchAppointment(p.cfg.NodeBridgeAddr, p.http)
	if err != nil {
		log.Printf("[probe] %v", err)
		p.hasAppt = false
		return
	}
	if !ok {
		// Logged once per transition, not once per hour. An idle prober is the
		// normal state for a node that has the mesh but no appointment, and
		// repeating it hourly would bury the messages that matter.
		if !p.idleLogged {
			log.Printf("[probe] no appointment installed — idle (issuer mints one with `ivm account issue --kind prober`, then `isann cred add`)")
			p.idleLogged = true
		}
		p.hasAppt, p.targets = false, nil
		return
	}
	if !appt.Active(time.Now()) {
		log.Printf("[probe] appointment %q expired %s — idle (install a new one, then `isann cred use`)",
			appt.Alias, appt.Expires().UTC().Format(time.RFC3339))
		p.hasAppt, p.targets = false, nil
		return
	}
	// Open the key the appointment is bound to. This is the only reason the
	// key is touched at all in this milestone — firing is anonymous — but
	// finding out here that the node holds no such key beats finding out later
	// as "everything looks configured and nothing ever gets paid".
	changed := !p.hasAppt || p.appt.Token != appt.Token
	if changed {
		signer, hasSigner, err := OpenSignerFor(appt, p.cfg.Signer)
		if err != nil {
			log.Printf("[probe] %v — idle", err)
			p.hasAppt, p.hasSigner, p.targets = false, false, nil
			return
		}
		p.signer, p.hasSigner = signer, hasSigner

		log.Printf("[probe] appointment %q active until %s (issuer %s, prober %s)",
			appt.Alias, appt.Expires().UTC().Format(time.RFC3339), appt.Issuer, appt.Prober)
		if hasSigner {
			log.Printf("[probe] signing key %s opened", signer.Address)
		} else {
			// Allowed for now, since nothing is signed yet — but the operator
			// should know the key was never proven to be here.
			log.Printf("[probe] no signer passphrase set; the key for %s is UNVERIFIED (tickets cannot be signed)", appt.Prober)
		}
	}
	p.appt, p.hasAppt, p.idleLogged = appt, true, false

	nodes, err := rvnodes.Fetch(p.cfg.NodeBridgeAddr, true)
	if err != nil {
		log.Printf("[probe] %v", err)
		return
	}
	now := time.Now()
	if err := p.store.RecordObservations(observationsOf(nodes), now); err != nil {
		log.Printf("[probe] record observations: %v", err)
	}
	p.targets = eligible(nodes)

	// Continuity anchors, recomputed from the observation history this poll
	// just extended. Derived from the table rather than kept in memory so a
	// prober restart does not reset everyone's day.
	if sightings, err := p.store.SightingsToday(dayStart(now)); err != nil {
		log.Printf("[probe] sightings: %v", err)
	} else {
		p.anchors = anchorsFrom(sightings)
	}
	log.Printf("[probe] directory: %d nodes, %d eligible", len(nodes), len(p.targets))

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

	if depth[CatMath] < p.cfg.QueueLowWater {
		if err := p.store.AddQuestions(FillMath(batchTarget, p.rng), now); err != nil {
			log.Printf("[probe] refill math: %v", err)
		}
	}
	// Everything below needs an engine on THIS node. Without one the prober
	// runs on arithmetic alone — not a degraded mode so much as a narrower
	// one: math is half the intended mix anyway, and it is the only category
	// whose answers are certain, so it never needs a re-check panel.
	if !p.gen.HasEngine() {
		if p.genFailures == 0 {
			log.Printf("[probe] no question generator service configured — arithmetic questions only")
			p.genFailures = genFailureLimit // say it once, not every hour
		}
		return
	}
	if p.genFailures >= genFailureLimit {
		return // an engine that was not there an hour ago is still not there
	}

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
		if err := p.store.AddQuestions(qs, now); err != nil {
			log.Printf("[probe] store %s batch: %v", spec.cat, err)
			continue
		}
		p.genFailures = 0 // one success clears the count
	}
	if failed {
		p.genFailures++
		if p.genFailures >= genFailureLimit {
			log.Printf("[probe] question generation has failed %d times — falling back to arithmetic only (fix %q, then restart)",
				p.genFailures, p.cfg.QuestionGeneratorService)
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
	if !p.hasAppt || len(p.targets) == 0 {
		return
	}
	now := time.Now()
	if !p.appt.Active(now) {
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
	due := dueTargets(p.targets, p.anchors, counts, p.schedule, now)
	if len(due) == 0 {
		return
	}
	groups := groupBySlash24(due)
	if len(groups) == 0 {
		return
	}

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
					p.fireOne(t, q)
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
func (p *Prober) fireOne(t Target, q Question) {
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
		Appointment: p.appt.Token,
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
		if ferr != nil {
			log.Printf("[probe] %s: %v", short(t.Node.ID), ferr)
		}
		return
	}

	// Observed latency, not the node's generation time — the two differ by
	// however long the polling loop waited before asking. Stored as an upper
	// bound and labelled as such in the schema.
	if err := p.store.CompleteShot(id, end, end.Sub(now).Milliseconds(),
		fetched.PromptTokens, fetched.CompletionTokens, fetched.Answer, OutcomeAnswered); err != nil {
		log.Printf("[probe] complete shot: %v", err)
	}
}

// deadlineFor scales the response allowance to the declared model.
//
// A fixed cap would penalise a node for running a bigger model, which is the
// opposite of what the probe is meant to reward.
func (p *Prober) deadlineFor(t Target) time.Duration {
	base := time.Duration(p.cfg.ResponseDeadline) * time.Second
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
