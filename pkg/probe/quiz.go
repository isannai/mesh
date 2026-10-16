package probe

// quiz.go: the quiz, asked three times a slot.
//
// Every text node gets the quiz in EACH sampling window of a slot (sample.go):
// 0:30 to 1:00, 1:15 to 1:45 and 2:00 to 2:30 of a three hour slot, at a random
// moment inside each. Nothing is issued when an answer comes back. The slot's
// tickets are settled together once the last window is over (settle.go), and a node
// needs two of the three passes (Config.SamplePassMin).
//
// WHY THREE. One shot at one moment shows a node was up then. Three, this far apart,
// show it was up across the slot, and the moment of each is not known in advance.
// Needing two of them lets a node miss one, a busy moment or a restart, and still
// be paid, where two shots with both required made a single miss cost the slot. It is also the same pattern the model-identity check follows
// (compare.go), so a ticket always goes out at the same time, whatever else the
// node is checked on.
//
// WHICH NODES, AND WHEN. The unit is the /24, as before: every node behind one
// connection is asked at the SAME moment, because a farm of registrations sharing
// one GPU answers one at a time fine and fails all at once. So the random moment is
// drawn per /24 and per round, not per node.
//
// NO RETRY. A node is asked once per round whatever came back, a refusal and a
// timeout included. The shot is written before the answer is waited for, so a
// prober that restarts mid-round does not ask again.

import (
	"sort"
	"time"
)

// slash24Key is the unit the quiz is asked in: a node's /24, or the node itself
// when it has no usable prefix. An IPv6 or malformed address is fired at alone
// rather than lumped in with every other unparseable one, which would look like a
// farm that is not there.
func slash24Key(t Target) string {
	if t.Slash24 == "" {
		return "node:" + t.Node.ID
	}
	return t.Slash24
}

// dueQuiz picks the nodes whose quiz of this round has come due: not yet asked in
// the round, and past the moment drawn for their /24.
//
// asked is counted from the database, so a prober that restarted mid-round does not
// ask a node it already asked.
//
// The longest-waiting come first. A round only takes so many /24s (Config.Batch),
// and when more are due than that the rest wait for the next tick, or for good if
// the window closes. Taken in the order of the moments drawn, the ones left over
// are different nodes every slot; taken in the order of their names, they would be
// the same nodes every slot.
func (p *Prober) dueQuiz(now time.Time, round int, asked map[string]int) []Target {
	if round < 1 || round > SampleRounds {
		return nil
	}
	start, win := p.sampleClock()
	w := win[round-1]
	type waiting struct {
		t  Target
		at time.Time
	}
	var due []waiting
	for _, t := range p.targets {
		if asked[t.Node.ID] > 0 {
			continue
		}
		// A group with references is checked by them (refcheck.go), and an
		// unlisted GPU is not checked at all: only quiz groups get the quiz.
		if _, mode := p.modeOf(nodeAddressOf(t.Node.ID)); mode != ModeQuiz {
			continue
		}
		at := start.Add(w[0]).Add(p.sampleOffset("quiz", round, slash24Key(t), w[1]-w[0]))
		if now.Before(at) {
			continue
		}
		due = append(due, waiting{t, at})
	}
	sort.SliceStable(due, func(i, j int) bool {
		if !due[i].at.Equal(due[j].at) {
			return due[i].at.Before(due[j].at)
		}
		return slash24Key(due[i].t) < slash24Key(due[j].t)
	})
	out := make([]Target, len(due))
	for i, d := range due {
		out[i] = d.t
	}
	return out
}

// groupInOrder buckets targets by /24 and keeps the order in which each /24 first
// appears, so the groups come out in the order dueQuiz put them. The group is the
// firing unit, as in groupBySlash24, which sorts by name instead.
func groupInOrder(targets []Target) [][]Target {
	index := map[string]int{}
	var out [][]Target
	for _, t := range targets {
		k := slash24Key(t)
		i, ok := index[k]
		if !ok {
			i = len(out)
			index[k] = i
			out = append(out, nil)
		}
		out[i] = append(out[i], t)
	}
	return out
}

// claimTextNodes marks every text node as taken for the slot in the shot counts the
// picture track reads.
//
// One node is one node whichever engine answers, and its slot yields one ticket
// either way, so a machine running both a text and a picture service must be
// checked on one track only. That used to follow from the text shot going out first,
// as soon as the window opened. The quiz is now asked at a random moment, so the
// picture track would get there first, and a node keeps the first ticket it is
// handed for a slot: the text ticket, which may carry a model budget, would arrive
// later and be dropped as a duplicate. The text track keeps the node.
func (p *Prober) claimTextNodes(counts map[string]int) {
	for _, t := range p.targets {
		counts[t.Node.ID]++
	}
}
