package probe

import (
	"testing"
	"time"

	"github.com/isannai/mesh/pkg/rvnodes"
)

// hrs builds a ladder from HOURS, the unit the tests were written in.
// The config now takes seconds, so convert on the way in.
func hrs(v ...float64) []time.Duration { return parseScheduleSec(hoursToSec(v)) }

// The ladder is the whole point: six scattered shots would prove "alive six
// times", while thresholds that keep climbing prove "there for most of the day".
func TestDueShots(t *testing.T) {
	sch := hrs(3, 6, 9, 12, 15)

	cases := []struct{ up, want float64 }{
		{0, 0}, {2.9, 0}, {3, 1}, {5.9, 1}, {6, 2}, {8.9, 2},
		{9, 3}, {11.9, 3}, {12, 4}, {15, 5}, {30, 5},
	}
	for _, c := range cases {
		present := time.Duration(c.up * float64(time.Hour))
		if got := DueShots(present, sch); got != int(c.want) {
			t.Errorf("after %.1fh present: due = %d, want %.0f", c.up, got, c.want)
		}
	}

	// Never seen today ⇒ nothing is owed. Zero must not read as "due
	// everything", which is what a naive comparison against an empty value gives.
	if got := DueShots(0, sch); got != 0 {
		t.Errorf("never seen: due = %d, want 0", got)
	}
}

// An out-of-order list would make a later shot come due before an earlier one
// and the counter would run backwards.
func TestParseScheduleSortsAndDefaults(t *testing.T) {
	got := parseScheduleSec(hoursToSec([]float64{8, 1, 13, 3, 5}))
	want := []time.Duration{time.Hour, 3 * time.Hour, 5 * time.Hour, 8 * time.Hour, 13 * time.Hour}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("schedule = %v, want %v", got, want)
		}
	}
	if len(parseScheduleSec(nil)) != len(DefaultSchedule) {
		t.Error("an empty list should fall back to the default")
	}
	if len(parseScheduleSec([]float64{0, -1})) != len(DefaultSchedule) {
		t.Error("a list of non-positive values should fall back to the default")
	}
}

// 🔴 The forgiving part. A home connection drops — ISP resets, DHCP renewals, a
// sleeping laptop — and a node reconnecting every 90 minutes must not be locked
// out. Presence is measured between hourly polls, so a blip that falls between
// them is invisible.
func TestPresenceForgivesShortGaps(t *testing.T) {
	base := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	obs := []NodeSighting{
		{"a", base},
		{"a", base.Add(time.Hour)},     // the node reconnected in between —
		{"a", base.Add(2 * time.Hour)}, // invisible at this granularity
	}
	if got := presenceFrom(obs)["a"]; got != 2*time.Hour {
		t.Errorf("present = %s, want the full 2h", got)
	}
}

// 🔴 THE CHANGE FROM CONTINUOUS TO CUMULATIVE. A real absence is not counted,
// but it no longer erases what came before. Under the old anchor rule this node
// would have been back to zero and every hour of the morning thrown away — the
// ISP being measured instead of the machine.
func TestPresenceSurvivesARealAbsence(t *testing.T) {
	base := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	back := base.Add(5 * time.Hour)
	obs := []NodeSighting{
		{"a", base},
		{"a", base.Add(time.Hour)},
		{"a", base.Add(2 * time.Hour)}, // 2h earned here
		// 02:00 → 05:00: three hours away, well past observationGap
		{"a", back},
		{"a", back.Add(time.Hour)}, // 1h more
	}
	if got := presenceFrom(obs)["a"]; got != 3*time.Hour {
		t.Errorf("present = %s, want 2h + 1h = 3h (the 3h gap uncounted, "+
			"the morning NOT erased)", got)
	}
}

// The case that started this: four tickets earned, a two-hour break, then three
// hours back. Cumulatively that is 15h and the fifth ticket is owed.
//
// 🔴 Under the old anchor rule the break reset everything, so those first twelve
// hours were gone and the fifth ticket was out of reach for the rest of the day.
func TestPresenceAcrossABreakStillReachesTheTop(t *testing.T) {
	sch := hrs(3, 6, 9, 12, 15)
	base := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)

	var obs []NodeSighting
	for h := 0; h <= 12; h++ { // 12h present
		obs = append(obs, NodeSighting{"a", base.Add(time.Duration(h) * time.Hour)})
	}
	if got := DueShots(presenceFrom(obs)["a"], sch); got != 4 {
		t.Fatalf("after 12h: due = %d, want 4", got)
	}
	back := base.Add(14 * time.Hour) // two hours away
	for h := 0; h <= 3; h++ {
		obs = append(obs, NodeSighting{"a", back.Add(time.Duration(h) * time.Hour)})
	}
	if got := presenceFrom(obs)["a"]; got != 15*time.Hour {
		t.Fatalf("present = %s, want 15h", got)
	}
	if got := DueShots(presenceFrom(obs)["a"], sch); got != 5 {
		t.Errorf("after the break: due = %d, want 5 — the break costs its own "+
			"two hours and nothing more", got)
	}
}

func TestPresencePerNode(t *testing.T) {
	base := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	obs := []NodeSighting{
		{"a", base},
		{"a", base.Add(time.Hour)},
		{"b", base.Add(2 * time.Hour)},
		{"b", base.Add(3 * time.Hour)},
		{"b", base.Add(4 * time.Hour)},
	}
	got := presenceFrom(obs)
	if got["a"] != time.Hour || got["b"] != 2*time.Hour {
		t.Errorf("presence = %v", got)
	}
	// Seen once and only once: an instant, not a span.
	if got := presenceFrom([]NodeSighting{{"c", base}})["c"]; got != 0 {
		t.Errorf("a single sighting = %s, want 0", got)
	}
	if len(presenceFrom(nil)) != 0 {
		t.Error("no sightings should give no presence")
	}
}

// A node is fired at because it EARNED the shot, not because it was drawn.
func TestDueTargets(t *testing.T) {
	sch := hrs(3, 6, 9, 12, 15)

	targets := eligible([]rvnodes.Node{
		node("up", "203.0.113.1:1", "public", true),
		node("fresh", "203.0.113.2:1", "public", true),
		node("done", "203.0.113.3:1", "public", true),
		node("unseen", "203.0.113.4:1", "public", true),
	}, nil)
	present := map[string]time.Duration{
		"up":    7 * time.Hour,    // 2 thresholds crossed, 1 shot taken
		"fresh": 30 * time.Minute, // not past the first threshold
		"done":  7 * time.Hour,    // 2 crossed, already had 3 (config changed under it)
		// "unseen" has no presence at all
	}
	shots := map[string]int{"up": 1, "done": 3}

	due := dueTargets(targets, present, shots, sch)
	if len(due) != 1 || due[0].Node.ID != "up" {
		t.Fatalf("due = %+v, want only the node owed a shot", due)
	}
}

// Firing a /24 serially lets a farm sharing one GPU answer each in turn, so the
// group has to stay whole.
func TestGroupBySlash24(t *testing.T) {
	targets := eligible([]rvnodes.Node{
		node("a", "203.0.113.1:1", "public", true),
		node("b", "203.0.113.2:1", "public", true),
		node("c", "198.51.100.1:1", "public", true),
		node("d", "garbage", "public", true),
	}, nil)
	groups := groupBySlash24(targets)
	if len(groups) != 3 {
		t.Fatalf("want 3 groups (two /24s + one unparseable), got %d: %+v", len(groups), groups)
	}
	sizes := map[int]int{}
	for _, g := range groups {
		sizes[len(g)]++
	}
	if sizes[2] != 1 || sizes[1] != 2 {
		t.Errorf("group sizes = %v, want one of 2 and two of 1", sizes)
	}
}
