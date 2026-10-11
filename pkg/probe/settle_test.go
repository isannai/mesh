package probe

import (
	"sort"
	"strings"
	"testing"
	"time"
)

func settleAt() time.Time { return compareSlotStart().Add(2*time.Hour + 31*time.Minute) }

// quizShot records node idx's shot in a round and how it ended: a verdict
// (pass | fail | truncated) for an answered shot, an outcome (timeout | refused |
// queue_full | skipped) for one that never produced an answer, or "submitted" for
// one still in the air.
func quizShot(t *testing.T, p *Prober, idx, round int, end string) {
	t.Helper()
	tg := p.targets[idx]
	at := compareSlotStart().Add(time.Hour)
	id, err := p.store.RecordShot(Shot{
		FiredAt: at, NodeID: tg.Node.ID, NodeAddr: tg.Node.Addr, Slash24: tg.Slash24,
		Service: tg.Service.Name, ModelHash: tg.Service.ModelHash,
		Outcome: OutcomeSubmitted, AssignEpoch: compareEpoch, Round: round,
	})
	if err != nil {
		t.Fatal(err)
	}
	switch end {
	case VerdictPass, VerdictFail, VerdictTruncated:
		err = p.store.CompleteShot(id, at, 100, 10, 3, "x", OutcomeAnswered, end)
	case OutcomeSubmitted:
	default:
		err = p.store.FailShot(id, at, end)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// quizBoth gives the listed nodes a passing shot in both rounds.
func quizBoth(t *testing.T, p *Prober, idx ...int) {
	t.Helper()
	for _, i := range idx {
		quizShot(t, p, i, 1, VerdictPass)
		quizShot(t, p, i, 2, VerdictPass)
	}
}

func sortedIDs(ids []string) string {
	c := append([]string(nil), ids...)
	sort.Strings(c)
	return strings.Join(c, " ")
}

func nodeIDs(p *Prober, idx ...int) string {
	var out []string
	for _, i := range idx {
		out = append(out, p.targets[i].Node.ID)
	}
	return sortedIDs(out)
}

func TestDecideTicket(t *testing.T) {
	sh := func(outcome, verdict string) *QuizShot { return &QuizShot{Outcome: outcome, Verdict: verdict} }
	pass := sh(OutcomeAnswered, VerdictPass)
	cases := []struct {
		name string
		r    [2]*QuizShot
		d    RoundDecisions
		want ticketCall
	}{
		{"both rounds passed", [2]*QuizShot{pass, pass}, RoundDecisions{}, callIssue},
		{"the first round wrong", [2]*QuizShot{sh(OutcomeAnswered, VerdictFail), pass}, RoundDecisions{}, callRefuse},
		{"the second round wrong", [2]*QuizShot{pass, sh(OutcomeAnswered, VerdictFail)}, RoundDecisions{}, callRefuse},
		{"an answer cut off is not a pass", [2]*QuizShot{pass, sh(OutcomeAnswered, VerdictTruncated)}, RoundDecisions{}, callRefuse},
		{"no answer in time", [2]*QuizShot{pass, sh(OutcomeTimeout, "")}, RoundDecisions{}, callRefuse},
		{"refused", [2]*QuizShot{sh(OutcomeRefused, ""), pass}, RoundDecisions{}, callRefuse},
		{"queue full", [2]*QuizShot{pass, sh(OutcomeQueueFull, "")}, RoundDecisions{}, callRefuse},
		{"asked in round 1 only: one pass is not two", [2]*QuizShot{pass, nil}, RoundDecisions{}, callRefuse},
		{"asked in round 2 only: one pass is not two", [2]*QuizShot{nil, pass}, RoundDecisions{}, callRefuse},
		{"a round the prober skipped is not a pass", [2]*QuizShot{sh(OutcomeSkipped, ""), pass}, RoundDecisions{}, callRefuse},
		{"only the prober's own failure", [2]*QuizShot{sh(OutcomeSkipped, ""), nil}, RoundDecisions{}, callNone},
		{"never asked", [2]*QuizShot{nil, nil}, RoundDecisions{}, callNone},
		{"a result still to come", [2]*QuizShot{pass, sh(OutcomeSubmitted, "")}, RoundDecisions{}, callWait},
		{"a lost round decides even with another in the air", [2]*QuizShot{sh(OutcomeAnswered, VerdictFail), sh(OutcomeSubmitted, "")}, RoundDecisions{}, callRefuse},
		{"a sentence round lost", [2]*QuizShot{pass, pass}, RoundDecisions{Round1: DecisionWin, Round2: DecisionLose}, callRefuse},
		{"sentence rounds that were not judged", [2]*QuizShot{pass, pass}, RoundDecisions{Round1: DecisionSkip, Round2: DecisionSkip}, callIssue},
		{"a sentence round lost but the quiz not asked", [2]*QuizShot{nil, nil}, RoundDecisions{Round1: DecisionLose}, callNone},
	}
	for _, c := range cases {
		got, why := decideTicket(c.r, c.d)
		if got != c.want {
			t.Errorf("%s: call %d (%q), want %d", c.name, got, why, c.want)
		}
		if got == callRefuse && why == "" {
			t.Errorf("%s: a refusal with no reason", c.name)
		}
	}
}

// 🔴 A ticket needs the quiz passed in BOTH rounds.
func TestSettlePaysOnlyThoseWhoPassedBothRounds(t *testing.T) {
	p := compareProber(t, 7)
	var issued []string
	issuedBy(p, &issued)

	quizBoth(t, p, 0)                      // 0: pass, pass
	quizShot(t, p, 1, 1, VerdictPass)      // 1: pass, fail
	quizShot(t, p, 1, 2, VerdictFail)      //
	quizShot(t, p, 2, 1, VerdictFail)      // 2: fail, pass
	quizShot(t, p, 2, 2, VerdictPass)      //
	quizShot(t, p, 3, 1, VerdictPass)      // 3: asked in round 1 only
	quizShot(t, p, 4, 1, OutcomeTimeout)   // 4: no answer, then pass
	quizShot(t, p, 4, 2, VerdictPass)      //
	quizShot(t, p, 5, 1, VerdictPass)      // 5: pass, cut off
	quizShot(t, p, 5, 2, VerdictTruncated) //
	// 6 was never asked

	p.settleTickets(settleAt())
	if got, want := sortedIDs(issued), nodeIDs(p, 0); got != want {
		t.Errorf("issued %v, want only node 0, the one that passed both rounds", issued)
	}

	settled, err := p.store.SettledTickets(compareEpoch)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if !settled[p.targets[i].Node.ID] {
			t.Errorf("node %d was decided but not recorded", i)
		}
	}
	if settled[p.targets[6].Node.ID] {
		t.Error("a node that was never asked was recorded as decided")
	}
}

// The decision is on record, win for a ticket and lose for a refusal, and only the
// wins are counted as earned.
func TestSettleRecordsEachDecision(t *testing.T) {
	p := compareProber(t, 3)
	var issued []string
	issuedBy(p, &issued)
	quizBoth(t, p, 0)
	quizShot(t, p, 1, 1, VerdictFail)
	quizShot(t, p, 1, 2, VerdictPass)

	p.settleTickets(settleAt())

	decision := func(i int) string {
		var d string
		err := p.store.db.QueryRow(`SELECT decision FROM compare WHERE epoch = ? AND node_id = ? AND round = 0`,
			compareEpoch, p.targets[i].Node.ID).Scan(&d)
		if err != nil {
			return "none"
		}
		return d
	}
	if decision(0) != DecisionWin || decision(1) != DecisionLose || decision(2) != "none" {
		t.Errorf("decisions = %s %s %s, want win lose none", decision(0), decision(1), decision(2))
	}
	earned, err := p.store.PassesToday(dayStart(settleAt()))
	if err != nil {
		t.Fatal(err)
	}
	if earned[p.targets[0].Node.ID] != 1 || earned[p.targets[1].Node.ID] != 0 {
		t.Errorf("earned today = %v, want one for node 0 and none for node 1", earned)
	}
}

// Nothing is decided before the margin after the second window, and nothing once
// the slot is over.
func TestSettleWaitsForItsTimeAndStopsWithTheSlot(t *testing.T) {
	p := compareProber(t, 2)
	var issued []string
	issuedBy(p, &issued)
	quizBoth(t, p, 0, 1)
	start := compareSlotStart()

	p.settleTickets(start.Add(2*time.Hour + 30*time.Minute))
	p.settleTickets(start.Add(2*time.Hour + 30*time.Minute + 59*time.Second))
	if len(issued) != 0 {
		t.Fatalf("settled inside the margin: %v", issued)
	}
	p.settleTickets(start.Add(4 * time.Hour)) // another slot by then
	if len(issued) != 0 {
		t.Fatalf("settled a slot that was over: %v", issued)
	}
	p.settleTickets(settleAt())
	if len(issued) != 2 {
		t.Errorf("issued %v, want both", issued)
	}
}

// A node is decided once. A result that comes in late is decided on the next tick.
func TestSettleDecidesEachNodeOnce(t *testing.T) {
	p := compareProber(t, 3)
	var issued []string
	issuedBy(p, &issued)
	quizBoth(t, p, 0, 1)

	p.settleTickets(settleAt())
	p.settleTickets(settleAt().Add(time.Minute))
	if len(issued) != 2 {
		t.Fatalf("issued %v, want each of the two once", issued)
	}

	// Node 2's result is only in now.
	quizBoth(t, p, 2)
	p.settleTickets(settleAt().Add(2 * time.Minute))
	if got, want := sortedIDs(issued), nodeIDs(p, 0, 1, 2); got != want {
		t.Errorf("issued %v, want all three exactly once", issued)
	}
}

// A shot still in the air is waited for, not settled on.
func TestSettleWaitsForAResultStillComing(t *testing.T) {
	p := compareProber(t, 2)
	var issued []string
	issuedBy(p, &issued)
	quizShot(t, p, 0, 1, VerdictPass)
	quizShot(t, p, 0, 2, OutcomeSubmitted)
	quizBoth(t, p, 1)

	p.settleTickets(settleAt())
	if got, want := sortedIDs(issued), nodeIDs(p, 1); got != want {
		t.Fatalf("issued %v, want only the node with nothing outstanding", issued)
	}
	if settled, _ := p.store.SettledTickets(compareEpoch); settled[p.targets[0].Node.ID] {
		t.Error("a node with a shot in the air was recorded as decided")
	}
}

// 🔴 The sentence rounds are judged before anyone is settled on them, and a node
// that lost one gets nothing however well it did at the quiz.
func TestSettleNeedsTheSentenceRoundsToo(t *testing.T) {
	p := compareProber(t, 5)
	var issued []string
	issuedBy(p, &issued)
	start := compareSlotStart()

	quizBoth(t, p, 0, 1, 2, 3, 4)
	// Round 1: node 4 is the odd one out. Round 2: node 3 is.
	compareRound(t, p, 1, "a1", "a1", "a1", "a1", "b1")
	compareRound(t, p, 2, "a2", "a2", "a2", "b2", "a2")

	// Not judged yet: the quiz alone is not enough to pay anybody.
	p.settleTickets(start.Add(2*time.Hour + 31*time.Minute))
	if len(issued) != 0 {
		t.Fatalf("settled before the sentence rounds were judged: %v", issued)
	}

	at := start.Add(2*time.Hour + 32*time.Minute)
	p.judgeRounds(at)
	p.settleTickets(at)
	if got, want := sortedIDs(issued), nodeIDs(p, 0, 1, 2); got != want {
		t.Fatalf("issued %v, want the 3 that lost no sentence round", issued)
	}
	if tally, _ := p.store.CompareStats(p.targets[4].Node.ID, start.Add(-time.Hour)); tally.Lose != 1 {
		t.Errorf("the lost round was not recorded: %+v", tally)
	}
}

// A silent member loses a sentence round that is judged, and a round too small or
// too split to judge pays everybody.
func TestSettleSentenceSilenceLosesAndUnjudgedRoundsPayEveryone(t *testing.T) {
	p := compareProber(t, 4)
	var issued []string
	issuedBy(p, &issued)
	quizBoth(t, p, 0, 1, 2, 3)
	compareRound(t, p, 1, "a", "a", "a", "") // node 3 did not answer
	compareRound(t, p, 2, "a", "a", "a", "a")
	at := settleAt()
	p.judgeRounds(at)
	p.settleTickets(at)
	if got, want := sortedIDs(issued), nodeIDs(p, 0, 1, 2); got != want {
		t.Errorf("issued %v, want the three that answered", issued)
	}

	q := compareProber(t, 4)
	var all []string
	issuedBy(q, &all)
	quizBoth(t, q, 0, 1, 2, 3)
	compareRound(t, q, 1, "a", "b", "", "") // only two answered
	compareRound(t, q, 2, "a", "a", "b", "b")
	q.judgeRounds(at)
	q.settleTickets(at)
	if len(all) != 4 {
		t.Errorf("issued %d tickets, want all 4: %v", len(all), all)
	}
}

// The switch stops the sentence rounds, not the quiz: a ticket is still settled
// on the quiz alone.
func TestSettleWithTheCheckSwitchedOff(t *testing.T) {
	p := compareProber(t, 3)
	p.cfg.NoCompare = true
	var issued []string
	issuedBy(p, &issued)
	quizBoth(t, p, 0, 1, 2)
	compareRound(t, p, 1, "a", "a", "b") // would be open, and would cost node 2 its ticket

	p.judgeRounds(settleAt())
	p.settleTickets(settleAt())
	if len(issued) != 3 {
		t.Errorf("issued %v, want all three on the quiz alone", issued)
	}
}

// 🔴 The ticket names the model the directory reports when it is settled. If the
// node's model changed since it was asked, the quiz was answered on another model
// from the one being paid, so it gets nothing.
func TestSettleRefusesAChangedModel(t *testing.T) {
	p := compareProber(t, 3)
	var issued []string
	issuedBy(p, &issued)
	quizBoth(t, p, 0, 1, 2)
	other := "sha256:" + strings.Repeat("ab", 32)
	p.targets[0].Service.ModelHash = other // changed since both rounds
	// Node 2 was asked on another model in the second round only.
	if _, err := p.store.db.Exec(`UPDATE shot SET model_hash = ? WHERE node_id = ? AND round = 2`,
		other, p.targets[2].Node.ID); err != nil {
		t.Fatal(err)
	}

	p.settleTickets(settleAt())
	if got, want := sortedIDs(issued), nodeIDs(p, 1); got != want {
		t.Errorf("issued %v, want only the node asked on its current model both times", issued)
	}
	for _, i := range []int{0, 2} {
		var d string
		if err := p.store.db.QueryRow(`SELECT decision FROM compare WHERE epoch = ? AND node_id = ? AND round = 0`,
			compareEpoch, p.targets[i].Node.ID).Scan(&d); err != nil || d != DecisionLose {
			t.Errorf("node %d's record = %q, %v, want lose", i, d, err)
		}
	}
}

// A node that passed but has left the directory cannot be handed a ticket; it is
// not written off, because it may be back on the next tick.
func TestSettleKeepsANodeThatLeftTheDirectory(t *testing.T) {
	p := compareProber(t, 2)
	var issued []string
	issuedBy(p, &issued)
	quizBoth(t, p, 0, 1)
	gone := p.targets[0]
	p.targets = p.targets[1:]

	p.settleTickets(settleAt())
	if len(issued) != 1 {
		t.Fatalf("issued %v, want the node still listed", issued)
	}
	if settled, _ := p.store.SettledTickets(compareEpoch); settled[gone.Node.ID] {
		t.Fatal("a node that left was written off")
	}
	p.targets = append(p.targets, gone)
	p.settleTickets(settleAt().Add(time.Minute))
	if len(issued) != 2 {
		t.Errorf("issued %v, want the returning node paid", issued)
	}
}

// Signing takes seconds a ticket, so the loop watches the slot end under it rather
// than trusting the moment the tick began.
func TestSettleStopsWhenTheSlotRunsOutUnderIt(t *testing.T) {
	p := compareProber(t, 3)
	var issued []string
	p.issueFn = func(tg Target, _ time.Time, _ string) {
		time.Sleep(400 * time.Millisecond)
		issued = append(issued, tg.Node.ID)
	}
	quizBoth(t, p, 0, 1, 2)

	slotEnd := compareSlotStart().Add(3 * time.Hour)
	p.settleTickets(slotEnd.Add(-200 * time.Millisecond))
	if len(issued) != 1 {
		t.Errorf("issued %d tickets, want the one signed before the slot ended", len(issued))
	}
	if settled, _ := p.store.SettledTickets(compareEpoch); len(settled) != 1 {
		t.Errorf("%d recorded, want only the one issued", len(settled))
	}
}

// Passes are counted by slot: two quiz passes and a ticket are one earned slot, a
// picture's own passing row is one, and a refusal is none.
func TestPassesTodayCountsSlots(t *testing.T) {
	p := compareProber(t, 3)
	var issued []string
	issuedBy(p, &issued)
	quizBoth(t, p, 0)
	quizShot(t, p, 1, 1, VerdictFail)
	quizShot(t, p, 1, 2, VerdictPass)
	p.settleTickets(settleAt())

	// A picture from another slot, passed, round 0.
	id, err := p.store.RecordShot(Shot{
		FiredAt: compareSlotStart().Add(-time.Hour), NodeID: p.targets[2].Node.ID, Slash24: "10.0.2",
		Outcome: OutcomeSubmitted, AssignEpoch: compareEpoch - 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.store.CompleteShot(id, time.Now(), 100, 1, 1, "x", OutcomeAnswered, VerdictPass); err != nil {
		t.Fatal(err)
	}

	got, err := p.store.PassesToday(dayStart(compareSlotStart().Add(-time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	if got[p.targets[0].Node.ID] != 1 || got[p.targets[1].Node.ID] != 0 || got[p.targets[2].Node.ID] != 1 {
		t.Errorf("earned = %v, want 1 for node 0 (two passes, one slot), 0 for node 1, 1 for node 2", got)
	}
}

// An older prober left a round-0 row with no decision for a ticket it was holding.
// Settling that node replaces the row, so it is decided once and not on every tick.
func TestSettleReplacesARowLeftUndecided(t *testing.T) {
	p := compareProber(t, 1)
	var issued []string
	issuedBy(p, &issued)
	quizBoth(t, p, 0)
	err := p.store.RecordCompare(CompareRow{
		Epoch: compareEpoch, Group: 0, Model: "0x" + qwen14Hex, NodeID: p.targets[0].Node.ID, Round: 0,
		Service: "llm", Slash24: "10.0.0", AskedAt: compareSlotStart().Add(time.Hour), Outcome: "held",
	})
	if err != nil {
		t.Fatal(err)
	}

	p.settleTickets(settleAt())
	p.settleTickets(settleAt().Add(time.Minute))
	p.settleTickets(settleAt().Add(2 * time.Minute))
	if len(issued) != 1 {
		t.Errorf("issued %d tickets for one node, want 1: %v", len(issued), issued)
	}
	var n int
	if err := p.store.db.QueryRow(`SELECT COUNT(*) FROM compare WHERE epoch = ? AND node_id = ? AND round = 0`,
		compareEpoch, p.targets[0].Node.ID).Scan(&n); err != nil || n != 1 {
		t.Errorf("%d round-0 rows, %v, want the one", n, err)
	}
}

// 🔴 The slot a new prober is swapped into. The old prober asked the quiz once, and
// that shot is a round-0 row the new settlement does not read, so a node that then
// passes the one round left is a node that passed one round. One is not two.
func TestSettleDoesNotPayOnOnePassWhateverTheReason(t *testing.T) {
	p := compareProber(t, 2)
	var issued []string
	issuedBy(p, &issued)

	// Node 0: the old prober's single quiz (round 0, truncated), then a pass in round 2.
	quizShot(t, p, 0, 0, VerdictTruncated)
	quizShot(t, p, 0, 2, VerdictPass)
	// Node 1: a pass in round 2 and the prober's own failure in round 1.
	quizShot(t, p, 1, 1, OutcomeSkipped)
	quizShot(t, p, 1, 2, VerdictPass)

	p.settleTickets(settleAt())
	if len(issued) != 0 {
		t.Fatalf("issued %v on a single pass", issued)
	}
	for i := 0; i < 2; i++ {
		var d string
		if err := p.store.db.QueryRow(`SELECT decision FROM compare WHERE epoch = ? AND node_id = ? AND round = 0`,
			compareEpoch, p.targets[i].Node.ID).Scan(&d); err != nil || d != DecisionLose {
			t.Errorf("node %d's record = %q, %v, want lose", i, d, err)
		}
	}
}
