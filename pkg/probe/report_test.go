package probe

import (
	"strings"
	"testing"
	"time"
)

// A text node is asked twice a slot, so the report says which of the two a shot was.
func TestReportShowsTheRound(t *testing.T) {
	s := newTestStore(t)
	id, err := s.RecordShot(Shot{FiredAt: time.Now(), NodeID: "S:0xabcdef0123456789", Outcome: OutcomeSubmitted, Round: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteShot(id, time.Now(), 100, 1, 1, "x", OutcomeAnswered, VerdictPass); err != nil {
		t.Fatal(err)
	}
	out, err := s.Report(5)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "round 2  answered/pass") {
		t.Errorf("the report does not show the round:\n%s", out)
	}
}
