package probe

import (
	"fmt"
	"testing"
	"time"

	"github.com/isannai/mesh/pkg/faucet"
)

// The three sampling windows are the first half hour of the firing window, the last,
// and one centred between: 0:30 to 1:00, 1:15 to 1:45 and 2:00 to 2:30 of a three
// hour slot.
func TestSampleWindows(t *testing.T) {
	w := sampleWindows(faucet.SlotSeconds, 30*time.Minute, 30*time.Minute)
	want := [SampleRounds][2]time.Duration{
		{30 * time.Minute, time.Hour},
		{75 * time.Minute, 105 * time.Minute},
		{2 * time.Hour, 2*time.Hour + 30*time.Minute},
	}
	if w != want {
		t.Errorf("windows = %v, want %v", w, want)
	}
	// A firing window too short for three half hours gives each a third, side by
	// side and never overlapping.
	short := sampleWindows(3600, 10*time.Minute, 10*time.Minute) // 10m .. 50m
	third := 40 * time.Minute / SampleRounds
	for i, win := range short {
		if win[1]-win[0] != third {
			t.Errorf("short window %d is %v long, want %v: %v", i+1, win[1]-win[0], third, short)
		}
		if i > 0 && win[0] < short[i-1][1] {
			t.Errorf("short window %d starts before window %d ends: %v", i+1, i, short)
		}
	}
	if short[0][0] != 10*time.Minute || short[SampleRounds-1][1] != 50*time.Minute {
		t.Errorf("short windows do not span the firing window: %v", short)
	}

	p := compareProber(t, 3)
	start := compareSlotStart()
	for _, c := range []struct {
		at    time.Duration
		round int
	}{
		{0, 0}, {29 * time.Minute, 0}, {30 * time.Minute, 1}, {59*time.Minute + 59*time.Second, 1},
		{time.Hour, 0}, {74 * time.Minute, 0}, {75 * time.Minute, 2}, {90 * time.Minute, 2},
		{104*time.Minute + 59*time.Second, 2}, {105 * time.Minute, 0}, {119 * time.Minute, 0},
		{2 * time.Hour, 3}, {2*time.Hour + 29*time.Minute, 3},
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
