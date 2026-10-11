package probe

import (
	"fmt"
	"testing"
	"time"

	"github.com/isannai/mesh/pkg/faucet"
)

// The two sampling windows are the first and the last half hour of the firing
// window: 0:30 to 1:00 and 2:00 to 2:30 of a three hour slot.
func TestSampleWindows(t *testing.T) {
	w := sampleWindows(faucet.SlotSeconds, 30*time.Minute, 30*time.Minute)
	want := [2][2]time.Duration{{30 * time.Minute, time.Hour}, {2 * time.Hour, 2*time.Hour + 30*time.Minute}}
	if w != want {
		t.Errorf("windows = %v, want %v", w, want)
	}
	// A firing window too short for two half hours is split in half.
	short := sampleWindows(3600, 10*time.Minute, 10*time.Minute) // 10m .. 50m
	if short[0][1] != 30*time.Minute || short[1][0] != 30*time.Minute {
		t.Errorf("short windows = %v", short)
	}

	p := compareProber(t, 3)
	start := compareSlotStart()
	for _, c := range []struct {
		at    time.Duration
		round int
	}{
		{0, 0}, {29 * time.Minute, 0}, {30 * time.Minute, 1}, {59*time.Minute + 59*time.Second, 1},
		{time.Hour, 0}, {90 * time.Minute, 0}, {2 * time.Hour, 2}, {2*time.Hour + 29*time.Minute, 2},
		{2*time.Hour + 30*time.Minute, 0}, {2*time.Hour + 45*time.Minute, 0},
	} {
		if got := p.sampleRoundAt(start.Add(c.at)); got != c.round {
			t.Errorf("at %v: round %d, want %d", c.at, got, c.round)
		}
	}
}

// The moment something is asked is random inside the window, stays the same for
// the slot, and stops short of the window's end so nothing falls between two ticks.
func TestSampleOffset(t *testing.T) {
	p := compareProber(t, 3)
	seen := map[time.Duration]bool{}
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("S:0x%040x", i)
		off := p.sampleOffset("compare", 1, id, sampleWindow)
		if off < 0 || off >= sampleWindow-2*time.Minute {
			t.Fatalf("offset %v is outside the window", off)
		}
		if again := p.sampleOffset("compare", 1, id, sampleWindow); again != off {
			t.Fatalf("the offset changed between ticks: %v then %v", off, again)
		}
		seen[off] = true
	}
	if len(seen) < 25 {
		t.Errorf("only %d distinct moments for 50 nodes: not random", len(seen))
	}
	// A new slot draws again.
	p.sampleOffset("compare", 1, "S:0x01", sampleWindow)
	p.assign.Epoch++
	if p.sampleAtEpoch == p.assign.Epoch {
		t.Fatal("test setup")
	}
	p.sampleOffset("compare", 1, "S:0x01", sampleWindow)
	if p.sampleAtEpoch != p.assign.Epoch {
		t.Error("the offsets of the last slot were kept")
	}
}

// The quiz and the sentence request each draw their own moment, and each round
// draws again: being asked early in the first window says nothing about the second.
func TestSampleOffsetKeepsKindsAndRoundsApart(t *testing.T) {
	p := compareProber(t, 3)
	p.sampleOffset("quiz", 1, "10.0.0", sampleWindow)
	p.sampleOffset("compare", 1, "10.0.0", sampleWindow)
	p.sampleOffset("quiz", 2, "10.0.0", sampleWindow)
	if len(p.sampleAt) != 3 {
		t.Errorf("%d moments kept for three different questions: %v", len(p.sampleAt), p.sampleAt)
	}
}

// A window shorter than the two ticks it keeps clear has no span to draw from, so
// the moment is the start of the window rather than a panic.
func TestSampleOffsetInATinyWindow(t *testing.T) {
	p := compareProber(t, 3)
	if off := p.sampleOffset("quiz", 1, "10.0.0", 90*time.Second); off != 0 {
		t.Errorf("offset in a window of 90s = %v, want 0", off)
	}
}
