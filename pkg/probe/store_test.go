package probe

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "probe.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// A question must never be asked twice: a repeat is a question whose answer the
// node may already know, which measures memory instead of capability.
func TestTakeQuestionConsumes(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()

	qs := []Question{
		{Category: CatMath, Q: "What is 1 + 1?", Draft: "2", Fewshot: mathFewshot},
		{Category: CatMath, Q: "What is 2 + 2?", Draft: "4", Fewshot: mathFewshot},
	}
	if _, err := s.AddQuestions(qs, now); err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		q, ok, err := s.TakeQuestion(CatMath, now)
		if err != nil || !ok {
			t.Fatalf("take %d: ok=%v err=%v", i, ok, err)
		}
		if seen[q.Q] {
			t.Fatalf("question %q handed out twice", q.Q)
		}
		seen[q.Q] = true
		if len(q.Fewshot) != 2 {
			t.Errorf("few-shot lost in round trip: %+v", q.Fewshot)
		}
	}
	if _, ok, err := s.TakeQuestion(CatMath, now); ok || err != nil {
		t.Errorf("queue should be empty: ok=%v err=%v", ok, err)
	}
}

// 🔴 A model asked twice for capital cities returns France both times — the
// well-known ones are a small set. Without this check the queue fills with
// repeats and a node gets asked something it has already answered, which is
// exactly what consuming a question once exists to prevent.
func TestAddQuestionsSkipsDuplicates(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()

	batch := []Question{
		{Category: CatGeography, Q: "What is the capital of France?", Draft: "Paris"},
		{Category: CatGeography, Q: "What is the capital of Japan?", Draft: "Tokyo"},
	}
	if n, err := s.AddQuestions(batch, now); err != nil || n != 2 {
		t.Fatalf("first batch: added %d, err %v", n, err)
	}

	// The next batch overlaps, as real ones do.
	second := []Question{
		batch[0],
		{Category: CatGeography, Q: "What is the capital of Italy?", Draft: "Rome"},
	}
	n, err := s.AddQuestions(second, now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("added %d, want 1 — the repeat should have been dropped", n)
	}
	if d, _ := s.QueueDepth(); d[CatGeography] != 3 {
		t.Fatalf("queue = %d, want 3 distinct questions", d[CatGeography])
	}

	// 🔴 Consumed questions still count. One asked last week is no fresher than
	// one asked this morning, so the check must not look only at the queue.
	if _, _, err := s.TakeQuestion(CatGeography, now); err != nil {
		t.Fatal(err)
	}
	if n, err := s.AddQuestions(batch, now); err != nil || n != 0 {
		t.Fatalf("added %d after consuming, want 0 — consumed questions must still block", n)
	}

	// The same text in a different category is a different question.
	if n, err := s.AddQuestions([]Question{
		{Category: CatUnits, Q: "What is the capital of France?", Draft: "blue"},
	}, now); err != nil || n != 1 {
		t.Fatalf("added %d across categories, want 1", n)
	}
}

func TestQueueDepth(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	if _, err := s.AddQuestions([]Question{
		{Category: CatMath, Q: "a", Draft: "1"},
		{Category: CatMath, Q: "b", Draft: "2"},
		{Category: CatUnits, Q: "c", Draft: "red"},
	}, now); err != nil {
		t.Fatal(err)
	}
	d, err := s.QueueDepth()
	if err != nil {
		t.Fatal(err)
	}
	if d[CatMath] != 2 || d[CatUnits] != 1 {
		t.Fatalf("depth = %+v", d)
	}
	if _, _, err := s.TakeQuestion(CatMath, now); err != nil {
		t.Fatal(err)
	}
	d, _ = s.QueueDepth()
	if d[CatMath] != 1 {
		t.Errorf("consumed question still counted: %+v", d)
	}
}

// The cap bounds what the prober costs a node, so it counts EVERY attempt —
// counting only successes would let an unreachable node be hammered.
func TestShotCountsIncludeFailures(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	day := dayStart(now)

	for _, outcome := range []string{OutcomeAnswered, OutcomeQueueFull, OutcomeRefused, OutcomeTimeout} {
		if _, err := s.RecordShot(Shot{
			FiredAt: now, NodeID: "n1", NodeAddr: "1.2.3.4:1", Slash24: "1.2.3", Outcome: outcome,
		}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.ShotsToday("n1", day)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Errorf("ShotsToday = %d, want 4 (refusals count too)", n)
	}

	counts, err := s.ShotCountsToday(day)
	if err != nil {
		t.Fatal(err)
	}
	if counts["n1"] != 4 {
		t.Errorf("ShotCountsToday = %+v", counts)
	}
}

// Yesterday's shots must not consume today's quota.
func TestShotCountsAreDaily(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	if _, err := s.RecordShot(Shot{
		FiredAt: now.AddDate(0, 0, -1), NodeID: "n1", NodeAddr: "a", Slash24: "s", Outcome: OutcomeAnswered,
	}); err != nil {
		t.Fatal(err)
	}
	n, err := s.ShotsToday("n1", dayStart(now))
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("yesterday counted against today: %d", n)
	}
}

func TestCompleteShot(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	id, err := s.RecordShot(Shot{
		FiredAt: now, NodeID: "n1", NodeAddr: "a", Slash24: "s",
		JobID: "job-1", Outcome: OutcomeSubmitted,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Before completion it shows up as pending — this is what survives a
	// prober restart mid-flight.
	pend, err := s.PendingShots()
	if err != nil {
		t.Fatal(err)
	}
	if len(pend) != 1 || pend[0].JobID != "job-1" {
		t.Fatalf("pending = %+v", pend)
	}

	if err := s.CompleteShot(id, now.Add(2*time.Second), 2000, 30, 5, "Paris", OutcomeAnswered, VerdictPass); err != nil {
		t.Fatal(err)
	}
	if pend, _ := s.PendingShots(); len(pend) != 0 {
		t.Errorf("completed shot still pending: %+v", pend)
	}
}

// A node protected all morning leaves no shots, so the observation table is the
// only place the answer to "was it public all day" can come from.
func TestObservationsRoundTrip(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	if err := s.RecordObservations([]Observation{
		{NodeID: "n1", AuthMode: "public", Online: true, MaxQueue: 8, Version: "0.1.2"},
		{NodeID: "n2", AuthMode: "protected", Online: true},
	}, now); err != nil {
		t.Fatal(err)
	}

	// Old rows age out; recent ones stay.
	if err := s.RecordObservations([]Observation{{NodeID: "n1", AuthMode: "public"}},
		now.AddDate(0, 0, -60)); err != nil {
		t.Fatal(err)
	}
	n, err := s.PruneObservations(now.AddDate(0, 0, -30))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("pruned %d rows, want 1", n)
	}
}

// Opening an existing database must be a no-op, not an error — the prober is
// restarted by isannd whenever isannd restarts.
func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "probe.db")
	s1, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s1.AddQuestions([]Question{{Category: CatMath, Q: "a", Draft: "1"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	s1.Close()

	s2, err := OpenStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	d, _ := s2.QueueDepth()
	if d[CatMath] != 1 {
		t.Errorf("data lost across reopen: %+v", d)
	}
}
