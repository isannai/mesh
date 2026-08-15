package probe

// target.go — who gets fired at, and when.
//
// Two clocks, not one:
//
//	hourly    refresh the directory from the RV. Node membership changes slowly
//	          and every fetch costs the RV a round trip.
//	minutely  pick a group and fire. Spreading shots across the day is what
//	          makes them evidence of being CONTINUOUSLY online rather than of
//	          having been alive once.
//
// A node is fired at at most `daily cap` times per day. The cap exists to bound
// what the prober costs a node, not to measure the node — so it counts every
// attempt, including ones the node refused.

import (
	"sort"
	"strings"
	"time"

	"github.com/isannai/mesh/pkg/rvnodes"
)

// Target is one node selected for a shot, with the service to aim at.
type Target struct {
	Node    rvnodes.Node
	Service rvnodes.Service
	Slash24 string
}

// eligible filters the directory down to nodes worth firing at.
//
// Two conditions on the node itself, and deliberately no more:
//
//   - public: a protected node rejects an anonymous probe, so firing at one
//     produces a refusal that says nothing about the node.
//   - a ready text service: the shot needs somewhere to land.
//
// `max_queue >= 5` is NOT checked. The floor is enforced at the source
// (pkg/stationwire MinMaxQueue), so any node built from this tree already
// satisfies it, and re-checking here would only reject nodes for advertising
// honestly. The value is recorded instead.
//
// `exclude` drops the prober's own node — see excluded.
func eligible(nodes []rvnodes.Node, exclude map[string]bool) []Target {
	var out []Target
	for _, n := range nodes {
		if exclude[strings.ToLower(nodeAddressOf(n.ID))] {
			continue
		}
		if !n.IsPublic() {
			continue
		}
		svc, ok := n.TextService()
		if !ok {
			continue
		}
		out = append(out, Target{Node: n, Service: svc, Slash24: n.Slash24()})
	}
	return out
}

// excluded is the set of node addresses the prober must not fire at.
//
// Only itself. A prober that fires at its own node signs tickets for a machine
// it controls, and no reading of the faucet makes that a measurement.
//
// The nodes that WRITE the questions are deliberately NOT excluded. A writer
// knows the model-written categories in advance, but arithmetic is generated
// here, fresh per shot, and is half the mix — so an allied node still has to
// answer something it could not have prepared. On a small network the ally may
// also be the only public node there is, and excluding it would leave the
// prober with nothing to do.
//
// Keyed on the bare address so "S:0xAB…", "0xab…" and a favorite alias's
// resolved id all collapse to one key.
func excluded(selfID string) map[string]bool {
	out := map[string]bool{}
	if a := strings.ToLower(nodeAddressOf(selfID)); a != "" {
		out[a] = true
	}
	return out
}

// selfIfExcluded returns the self id to exclude, or "" when the operator has
// asked for self-firing. Separated from excluded() so the flag is read in one
// place and the exclusion rule itself stays unconditional.
func selfIfExcluded(cfg Config, selfID string) string {
	if cfg.FireAtSelf {
		return ""
	}
	return selfID
}

// nodeAddressOf strips a role prefix ("S:0xab…" → "0xab…"). isannd does the
// same thing on the way out (nodeIDAddress); the prefix names a role, not an
// identity, so it must not make two spellings of one node look like two nodes.
func nodeAddressOf(id string) string {
	id = strings.TrimSpace(id)
	if i := strings.Index(id, ":"); i >= 0 {
		id = id[i+1:]
	}
	return strings.TrimSpace(id)
}

// groupBySlash24 buckets the due targets by /24.
//
// 🔴 The group is the firing unit. Nodes behind one connection must be asked at
// the SAME MOMENT: a farm of registrations sharing a single GPU answers them
// fine one at a time and fails them all at once. Firing them serially would
// serialise exactly the case the burst exists to expose.
//
// A node with no usable prefix gets a bucket of its own, so an IPv6 or
// malformed address is fired at alone rather than lumped in with every other
// unparseable one — which would look like a farm that is not there.
func groupBySlash24(targets []Target) [][]Target {
	buckets := map[string][]Target{}
	for _, t := range targets {
		key := t.Slash24
		if key == "" {
			key = "node:" + t.Node.ID
		}
		buckets[key] = append(buckets[key], t)
	}
	keys := make([]string, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Strings(keys) // stable order so a round can be replayed from logs
	out := make([][]Target, 0, len(keys))
	for _, k := range keys {
		out = append(out, buckets[k])
	}
	return out
}

// dayStart returns midnight UTC for t.
//
// UTC rather than local time so the reset does not move under a prober that
// changes timezone or observes DST — the cap is a fairness rule between nodes
// scattered across the world, and it should tick at the same instant for all
// of them.
func dayStart(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

// observationsOf turns a directory snapshot into rows to keep.
//
// EVERY node is recorded, not just the ones fired at. The eventual rule — "was
// this node public for the whole day" — cannot be answered from shots, because
// a node that was protected all morning has no shots to show for it. Recording
// the whole directory now is what makes that rule implementable later without
// waiting another day for data.
func observationsOf(nodes []rvnodes.Node) []Observation {
	out := make([]Observation, 0, len(nodes))
	for _, n := range nodes {
		o := Observation{
			NodeID:   n.ID,
			AuthMode: n.AuthMode,
			// The directory was fetched with online=true, so everything in it
			// was live as of the RV's own 90-second cutoff.
			Online:  true,
			Version: n.Version,
		}
		if svc, ok := n.TextService(); ok {
			o.MaxQueue = svc.MaxQueue
		}
		out = append(out, o)
	}
	return out
}
