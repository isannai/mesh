package probe

// schedule.go — when each node's shot is due.
//
// ONE SHOT PER SLOT, FIRED INSIDE THE SLOT.
//
// The slot is the unit everything downstream is denominated in. A ticket's key
// is (prober, node, root) and the root changes only at a slot boundary, so two
// shots inside one slot collapse into a single ticket no matter how well the
// node answered both. Firing more often than once a slot therefore costs the
// node work and returns nothing — the extra shots are invisible by the time
// they become money.
//
// So the schedule is the slot calendar itself. No ladder, no daily cap to keep
// in step with anything: eight slots in a day, one shot each.
//
// # WHY NOT AT THE SLOT BOUNDARY
//
// Both edges are unusable, for different reasons.
//
//	the OPENING   the root has just been replaced. A node that has not yet
//	              taken delivery of the new one rejects the bundle, and a
//	              prober still holding the old one signs the wrong slot.
//	the CLOSING   the verdict has to land while the assignment is still held.
//	              The image track judges a round or two after it fires, and a
//	              picture ordered at the end of a slot is graded after the
//	              membership proof it would need is gone — the ticket is then
//	              dropped, which is what "slot turned over before the verdict"
//	              in the log was.
//
// Half an hour of clearance at each end leaves a two-hour window in a
// three-hour slot. The tail is the one that has already cost tickets, so it is
// sized for the slower track rather than the faster one.
//
// # WHEN INSIDE THE WINDOW
//
// As soon as it opens, in whatever order the round hands them over, and the
// concurrency limits do the spacing (sixteen text groups at a time, four
// pictures). A node not reached in one round is still due in the next, so the
// prober works through the directory rather than trying to place every node on
// a calendar.
//
// This replaced a hashed per-node offset. The hash spread load evenly and was
// reproducible, but it made the schedule unreadable in the small: an operator
// watching one node saw "nothing happened for 97 minutes" and had no way to
// tell that from a broken prober. Sequential firing gives one sentence instead
// — "it starts at half past and works down the list" — and the window is two
// hours wide precisely so working down the list has room.
//
// 🔴 THE WINDOW IS A HARD BOUND, NOT A PREFERENCE. Once the tail is reached the
// remaining nodes are dropped and wait for the next slot. Firing into the tail
// would produce a verdict that lands after the assignment is gone, which is a
// ticket the node cannot be paid for — the node does the work for nothing. A
// prober that routinely runs out of window is one whose group is too large,
// and that is a decision for the RV's `num_of_node`, not something to paper
// over here.

import (
	"time"

	"github.com/isannai/mesh/pkg/faucet"
)

// DefaultFireLead and DefaultFireTail are the dead zones at each end of a slot.
//
// Thirty minutes each, which in a three-hour slot leaves two hours to fire in.
// Expressed in seconds in the config for the same reason the old ladder was:
// production wants half an hour and a smoke test wants ten seconds, and only
// one of those is writable in both units.
const (
	DefaultFireLead = 30 * time.Minute
	DefaultFireTail = 30 * time.Minute
)

// fireWindow returns the offsets from slot start between which a shot may go
// out, as [start, end].
//
// A slot too short to hold both dead zones would leave nothing to fire in and
// the prober would go permanently silent — the failure mode is invisible,
// because "no shots" is also what an unappointed prober looks like. So the
// window collapses to the middle instant instead: still inside the slot, still
// deterministic, and a slot that small is a test configuration anyway.
func fireWindow(slotSec int, lead, tail time.Duration) (time.Duration, time.Duration) {
	if slotSec <= 0 {
		slotSec = faucet.SlotSeconds
	}
	slot := time.Duration(slotSec) * time.Second
	if lead < 0 {
		lead = 0
	}
	if tail < 0 {
		tail = 0
	}
	if lead+tail >= slot {
		mid := slot / 2
		return mid, mid
	}
	return lead, slot - tail
}

// dueTargets picks the nodes whose shot has come due in this slot.
//
// shotsInSlot is counted from the database rather than kept in memory, so a
// prober that restarted mid-slot does not fire a second time at a node it
// already served. It counts EVERY shot including refusals and timeouts: the
// one-per-slot rule bounds what the prober costs a node, and a node that is
// unreachable must not be retried in a loop for being unreachable.
func dueTargets(targets []Target, now time.Time, a Assignment, shotsInSlot map[string]int,
	lead, tail time.Duration) []Target {

	slotSec := a.SlotSec
	if slotSec <= 0 {
		slotSec = faucet.SlotSeconds
	}
	slotStart := time.Unix(faucet.SlotStartAt(a.Epoch, slotSec), 0)
	opens, closes := fireWindow(slotSec, lead, tail)

	// Both edges are checked. Only testing the opening one was a real hole: a
	// node the round could not reach stayed due for the rest of the slot and
	// was eventually fired at inside the tail, which is the exact case the tail
	// exists to prevent.
	if now.Before(slotStart.Add(opens)) || now.After(slotStart.Add(closes)) {
		return nil
	}

	var out []Target
	for _, t := range targets {
		if shotsInSlot[t.Node.ID] > 0 {
			continue
		}
		out = append(out, t)
	}
	return out
}

// presenceFrom totals how long each node has been up today.
//
// 🔴 NOT USED FOR FIRING. The schedule is the slot calendar now; this is kept
// for `probe -report`, where "how much of the day was this node up" is a
// question an operator still asks. It stays here rather than moving to
// report.go because the tolerance it encodes belongs with the observation
// cadence, not with the rendering.
//
// CUMULATIVE, NOT CONTINUOUS. A gap is not counted (the node was away and earns
// nothing for it) but it does not erase what came before either. Eighteen hours
// is still eighteen hours of a twenty-four hour day, so the statement survives
// being made of pieces.
//
// obs must be ordered by node then time; SightingsToday returns it that way so
// this is a single pass.
func presenceFrom(obs []NodeSighting) map[string]time.Duration {
	out := map[string]time.Duration{}
	var (
		current string
		prev    time.Time
	)
	for _, o := range obs {
		if o.NodeID != current {
			// First sighting of this node. It carries no span of its own: the
			// node was seen at an instant, and time only accrues between two
			// sightings.
			current, prev = o.NodeID, o.SeenAt
			if _, ok := out[current]; !ok {
				out[current] = 0
			}
			continue
		}
		if gap := o.SeenAt.Sub(prev); gap <= observationGap {
			out[current] += gap
		}
		prev = o.SeenAt
	}
	return out
}

// observationGap is how long a node may vanish between polls and still be
// counted as having been there the whole time.
//
// Sized against the refresh cadence: at a half-hour poll one MISSED poll leaves
// a one-hour gap and two leave ninety minutes, so seventy forgives the first
// and charges for the second. A poll can be late (a slow directory fetch, a
// restarted prober) and charging a node for the prober's hiccup would be
// measuring the wrong machine.
const observationGap = 70 * time.Minute

// slotsPerDay is how many slots a day holds, which is also the most tickets a
// node can earn from one prober in a day.
//
// Derived rather than configured: the slot length already decides it, and a
// second number saying the same thing is one that can disagree.
func slotsPerDay(slotSec int) int {
	if slotSec <= 0 {
		slotSec = faucet.SlotSeconds
	}
	if n := 86400 / slotSec; n > 0 {
		return n
	}
	return 1
}
