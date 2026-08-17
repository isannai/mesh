package probe

// schedule.go — when each node's next shot is due.
//
// A node earns its five shots by BEING UP, not by being lucky enough to be
// picked. The n-th shot becomes due once the node has been present for the n-th
// threshold, counted across the whole day:
//
//	3h → 1st   6h → 2nd   9h → 3rd   12h → 4th   15h → 5th
//
// That is the whole point of the faucet: five randomly scattered shots would
// only prove "this node was alive five times", while thresholds that keep
// climbing prove "this node was there for most of the day".
//
// WHY NOT `connected_at` DIRECTLY
//
// The RV reports when the node's current control connection opened, and that
// resets on every reconnect. Home connections drop — ISP resets, DHCP renewals,
// a laptop sleeping — and a node that reconnects every 90 minutes would never
// cross even the first threshold and would earn NOTHING, forever. Since this is
// a network of self-hosted machines, that would exclude a large share of the
// people it exists for.
//
// So continuity is measured by the prober's OWN hourly observations instead:
// a node keeps its anchor as long as it shows up in each successive poll. The
// hourly granularity is the tolerance — a three-minute blip between polls is
// invisible and forgiven, while a genuine hour-long absence resets the anchor.

import (
	"sort"
	"time"
)

// DefaultSchedule is the uptime each shot requires.
//
// A flat three-hour step, chosen over the widening 1/3/5/8/13 ladder it
// replaced. Two knobs live in the list and they were decided separately:
//
//	the FIRST entry   decides whether an unstable node earns anything at all.
//	the LAST entry    decides how much of a day earns the full five: 15h is
//	                  most of a waking day, and leaves room to fall short.
//
// 🔴 THE FIRST ENTRY COSTS SOMETHING, DELIBERATELY. At one hour a machine that
// is only ever on for a couple of hours still earned a ticket; at three it
// earns nothing. That is the accepted price of a rule that reads as one
// sentence — "every three hours, five a day" — instead of a paragraph
// explaining why the gaps widen. Uniform spacing is also what makes the point
// curve legible: every ticket stands for the same three hours.
//
// The length of this list IS the daily cap, and PointCurve must have an entry
// for each.
var DefaultSchedule = []time.Duration{
	3 * time.Hour,
	6 * time.Hour,
	9 * time.Hour,
	12 * time.Hour,
	15 * time.Hour,
}

// parseScheduleSec turns configured SECONDS into durations, falling back to the
// default when unset.
//
// Seconds rather than hours because the unit has to serve two jobs. In
// production the thresholds are hours, but testing means asking "does a shot go
// out at all", and expressing ten seconds in hours is 0.00277 — a number nobody
// can read and everybody mistypes. Seconds make both ends writable:
//
//	production   [3600, 10800, 18000, 28800, 46800]
//	testing      [10, 20, 30]
//
// Sorted ascending because the n-th shot must require more uptime than the
// (n-1)-th — an out-of-order list would make a later shot due before an earlier
// one and the counter would run backwards.
//
// Non-positive entries are dropped rather than honoured: a zero threshold means
// "due the instant the node is first seen", which is not a statement about
// uptime at all. If that leaves nothing, the default stands.
func parseScheduleSec(sec []float64) []time.Duration {
	if len(sec) == 0 {
		return DefaultSchedule
	}
	out := make([]time.Duration, 0, len(sec))
	for _, s := range sec {
		if s <= 0 {
			continue
		}
		out = append(out, time.Duration(s*float64(time.Second)))
	}
	if len(out) == 0 {
		return DefaultSchedule
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// hoursToSec converts the superseded `schedule_hours` spelling. Configs are
// already deployed with it, and dropping it would silently reset an operator's
// ladder to the default.
func hoursToSec(hours []float64) []float64 {
	out := make([]float64, 0, len(hours))
	for _, h := range hours {
		out = append(out, h*3600)
	}
	return out
}

// observationGap is how long a node may vanish between polls and still be
// counted as having been there the whole time.
//
// Slightly more than the hourly poll so one missed poll is forgiven and two are
// not: a poll can be late (a slow directory fetch, a restarted prober), and
// charging a node for the prober's hiccup would be measuring the wrong machine.
const observationGap = 100 * time.Minute

// presenceFrom totals how long each node has been up today.
//
// 🔴 CUMULATIVE, NOT CONTINUOUS. This used to track an anchor — the start of the
// most recent unbroken run — and any gap longer than observationGap threw the
// whole day away and started the ladder over. A home connection that drops once
// in the afternoon lost every hour it had already earned, and one that drops
// every couple of hours could never reach even the first threshold no matter
// how long the machine was left on. It was measuring the ISP, not the node.
//
// Time present is added up instead. A gap is not counted (the node was away and
// earns nothing for it) but it does not erase what came before either. Eighteen
// hours is still eighteen hours of a twenty-four hour day, so the statement the
// faucet wants — "this machine was there for most of the day" — survives being
// made of pieces.
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

// DueShots reports how many shots a node is owed right now.
//
// It returns the number of thresholds its accumulated presence has passed, so
// the caller compares that against how many shots the node has actually had
// today. Zero means "not yet" — either it has not been up long enough, or it
// has not been seen at all today.
func DueShots(present time.Duration, schedule []time.Duration) int {
	n := 0
	for _, threshold := range schedule {
		if present < threshold {
			break
		}
		n++
	}
	return n
}

// 🔴 THE PROBER DOES NOT SCORE. It counts: how many of the day's five tickets a
// node earned, which is the number of passing shots on record. What a ticket is
// WORTH is decided when the voucher is signed, from that count, by the RV — so
// the curve lives there and nothing here has to be redeployed to change it.
//
// The temptation is to store points alongside each shot "while we know them".
// It buys nothing and costs correctness: a stored figure is a second copy of
// something the rows already say, and the two drift the moment the curve moves.
// The rows are the record.

// dueTargets picks the nodes whose next shot has come due.
//
// This replaces choosing at random: a node is fired at because it earned the
// shot by being up, not because it was drawn. Load spreads on its own, since
// nodes reach each threshold at whatever hour they happened to accumulate it.
func dueTargets(targets []Target, present map[string]time.Duration, shotsToday map[string]int,
	schedule []time.Duration) []Target {

	var out []Target
	for _, t := range targets {
		due := DueShots(present[t.Node.ID], schedule)
		if due > shotsToday[t.Node.ID] {
			out = append(out, t)
		}
	}
	return out
}
