package probe

// The model-identity check (WS-25).
//
// A node says which model it runs by reporting the model file's hash to the
// directory, and the prober believed it. The size check (models.go, paramsFit)
// catches a hash that does not fit the budget, but the size is the node's own
// word as well. This check asks the model itself.
//
// HOW IT WORKS
//
//	cohort    the nodes of ONE group, in ONE slot, whose ticket would name the
//	          same model budget. Nodes are regrouped every slot and a prober can
//	          cover several groups, so each (slot, group, model) is kept apart.
//	prompt    one open-ended sentence request, the same for the whole cohort in
//	          a round, at temperature 0. Same weights, same prompt: the same text.
//	          Different weights write something else, which a quiz answer
//	          ("Paris") would not show.
//	rounds    every node of a cohort is asked TWICE in a slot, once in each
//	          sampling window, at a random moment inside it: the first half hour
//	          of the firing window and the last. The two rounds have different
//	          prompts. There is no retry: a node that does not answer within its
//	          deadline has answered nothing.
//	verdict   in each round answers are grouped by their first words. A group of
//	          answers holding MORE THAN HALF the cohort wins; the nodes in it
//	          pass the round and the rest fail it. No such group, or fewer than
//	          compareMin answers: nobody fails.
//	ticket    a node gets its ticket only if its quiz passed AND it did not fail
//	          either round. Anything else, and the slot pays it nothing.
//
// WHY NO STORED ANSWERS. The prompt is built from the slot, the group, the model
// and the round, and carries a number only that seed fixes, so it is never asked
// twice and a node cannot have the answer ready. The price is that the cohort is
// the only reference: a majority of copies of one fake would win, which is why a
// /24 is counted once (see decideCohort).
//
// WHEN. A passing node's ticket is HELD (a round-0 row) until both rounds are
// judged, which is a minute after the last sampling window closes. The ticket is
// bound to the slot's root, so everything has to be settled before the slot ends.

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"log"
	"math/big"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/isannai/mesh/pkg/faucet"
)

const (
	// compareMin is the smallest cohort that is compared. Below it nobody can be
	// told apart from a majority, so everybody passes.
	compareMin = 3

	// compareMaxTokens is how much of the answer is asked for. A sentence is
	// enough to tell two models apart and short enough to stay cheap.
	compareMaxTokens = 48

	// compareKeyRunes is how much of the answer is compared. Greedy decoding on
	// the same weights agrees at the start of the text; GPUs and engine builds
	// can drift later, so only the start counts.
	compareKeyRunes = 48

	// compareKeepRunes is how much of the raw answer is stored for a later look.
	compareKeepRunes = 200

	// compareWindow is how long each sampling window is: the first and the last
	// half hour of the firing window.
	compareWindow = 30 * time.Minute

	// compareSettleMargin is how long after a window closes the round waits
	// before it is judged. Every ask is made inside the window and the prober
	// waits for all of them before its next tick, so by then they are all in.
	compareSettleMargin = time.Minute
)

// OutcomeHeld marks a round-0 row: the ticket of a node whose quiz passed,
// waiting for the two rounds.
const OutcomeHeld = "held"

// The decisions a settled round writes (and, on a held ticket, win = issued and
// lose = refused).
const (
	DecisionWin  = "win"  // in the winning group of answers
	DecisionLose = "lose" // answered differently, or did not answer, in a round that was judged
	DecisionSkip = "skip" // the round was not judged: too few answers, or none held more than half
)

// cohortKey names one cohort.
type cohortKey struct {
	Epoch int64
	Group int
	Model string // 0x + 64 hex, the ticket spelling
}

var compareTopics = []string{
	"a lighthouse", "a night train", "a market in the rain", "an old library", "a mountain village",
	"a river crossing", "a small bakery", "a snowy road", "a broken clock", "a fishing boat",
	"a quiet harbor", "a city rooftop", "a desert well", "a paper lantern", "a garden gate",
	"a last bus", "a stone bridge", "a winter orchard", "a radio tower", "a lonely island",
	"a tea house", "a mill wheel", "a ferry dock", "a cobbled street", "a glass greenhouse",
	"a salt marsh", "a clock tower", "a canal boat", "a pine forest", "an empty stadium",
	"a hilltop chapel", "a weaving shop",
}

var compareWords = []string{
	"amber", "bridge", "candle", "drift", "ember", "fable", "garnet", "harbor",
	"ivory", "juniper", "kettle", "lantern", "meadow", "nectar", "orchid", "pebble",
	"quartz", "ripple", "saffron", "thistle", "umber", "velvet", "willow", "yarrow",
	"zephyr", "anchor", "breeze", "cobalt", "dusk", "echo", "flint", "glimmer",
}

// comparePrompt is the cohort's prompt for one round: the same for every member
// and every prober of the group, different for every slot, group, model and round.
func comparePrompt(k cohortKey, round int) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("isann-probe-compare|%d|%d|%s|%d", k.Epoch, k.Group, k.Model, round)))
	topic := compareTopics[int(binary.BigEndian.Uint16(h[0:2]))%len(compareTopics)]
	word := compareWords[int(binary.BigEndian.Uint16(h[2:4]))%len(compareWords)]
	n := 1 + int(binary.BigEndian.Uint32(h[4:8])%9999)
	return fmt.Sprintf("Write one short sentence about %s that uses the word \"%s\" and the number %d.", topic, word, n)
}

// answerKey is the part of an answer that is compared: lower case, white space
// folded, the first compareKeyRunes characters. "" for an empty answer.
func answerKey(s string) string {
	r := []rune(strings.ToLower(strings.Join(strings.Fields(s), " ")))
	if len(r) > compareKeyRunes {
		r = r[:compareKeyRunes]
	}
	return string(r)
}

// keepAnswer cuts a raw answer to what is stored.
func keepAnswer(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > compareKeepRunes {
		r = r[:compareKeepRunes]
	}
	return string(r)
}

// cmpVote is one cohort member as decideCohort sees it.
type cmpVote struct {
	Node    string
	Slash24 string
	Key     string // answerKey of what the node said; "" = no answer
}

// decideCohort turns a round's answers into one decision per node.
//
//	fewer than compareMin answers          everybody skip
//	no group of answers over half          everybody skip
//	otherwise                              the group over half win, the rest lose
//
// Exactly half is not over half: two groups of two answers are a draw, and a
// draw drops nobody.
//
// 🔴 A /24 COUNTS ONCE. Each node's vote weighs 1/(nodes answering from its /24),
// so a farm of machines behind one connection is one voice however many it
// registers, which is the same unit the shot scheduler treats as one owner. Without
// it five copies of one fake would outvote three honest nodes.
//
// A member that gave no answer loses once the round is judged: otherwise
// refusing the question would be the way to avoid it. In a round that is not
// judged it skips like everybody else.
func decideCohort(votes []cmpVote) map[string]string {
	out := make(map[string]string, len(votes))
	skipAll := func() map[string]string {
		for _, v := range votes {
			out[v.Node] = DecisionSkip
		}
		return out
	}

	bucket := func(v cmpVote) string {
		if v.Slash24 == "" {
			return "node:" + v.Node // unknown network: its own voice
		}
		return v.Slash24
	}
	var answered []cmpVote
	per := map[string]int{}
	for _, v := range votes {
		if v.Key == "" {
			continue
		}
		answered = append(answered, v)
		per[bucket(v)]++
	}
	if len(answered) < compareMin {
		return skipAll()
	}

	total := new(big.Rat)
	byKey := map[string]*big.Rat{}
	for _, v := range answered {
		w := big.NewRat(1, int64(per[bucket(v)]))
		total.Add(total, w)
		if byKey[v.Key] == nil {
			byKey[v.Key] = new(big.Rat)
		}
		byKey[v.Key].Add(byKey[v.Key], w)
	}
	winner, found := "", false
	two := big.NewRat(2, 1)
	for key, w := range byKey {
		if new(big.Rat).Mul(w, two).Cmp(total) > 0 {
			winner, found = key, true
			break // only one group can hold more than half
		}
	}
	if !found {
		return skipAll()
	}
	for _, v := range votes {
		if v.Key == winner {
			out[v.Node] = DecisionWin
		} else {
			out[v.Node] = DecisionLose
		}
	}
	return out
}

// buildCohorts groups this slot's targets into cohorts and returns the cohort of
// every node that is in one.
//
// Only a node whose ticket would carry a model budget is in a cohort: the check
// guards what a model budget pays, and a plain ticket pays the same whatever the
// node runs. Distinct nodes are counted, so a node serving the model twice is one.
func (p *Prober) buildCohorts() map[string]cohortKey {
	if p.cfg.NoCompare || !p.hasAssign {
		return nil
	}
	type slot struct {
		group int
		model string
	}
	members := map[slot]map[string]bool{}
	for _, t := range p.targets {
		model, _, _, write := p.nodeTicketBudget(t)
		if !write || model == "" {
			continue
		}
		gi := p.assign.GroupOf(nodeAddressOf(t.Node.ID))
		if gi < 0 {
			continue
		}
		k := slot{gi, model}
		if members[k] == nil {
			members[k] = map[string]bool{}
		}
		members[k][t.Node.ID] = true
	}
	out := map[string]cohortKey{}
	for k, nodes := range members {
		if len(nodes) < compareMin {
			continue
		}
		for id := range nodes {
			out[id] = cohortKey{Epoch: p.assign.Epoch, Group: k.group, Model: k.model}
		}
	}
	return out
}

// compareWindows returns the two sampling windows as offsets from the slot
// start: the first compareWindow of the firing window and the last. A firing
// window too short for two of them is split in half.
func compareWindows(slotSec int, lead, tail time.Duration) [2][2]time.Duration {
	opens, closes := fireWindow(slotSec, lead, tail)
	w := compareWindow
	if half := (closes - opens) / 2; w > half {
		w = half
	}
	return [2][2]time.Duration{{opens, opens + w}, {closes - w, closes}}
}

// compareClock is this slot's start and sampling windows.
func (p *Prober) compareClock() (time.Time, [2][2]time.Duration) {
	slotSec := p.assign.SlotSec
	if slotSec <= 0 {
		slotSec = faucet.SlotSeconds
	}
	start := time.Unix(faucet.SlotStartAt(p.assign.Epoch, slotSec), 0)
	return start, compareWindows(slotSec, p.cfg.FireLead(), p.cfg.FireTail())
}

// compareMargin is how long after a window closes its round is judged.
func (p *Prober) compareMargin() time.Duration {
	m := compareSettleMargin
	if half := p.cfg.FireTail() / 2; half < m {
		m = half
	}
	return m
}

// compareRoundAt is the sampling round whose window holds now: 1, 2, or 0.
func (p *Prober) compareRoundAt(now time.Time) int {
	start, win := p.compareClock()
	for i, w := range win {
		if !now.Before(start.Add(w[0])) && now.Before(start.Add(w[1])) {
			return i + 1
		}
	}
	return 0
}

// compareOffset is the moment inside a round's window at which a node is asked.
//
// Random, so the whole cohort is not asked at one instant and a node cannot know
// when its sample comes. It is kept for the slot, so a tick that comes round again
// before the node is due does not draw a new moment. The span stops short of the
// window's end by two ticks: a node due in the last seconds would otherwise fall
// between two ticks and never be asked.
func (p *Prober) compareOffset(round int, nodeID string, window time.Duration) time.Duration {
	if p.compareAt == nil || p.compareAtEpoch != p.assign.Epoch {
		p.compareAt, p.compareAtEpoch = map[string]time.Duration{}, p.assign.Epoch
	}
	key := fmt.Sprintf("%d|%s", round, nodeID)
	if off, ok := p.compareAt[key]; ok {
		return off
	}
	span := window - 2*time.Duration(p.cfg.FireIntervalSec)*time.Second
	var off time.Duration
	if span > 0 {
		off = time.Duration(p.rng.Int63n(int64(span)))
	}
	p.compareAt[key] = off
	return off
}

// fireCompare asks the nodes of this slot's cohorts whose moment in the current
// sampling window has come. Nobody is asked twice in a round, whatever came back.
func (p *Prober) fireCompare(now time.Time) {
	if p.cfg.NoCompare || !p.hasAssign || p.assign.Stale(now) {
		return
	}
	round := p.compareRoundAt(now)
	if round == 0 {
		return
	}
	cohorts := p.buildCohorts()
	if len(cohorts) == 0 {
		return
	}
	asked, err := p.store.CompareAsked(p.assign.Epoch, round)
	if err != nil {
		log.Printf("[probe] compare: asked nodes: %v", err)
		return
	}

	start, win := p.compareClock()
	w := win[round-1]
	var due []Target
	seen := map[string]bool{}
	for _, t := range p.targets {
		k, ok := cohorts[t.Node.ID]
		if !ok || asked[t.Node.ID] || seen[t.Node.ID] {
			continue
		}
		// Only the service that carries the model ticket is asked.
		if m, _, _, write := p.nodeTicketBudget(t); !write || m != k.Model {
			continue
		}
		seen[t.Node.ID] = true
		if now.Before(start.Add(w[0]).Add(p.compareOffset(round, t.Node.ID, w[1]-w[0]))) {
			continue
		}
		due = append(due, t)
	}
	if len(due) == 0 {
		return
	}

	sem := make(chan struct{}, maxConcurrentGroups)
	var wg sync.WaitGroup
	for _, t := range due {
		wg.Add(1)
		go func(t Target) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			p.askOne(t, cohorts[t.Node.ID], round)
		}(t)
	}
	wg.Wait()
	log.Printf("[probe] compare: round %d: asked %d nodes", round, len(due))
}

// askOne puts a cohort's prompt to one node and records what came back.
func (p *Prober) askOne(t Target, k cohortKey, round int) {
	answer, outcome := p.askCompare(t, k, round)
	row := CompareRow{
		Epoch: k.Epoch, Group: k.Group, Model: k.Model, Round: round,
		NodeID: t.Node.ID, Service: t.Service.Name, Slash24: t.Slash24,
		AskedAt: time.Now(), Key: answerKey(answer), Answer: keepAnswer(answer), Outcome: outcome,
	}
	if err := p.store.RecordCompare(row); err != nil {
		log.Printf("[probe] compare: record %s: %v", short(t.Node.ID), err)
	}
}

// askCompare sends the cohort's prompt to one node and returns what came back.
// One submit and one wait, no retry: a node that does not answer inside its
// deadline has answered nothing.
func (p *Prober) askCompare(t Target, k cohortKey, round int) (answer, outcome string) {
	res, err := p.firer.SubmitPrompt(t, comparePrompt(k, round), compareMaxTokens)
	if err != nil || res.Outcome != OutcomeSubmitted {
		if res.Outcome == "" {
			return "", OutcomeRefused
		}
		return "", res.Outcome
	}
	fetched, err := p.firer.Collect(t.Node.ID, res.JobID, p.deadlineFor(t))
	if err != nil || fetched.Outcome != OutcomeAnswered {
		if fetched.Outcome == "" {
			return "", OutcomeRefused
		}
		return "", fetched.Outcome
	}
	return fetched.Answer, OutcomeAnswered
}

// deferToCompare holds the ticket of a node whose quiz passed, if it is in a
// cohort. It reports whether the ticket is held; false means issue it now.
//
// Nothing is sent to the node here. The sentence requests are made in the
// sampling windows (fireCompare), whenever the quiz happened.
func (p *Prober) deferToCompare(t Target) bool {
	k, ok := p.cohorts[t.Node.ID]
	if !ok || k.Epoch != p.assign.Epoch {
		return false
	}
	if m, _, _, write := p.nodeTicketBudget(t); !write || m != k.Model {
		return false
	}
	row := CompareRow{
		Epoch: k.Epoch, Group: k.Group, Model: k.Model, Round: 0,
		NodeID: t.Node.ID, Service: t.Service.Name, Slash24: t.Slash24,
		AskedAt: time.Now(), Outcome: OutcomeHeld,
	}
	if err := p.store.RecordCompare(row); err != nil {
		// Without the row the ticket could never be settled; paying it is the
		// safer way to fail.
		log.Printf("[probe] compare: hold %s: %v (ticket issued without the check)", short(t.Node.ID), err)
		return false
	}
	return true
}

// emitTicket issues a ticket. It is a field-sized seam so a test can see who
// would have been paid without signing anything.
func (p *Prober) emitTicket(t Target, at time.Time, kind string) {
	if p.issueFn != nil {
		p.issueFn(t, at, kind)
		return
	}
	p.issueTicket(t, at, kind)
}

// judgeRounds decides each sampling round once its window has closed and its
// margin has passed. Answers are judged per cohort: one group, one model.
func (p *Prober) judgeRounds(now time.Time) {
	if p.cfg.NoCompare || !p.hasAssign || p.assign.Stale(now) {
		return
	}
	start, win := p.compareClock()
	margin := p.compareMargin()
	since := now.AddDate(0, 0, -p.cfg.ObservationDays)

	for round := 1; round <= 2; round++ {
		if now.Before(start.Add(win[round-1][1]).Add(margin)) {
			continue
		}
		rows, err := p.store.OpenCompare(p.assign.Epoch, round)
		if err != nil {
			log.Printf("[probe] compare: open rows: %v", err)
			continue
		}
		type ck struct {
			group int
			model string
		}
		var order []ck
		by := map[ck][]CompareRow{}
		for _, r := range rows {
			k := ck{r.Group, r.Model}
			if _, seen := by[k]; !seen {
				order = append(order, k)
			}
			by[k] = append(by[k], r)
		}
		sort.SliceStable(order, func(i, j int) bool {
			if order[i].group != order[j].group {
				return order[i].group < order[j].group
			}
			return order[i].model < order[j].model
		})

		for _, k := range order {
			members := by[k]
			votes := make([]cmpVote, len(members))
			for i, r := range members {
				votes[i] = cmpVote{Node: r.NodeID, Slash24: r.Slash24, Key: r.Key}
			}
			decisions := decideCohort(votes)

			winKey := ""
			for _, r := range members {
				if decisions[r.NodeID] == DecisionWin {
					winKey = r.Key
					break
				}
			}
			var wins, loses, skips int
			for _, r := range members {
				d := decisions[r.NodeID]
				if err := p.store.SetCompareDecision(r, d); err != nil {
					log.Printf("[probe] compare: write decision for %s: %v", short(r.NodeID), err)
				}
				switch d {
				case DecisionWin:
					wins++
				case DecisionLose:
					loses++
					tally, _ := p.store.CompareStats(r.NodeID, since)
					log.Printf("[probe] compare: round %d: %s answered %q, the group answered %q: failed (lost %d of %d decided in %d days)",
						round, short(r.NodeID), shorten(r.Key), shorten(winKey),
						tally.Lose, tally.Win+tally.Lose+tally.Skip, p.cfg.ObservationDays)
				default:
					skips++
				}
			}
			log.Printf("[probe] compare: round %d group %d model %s: %d nodes, %d win, %d lose, %d skip",
				round, k.group, shortModel(k.model), len(members), wins, loses, skips)
		}
	}
}

// settleTickets issues the held tickets of nodes that did not fail either round.
//
// It waits until both rounds are judged and then runs on every tick until the
// slot ends, so a quiz that finished late is settled on the next tick. A node
// that was never asked in a round (the prober was not up for it) has nothing
// against it there: only an answer that failed counts against a node.
func (p *Prober) settleTickets(now time.Time) {
	if p.cfg.NoCompare || !p.hasAssign || p.assign.Stale(now) {
		return
	}
	start, win := p.compareClock()
	if now.Before(start.Add(win[1][1]).Add(p.compareMargin())) {
		return
	}
	for round := 1; round <= 2; round++ {
		open, err := p.store.OpenCompare(p.assign.Epoch, round)
		if err != nil {
			log.Printf("[probe] compare: open rows: %v", err)
			return
		}
		if len(open) > 0 {
			return // a round is not judged yet
		}
	}
	held, err := p.store.OpenCompare(p.assign.Epoch, 0)
	if err != nil {
		log.Printf("[probe] compare: held tickets: %v", err)
		return
	}
	if len(held) == 0 {
		return
	}
	decisions, err := p.store.CompareDecisions(p.assign.Epoch)
	if err != nil {
		log.Printf("[probe] compare: decisions: %v", err)
		return
	}
	targets := map[string]Target{}
	for _, t := range p.targets {
		targets[t.Node.ID+"|"+t.Service.Name] = t
	}

	var issued, refused int
	for _, r := range held {
		d := decisions[r.NodeID]
		if d.Round1 == DecisionLose || d.Round2 == DecisionLose {
			refused++
			if err := p.store.SetCompareDecision(r, DecisionLose); err != nil {
				log.Printf("[probe] compare: write decision for %s: %v", short(r.NodeID), err)
			}
			log.Printf("[probe] compare: %s passed the quiz but failed the model check (round 1 %s, round 2 %s): no ticket",
				short(r.NodeID), orDash(d.Round1), orDash(d.Round2))
			continue
		}
		t, ok := targets[r.NodeID+"|"+r.Service]
		if !ok {
			// Not in the directory any more: it cannot be given a ticket, and
			// leaving the row open lets the next tick try again.
			log.Printf("[probe] compare: %s is gone from the targets; ticket not issued", short(r.NodeID))
			continue
		}
		p.emitTicket(t, now, faucet.KindText)
		p.noteTicket(r.NodeID, VerdictPass, now)
		issued++
		if err := p.store.SetCompareDecision(r, DecisionWin); err != nil {
			log.Printf("[probe] compare: write decision for %s: %v", short(r.NodeID), err)
		}
	}
	log.Printf("[probe] compare: held tickets settled: %d issued, %d refused", issued, refused)
}

// orDash prints a decision for a log line, "-" for a round the node was not in.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// shorten cuts an answer prefix for a log line.
func shorten(s string) string {
	r := []rune(s)
	if len(r) > 32 {
		return string(r[:32]) + "…"
	}
	return s
}

// shortModel is the front of a model hash for a log line.
func shortModel(m string) string {
	if len(m) > 14 {
		return m[:14] + "…"
	}
	return m
}
