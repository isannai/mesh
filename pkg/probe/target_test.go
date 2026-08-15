package probe

import (
	"testing"
	"time"

	"github.com/isannai/mesh/pkg/rvnodes"
)

func node(id, addr, mode string, ready bool) rvnodes.Node {
	return rvnodes.Node{
		ID: id, Addr: addr, AuthMode: mode,
		Services: []rvnodes.Service{{Name: "llm-api", Engine: "llama", ServerReady: ready, MaxQueue: 8}},
	}
}

// Firing at a protected node produces a refusal that says nothing about the
// node, and firing at one with no ready engine has nowhere to land.
func TestEligible(t *testing.T) {
	nodes := []rvnodes.Node{
		node("a", "203.0.113.1:1", "public", true),
		node("b", "203.0.113.2:1", "protected", true),        // rejects anonymous
		node("c", "203.0.113.3:1", "public", false),          // engine not ready
		node("d", "203.0.113.4:1", "", true),                 // empty = public
		{ID: "e", Addr: "203.0.113.5:1", AuthMode: "public"}, // no services at all
	}
	got := eligible(nodes)
	if len(got) != 2 {
		t.Fatalf("want 2 eligible, got %d: %+v", len(got), got)
	}
	ids := map[string]bool{got[0].Node.ID: true, got[1].Node.ID: true}
	if !ids["a"] || !ids["d"] {
		t.Errorf("wrong nodes selected: %v", ids)
	}
	if got[0].Service.Name != "llm-api" || got[0].Slash24 != "203.0.113" {
		t.Errorf("target not populated: %+v", got[0])
	}
}

// UTC so the reset ticks at the same instant for nodes across the world, and
// does not move under DST.
func TestDayStart(t *testing.T) {
	loc := time.FixedZone("KST", 9*3600)
	got := dayStart(time.Date(2026, 8, 15, 3, 0, 0, 0, loc)) // 2026-08-14 18:00 UTC
	want := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("dayStart = %s, want %s", got, want)
	}
	if got.Location() != time.UTC {
		t.Errorf("dayStart must be UTC, got %s", got.Location())
	}
}

// Nodes never fired at must still be observed — "was it public all day" cannot
// be answered from shots, because a node protected all morning has none.
func TestObservationsIncludeIneligible(t *testing.T) {
	nodes := []rvnodes.Node{
		node("a", "203.0.113.1:1", "public", true),
		node("b", "203.0.113.2:1", "protected", true),
	}
	obs := observationsOf(nodes)
	if len(obs) != 2 {
		t.Fatalf("want an observation per node, got %d", len(obs))
	}
	byID := map[string]Observation{}
	for _, o := range obs {
		byID[o.NodeID] = o
	}
	if byID["b"].AuthMode != "protected" {
		t.Errorf("protected node not recorded: %+v", byID["b"])
	}
	if byID["a"].MaxQueue != 8 {
		t.Errorf("max_queue not carried: %+v", byID["a"])
	}
}

// The sharing rule has a hard floor. Fewer questions than the widest group has
// members and the round-robin wraps, putting the SAME question on two nodes in
// the SAME /24 — which is exactly the shortcut a shared GPU needs (generate
// once, answer twice). The sybil burst would stop measuring anything.
func TestQuestionsNeeded(t *testing.T) {
	g := func(sizes ...int) [][]Target {
		var out [][]Target
		for _, n := range sizes {
			out = append(out, make([]Target, n))
		}
		return out
	}

	cases := []struct {
		name   string
		groups [][]Target
		fanout int
		want   int
	}{
		// 20 shots, 5 per question → 4 questions.
		{"fanout divides evenly", g(2, 2, 2, 2, 2, 2, 2, 2, 2, 2), 5, 4},
		// 21 shots → 5 questions (rounds up, or the tail is never asked).
		{"rounds up", g(2, 2, 2, 2, 2, 2, 2, 2, 2, 3), 5, 5},
		// 🔴 One 8-node group: 8 shots / 5 = 2, but 2 questions would repeat
		// inside that group. The widest group wins.
		{"widest group is a floor", g(8), 5, 8},
		{"floor beats the ratio", g(6, 1, 1), 5, 6},
		{"single node", g(1), 5, 1},
		{"no groups", nil, 5, 1},
		{"fanout of 1 means one question per shot", g(3, 3), 1, 6},
		{"bad fanout is clamped", g(2, 2), 0, 4},
	}
	for _, c := range cases {
		if got := questionsNeeded(c.groups, c.fanout); got != c.want {
			t.Errorf("%s: questionsNeeded = %d, want %d", c.name, got, c.want)
		}
	}
}

// With N questions handed out round-robin across a flat shot index, the members
// of one group (consecutive indices) always land on different questions, and a
// question repeats only N shots later — i.e. in another group, another /24.
func TestQuestionAssignmentNeverRepeatsWithinAGroup(t *testing.T) {
	groups := [][]Target{
		make([]Target, 3), make([]Target, 2), make([]Target, 5), make([]Target, 1),
	}
	n := questionsNeeded(groups, 5)

	shot := 0
	for gi, g := range groups {
		seen := map[int]bool{}
		for i := range g {
			q := (shot + i) % n
			if seen[q] {
				t.Fatalf("group %d got question %d twice — same /24, same question", gi, q)
			}
			seen[q] = true
		}
		shot += len(g)
	}
}
