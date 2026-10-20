package probe

// sample.go: the moments of a slot at which a node is asked.
//
// A node is asked SampleRounds times in every slot, once in each SAMPLING WINDOW,
// at a random moment inside it. The windows sit inside the firing window
// (schedule.go): the first opens it, the last closes it, and the others are spread
// evenly between, so in a three hour slot with the default half hour of clearance
// at each end:
//
//	round 1   0:30 to 1:00
//	round 2   1:15 to 1:45
//	round 3   2:00 to 2:30
//
// Samples this far apart say the node was up and answering across the slot, not
// only once. A random moment inside each window means a node cannot know when it
// will be asked.
//
// The quiz (quiz.go) follows this clock. A ticket is settled once, a minute after
// the last window closes (settle.go), and needs SamplePassNeeded of the rounds
// passed.

import (
	"fmt"
	"time"

	"github.com/isannai/mesh/pkg/faucet"
)

const (
	// SampleRounds is how many times a node is asked in a slot.
	SampleRounds = 3

	// DefaultSamplePassMin is how many of those rounds a node has to pass for a
	// ticket when the config does not say (Config.SamplePassMin).
	DefaultSamplePassMin = 2

	// sampleWindow is how long each sampling window is.
	sampleWindow = 30 * time.Minute

	// sampleSettleMargin is how long after a window closes its round is judged. Every
	// ask is made inside the window and the prober waits for all of them before
	// its next tick, so by then they are all in.
	sampleSettleMargin = time.Minute
)

// sampleWindows returns the sampling windows as offsets from the slot start. Each
// is sampleWindow long, or a third of the firing window if that is shorter, so
// that they never overlap: the first starts the firing window, the last ends it, and
// the rest are centred at equal steps between.
func sampleWindows(slotSec int, lead, tail time.Duration) [SampleRounds][2]time.Duration {
	opens, closes := fireWindow(slotSec, lead, tail)
	span := closes - opens
	w := sampleWindow
	if limit := span / SampleRounds; w > limit {
		w = limit
	}
	var out [SampleRounds][2]time.Duration
	last := SampleRounds - 1
	for i := range out {
		switch i {
		case 0:
			out[i] = [2]time.Duration{opens, opens + w}
		case last:
			out[i] = [2]time.Duration{closes - w, closes}
		default:
			centre := opens + span*time.Duration(i)/time.Duration(last)
			out[i] = [2]time.Duration{centre - w/2, centre - w/2 + w}
		}
	}
	return out
}

// sampleClock is this slot's start and sampling windows.
func (p *Prober) sampleClock() (time.Time, [SampleRounds][2]time.Duration) {
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

// sampleRoundAt is the sampling round whose window holds now: 1 to SampleRounds, or 0 between
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
