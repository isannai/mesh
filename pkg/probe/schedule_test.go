package probe

import (
	"testing"
	"time"

	"github.com/isannai/mesh/pkg/rvnodes"
)

func hrs(v ...float64) []time.Duration { return parseSchedule(v) }

// The ladder is the whole point: five scattered shots would prove "alive five
// times", while widening gaps prove "there all day".
func TestDueShots(t *testing.T) {
	sch := hrs(1, 3, 5, 8, 13)
	anchor := time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC)

	cases := []struct{ up, want float64 }{
		{0, 0}, {0.9, 0}, {1, 1}, {2.9, 1}, {3, 2}, {4.9, 2},
		{5, 3}, {7.9, 3}, {8, 4}, {12.9, 4}, {13, 5}, {30, 5},
	}
	for _, c := range cases {
		now := anchor.Add(time.Duration(c.up * float64(time.Hour)))
		if got := DueShots(anchor, now, sch); got != int(c.want) {
			t.Errorf("after %.1fh: due = %d, want %.0f", c.up, got, c.want)
		}
	}

	// Never observed today ⇒ nothing is owed. A node with no anchor is not
	// "due everything", which is what a zero time would mean if compared naively.
	if got := DueShots(time.Time{}, anchor, sch); got != 0 {
		t.Errorf("no anchor: due = %d, want 0", got)
	}
}

// An out-of-order list would make a later shot come due before an earlier one
// and the counter would run backwards.
func TestParseScheduleSortsAndDefaults(t *testing.T) {
	got := parseSchedule([]float64{8, 1, 13, 3, 5})
	want := []time.Duration{time.Hour, 3 * time.Hour, 5 * time.Hour, 8 * time.Hour, 13 * time.Hour}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("schedule = %v, want %v", got, want)
		}
	}
	if len(parseSchedule(nil)) != len(DefaultSchedule) {
		t.Error("an empty list should fall back to the default")
	}
	if len(parseSchedule([]float64{0, -1})) != len(DefaultSchedule) {
		t.Error("a list of non-positive values should fall back to the default")
	}
}

// 🔴 The forgiving part. A home connection drops — ISP resets, DHCP renewals, a
// sleeping laptop — and a node reconnecting every 90 minutes must not be locked
// out forever. Continuity is measured between hourly polls, so a blip that
// falls between them is invisible.
func TestAnchorsForgiveShortGaps(t *testing.T) {
	base := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	obs := []NodeSighting{
		{"a", base},
		{"a", base.Add(time.Hour)},     // the node reconnected in between —
		{"a", base.Add(2 * time.Hour)}, // invisible at this granularity
	}
	got := anchorsFrom(obs)
	if !got["a"].Equal(base) {
		t.Errorf("anchor = %s, want the original %s", got["a"], base)
	}
}

// A genuine absence does reset it: missing two polls is not a blip.
func TestAnchorsResetOnRealAbsence(t *testing.T) {
	base := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	back := base.Add(4 * time.Hour)
	obs := []NodeSighting{
		{"a", base},
		{"a", base.Add(time.Hour)},
		// 01:00 → 04:00: three hours missing, well past observationGap
		{"a", back},
		{"a", back.Add(time.Hour)},
	}
	if got := anchorsFrom(obs)["a"]; !got.Equal(back) {
		t.Errorf("anchor = %s, want the restart at %s", got, back)
	}
}

func TestAnchorsPerNode(t *testing.T) {
	base := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	obs := []NodeSighting{
		{"a", base},
		{"a", base.Add(time.Hour)},
		{"b", base.Add(2 * time.Hour)},
		{"b", base.Add(3 * time.Hour)},
	}
	got := anchorsFrom(obs)
	if !got["a"].Equal(base) || !got["b"].Equal(base.Add(2*time.Hour)) {
		t.Errorf("anchors = %v", got)
	}
	if len(anchorsFrom(nil)) != 0 {
		t.Error("no sightings should give no anchors")
	}
}

// A node is fired at because it EARNED the shot, not because it was drawn.
func TestDueTargets(t *testing.T) {
	sch := hrs(1, 3, 5, 8, 13)
	base := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	now := base.Add(6 * time.Hour) // 3 thresholds crossed

	targets := eligible([]rvnodes.Node{
		node("up", "203.0.113.1:1", "public", true),
		node("fresh", "203.0.113.2:1", "public", true),
		node("done", "203.0.113.3:1", "public", true),
		node("unseen", "203.0.113.4:1", "public", true),
	})
	anchors := map[string]time.Time{
		"up":    base,
		"fresh": now.Add(-30 * time.Minute), // not past the first threshold
		"done":  base,
		// "unseen" has no anchor at all
	}
	shots := map[string]int{"up": 1, "done": 3}

	due := dueTargets(targets, anchors, shots, sch, now)
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
	})
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
