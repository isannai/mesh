package probe

// refcheck.go: the reference check (workspace docs/todo/probe-기준노드.md 4절 · 5절,
// docs/todo/probe.md 「기준 노드」 「판정」).
//
// The RV now groups a slot by the budget each node's owner registered in and by
// GPU, and puts the budget's registered reference nodes into every group of
// their GPU (servers pkg/rendezvous faucet_plan.go). A group says how it is
// checked (AssignGroup.Mode):
//
//	ref       for every checked node, a question of its own goes FIRST to all the
//	          group's reference nodes. Their answers make the reference value, and
//	          only then is the node asked the same question and held against it.
//	cache     the references are all offline: the node gets a question stored
//	          while they were online, held against the stored reference value.
//	quiz      no reference: the quiz (quiz.go), as before.
//	unlisted  the budget's check info does not list this GPU: no ticket.
//
// ONE QUESTION PER NODE. Every checked node gets its own question, so nodes cannot
// pass answers to each other, and it reaches a node only after the reference value
// is fixed, so how a node answers cannot move the value it is judged by.
//
// THE QUESTION. A short sentence request, with words and a number drawn from
// crypto/rand, so it is never asked twice and cannot be answered in advance. Asked
// through the service's default run with max-tokens 8, temperature 0, logprobs with
// top-logprobs 5 and stop "." (probprobs.go). A question whose reference
// answer is under two tokens is replaced: a one-token answer is easy to hit.
//
// THE REFERENCE VALUE. One reference: its answer. Two or more: the answers are
// grouped by text and token count, the largest group must hold at least two that
// agree within the tolerance, and the value is the median of each candidate's
// probability over that group. A candidate not every one of them listed is left
// out (counted in Dropped). No such group: the references disagree, the check
// fails, and a log line says so; that is a reference set up wrong.
//
// THE VERDICT on a node (probe.md 「판정」, KV type left out):
//
//	text   the answer text must equal the reference text. If not, the node gets a
//	       second, new question; passing that one passes the round
//	count  the same number of answer tokens (stop and end tokens not counted)
//	probs  every candidate's probability within the tolerance (Config.RefTolerance,
//	       default 0.001) of the reference value, at most RefMaxMisses misses
//	       (default 0). Both are to be set by measurement.
//
// REFERENCE NODES are paid too. With two or more, each is held against the median
// of the others for every question of the round; one disagreeing answer fails its
// round. With only one there is nothing to hold it against, and it passes a round
// in which it answered with candidates (a choice made here; the design leaves it
// open, workspace docs/todo/probe-기준노드.md 8절). Each group also asks its
// references one question of their own per round, so a reference is checked even
// when no other node of its group is.
//
// STORED QUESTIONS (5절). Every question the references answered is stored with
// their answers and the value, keyed by the budget, GPU, profile, engine and model
// hash; 15 days; one row per setting and prompt. A stored question is reused when
// the group is in cache mode, or when no reference answered this time. It is
// handed to one node of a group per round, the one unused for longest first, and
// is not thrown away after use. With none left, the check fails (probe.md: a check
// with no reference fails).

import (
	crand "crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/isannai/mesh/pkg/faucet"
)

// Why a reference check failed (refcheck.reason).
const (
	reasonText       = "text"          // a different answer
	reasonCount      = "count"         // a different number of answer tokens
	reasonProbs      = "probs"         // candidate probabilities beyond the tolerance
	reasonNoProbs    = "no_probs"      // the answer came without candidates
	reasonNoRef      = "no_reference"  // no reference answered and none was stored
	reasonDisagree   = "refs_disagree" // the references did not agree among themselves
	reasonShort      = "short"         // the reference answer was under two tokens
	reasonNoQuestion = "no_question"   // no stored question left for this setting
)

// DefaultRefTolerance is the allowed probability gap (probe.md 「판정」, to be measured).
const DefaultRefTolerance = 0.001

// refTries is how many questions are drawn when the reference answers come back
// too short to judge.
const refTries = 3

// refKeep is how long stored questions and refcheck rows are kept.
const refKeep = 15 * 24 * time.Hour

// Reference is the value a node's answer is held against.
type Reference struct {
	Text    string       `json:"text"`
	Tokens  []TokenProbs `json:"tokens"`
	Refs    int          `json:"refs"`    // reference answers it was built from
	Dropped int          `json:"dropped"` // candidates left out: not every reference listed them
}

func (p *Prober) refTol() float64 {
	if p.cfg.RefTolerance > 0 {
		return p.cfg.RefTolerance
	}
	return DefaultRefTolerance
}

// --- judging ---------------------------------------------------------------

// compareAnswer holds an answer against a reference value. reason is "" when it
// passes; worst is the largest probability gap seen, misses how many exceeded tol.
func compareAnswer(a ProbAnswer, ref Reference, tol float64, maxMiss int) (reason string, worst float64, misses int) {
	if len(a.Tokens) == 0 {
		return reasonNoProbs, 0, 0
	}
	// Text first: a flipped token changes the text and often the count too, and
	// it is the text mismatch that earns a second question (probe.md 「재검사」).
	if a.Text != ref.Text {
		return reasonText, 0, 0
	}
	if len(a.Tokens) != len(ref.Tokens) {
		return reasonCount, 0, 0
	}
	for i := range ref.Tokens {
		rc, nc := ref.Tokens[i].Cands, a.Tokens[i].Cands
		// A candidate missing from one list is below that list's last entry, so
		// its gap is at least (the other side's value - that last entry).
		rMin, nMin := minProb(rc), minProb(nc)
		check := func(gap float64) {
			if gap > worst {
				worst = gap
			}
			if gap > tol {
				misses++
			}
		}
		for tok, rp := range rc {
			if np, ok := nc[tok]; ok {
				check(math.Abs(np - rp))
			} else {
				check(math.Max(0, rp-nMin))
			}
		}
		for tok, np := range nc {
			if _, ok := rc[tok]; !ok {
				check(math.Max(0, np-rMin))
			}
		}
	}
	if misses > maxMiss {
		return reasonProbs, worst, misses
	}
	return "", worst, misses
}

func minProb(m map[string]float64) float64 {
	first, min := true, 0.0
	for _, v := range m {
		if first || v < min {
			min, first = v, false
		}
	}
	return min
}

// medianOf builds a reference value from answers that share text and token count.
func medianOf(list []ProbAnswer) Reference {
	ref := Reference{Text: list[0].Text, Refs: len(list)}
	for i := range list[0].Tokens {
		seen := map[string]int{}
		for _, a := range list {
			for tok := range a.Tokens[i].Cands {
				seen[tok]++
			}
		}
		cands := map[string]float64{}
		for tok, n := range seen {
			if n < len(list) {
				ref.Dropped++
				continue
			}
			vals := make([]float64, 0, len(list))
			for _, a := range list {
				vals = append(vals, a.Tokens[i].Cands[tok])
			}
			cands[tok] = median(vals)
		}
		ref.Tokens = append(ref.Tokens, TokenProbs{Text: list[0].Tokens[i].Text, Cands: cands})
	}
	return ref
}

func median(v []float64) float64 {
	sort.Float64s(v)
	n := len(v)
	if n%2 == 1 {
		return v[n/2]
	}
	return (v[n/2-1] + v[n/2]) / 2
}

var errRefsDisagree = errors.New("the reference nodes do not agree")

// buildReference makes the reference value from the reference nodes' answers and
// says, per reference node, whether its answer agreed with the others. A node
// missing from evals was not judged (a two-way split names nobody).
func buildReference(answers map[string]ProbAnswer, tol float64, maxMiss int) (Reference, map[string]bool, error) {
	ids := make([]string, 0, len(answers))
	for id := range answers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == 1 {
		return medianOf([]ProbAnswer{answers[ids[0]]}), map[string]bool{ids[0]: true}, nil
	}

	// The largest group of answers with the same text and token count.
	byKey := map[string][]string{}
	for _, id := range ids {
		a := answers[id]
		k := fmt.Sprintf("%d|%s", len(a.Tokens), a.Text)
		byKey[k] = append(byKey[k], id)
	}
	var best []string
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if len(byKey[k]) > len(best) {
			best = byKey[k]
		}
	}
	if len(best) < 2 {
		return Reference{}, nil, errRefsDisagree
	}

	// Within it, the median of all of them stays on the right side even when one
	// is off (probe.md 「기준 값 만들기」), so it picks out who agrees. Then each of
	// those is judged against the median of the OTHERS that agree, never one that
	// includes its own value (probe.md 「자기 값을 기준에 넣기」).
	all := make([]ProbAnswer, 0, len(best))
	for _, id := range best {
		all = append(all, answers[id])
	}
	mid := medianOf(all)
	var agreeing []string
	for _, id := range best {
		if r, _, _ := compareAnswer(answers[id], mid, tol, maxMiss); r == "" {
			agreeing = append(agreeing, id)
		}
	}
	evals := map[string]bool{}
	for _, id := range ids {
		evals[id] = false
	}
	var agree []ProbAnswer
	for _, id := range agreeing {
		var others []ProbAnswer
		for _, o := range agreeing {
			if o != id {
				others = append(others, answers[o])
			}
		}
		if len(others) == 0 {
			continue
		}
		if r, _, _ := compareAnswer(answers[id], medianOf(others), tol, maxMiss); r == "" {
			evals[id] = true
			agree = append(agree, answers[id])
		}
	}
	if len(agree) < 2 {
		if len(best) == 2 {
			// Two that differ in probability: no telling which is off.
			return Reference{}, nil, errRefsDisagree
		}
		return Reference{}, evals, errRefsDisagree
	}
	return medianOf(agree), evals, nil
}

// --- questions -------------------------------------------------------------

var refTopics = []string{
	"a lighthouse", "a night train", "a market in the rain", "an old library", "a mountain village",
	"a river crossing", "a small bakery", "a snowy road", "a broken clock", "a fishing boat",
	"a quiet harbor", "a city rooftop", "a desert well", "a paper lantern", "a garden gate",
	"a last bus", "a stone bridge", "a winter orchard", "a radio tower", "a lonely island",
	"a tea house", "a mill wheel", "a ferry dock", "a cobbled street", "a glass greenhouse",
	"a salt marsh", "a clock tower", "a canal boat", "a pine forest", "an empty stadium",
	"a hilltop chapel", "a weaving shop",
}

var refWords = []string{
	"amber", "bridge", "candle", "drift", "ember", "fable", "garnet", "harbor",
	"ivory", "juniper", "kettle", "lantern", "meadow", "nectar", "orchid", "pebble",
	"quartz", "ripple", "saffron", "thistle", "umber", "velvet", "willow", "yarrow",
	"zephyr", "anchor", "breeze", "cobalt", "dusk", "echo", "flint", "glimmer",
}

// newRefPrompt draws a question nobody has seen.
func newRefPrompt() string {
	var b [8]byte
	_, _ = crand.Read(b[:])
	topic := refTopics[int(binary.BigEndian.Uint16(b[0:2]))%len(refTopics)]
	word := refWords[int(binary.BigEndian.Uint16(b[2:4]))%len(refWords)]
	n := 1 + int(binary.BigEndian.Uint32(b[4:8])%9999)
	return fmt.Sprintf("Q: Write one short sentence about %s that uses the word \"%s\" and the number %d.\nA:", topic, word, n)
}

// --- asking ----------------------------------------------------------------

// refResult is one check of one node.
type refResult struct {
	outcome  string
	verdict  string
	reason   string
	detail   string
	cached   bool
	attempts int
}

// askProbs asks one node and waits for its answer with candidates. A node whose
// engine has no candidate run answers with no tokens, which judges as no_probs.
func (p *Prober) askProbs(t Target, prompt string) (ProbAnswer, string) {
	res, err := p.firer.SubmitProbs(t, prompt)
	if errors.Is(err, errNoProbeRun) {
		return ProbAnswer{}, OutcomeAnswered
	}
	if err != nil || res.Outcome != OutcomeSubmitted {
		if res.Outcome == "" || res.Outcome == OutcomeSubmitted {
			return ProbAnswer{}, OutcomeRefused
		}
		return ProbAnswer{}, res.Outcome
	}
	a, outcome, _ := p.firer.CollectProbs(t.Node.ID, res.JobID, p.deadlineFor(t))
	if outcome == "" {
		outcome = OutcomeRefused
	}
	return a, outcome
}

// targetsByAddr indexes this round's targets by node address.
func (p *Prober) targetsByAddr() map[string]Target {
	out := make(map[string]Target, len(p.targets))
	for _, t := range p.targets {
		out[strings.ToLower(strings.TrimSpace(nodeAddressOf(t.Node.ID)))] = t
	}
	return out
}

// onModel points t at the node's service that runs the group's model, when it
// runs several. A system group has no model and keeps t as it is.
func onModel(t Target, g AssignGroup) Target {
	want := strings.ToLower(strings.TrimSpace(g.Model))
	if want == "" {
		return t
	}
	if m, err := faucet.ModelFromReport(t.Service.ModelHash); err == nil && m == want {
		return t
	}
	for _, s := range t.Node.Services {
		if !s.ServerReady {
			continue
		}
		if m, err := faucet.ModelFromReport(s.ModelHash); err == nil && m == want {
			t.Service = s
			return t
		}
	}
	return t
}

// refTargetsOf is the group's reference nodes this prober can reach now. A prober
// never checks itself, so it does not ask itself as a reference either.
func (p *Prober) refTargetsOf(g AssignGroup, byAddr map[string]Target) []Target {
	var out []Target
	for _, r := range g.Refs {
		addr := strings.ToLower(strings.TrimSpace(r))
		if addr == p.self && !p.cfg.FireAtSelf {
			continue
		}
		if t, ok := byAddr[addr]; ok {
			out = append(out, onModel(t, g))
		}
	}
	return out
}

func refSettingOf(g AssignGroup) RefSetting {
	return RefSetting{
		BudgetID: g.BudgetID,
		MetaHash: strings.ToLower(g.MetaHash),
		GPU:      strings.Join(strings.Fields(strings.ToLower(g.GPU)), " "),
		Profile:  strings.TrimSpace(g.Profile),
		Engine:   strings.TrimSpace(g.Engine),
		Model:    strings.ToLower(strings.TrimSpace(g.Model)),
	}
}

// askReferences puts a question to the references, builds the reference value,
// records how each reference did, and stores the question. reason is "" when a
// value was built.
func (p *Prober) askReferences(epoch int64, gi, round int, g AssignGroup, refs []Target, prompt string) (Reference, string) {
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		answers  = map[string]ProbAnswer{}
		noProbs  []string
		outcomes = map[string]string{}
	)
	for _, rt := range refs {
		wg.Add(1)
		go func(rt Target) {
			defer wg.Done()
			a, outcome := p.askProbs(rt, prompt)
			mu.Lock()
			defer mu.Unlock()
			outcomes[rt.Node.ID] = outcome
			switch {
			case outcome == OutcomeAnswered && len(a.Tokens) > 0:
				answers[rt.Node.ID] = a
			case outcome == OutcomeAnswered:
				noProbs = append(noProbs, rt.Node.ID)
			}
		}(rt)
	}
	wg.Wait()

	now := time.Now()
	eval := func(id string, pass bool, reason, detail string) {
		v := VerdictFail
		if pass {
			v = VerdictPass
		}
		if err := p.store.RecordRefEval(RefRow{Epoch: epoch, NodeID: id, Round: round, Group: gi,
			AskedAt: now, Outcome: OutcomeAnswered, Verdict: v, Reason: reason, Detail: detail}); err != nil {
			log.Printf("[probe] refcheck: record reference %s: %v", short(id), err)
		}
	}
	// A reference whose engine gives no candidates cannot be a reference.
	for _, id := range noProbs {
		eval(id, false, reasonNoProbs, "")
	}
	if len(answers) == 0 {
		return Reference{}, reasonNoRef
	}

	ref, evals, err := buildReference(answers, p.refTol(), p.cfg.RefMaxMisses)
	for id, ok := range evals {
		reason := ""
		if !ok {
			reason = reasonDisagree
		}
		eval(id, ok, reason, refDetail(answers[id], ref, 0, 0))
	}
	if err != nil {
		texts := make([]string, 0, len(answers))
		for id, a := range answers {
			texts = append(texts, fmt.Sprintf("%s=%q", short(id), a.Text))
		}
		sort.Strings(texts)
		log.Printf("[probe] refcheck: budget %d GPU %q: the reference nodes disagree (%s) - check their settings",
			g.BudgetID, g.GPU, strings.Join(texts, ", "))
		return Reference{}, reasonDisagree
	}
	if len(ref.Tokens) < 2 {
		return ref, reasonShort
	}
	if err := p.store.SaveRefCache(refSettingOf(g), prompt, answers, ref, now); err != nil {
		log.Printf("[probe] refcheck: store question: %v", err)
	}
	return ref, ""
}

// takeStored hands out a stored question for one node of a group this round.
func (p *Prober) takeStored(epoch int64, gi, round int, g AssignGroup) (RefCached, bool) {
	p.refMu.Lock()
	defer p.refMu.Unlock()
	if p.refUsed == nil {
		p.refUsed = map[string]map[int64]bool{}
	}
	key := fmt.Sprintf("%d|%d|%d", epoch, gi, round)
	used := p.refUsed[key]
	if used == nil {
		used = map[int64]bool{}
		p.refUsed[key] = used
	}
	c, ok, err := p.store.TakeRefCache(refSettingOf(g), used, time.Now())
	if err != nil {
		log.Printf("[probe] refcheck: stored question: %v", err)
		return RefCached{}, false
	}
	if ok {
		used[c.ID] = true
	}
	return c, ok
}

// judgeNode asks the node a question and holds the answer against ref.
func (p *Prober) judgeNode(t Target, prompt string, ref Reference, cached bool) refResult {
	a, outcome := p.askProbs(t, prompt)
	r := refResult{outcome: outcome, cached: cached, verdict: VerdictFail}
	if outcome != OutcomeAnswered {
		r.reason = outcome
		return r
	}
	reason, worst, misses := compareAnswer(a, ref, p.refTol(), p.cfg.RefMaxMisses)
	r.reason, r.detail = reason, refDetail(a, ref, worst, misses)
	if reason == "" {
		r.verdict = VerdictPass
	}
	return r
}

func refDetail(a ProbAnswer, ref Reference, worst float64, misses int) string {
	b, _ := json.Marshal(map[string]any{
		"answer": a.Text, "reference": ref.Text, "tokens": len(a.Tokens), "refTokens": len(ref.Tokens),
		"refs": ref.Refs, "dropped": ref.Dropped, "worstGap": worst, "misses": misses,
	})
	return string(b)
}

// refAttempt is one question for one node: from the references when they answer,
// else a stored one.
func (p *Prober) refAttempt(epoch int64, gi, round int, g AssignGroup, t Target, refs []Target) refResult {
	if g.CheckMode() == ModeRef && len(refs) > 0 {
	draw:
		for try := 0; try < refTries; try++ {
			prompt := newRefPrompt()
			ref, why := p.askReferences(epoch, gi, round, g, refs, prompt)
			switch why {
			case "":
				return p.judgeNode(t, prompt, ref, false)
			case reasonShort:
				continue
			case reasonNoRef:
				break draw // nobody answered: fall back to a stored question
			default:
				return refResult{outcome: OutcomeSkipped, verdict: VerdictFail, reason: why}
			}
		}
	}
	c, ok := p.takeStored(epoch, gi, round, g)
	if !ok {
		reason := reasonNoQuestion
		if g.CheckMode() == ModeRef {
			reason = reasonNoRef
		}
		return refResult{outcome: OutcomeSkipped, verdict: VerdictFail, reason: reason}
	}
	return p.judgeNode(t, c.Prompt, c.Reference, true)
}

// refJob is one check to run: a node of a group, or (target nil) a question for
// the group's references alone.
type refJob struct {
	gi     int
	round  int
	target *Target
}

// runRefJob runs one check and writes its row.
func (p *Prober) runRefJob(epoch int64, j refJob, byAddr map[string]Target) {
	g := p.assign.Groups[j.gi]
	refs := p.refTargetsOf(g, byAddr)
	if j.target == nil {
		if len(refs) > 0 {
			for try := 0; try < refTries; try++ {
				if _, why := p.askReferences(epoch, j.gi, j.round, g, refs, newRefPrompt()); why != reasonShort {
					break
				}
			}
		}
		return
	}
	t := onModel(*j.target, g)
	row := RefRow{Epoch: epoch, NodeID: t.Node.ID, Round: j.round, Group: j.gi, Mode: g.CheckMode(), AskedAt: time.Now()}
	if ok, err := p.store.BeginRefCheck(row); err != nil || !ok {
		if err != nil {
			log.Printf("[probe] refcheck: begin %s: %v", short(t.Node.ID), err)
		}
		return
	}
	res := p.refAttempt(epoch, j.gi, j.round, g, t, refs)
	res.attempts = 1
	if res.reason == reasonText {
		// probe.md 「재검사」: a new question, never the same one.
		second := p.refAttempt(epoch, j.gi, j.round, g, t, refs)
		second.attempts = 2
		res = second
	}
	row.Attempts, row.Outcome, row.Verdict, row.Reason, row.Detail, row.Cached =
		res.attempts, res.outcome, res.verdict, res.reason, res.detail, res.cached
	if res.cached {
		row.Mode = ModeCache
	}
	if err := p.store.FinishRefCheck(row); err != nil {
		log.Printf("[probe] refcheck: finish %s: %v", short(t.Node.ID), err)
	}
	if res.verdict != VerdictPass {
		log.Printf("[probe] refcheck: round %d: %s failed (%s) %s", j.round, short(t.Node.ID), res.reason, res.detail)
	}
}

// fireRefChecks asks the checked nodes of ref and cache groups whose moment in the
// current sampling window has come, and each ref group's references once a round.
// A node is asked once a round, whatever came back.
func (p *Prober) fireRefChecks(now time.Time) {
	if !p.hasAssign || p.assign.Stale(now) || !p.assign.Planned() {
		return
	}
	round := p.sampleRoundAt(now)
	if round == 0 {
		return
	}
	epoch := p.assign.Epoch
	rows, err := p.store.RefRows(epoch)
	if err != nil {
		log.Printf("[probe] refcheck: rows: %v", err)
		return
	}
	p.forgetOldRefUse(epoch)
	start, win := p.sampleClock()
	w := win[round-1]
	due := func(key string) bool {
		return !now.Before(start.Add(w[0]).Add(p.sampleOffset("ref", round, key, w[1]-w[0])))
	}
	byAddr := p.targetsByAddr()

	var jobs []refJob
	seen := map[string]bool{}
	for gi, g := range p.assign.Groups {
		mode := g.CheckMode()
		if mode != ModeRef && mode != ModeCache {
			continue
		}
		for _, m := range g.Members {
			addr := strings.ToLower(strings.TrimSpace(m))
			if g.IsRef(addr) || (addr == p.self && !p.cfg.FireAtSelf) {
				continue
			}
			t, ok := byAddr[addr]
			if !ok || seen[t.Node.ID] || rows[t.Node.ID][round-1] != nil || !due(t.Node.ID) {
				continue
			}
			seen[t.Node.ID] = true
			tt := t
			jobs = append(jobs, refJob{gi: gi, round: round, target: &tt})
		}
		// The references' own question, for a reference no other question of this
		// round has judged yet.
		// Asked once a round, answered or not: a reference that is down is not asked
		// again every minute.
		if mode == ModeRef && due(fmt.Sprintf("refs|%d", gi)) && p.markRefsAsked(epoch, gi, round) {
			for _, rt := range p.refTargetsOf(g, byAddr) {
				if rows[rt.Node.ID][round-1] == nil {
					jobs = append(jobs, refJob{gi: gi, round: round})
					break
				}
			}
		}
	}
	if len(jobs) == 0 {
		return
	}
	sem := make(chan struct{}, maxConcurrentGroups)
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func(j refJob) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			p.runRefJob(epoch, j, byAddr)
		}(j)
	}
	wg.Wait()
	log.Printf("[probe] refcheck: round %d: %d checks", round, len(jobs))
}

// markRefsAsked records that a group's references got their own question this
// round. false when they already did.
func (p *Prober) markRefsAsked(epoch int64, gi, round int) bool {
	p.refMu.Lock()
	defer p.refMu.Unlock()
	if p.refAsked == nil {
		p.refAsked = map[string]bool{}
	}
	key := fmt.Sprintf("%d|%d|%d", epoch, gi, round)
	if p.refAsked[key] {
		return false
	}
	p.refAsked[key] = true
	return true
}

// forgetOldRefUse drops the bookkeeping of earlier slots.
func (p *Prober) forgetOldRefUse(epoch int64) {
	p.refMu.Lock()
	defer p.refMu.Unlock()
	prefix := fmt.Sprintf("%d|", epoch)
	for k := range p.refUsed {
		if !strings.HasPrefix(k, prefix) {
			delete(p.refUsed, k)
		}
	}
	for k := range p.refAsked {
		if !strings.HasPrefix(k, prefix) {
			delete(p.refAsked, k)
		}
	}
}

// --- settling --------------------------------------------------------------

// modeOf is how a node is checked this slot: ref for a reference node, otherwise
// its group's mode (quiz for a node in no group, or from an older RV).
func (p *Prober) modeOf(addr string) (AssignGroup, string) {
	addr = strings.ToLower(strings.TrimSpace(addr))
	first := -1
	for i, g := range p.assign.Groups {
		in := false
		for _, m := range g.Members {
			if strings.EqualFold(strings.TrimSpace(m), addr) {
				in = true
				break
			}
		}
		if !in {
			continue
		}
		if g.IsRef(addr) {
			return g, ModeRef
		}
		if first < 0 {
			first = i
		}
	}
	if first < 0 {
		return AssignGroup{}, ModeQuiz
	}
	g := p.assign.Groups[first]
	return g, g.CheckMode()
}

// decideRef turns a node's reference-check rounds into a call, like decideTicket
// does for the quiz: need of the rounds passed, or no ticket.
func decideRef(rounds [SampleRounds]*RefRow, need int) (ticketCall, string) {
	passed, pending, failed := 0, 0, 0
	var lost []string
	for i, r := range rounds {
		switch {
		case r == nil:
		case r.Verdict == "":
			pending++
		case r.Verdict == VerdictPass:
			passed++
		default:
			failed++
			lost = append(lost, fmt.Sprintf("round %d %s", i+1, r.Reason))
		}
	}
	call := callFor(passed, pending, failed, need)
	if call != callRefuse {
		return call, ""
	}
	if len(lost) > 0 {
		return call, fmt.Sprintf("reference check passed %d of %d rounds, needs %d (%s)",
			passed, SampleRounds, need, strings.Join(lost, ", "))
	}
	return call, fmt.Sprintf("reference check passed %d of %d rounds, needs %d", passed, SampleRounds, need)
}
