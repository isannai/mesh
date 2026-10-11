package probe

// settle.go: deciding a slot's tickets.
//
// A ticket is the receipt for a slot, and nothing is issued while a slot is still
// asking. Once the second sampling window is over (sample.go) and the sentence
// rounds are judged (compare.go), every node the slot asked is decided at once:
//
//	quiz      asked in each sampling window (quiz.go). A ticket needs the quiz
//	          passed in BOTH rounds.
//	sentence  for a node in a group of nodes running the same model, its answer in
//	          each sentence round must not have lost (compare.go).
//
// 🔴 TWO PASSES, OR NO TICKET. A round the node was asked in and did not pass is
// lost, whatever the reason (wrong, cut off, refused, busy, no answer inside its
// deadline): there is no retry, and refusing the question must not be a way to
// avoid it. A round it was not asked in is not a pass either. By the time a slot is
// settled both windows are closed, so there is no asking it again, and one sample
// is not what a ticket is meant to rest on. That costs a node its slot when the
// prober was down for a whole window, when the node was not in the directory
// during one, or when the prober's own shot failed (OutcomeSkipped). A node that
// was not asked at all in either round has nothing to decide.
//
// The sentence rounds are the other way round: one the node was not asked in is
// not held against it, because it only ever decides a loss.
//
// WHEN. From a minute after the second window closes until the slot ends, on every
// tick, so a node whose last result came in late is decided on the next one. A
// ticket is bound to the slot's root and the node keeps only the current one, so
// nothing is issued once the slot is over.
//
// 🔴 SIGNING IS THE LIMIT. Every ticket is signed by the machine's TPM, which
// does one signature at a time and takes seconds for each, and all of a slot's
// tickets now go out in the last half hour of it. That is a few hundred tickets for
// one prober in one slot. Past that the nodes left over are the ones that go
// unpaid, so they are taken in a different order every slot.
//
// Each node is decided once. The record is a round-0 row in the compare table,
// written whichever way it went, and it is what stops the next tick from deciding
// the same node again.

import (
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/isannai/mesh/pkg/faucet"
)

// OutcomeSettled marks a round-0 row: the record that a node's ticket for the slot
// has been decided.
const OutcomeSettled = "settled"

// ticketCall is what a node's results come to.
type ticketCall int

const (
	callWait   ticketCall = iota // a result is still to come in: look again next tick
	callNone                     // never asked, or only the prober's own failures: nothing to decide
	callIssue                    // pay it
	callRefuse                   // the slot pays it nothing
)

// quizLabel is how a quiz shot reads in a log line: its verdict if it was answered,
// otherwise how it ended.
func quizLabel(s *QuizShot) string {
	switch {
	case s == nil:
		return "-"
	case s.Outcome == OutcomeAnswered:
		return s.Verdict
	default:
		return s.Outcome
	}
}

// quizSummary renders both rounds for a log line.
func quizSummary(r [2]*QuizShot) string {
	return fmt.Sprintf("round 1 %s, round 2 %s", quizLabel(r[0]), quizLabel(r[1]))
}

// decideTicket turns a node's two quiz rounds and its sentence decisions into a
// call. The string says why, for a refusal.
func decideTicket(rounds [2]*QuizShot, d RoundDecisions) (ticketCall, string) {
	passed, pending, lost := 0, false, false
	for _, r := range rounds {
		switch {
		case r == nil, r.Outcome == OutcomeSkipped:
			// not asked, or the prober's own failure: not a pass
		case r.Outcome == OutcomeSubmitted:
			pending = true
		case r.Outcome == OutcomeAnswered && r.Verdict == VerdictPass:
			passed++
		default:
			lost = true
		}
	}
	if lost {
		return callRefuse, "quiz not passed (" + quizSummary(rounds) + ")"
	}
	if pending {
		return callWait, ""
	}
	if passed == 0 {
		return callNone, ""
	}
	if passed < 2 {
		return callRefuse, "quiz passed in one round only (" + quizSummary(rounds) + ")"
	}
	if d.Round1 == DecisionLose || d.Round2 == DecisionLose {
		return callRefuse, fmt.Sprintf("model check failed (sentence round 1 %s, round 2 %s)", orDash(d.Round1), orDash(d.Round2))
	}
	return callIssue, ""
}

// modelShifted reports whether a node was asked on a model other than the one the
// directory names for it now, and which. A shot the prober skipped was not asked.
func modelShifted(rounds [2]*QuizShot, current string) (was string, shifted bool) {
	for _, r := range rounds {
		if r == nil || r.Outcome == OutcomeSkipped {
			continue
		}
		if r.ModelHash != current {
			return r.ModelHash, true
		}
	}
	return "", false
}

// settleTickets decides the tickets of every node the slot asked, once the slot has
// asked all it will.
func (p *Prober) settleTickets(now time.Time) {
	if !p.hasAssign || p.assign.Stale(now) {
		return
	}
	start, win := p.sampleClock()
	if now.Before(start.Add(win[1][1]).Add(p.sampleMargin())) {
		return
	}
	epoch := p.assign.Epoch

	// The sentence rounds are judged before anyone is settled on them.
	if !p.cfg.NoCompare {
		for round := 1; round <= 2; round++ {
			open, err := p.store.OpenCompare(epoch, round)
			if err != nil {
				log.Printf("[probe] settle: open rows: %v", err)
				return
			}
			if len(open) > 0 {
				return
			}
		}
	}

	shots, err := p.store.QuizShots(epoch)
	if err != nil {
		log.Printf("[probe] settle: quiz shots: %v", err)
		return
	}
	if len(shots) == 0 {
		return
	}
	settled, err := p.store.SettledTickets(epoch)
	if err != nil {
		log.Printf("[probe] settle: settled tickets: %v", err)
		return
	}
	decisions, err := p.store.CompareDecisions(epoch)
	if err != nil {
		log.Printf("[probe] settle: decisions: %v", err)
		return
	}

	// The first result of a node in a round stands.
	byNode := map[string]*[2]*QuizShot{}
	var ids []string
	for i := range shots {
		s := &shots[i]
		if s.Round < 1 || s.Round > 2 {
			continue
		}
		r := byNode[s.NodeID]
		if r == nil {
			r = &[2]*QuizShot{}
			byNode[s.NodeID] = r
			ids = append(ids, s.NodeID)
		}
		if r[s.Round-1] == nil {
			r[s.Round-1] = s
		}
	}
	sort.Strings(ids)
	if p.rng != nil {
		p.rng.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
	}

	targets := make(map[string]Target, len(p.targets))
	for _, t := range p.targets {
		targets[t.Node.ID] = t
	}

	record := func(id, service, slash24, model, decision string) {
		row := CompareRow{
			Epoch: epoch, Group: p.assign.GroupOf(nodeAddressOf(id)), Model: model, NodeID: id, Round: 0,
			Service: service, Slash24: slash24, AskedAt: now, Outcome: OutcomeSettled, Decision: decision,
		}
		if err := p.store.RecordSettlement(row); err != nil {
			log.Printf("[probe] settle: record %s: %v", short(id), err)
		}
	}

	began := time.Now()
	var issued, refused, gone int
	for _, id := range ids {
		if settled[id] {
			continue
		}
		// Signing takes seconds a ticket: stop when the slot has run out under us,
		// not when the tick that began it did.
		if p.assign.Stale(now.Add(time.Since(began))) {
			log.Printf("[probe] settle: the slot ended with tickets still to sign")
			break
		}
		rounds := *byNode[id]
		call, why := decideTicket(rounds, decisions[id])
		switch call {
		case callWait, callNone:
			continue
		case callRefuse:
			t := targets[id]
			record(id, firstService(rounds), t.Slash24, "", DecisionLose)
			log.Printf("[probe] %s: no ticket this slot: %s", short(id), why)
			refused++
		case callIssue:
			t, ok := targets[id]
			if !ok {
				gone++
				continue
			}
			// The ticket names the model the directory reports now. It has to be the
			// one the node was asked on in every round, or the quiz was answered on a
			// different model from the one being paid.
			if was, shifted := modelShifted(rounds, t.Service.ModelHash); shifted {
				record(id, t.Service.Name, t.Slash24, "", DecisionLose)
				log.Printf("[probe] %s: no ticket this slot: its model changed during the slot (%s, now %s)",
					short(id), shortModel(was), shortModel(t.Service.ModelHash))
				refused++
				continue
			}
			model, _, _, _ := p.nodeTicketBudget(t)
			p.emitTicket(t, now, faucet.KindText)
			record(id, t.Service.Name, t.Slash24, model, DecisionWin)
			p.noteTicket(id, VerdictPass, now)
			issued++
		}
	}
	if gone > 0 {
		log.Printf("[probe] settle: %d nodes passed but are not in the directory now; trying again next tick", gone)
	}
	if issued+refused > 0 {
		log.Printf("[probe] settle: %d tickets issued, %d refused", issued, refused)
	}
}

// firstService is the service a node was asked on, for a record kept without its
// target.
func firstService(rounds [2]*QuizShot) string {
	for _, r := range rounds {
		if r != nil && r.Service != "" {
			return r.Service
		}
	}
	return ""
}
