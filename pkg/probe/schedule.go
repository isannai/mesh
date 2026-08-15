package probe

// schedule.go — when each node's next shot is due.
//
// A node earns its five shots by STAYING UP, not by being lucky enough to be
// picked. The n-th shot becomes due once the node has been continuously present
// for the n-th threshold:
//
//	1h → 1st    3h → 2nd    5h → 3rd    8h → 4th    13h → 5th
//
// That is the whole point of the faucet: five randomly scattered shots would
// only prove "this node was alive five times", while widening gaps prove "this
// node was there all day". The gaps grow (1, 2, 2, 3, 5) so the early checks
// weed out a node that was just switched on, and the late ones cover the day.
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
// Two independent knobs live in this list, and they were chosen separately:
//
//	the FIRST entry   decides whether an unstable node earns anything at all.
//	                  One hour rather than two because "0 tickets" and "some
//	                  tickets" are the difference between taking part and not.
//	the LAST entry    decides how late in the day a node can connect and still
//	                  earn the full five: 13h means connecting by 11:00 UTC.
var DefaultSchedule = []time.Duration{
	1 * time.Hour,
	3 * time.Hour,
	5 * time.Hour,
	8 * time.Hour,
	13 * time.Hour,
}

// parseSchedule turns configured hours into durations, falling back to the
// default when unset. Sorted ascending because the n-th shot must require more
// uptime than the (n-1)-th — an out-of-order list would make a later shot due
// before an earlier one and the counter would run backwards.
func parseSchedule(hours []float64) []time.Duration {
	if len(hours) == 0 {
		return DefaultSchedule
	}
	out := make([]time.Duration, 0, len(hours))
	for _, h := range hours {
		if h <= 0 {
			continue
		}
		out = append(out, time.Duration(h*float64(time.Hour)))
	}
	if len(out) == 0 {
		return DefaultSchedule
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// observationGap is how long a node may vanish between polls and still keep its
// anchor.
//
// Slightly more than the hourly poll so one missed poll is forgiven and two are
// not: a poll can be late (a slow directory fetch, a restarted prober), and
// resetting a node's whole day over that would punish the prober's hiccup
// rather than the node's.
const observationGap = 100 * time.Minute

// anchorsFrom derives each node's continuity anchor from observation history.
//
// The anchor is the start of the node's most recent unbroken run of sightings.
// A gap longer than observationGap ends the run, so the anchor moves forward
// and the node starts earning again from the first threshold.
//
// obs must be ordered by node then time; StoreObservationsToday returns it that
// way so this is a single pass.
func anchorsFrom(obs []NodeSighting) map[string]time.Time {
	out := map[string]time.Time{}
	var (
		current string
		anchor  time.Time
		prev    time.Time
	)
	for _, o := range obs {
		if o.NodeID != current {
			current, anchor, prev = o.NodeID, o.SeenAt, o.SeenAt
			out[current] = anchor
			continue
		}
		if o.SeenAt.Sub(prev) > observationGap {
			anchor = o.SeenAt // the run broke; start over
		}
		prev = o.SeenAt
		out[current] = anchor
	}
	return out
}

// DueShots reports how many shots a node is owed right now.
//
// It returns the number of thresholds already crossed, so the caller compares
// it against how many shots the node has actually had today. Zero means "not
// yet" — either the node has not been up long enough, or it has no anchor at
// all (never observed today).
func DueShots(anchor time.Time, now time.Time, schedule []time.Duration) int {
	if anchor.IsZero() {
		return 0
	}
	up := now.Sub(anchor)
	n := 0
	for _, threshold := range schedule {
		if up < threshold {
			break
		}
		n++
	}
	return n
}

// dueTargets picks the nodes whose next shot has come due.
//
// This replaces choosing at random: a node is fired at because it earned the
// shot by staying up, not because it was drawn. Load spreads on its own, since
// nodes anchor at whatever time they happened to connect.
func dueTargets(targets []Target, anchors map[string]time.Time, shotsToday map[string]int,
	schedule []time.Duration, now time.Time) []Target {

	var out []Target
	for _, t := range targets {
		due := DueShots(anchors[t.Node.ID], now, schedule)
		if due > shotsToday[t.Node.ID] {
			out = append(out, t)
		}
	}
	return out
}
