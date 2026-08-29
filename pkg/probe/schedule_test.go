package probe

import (
	"testing"
	"time"

	"github.com/isannai/mesh/pkg/faucet"
	"github.com/isannai/mesh/pkg/rvnodes"
)

const testEpoch = int64(165553)

func testAssign(slotSec int) Assignment {
	return Assignment{SlotSec: slotSec, Epoch: testEpoch}
}

func slotStartOf(a Assignment) time.Time {
	return time.Unix(faucet.SlotStartAt(a.Epoch, a.SlotSec), 0)
}

// Both edges of a slot are unusable — the opening because the root has just
// been replaced, the closing because a verdict landing after the turnover has
// no membership proof left to be claimed with.
func TestFireWindow(t *testing.T) {
	start, end := fireWindow(10800, 30*time.Minute, 30*time.Minute)
	if start != 30*time.Minute {
		t.Errorf("window opens at %s, want 30m", start)
	}
	if end != 150*time.Minute {
		t.Errorf("window closes at %s, want 2h30m", end)
	}

	// Zero is an operator saying "use the whole slot", and is honoured.
	if s, e := fireWindow(10800, 0, 0); s != 0 || e != 3*time.Hour {
		t.Errorf("open window = [%s %s], want [0 3h]", s, e)
	}
}

// 🔴 A slot too short to hold both dead zones must not leave nothing to fire
// in. Silence is indistinguishable from an unappointed prober, so the window
// collapses to an instant rather than to nothing.
func TestFireWindowCollapsesRatherThanVanishing(t *testing.T) {
	start, end := fireWindow(60, 30*time.Minute, 30*time.Minute)
	if start != end {
		t.Fatalf("window = [%s %s], want a single instant", start, end)
	}
	if start != 30*time.Second {
		t.Errorf("collapsed to %s, want the slot midpoint 30s", start)
	}
	if start < 0 || start > time.Minute {
		t.Errorf("collapsed to %s, which is outside the slot", start)
	}
}

// Firing starts when the window opens, not at the slot boundary.
func TestDueTargetsWaitsForTheWindow(t *testing.T) {
	a := testAssign(10800)
	lead, tail := 30*time.Minute, 30*time.Minute
	targets := eligible([]rvnodes.Node{
		node("n", "203.0.113.1:1", "public", true),
	}, nil)
	start := slotStartOf(a)

	if due := dueTargets(targets, start, a, nil, lead, tail); len(due) != 0 {
		t.Errorf("fired at the slot boundary: %+v", due)
	}
	if due := dueTargets(targets, start.Add(29*time.Minute), a, nil, lead, tail); len(due) != 0 {
		t.Errorf("fired inside the opening dead zone: %+v", due)
	}
	if due := dueTargets(targets, start.Add(30*time.Minute), a, nil, lead, tail); len(due) != 1 {
		t.Error("not due the moment the window opened")
	}
}

// 🔴 THE HOLE THIS CLOSES. Only the opening edge used to be checked, so a node
// the round could not reach stayed due for the rest of the slot and was
// eventually fired at inside the tail — the exact case the tail exists to
// prevent, and the one that produced "slot turned over before the verdict".
func TestDueTargetsStopsAtTheTail(t *testing.T) {
	a := testAssign(10800)
	lead, tail := 30*time.Minute, 30*time.Minute
	targets := eligible([]rvnodes.Node{
		node("n", "203.0.113.1:1", "public", true),
	}, nil)
	start := slotStartOf(a)

	if due := dueTargets(targets, start.Add(150*time.Minute), a, nil, lead, tail); len(due) != 1 {
		t.Error("the last minute of the window should still fire")
	}
	if due := dueTargets(targets, start.Add(151*time.Minute), a, nil, lead, tail); len(due) != 0 {
		t.Errorf("fired inside the closing dead zone: %+v", due)
	}
	if due := dueTargets(targets, start.Add(179*time.Minute), a, nil, lead, tail); len(due) != 0 {
		t.Errorf("fired at the very end of the slot: %+v", due)
	}
}

// 🔴 A CONCURRENCY LIMIT IS NOT A BATCH LIMIT. Thirty-two groups used to go out
// in one round, sixteen at a time, and the round just took twice as long — the
// whole directory answering inside one minute.
func TestTakeBatch(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}
	if got := takeBatch(items, 2); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("takeBatch(5, 2) = %v, want the first two in order", got)
	}
	if got := takeBatch(items, 5); len(got) != 5 {
		t.Errorf("an exact fit should pass through whole, got %v", got)
	}
	if got := takeBatch(items, 99); len(got) != 5 {
		t.Errorf("a batch larger than the input should pass through whole, got %v", got)
	}
	// Zero would silently stop the prober, which reads as a broken schedule.
	if got := takeBatch(items, 0); len(got) != 5 {
		t.Errorf("a non-positive batch should not swallow the round, got %v", got)
	}
	if got := takeBatch([]int(nil), 3); len(got) != 0 {
		t.Errorf("nothing in, nothing out, got %v", got)
	}
}

// One slot yields one ticket, so it may yield only one shot. The count comes
// from the database, so a prober that restarted mid-slot still knows.
func TestDueTargetsFiresOncePerSlot(t *testing.T) {
	a := testAssign(10800)
	lead, tail := 30*time.Minute, 30*time.Minute
	targets := eligible([]rvnodes.Node{
		node("ready", "203.0.113.1:1", "public", true),
		node("done", "203.0.113.2:1", "public", true),
	}, nil)

	// Past every offset in the window, so only the shot count separates them.
	now := slotStartOf(a).Add(150 * time.Minute)
	due := dueTargets(targets, now, a, map[string]int{"done": 1}, lead, tail)
	if len(due) != 1 || due[0].Node.ID != "ready" {
		t.Fatalf("due = %+v, want only the node not yet served this slot", due)
	}
}

// The denominator on the ticket log line, and the most a node can earn from one
// prober in a day. Derived from the slot length so the two cannot disagree.
func TestSlotsPerDay(t *testing.T) {
	if got := slotsPerDay(10800); got != 8 {
		t.Errorf("slotsPerDay(3h) = %d, want 8", got)
	}
	if got := slotsPerDay(0); got != 8 {
		t.Errorf("slotsPerDay(unset) = %d, want the default 8", got)
	}
	if got := slotsPerDay(200000); got != 1 {
		t.Errorf("slotsPerDay(longer than a day) = %d, want 1", got)
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
