package probe

import (
	"os"
	"strings"
	"testing"
	"time"
)

func readSource(name string) (string, error) {
	b, err := os.ReadFile(name)
	return string(b), err
}

func containsLine(src, want string) bool { return strings.Contains(src, want) }

// 🔴 The fallback must be a state, not a one-way door.
//
// It used to `return` once genFailures hit the limit, and the counter only
// clears on a success — which needs an attempt. A prober that met one bad
// minute stayed on arithmetic until somebody restarted it, and the bad minute
// was usually its own boot: it refills before isannd has dialled the writer's
// node, so the first call meets an unfinished hole punch.
func TestFallbackIsNotPermanent(t *testing.T) {
	src, err := readSource("probe.go")
	if err != nil {
		t.Fatal(err)
	}
	if containsLine(src, "return // an engine that was not there an hour ago is still not there") {
		t.Error("refill still gives up permanently on genFailures")
	}
	// The counter may gate the log; it must not gate the attempt.
	if containsLine(src, "if p.genFailures >= genFailureLimit {\n\t\treturn") {
		t.Error("genFailures still short-circuits the refill")
	}
}

// A writer that raced the hole punch is back in seconds; one that is switched
// off is not coming back this hour. The same retry has to serve both, so the
// gap doubles from a minute up to the refresh period — never past it, or a dead
// writer would cost more than it did before any of this existed.
func TestGenBackoffDoublesAndCaps(t *testing.T) {
	const refresh = 8 * time.Minute
	var got []time.Duration
	d := time.Duration(0)
	for i := 0; i < 6; i++ {
		d = nextGenBackoff(d, refresh)
		got = append(got, d)
	}
	want := []time.Duration{
		1 * time.Minute, 2 * time.Minute, 4 * time.Minute,
		8 * time.Minute, 8 * time.Minute, 8 * time.Minute,
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("backoff = %v, want %v", got, want)
		}
	}
	// A refresh shorter than the first gap must not make the retry faster than
	// the fire tick it rides on.
	if d := nextGenBackoff(0, time.Second); d != genRetryEvery {
		t.Errorf("with a 1s refresh the first gap = %v, want %v", d, genRetryEvery)
	}
}
