package probe

// compare.go: what is left of the model-identity check (WS-25).
//
// It asked every node of a group running the same model one sentence and let the
// largest group of matching answers win. That is gone (2026-10-14): copies of one
// fake could out-vote the honest nodes, and probe.md 「쓰지 않는 방식」 rules out a
// reference built from the nodes being checked. The oracle-node comparison that
// replaced it was dropped as well (2026-10-20); the quiz (quiz.go) is the check.
//
// The compare table stays: a settled ticket is a round-0 row in it (settle.go),
// and rows from before keep their history until they are pruned.

import "time"

// The decisions a settled ticket is written with: win = issued, lose = refused.
// skip only appears on rows from before 2026-10-14.
const (
	DecisionWin  = "win"
	DecisionLose = "lose"
	DecisionSkip = "skip"
)

// emitTicket issues a ticket. It is a field-sized seam so a test can see who
// would have been paid without signing anything.
func (p *Prober) emitTicket(t Target, at time.Time, kind string) {
	if p.issueFn != nil {
		p.issueFn(t, at, kind)
		return
	}
	p.issueTicket(t, at, kind)
}

// shortModel is the front of a model hash for a log line.
func shortModel(m string) string {
	if len(m) > 14 {
		return m[:14] + "…"
	}
	return m
}
