package probe

// sample.go: the two moments of a slot at which a node is asked.
//
// A node is asked twice in every slot, once in each SAMPLING WINDOW, at a random
// moment inside it. The windows are the first and the last half hour of the
// firing window (schedule.go), so in a three hour slot:
//
//	round 1   0:30 to 1:00
//	round 2   2:00 to 2:30
//
// Two samples this far apart say the node was up and answering at both ends of
// the slot, not only once. A random moment inside each window means a node cannot
// know when it will be asked.
//
// Both kinds of request follow this clock: the quiz (quiz.go) and, for a group of
// nodes running the same model, the sentence request of the model-identity check
// (compare.go). A ticket is settled once, a minute after the second window closes
// (settle.go).

import (
	"fmt"
	"time"

	"github.com/isannai/mesh/pkg/faucet"
)

const (
	// sampleWindow is how long each sampling window is.
	sampleWindow = 30 * time.Minute

	// sampleSettleMargin is how long after a window closes its round is judged. Every
	// ask is made inside the window and the prober waits for all of them before
	// its next tick, so by then they are all in.
	sampleSettleMargin = time.Minute
)

// sampleWindows returns the two sampling windows as offsets from the slot start:
// the first sampleWindow of the firing window and the last. A firing window too
// short for two of them is split in half.
func sampleWindows(slotSec int, lead, tail time.Duration) [2][2]time.Duration {
	opens, closes := fireWindow(slotSec, lead, tail)
	w := sampleWindow
	if half := (closes - opens) / 2; w > half {
		w = half
	}
	return [2][2]time.Duration{{opens, opens + w}, {closes - w, closes}}
}

// sampleClock is this slot's start and sampling windows.
func (p *Prober) sampleClock() (time.Time, [2][2]time.Duration) {
	slotSec := p.assign.SlotSec
	if slotSec <= 0 {
		slotSec = faucet.SlotSeconds
	}
	start := time.Unix(faucet.SlotStartAt(p.assign.Epoch, slotSec), 0)
	return start, sampleWindows(slotSec, p.cfg.FireLead(), p.cfg.FireTail())
}

// sampleMargin is how long after a window closes its round is judged.
func (p *Prober) sampleMargin() time.Duration {
	m := sampleSettleMargin
	if half := p.cfg.FireTail() / 2; half < m {
		m = half
	}
	return m
}

// sampleRoundAt is the sampling round whose window holds now: 1, 2, or 0 between
// the windows and outside them.
func (p *Prober) sampleRoundAt(now time.Time) int {
	start, win := p.sampleClock()
	for i, w := range win {
		if !now.Before(start.Add(w[0])) && now.Before(start.Add(w[1])) {
			return i + 1
		}
	}
	return 0
}

// sampleOffset is the moment inside a round's window at which something is asked.
//
// kind and key say what: a quiz is asked of a /24 (every node behind one
// connection at the same moment, quiz.go), a sentence request of a node. Random,
// so not everyone is asked at one instant and nobody can know when their sample
// comes. It is kept for the slot, so a tick that comes round again before the
// moment does not draw a new one. The span stops short of the window's end by two
// ticks: something due in the last seconds would otherwise fall between two ticks
// and never be asked.
//
// Called from the firing goroutine only, like the generator it draws from.
func (p *Prober) sampleOffset(kind string, round int, key string, window time.Duration) time.Duration {
	if p.sampleAt == nil || p.sampleAtEpoch != p.assign.Epoch {
		p.sampleAt, p.sampleAtEpoch = map[string]time.Duration{}, p.assign.Epoch
	}
	k := fmt.Sprintf("%s|%d|%s", kind, round, key)
	if off, ok := p.sampleAt[k]; ok {
		return off
	}
	span := window - 2*time.Duration(p.cfg.FireIntervalSec)*time.Second
	var off time.Duration
	if span > 0 {
		off = time.Duration(p.rng.Int63n(int64(span)))
	}
	p.sampleAt[k] = off
	return off
}
