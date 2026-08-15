package probe

import (
	"errors"
	"testing"
	"time"
)

func TestParsePool(t *testing.T) {
	cases := []struct {
		name  string
		items []string
		def   string
		want  []poolEntry
	}{
		{
			// All the spellings an operator might reach for fold to one value,
			// so nothing downstream has to know there was more than one.
			name:  "self spellings fold to this",
			items: []string{"this", "local", "self", ""},
			def:   "llm-api",
			// The empty string is dropped, not folded: an empty item in a list
			// is an editing slip, while an absent LIST is the deliberate
			// "use this node" default and never reaches here.
			want: []poolEntry{
				{Node: "this", Service: "llm-api"},
				{Node: "this", Service: "llm-api"},
				{Node: "this", Service: "llm-api"},
			},
		},
		{
			name:  "default service carries",
			items: []string{"0xabc"},
			def:   "llm-api",
			want:  []poolEntry{{Node: "0xabc", Service: "llm-api"}},
		},
		{
			name:  "slash overrides the service",
			items: []string{"0xabc/llm-api-2", "home"},
			def:   "llm-api",
			want: []poolEntry{
				{Node: "0xabc", Service: "llm-api-2"},
				{Node: "home", Service: "llm-api"},
			},
		},
		{
			name:  "no service anywhere yields nothing to call",
			items: []string{"0xabc"},
			def:   "",
			want:  []poolEntry{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parsePool(c.items, c.def)
			if len(got) != len(c.want) {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("[%d] got %+v, want %+v", i, got[i], c.want[i])
				}
			}
		})
	}
}

func TestPoolRoundRobin(t *testing.T) {
	p := newPool(parsePool([]string{"a", "b", "c"}, "llm-api"))
	now := time.Now()

	var seen []string
	for i := 0; i < 5; i++ {
		e, err := p.Take(now, func(poolEntry) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, e.Node)
	}
	want := []string{"a", "b", "c", "a", "b"}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("order = %v, want %v", seen, want)
		}
	}
}

func TestPoolFailoverAndCooldown(t *testing.T) {
	p := newPool(parsePool([]string{"dead", "alive"}, "llm-api"))
	now := time.Now()
	boom := errors.New("connection refused")

	// First call: "dead" fails, the pass continues and "alive" answers.
	var calls []string
	e, err := p.Take(now, func(pe poolEntry) error {
		calls = append(calls, pe.Node)
		if pe.Node == "dead" {
			return boom
		}
		return nil
	})
	if err != nil || e.Node != "alive" {
		t.Fatalf("got %+v, %v", e, err)
	}
	if len(calls) != 2 || calls[0] != "dead" {
		t.Fatalf("calls = %v; the failing entry should have been tried first", calls)
	}

	// 🔴 The point of the cooldown: "dead" is not knocked on again next round.
	calls = nil
	if _, err := p.Take(now.Add(time.Minute), func(pe poolEntry) error {
		calls = append(calls, pe.Node)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, c := range calls {
		if c == "dead" {
			t.Error("a cooling-down entry was tried again")
		}
	}

	// And it comes back on its own — no operator action, no health probe.
	calls = nil
	if _, err := p.Take(now.Add(poolCooldown+time.Second), func(pe poolEntry) error {
		calls = append(calls, pe.Node)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(calls) == 0 {
		t.Fatal("nothing was tried after the cooldown expired")
	}
}

// Every entry failing is a different outcome from every entry cooling down: the
// first has an error worth logging, the second means nothing was even called.
func TestPoolExhausted(t *testing.T) {
	p := newPool(parsePool([]string{"a", "b"}, "llm-api"))
	now := time.Now()
	boom := errors.New("nope")

	if _, err := p.Take(now, func(poolEntry) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the underlying failure", err)
	}
	// Both are now cooling down.
	calls := 0
	_, err := p.Take(now.Add(time.Minute), func(poolEntry) error { calls++; return nil })
	if err != errAllCoolingDown {
		t.Fatalf("err = %v, want errAllCoolingDown", err)
	}
	if calls != 0 {
		t.Errorf("%d entries were called while all were cooling down", calls)
	}
}

// A pool with nothing in it is a configured state ("[]" = turned off), not an
// error condition to be logged every round.
func TestPoolEmpty(t *testing.T) {
	p := newPool(parsePool(nil, "llm-api"))
	if !p.Empty() {
		t.Error("a pool parsed from nothing is not empty")
	}
	if _, err := p.Take(time.Now(), func(poolEntry) error { return nil }); err != errNoPoolEntries {
		t.Errorf("err = %v, want errNoPoolEntries", err)
	}
	var nilPool *pool
	if !nilPool.Empty() {
		t.Error("a nil pool is not empty")
	}
}
