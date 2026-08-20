package probe

// image.go — the image track: order a picture, collect it, ask CLIP whether it
// matches the order.
//
// The text track and this one differ in one structural way. A text question has
// to be WRITTEN before it can be asked, so it lives in a queue and is consumed
// once. An image order is DRAWN in code from the slot table — 74 million
// combinations — so there is no queue, no cache attack to defend against, and
// nothing to store ahead of time. It is drawn at the moment of firing.
//
// 🔴 NO JUDGE, NO FIRING. If the validator pool is empty or every entry is
// down, the round does not fire at image nodes at all. Ordering a picture and
// then discarding it for want of a judge burns someone else's GPU for nothing —
// and the ticket is signed at fire time, so a verdict that arrives later has
// nothing left to affect.

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/isannai/mesh/pkg/rvnodes"
)

// sdSizes is what a 512-class model (sd15, pony, and anything undeclared) is
// asked for.
//
// 🔴 ONE resolution, not a family. The non-square 512 sizes are legal for SD 1.5
// and the model renders them fine, but they cost noticeably more VRAM than
// 512x512 — enough that a 4GB card spills to system memory and takes minutes,
// which we then record as a timeout. That is the prober calling an honest node
// dead over a resolution WE chose.
//
// Nothing is lost by fixing it. Size variation was never the anti-cache
// measure: the prompt is drawn from ~74 million slot combinations and the seed
// is fresh every shot. A replayed picture cannot survive either.
var sdSizes = []string{"512x512"}

// xlSizes is the same decision for an SDXL-class model.
//
// 🔴 THE ARCHITECTURE DECIDES THE SIZE, and getting it wrong wrecks the picture
// rather than merely slowing it down. SDXL at 512x512 produces mangled output —
// duplicated subjects, broken anatomy — and SD 1.5 at 1024x1024 does the same
// in the other direction. Either way an honest node fails a check for a mistake
// that was ours.
//
// One resolution here too, for the reason sdSizes gives: a size that moves makes
// generation time move with it, and imageDeadline is calibrated from a node's
// reported average. Leaving XL free to pick among three would put that average
// back out of reach on exactly the nodes it matters most for.
var xlSizes = []string{"1024x1024"}

// xlArchs are the declared architectures that want the 1024 family. Matched
// against Service.ModelArch, which the node stamps from its own package.json.
var xlArchs = map[string]bool{"sdxl": true, "sd3": true, "flux": true}

// wantsXL decides which resolution family a target takes.
//
// The DECLARED architecture wins whenever the node sent one. `model_arch`
// arrives on the register frame from the package.json `isann model pull --arch`
// wrote, which is the node stating a fact about itself rather than us inferring
// one — and it is the whole reason that field was added.
//
// The name guess is the fallback, for two populations that will not go away
// quickly: nodes running an isannd older than the release that stamps the field,
// and models registered with no arch at all. Naming an SDXL checkpoint with "xl"
// in it is near-universal (sd_xl_base_1.0, sdxl-turbo, juggernautXL), so it is
// right far more often than assuming everything is 512.
//
// The fallback fails open toward SD 1.5 — the larger population and the cheaper
// mistake. A 1.5 model asked for 512 works; an unrecognised XL asked for 512
// produces duplicated subjects and broken anatomy, and the node wears the blame
// for our guess.
func wantsXL(arch, model string) bool {
	if a := strings.ToLower(strings.TrimSpace(arch)); a != "" {
		// 🔴 Declared and NOT in the XL set means declared 512 (sd15, pony).
		// Falling through to the name guess here would let a checkpoint called
		// "…xl-merge" override the node's own statement about itself.
		return xlArchs[a]
	}
	return isXL(model)
}

// isXL guesses the architecture from the model name. Fallback only — see
// wantsXL for when it applies and why it leans toward 512.
func isXL(model string) bool {
	m := strings.ToLower(model)
	for _, tag := range []string{"xl", "sdxl", "playground", "flux"} {
		if strings.Contains(m, tag) {
			return true
		}
	}
	return false
}

// imageParams resolves the size list for one target.
//
// The configured list wins when the operator set one — they know their fleet —
// and otherwise the architecture picks between the 512 and 1024 families.
//
// There is no step parameter here on purpose. See imageRun.
func (p *Prober) imageParams(arch, model string) []string {
	if sizes := p.cfg.ImageSizes; len(sizes) > 0 {
		return sizes
	}
	if wantsXL(arch, model) {
		return xlSizes
	}
	return sdSizes
}

// imageRetention is the hard ceiling on waiting for a picture.
//
// It is the station's number, not ours: a finished job is held for DoneTTL
// (default 1h) or until MaxDone newer jobs push it out. Past that the answer no
// longer exists to be fetched, so no policy of ours can justify waiting longer.
const imageRetention = time.Hour

// giveUpAfter is how long an ordered picture may stay outstanding.
//
// 🔴 THIS IS NOT THE OLD DEADLINE. That one measured how long we would BLOCK
// waiting, and expiring it recorded a failure against a node that was drawing
// correctly. Nothing blocks now, so the only thing this bounds is how long one
// node stays ineligible: an open shot stops that node being fired at again, so
// a job that will never finish would otherwise cost it the rest of the day.
//
// Generous by design, and derived from the node's own average, because being
// wrong in this direction costs one delayed shot while being wrong in the other
// records a timeout for a picture that was on its way.
func (p *Prober) giveUpAfter(nodeID, service string) time.Duration {
	d := p.imageDeadline(nodeID, service)
	if d > imageRetention {
		return imageRetention
	}
	return d
}

// Deadline bounds. The floor applies when the node has told us nothing yet; the
// ceiling is where we stop calling a node probe-able at all.
const (
	minImageDeadline = 300 * time.Second
	maxImageDeadline = 900 * time.Second
	// deadlineSlack multiplies the node's reported average.
	//
	// Two on purpose. avg_job_sec is a MEAN, so roughly half of real jobs land
	// above it, and a fresh submission also waits behind whatever the node is
	// already doing. A deadline set at the mean would fail half the shots at a
	// node performing exactly as advertised.
	deadlineSlack = 2
)

// imageDeadline resolves how long this node gets to draw a picture.
//
// 🔴 A FIXED DEADLINE MEASURES OUR PATIENCE, NOT THE NODE. This was 300s flat,
// and a 4GB card that reported avg_job_sec 370.9 had every single shot recorded
// as a timeout — while drawing each picture correctly and finishing it 71
// seconds after we gave up. Nothing was wrong with the node, the picture, or the
// request. The number was ours.
//
// So the node's own measurement decides, bounded at both ends. An explicit
// image_deadline_sec still overrides everything: an operator who has decided how
// long they are willing to wait outranks a computation.
func (p *Prober) imageDeadline(nodeID, service string) time.Duration {
	if p.cfg.ImageDeadline > 0 {
		return time.Duration(p.cfg.ImageDeadline) * time.Second
	}
	m, ok := p.metrics.Get(nodeID, service)
	if !ok || m.AvgJobSec <= 0 {
		// Unknown is NOT fast. A node the RV has said nothing about gets the
		// floor, which is the old fixed value.
		return minImageDeadline
	}
	d := time.Duration(m.AvgJobSec*deadlineSlack) * time.Second
	if d < minImageDeadline {
		return minImageDeadline
	}
	if d > maxImageDeadline {
		return maxImageDeadline
	}
	return d
}

// imageTargets filters the directory to nodes worth ordering a picture from.
// Same two conditions as the text track, against the image service.
func imageTargets(nodes []rvnodes.Node, exclude map[string]bool) []Target {
	var out []Target
	for _, n := range nodes {
		if exclude[strings.ToLower(nodeAddressOf(n.ID))] {
			continue
		}
		if !n.IsPublic() {
			continue
		}
		svc, ok := n.ImageService()
		if !ok {
			continue
		}
		out = append(out, Target{Node: n, Service: svc, Slash24: n.Slash24()})
	}
	return out
}

// imageRun builds the run block for one order.
//
// Parameter names come from the sd manifest (prompt / size / seed). Unlike the
// text path this does not read the run schema first: every image engine in the
// tree declares these, and a schema fetch per shot would double the round trips
// for a field set that has not varied.
//
// 🔴 `steps` IS DELIBERATELY ABSENT. The manifest already declares it —
// `{"name":"steps","type":"int","default":20}` — so leaving it out makes the
// station fill in the engine's own default. Sending our own number, even the
// same 20, makes the prober the authority on a value it has no business owning:
// the day an engine changes its default, every prober keeps insisting on the old
// one.
//
// It used to send a random 12–28. That cost more than it bought. Nothing was
// gained — the slot draw and the fresh seed already defeat a replayed picture —
// while 12 and 28 differ by more than 2x in generation time, which put the
// per-node average imageDeadline calibrates from out of reach.
func (p *Prober) imageRun(o ImageOrder, svc rvnodes.Service, rng *rand.Rand) map[string]any {
	sizes := p.imageParams(svc.ModelArch, svc.Model)
	return map[string]any{
		"prompt": o.Prompt,
		"size":   sizes[rng.Intn(len(sizes))],
		// A fresh seed each time. With a fixed seed the same order would
		// produce the same picture, which is exactly the cached reply this
		// exists to prevent.
		"seed": rng.Int31(),
	}
}

// fireImageOne orders one picture and writes the order down. It does NOT wait
// for the picture — collectImageShot does that, in a later round.
func (p *Prober) fireImageOne(t Target, o ImageOrder, deferred int, stats *roundStats) {
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
		AssignEpoch: p.assign.Epoch,
		AssignRoot:  p.assign.Root,
		Deferred:    deferred,
	}

	run := p.imageRun(o, t.Service, p.rng)
	// 🔴 The order is written down BEFORE the shot leaves, not after it comes
	// back. imageNote used to be stored only on completion, so a shot that
	// timed out left the word "timeout" and nothing else — no prompt, no size,
	// no way to answer "what did we ask it to draw". That is the exact question
	// a timeout raises, and the record had thrown the answer away.
	sh.AnswerRaw = imageAsk(o, run)
	// The checks go with it. Judging happens in a later round now, possibly
	// after a restart, and an order that only lived in memory could never be
	// scored — the picture would arrive with nothing to compare it against.
	sh.OrderJSON = encodeOrder(o)

	res, err := p.firer.SubmitRunProbe(t.Node.ID, t.Service.Name, run, t.Probe)
	sh.JobID = res.JobID
	sh.SubmitStatus = res.Status
	sh.Outcome = res.Outcome
	if sh.Outcome == "" {
		sh.Outcome = OutcomeRefused
	}
	if _, serr := p.store.RecordShot(sh); serr != nil {
		log.Printf("[probe] record image shot: %v", serr)
		return
	}
	if err != nil || res.Outcome != OutcomeSubmitted {
		stats.record(sh.Outcome, "")
		if err != nil {
			log.Printf("[probe] %s image: %v", short(t.Node.ID), err)
		}
		return
	}
	// Counted as fired, not as answered. The verdict lands in whichever round
	// collects it, which is why a round's tally can show more answers than it
	// fired shots.
	stats.fired.Add(1)
}

// collectImageShot asks once whether an ordered picture is ready, and judges it
// if so. Returns false when the shot is still open and should be asked again.
func (p *Prober) collectImageShot(sh Shot, now time.Time, stats *roundStats) bool {
	img, err := p.firer.PeekImage(sh.NodeID, sh.JobID)
	if err == errJobPending {
		// 🔴 The ordinary case. Not logged, not recorded, not a mark against
		// anyone: the node is still drawing.
		if now.Sub(sh.FiredAt) > p.giveUpAfter(sh.NodeID, sh.Service) {
			// The node accepted the job and never finished it. This one IS the
			// node's — waiting longer would not produce an answer, and the open
			// shot is meanwhile blocking every further shot at that node.
			if e := p.store.FailShot(sh.ID, now, OutcomeTimeout); e != nil {
				log.Printf("[probe] fail image shot: %v", e)
			}
			stats.settle(OutcomeTimeout, "")
			log.Printf("[probe] %s image: no result after %s — giving up",
				short(sh.NodeID), now.Sub(sh.FiredAt).Round(time.Second))
			return true
		}
		return false
	}
	if err != nil {
		// 🔴 A transport fault is OURS, not the node's. It drew the picture or
		// it did not; we could not ask. Recording a refusal here is what put
		// marks on nodes that had done nothing wrong.
		if e := p.store.FailShot(sh.ID, now, OutcomeSkipped); e != nil {
			log.Printf("[probe] fail image shot: %v", e)
		}
		stats.settle(OutcomeSkipped, "")
		log.Printf("[probe] %s image: could not collect: %v", short(sh.NodeID), err)
		return true
	}

	o, ok := decodeOrder(sh.OrderJSON)
	if !ok {
		// Ordered by a build that did not write the checks down, or the row is
		// corrupt. The picture is here and it may well be correct; we simply
		// have nothing to judge it against. Not the node's fault.
		if e := p.store.CompleteShot(sh.ID, now, now.Sub(sh.FiredAt).Milliseconds(), 0, 0,
			sh.AnswerRaw+" | no order on record", OutcomeSkipped, ""); e != nil {
			log.Printf("[probe] complete image shot: %v", e)
		}
		stats.settle(OutcomeSkipped, "")
		return true
	}

	latency := now.Sub(sh.FiredAt).Milliseconds()

	// 🔴 A judging failure is NOT the node's failure. The picture arrived; we
	// could not get it judged. Recording that as a fail would penalise a node
	// for our validator being down, so the shot is stored as answered with no
	// verdict and simply does not count toward anything.
	j, jerr := p.validator.Validate(img, o.Checks)
	if jerr != nil {
		if e := p.store.CompleteShot(sh.ID, now, latency, 0, 0,
			imageNote(o, nil), OutcomeAnswered, ""); e != nil {
			log.Printf("[probe] complete image shot: %v", e)
		}
		stats.settle(OutcomeAnswered, "")
		log.Printf("[probe] %s image: not judged: %v", short(sh.NodeID), jerr)
		return true
	}

	verdict := VerdictFail
	if j.RequiredPass() {
		verdict = VerdictPass
	}
	if e := p.store.CompleteShot(sh.ID, now, latency, 0, 0,
		imageNote(o, &j), OutcomeAnswered, verdict); e != nil {
		log.Printf("[probe] complete image shot: %v", e)
	}
	// Scored against the day the shot was FIRED, not the day it was collected.
	// A picture ordered at 23:58 and picked up after midnight belongs to the
	// day it was earned in, and paying it into the new one would give that node
	// a head start it did not work for.
	p.noteTicket(sh.NodeID, verdict, sh.FiredAt)
	stats.settle(OutcomeAnswered, verdict)
	if verdict == VerdictFail {
		log.Printf("[probe] %s image failed: %s", short(sh.NodeID), failedChecks(j))
	}
	return true
}

// encodeOrder / decodeOrder persist an image order across rounds.
func encodeOrder(o ImageOrder) string {
	b, err := json.Marshal(o)
	if err != nil {
		return ""
	}
	return string(b)
}

func decodeOrder(s string) (ImageOrder, bool) {
	if strings.TrimSpace(s) == "" {
		return ImageOrder{}, false
	}
	var o ImageOrder
	if err := json.Unmarshal([]byte(s), &o); err != nil || len(o.Checks) == 0 {
		return ImageOrder{}, false
	}
	return o, true
}

// imageAsk renders what was ASKED for, stored at fire time.
//
// It carries the resolved run block, not just the prompt: the size is chosen
// from the target's declared architecture and the seed is drawn per shot, so
// neither can be reconstructed later from the order alone. When a shot times
// out this string is the entire record of what happened.
func imageAsk(o ImageOrder, run map[string]any) string {
	var b strings.Builder
	b.WriteString(o.Prompt)
	if v, ok := run["size"]; ok {
		fmt.Fprintf(&b, " | %v", v)
	}
	if v, ok := run["seed"]; ok {
		fmt.Fprintf(&b, " | seed %v", v)
	}
	return b.String()
}

// imageNote is what gets stored in place of an answer.
//
// The IMAGE is not kept — it is judged in the same round and a 500KB blob per
// shot would grow the database by gigabytes for something no later pass reads.
// What is kept is the order and the per-check confidences, which is what a
// human needs to see why a verdict went the way it did.
func imageNote(o ImageOrder, j *Judgement) string {
	var b strings.Builder
	b.WriteString(o.Prompt)
	if j == nil {
		b.WriteString(" | not judged")
		return b.String()
	}
	for _, c := range j.Checks {
		mark := "x"
		if c.Pass {
			mark = "o"
		}
		fmt.Fprintf(&b, " | %s %s %.2f", mark, c.Label, c.Confidence)
	}
	if j.Model != "" {
		// The judging criteria travel with the verdict: two validators can only
		// be compared when both match, so a stored verdict without them is not
		// comparable to anything.
		fmt.Fprintf(&b, " | %s/%s", j.Model, j.Version)
	}
	return b.String()
}

// failedChecks names the required checks that did not pass, for the log line.
func failedChecks(j Judgement) string {
	var bad []string
	for _, c := range j.Checks {
		if requiredLabels[c.Label] && !c.Pass {
			bad = append(bad, fmt.Sprintf("%s %.2f", c.Label, c.Confidence))
		}
	}
	if len(bad) == 0 {
		return "no required check failed (verdict came from an empty judgement)"
	}
	return strings.Join(bad, ", ")
}

// holdBusy decides, per target, whether to order a picture now or wait a round.
//
// WHY WAIT AT ALL. What the image track measures is how fast a node can draw,
// and the only clock we have is our own: submit to result in hand. That number
// is honest when the job starts immediately and inflated by however much was
// queued ahead of it. Ordering into an empty queue is what makes the measurement
// mean something.
//
// 🔴 WHY NOT WAIT FOREVER. A node that is busy all day is a node with customers,
// which is exactly what the faucet exists to reward. "Only fire at an idle
// queue" excludes it permanently, and no amount of tuning fixes that — so after
// deferMax rounds we fire regardless. The inflated time that produces is safe:
// waiting can only make a measurement slower, never faster, so a padded shot
// loses to that node's own clean ones and never manufactures a pass.
//
// The count rides along to the shot row. Read next to the latency it explains
// whether a slow time means "no GPU" or "GPU, but working".
//
// Unknown does not mean blocked: when the queue cannot be read at all we fire.
// One faulted request must not silently stop the whole image track.
func (p *Prober) holdBusy(due []Target) []Target {
	// The queue reads go out together. In series a single unreachable node would
	// hold the round for its whole 15s timeout, and a few of them would outlast
	// the firing interval entirely. The decisions below stay sequential so the
	// deferral map needs no lock and the kept order is stable.
	type probe struct {
		qs  QueueStats
		err error
	}
	probes := make([]probe, len(due))
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrentImages)
	for i, t := range due {
		wg.Add(1)
		go func(i int, t Target) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			probes[i].qs, probes[i].err = p.firer.QueueStats(t.Node.ID, t.Service.Name)
		}(i, t)
	}
	wg.Wait()

	out := due[:0:0]
	for i, t := range due {
		key := shotKey(t.Node.ID, t.Service.Name)
		qs, err := probes[i].qs, probes[i].err
		switch {
		case err != nil:
			log.Printf("[probe] %s %s: queue unreadable (%v) — firing anyway",
				short(t.Node.ID), t.Service.Name, err)
		case qs.Idle():
			// Nothing in front of us: the clean case this whole dance is for.
		case p.deferrals[key] >= p.deferMax:
			log.Printf("[probe] %s %s busy (running %d, pending %d) — deferred %d times, firing anyway",
				short(t.Node.ID), t.Service.Name, qs.Running, qs.Pending, p.deferrals[key])
		default:
			// Said out loud. A silent skip is indistinguishable from a broken
			// schedule, and "why is it not firing" has already cost an evening.
			p.deferrals[key]++
			log.Printf("[probe] %s %s busy (running %d, pending %d) — deferring (%d/%d)",
				short(t.Node.ID), t.Service.Name, qs.Running, qs.Pending, p.deferrals[key], p.deferMax)
			continue
		}
		out = append(out, t)
	}
	return out
}

// fireImageRound orders pictures from the image nodes that have come due.
//
// It shares the day's shot counts with the text round rather than keeping its
// own: the cap bounds what the prober costs a NODE, and a node running both
// engines should not be probed twice as often for it.
//
// The /24 grouping is deliberately NOT applied here. That rule exists so a farm
// sharing one GPU has to answer simultaneously; image generation already takes
// tens of seconds and firing a whole group at once would simply queue behind
// itself. The text track carries the sybil burst.
func (p *Prober) fireImageRound(now time.Time, shotsToday map[string]int, stats *roundStats) {
	if len(p.imgTargets) == 0 || !p.validator.Enabled() {
		return
	}
	due := dueTargets(p.imgTargets, p.present, shotsToday, p.schedule)
	// A node whose last picture is still on order is not fired at again. That is
	// what keeps a slow node's queue from growing under us — the failure mode
	// the old blocking collect produced, one abandoned job at a time.
	due = dropOpen(due, p.openShots)
	if len(due) == 0 {
		return
	}
	due = p.holdBusy(due)
	if len(due) == 0 {
		return
	}

	sem := make(chan struct{}, maxConcurrentImages)
	var wg sync.WaitGroup
	for _, t := range due {
		o, ok := NewImageOrder(p.rng)
		if !ok {
			log.Printf("[probe] slot table too small to draw an image order")
			return
		}
		key := shotKey(t.Node.ID, t.Service.Name)
		deferred := p.deferrals[key]
		delete(p.deferrals, key) // the wait is over; the count belongs to the shot now
		wg.Add(1)
		go func(t Target, o ImageOrder, deferred int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			p.fireImageOne(t, o, deferred, stats)
		}(t, o, deferred)
	}
	wg.Wait()
}

// dropOpen removes targets that already have a shot waiting to be collected.
func dropOpen(due []Target, open map[string]bool) []Target {
	out := due[:0:0]
	for _, t := range due {
		if open[shotKey(t.Node.ID, t.Service.Name)] {
			continue
		}
		out = append(out, t)
	}
	return out
}

// maxConcurrentImages is low on purpose. Each shot holds a ~500KB image in
// memory and occupies a validator that runs at concurrency 1 on a standard
// install, so a wide fan-out would spend its time queued at the judge.
const maxConcurrentImages = 4
