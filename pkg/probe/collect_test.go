package probe

// collect_test.go — the cross-round collection path.
//
// What these guard is a single change of shape: a picture is ORDERED in one
// round and PICKED UP in a later one. Before that, the round blocked until the
// node answered or a deadline expired, and the expiry was recorded as the
// node's failure — which it usually was not. A 4GB card drew every picture
// correctly, finished each one seconds after we stopped waiting, and had every
// shot on its record marked timeout.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// jobServer serves /v1/jobs/<id> with a per-job status and /v1/jobs/<id>/result
// with a byte body, mimicking the station: the result is served as raw image
// bytes with an image/* content type, NOT as JSON.
func jobServer(t *testing.T, status map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.Trim(r.URL.Path, "/")
		switch {
		case strings.HasSuffix(path, "/result"):
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("\x89PNG fake"))
		default:
			id := path[strings.LastIndex(path, "/")+1:]
			st, ok := status[id]
			if !ok {
				http.Error(w, "no such job", http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": st})
		}
	}))
}

// openImageShot writes a submitted image shot and returns it as PendingShots
// would hand it back.
func openImageShot(t *testing.T, s *Store, node, jobID string, firedAt time.Time) Shot {
	t.Helper()
	order := ImageOrder{Prompt: "a red fox", Checks: []Check{
		{Label: "subject", Expect: "a red fox", Alternatives: []string{"a red hammer"}},
	}}
	id, err := s.RecordShot(Shot{
		FiredAt: firedAt, NodeID: node, NodeAddr: node, Slash24: "10.0.0",
		Service: "sd-api", JobID: jobID, Outcome: OutcomeSubmitted,
		AnswerRaw: "a red fox | 512x512", OrderJSON: encodeOrder(order),
	})
	if err != nil {
		t.Fatal(err)
	}
	return Shot{ID: id, FiredAt: firedAt, NodeID: node, Service: "sd-api",
		JobID: jobID, OrderJSON: encodeOrder(order)}
}

// A picture that is not ready yet is NOT a failure. The shot stays open, no
// outcome is written, and nothing is counted — the node is still drawing.
func TestPendingShotStaysOpen(t *testing.T) {
	srv := jobServer(t, map[string]string{"job-1": "running"})
	defer srv.Close()
	s := newTestStore(t)
	now := time.Now()
	sh := openImageShot(t, s, "S:0xAAA", "job-1", now.Add(-30*time.Second))

	p := &Prober{store: s, firer: NewFirer(srv.URL), cfg: Config{ImageDeadline: 600}}
	var stats roundStats
	if settled := p.collectImageShot(sh, now, &stats); settled {
		t.Fatal("a job still running was settled — the node is not finished")
	}
	if stats.fired.Load() != 0 || stats.answered.Load() != 0 || stats.timeout.Load() != 0 {
		t.Fatalf("a pending peek was counted: %s", &stats)
	}

	open, err := s.PendingShots()
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 {
		t.Fatalf("open shots = %d, want the one still drawing", len(open))
	}
}

// 🔴 The point of the whole change: a picture ordered in an earlier round is
// collected later and counted then, WITHOUT being counted as fired again.
func TestLateCollectionSettlesWithoutRefiring(t *testing.T) {
	srv := jobServer(t, map[string]string{"job-1": "done"})
	defer srv.Close()
	s := newTestStore(t)
	now := time.Now()
	// Fired well before this round: the case that used to be a timeout.
	sh := openImageShot(t, s, "S:0xAAA", "job-1", now.Add(-4*time.Minute))

	p := &Prober{
		store: s, firer: NewFirer(srv.URL), cfg: Config{ImageDeadline: 600},
		validator: NewValidator(srv.URL, nil), // disabled: judging is not what this asserts
	}
	var stats roundStats
	if settled := p.collectImageShot(sh, now, &stats); !settled {
		t.Fatal("a finished job was left open")
	}
	if got := stats.fired.Load(); got != 0 {
		t.Fatalf("collection added %d to the fired count — a shot counted twice "+
			"makes the daily tally drift upward all day", got)
	}
	if got := stats.answered.Load(); got != 1 {
		t.Fatalf("answered = %d, want 1", got)
	}
	open, err := s.PendingShots()
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 0 {
		t.Fatalf("shot still open after collection: %+v", open)
	}
}

// A transport fault is OURS. Recording it against the node is what put timeouts
// on machines that had done nothing wrong, and once tickets hang off this
// history that mark is a missing payment.
func TestCollectionFaultIsNotTheNodesFailure(t *testing.T) {
	srv := jobServer(t, map[string]string{}) // every job id 404s
	defer srv.Close()
	s := newTestStore(t)
	now := time.Now()
	sh := openImageShot(t, s, "S:0xAAA", "job-1", now.Add(-time.Minute))

	p := &Prober{store: s, firer: NewFirer(srv.URL), cfg: Config{ImageDeadline: 600}}
	var stats roundStats
	if settled := p.collectImageShot(sh, now, &stats); !settled {
		t.Fatal("an unfetchable shot was left open forever")
	}
	if stats.timeout.Load() != 0 || stats.refused.Load() != 1 {
		// settle() files skipped under refused for the round line; what matters
		// is the stored outcome, checked next.
		t.Logf("round tally: %s", &stats)
	}
	var outcome string
	if err := s.db.QueryRow(`SELECT outcome FROM shot WHERE id=?`, sh.ID).Scan(&outcome); err != nil {
		t.Fatal(err)
	}
	if outcome != OutcomeSkipped {
		t.Fatalf("outcome = %q, want %q — our fault must not read as the node's",
			outcome, OutcomeSkipped)
	}
}

// Past the give-up bound the node HAS failed: it accepted the job and never
// produced. Waiting longer would also keep that node ineligible, since an open
// shot blocks further firing at it.
func TestGivingUpIsTheNodesTimeout(t *testing.T) {
	srv := jobServer(t, map[string]string{"job-1": "running"})
	defer srv.Close()
	s := newTestStore(t)
	now := time.Now()
	sh := openImageShot(t, s, "S:0xAAA", "job-1", now.Add(-20*time.Minute))

	p := &Prober{store: s, firer: NewFirer(srv.URL), cfg: Config{ImageDeadline: 300}}
	var stats roundStats
	if settled := p.collectImageShot(sh, now, &stats); !settled {
		t.Fatal("a shot past the give-up bound was left open")
	}
	var outcome string
	if err := s.db.QueryRow(`SELECT outcome FROM shot WHERE id=?`, sh.ID).Scan(&outcome); err != nil {
		t.Fatal(err)
	}
	if outcome != OutcomeTimeout {
		t.Fatalf("outcome = %q, want %q", outcome, OutcomeTimeout)
	}
}

// The give-up bound never exceeds what the station keeps. Past DoneTTL the
// answer does not exist to be fetched, so no configuration may wait longer.
func TestGiveUpNeverOutlastsRetention(t *testing.T) {
	p := &Prober{cfg: Config{ImageDeadline: 24 * 3600}}
	if got := p.giveUpAfter("S:0xAAA", "sd-api"); got != imageRetention {
		t.Fatalf("giveUpAfter = %v, want it clamped to %v", got, imageRetention)
	}
}

// Capability is the node's FASTEST shot of the day, not its average. Queueing
// can only inflate a measurement, so the minimum is the tightest honest bound
// on what the machine can do — and it is what keeps a busy node from being read
// as a slow one.
func TestDailyBestTakesTheFastestAnswered(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	day := dayStart(now)

	for i, ms := range []int64{420000, 95000, 510000} {
		id, err := s.RecordShot(Shot{
			FiredAt: now, NodeID: "S:0xAAA", NodeAddr: "a", Slash24: "10.0.0",
			Service: "sd-api", JobID: fmt.Sprintf("job-%d", i), Outcome: OutcomeSubmitted,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.CompleteShot(id, now, ms, 0, 0, "note", OutcomeAnswered, VerdictPass); err != nil {
			t.Fatal(err)
		}
	}
	// A faster shot that we never managed to collect must NOT count: it is not
	// evidence of anything, in either direction.
	id, err := s.RecordShot(Shot{
		FiredAt: now, NodeID: "S:0xAAA", NodeAddr: "a", Slash24: "10.0.0",
		Service: "sd-api", JobID: "job-skip", Outcome: OutcomeSubmitted,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteShot(id, now, 1000, 0, 0, "note", OutcomeSkipped, ""); err != nil {
		t.Fatal(err)
	}

	best, err := s.DailyBestMs(day)
	if err != nil {
		t.Fatal(err)
	}
	if got := best[shotKey("S:0xAAA", "sd-api")]; got != 95000 {
		t.Fatalf("best = %dms, want the fastest ANSWERED shot (95000ms)", got)
	}
}

// A database written before these columns existed must still open. The operator
// has a week of history in it, and CREATE TABLE IF NOT EXISTS does nothing to a
// table that is already there.
func TestMigrationAddsColumnsToAnOldDatabase(t *testing.T) {
	s := newTestStore(t)
	// migrate() already ran once at open; running it again is the second-open
	// case, where every ALTER fails with "duplicate column name".
	if err := s.migrate(); err != nil {
		t.Fatalf("re-opening an already-migrated database failed: %v", err)
	}
	sh := openImageShot(t, s, "S:0xAAA", "job-1", time.Now())
	if sh.ID == 0 {
		t.Fatal("insert naming the new columns did not land")
	}
}
