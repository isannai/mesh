package queue

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// runJobs sends n jobs one after another through a worker and waits for each.
func runJobs(t *testing.T, q *Queue, n int, process ProcessFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Worker(ctx, process)
	for i := 0; i < n; i++ {
		job, err := q.Submit("/x", nil, http.Header{})
		if err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
		wctx, cn := context.WithTimeout(ctx, 5*time.Second)
		_, err = q.Wait(wctx, job.ID)
		cn()
		if err != nil {
			t.Fatalf("wait %d: %v", i, err)
		}
	}
}

// saved waits until the store holds done answered jobs for service: the file is
// written just after a job is marked finished, not before.
func saved(t *testing.T, s *CounterStore, service string, done int64) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if s.Get(service).Done == done {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("store never reached %d done for %s (has %d)", done, service, s.Get(service).Done)
}

func okAfter(d time.Duration) ProcessFunc {
	return func(ctx context.Context, job *Job) (int, string, []byte, error) {
		time.Sleep(d)
		return 200, "text/plain", []byte("ok"), nil
	}
}

// The average is over the last ten jobs. Ten slow jobs followed by ten fast
// ones leave an average of the fast ones; over twenty it would still carry the
// slow ones.
func TestAvgJobSecIsTheLastTenJobs(t *testing.T) {
	q := New(Config{ServiceName: "llm-api"})
	var n int32
	process := func(ctx context.Context, job *Job) (int, string, []byte, error) {
		if atomic.AddInt32(&n, 1) <= 10 {
			time.Sleep(100 * time.Millisecond)
		} else {
			time.Sleep(5 * time.Millisecond)
		}
		return 200, "text/plain", []byte("ok"), nil
	}
	runJobs(t, q, 20, process)

	q.mu.Lock()
	kept := len(q.durations)
	q.mu.Unlock()
	if kept != avgWindow {
		t.Fatalf("kept %d job times, want %d", kept, avgWindow)
	}
	st := q.Stats()
	if st.TotalJobsDone != 20 {
		t.Errorf("total done = %d, want 20", st.TotalJobsDone)
	}
	if st.AvgJobSec <= 0 || st.AvgJobSec > 0.04 {
		t.Errorf("avg = %.3fs, want the fast jobs only (<0.04s); the slow ten leaked in", st.AvgJobSec)
	}
}

// Counts and the last job times survive a restart: a second queue built from
// the same file starts where the first stopped.
func TestCountsSurviveARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db", "job-counts.json")
	store, err := OpenCounterStore(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{ServiceName: "llm-api", Engine: "llama", Counts: store}

	// First run: three answered jobs.
	runJobs(t, New(cfg), 3, okAfter(20*time.Millisecond))
	saved(t, store, "llm-api", 3)

	// Second run, built from the saved counters: one job the engine refuses.
	failing := func(ctx context.Context, job *Job) (int, string, []byte, error) {
		return 0, "", nil, errors.New("engine refused")
	}
	runJobs(t, New(cfg), 1, failing)
	for i := 0; i < 200 && store.Get("llm-api").Failed != 1; i++ {
		time.Sleep(10 * time.Millisecond)
	}

	// Third run: the file is read again from disk, as after a restart.
	reopened, err := OpenCounterStore(path)
	if err != nil {
		t.Fatal(err)
	}
	st := New(Config{ServiceName: "llm-api", Engine: "llama", Counts: reopened}).Stats()
	if st.TotalJobsDone != 3 || st.TotalJobsFailed != 1 {
		t.Fatalf("after restart done=%d failed=%d, want 3 and 1", st.TotalJobsDone, st.TotalJobsFailed)
	}
	if st.AvgJobSec <= 0 {
		t.Errorf("the average was lost across the restart: %.3f", st.AvgJobSec)
	}
}

// Counts saved for one engine do not go to another that takes the service name.
func TestCountsAreNotTakenOverByAnotherEngine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "job-counts.json")
	store, _ := OpenCounterStore(path)
	q := New(Config{ServiceName: "llm-api", Engine: "llama", Counts: store})
	runJobs(t, q, 2, okAfter(time.Millisecond))
	saved(t, store, "llm-api", 2)

	other := New(Config{ServiceName: "llm-api", Engine: "vllm", Counts: store})
	if st := other.Stats(); st.TotalJobsDone != 0 {
		t.Errorf("another engine inherited %d answered jobs", st.TotalJobsDone)
	}
	same := New(Config{ServiceName: "llm-api", Engine: "llama", Counts: store})
	if st := same.Stats(); st.TotalJobsDone != 2 {
		t.Errorf("the same engine got %d, want 2", st.TotalJobsDone)
	}
}

// A write that would take the count backwards is dropped: two jobs finishing
// together each take a snapshot and the smaller one may write last.
func TestPutKeepsTheLargerCount(t *testing.T) {
	store, _ := OpenCounterStore(filepath.Join(t.TempDir(), "c.json"))
	store.Put("llm-api", Counters{Engine: "llama", Done: 5})
	store.Put("llm-api", Counters{Engine: "llama", Done: 3})
	if got := store.Get("llm-api").Done; got != 5 {
		t.Errorf("done = %d, want 5", got)
	}
	// A different engine is a fresh start and does replace it.
	store.Put("llm-api", Counters{Engine: "vllm", Done: 1})
	if got := store.Get("llm-api").Done; got != 1 {
		t.Errorf("after the engine changed done = %d, want 1", got)
	}
}

// A file that is not JSON is set aside and the store starts empty, still usable.
func TestCorruptCountFileIsSetAside(t *testing.T) {
	path := filepath.Join(t.TempDir(), "job-counts.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := OpenCounterStore(path)
	if err == nil {
		t.Error("a corrupt file must be reported")
	}
	if store == nil {
		t.Fatal("the store must still be usable")
	}
	if _, serr := os.Stat(path + ".bad"); serr != nil {
		t.Errorf("the corrupt file was not kept as .bad: %v", serr)
	}
	store.Put("llm-api", Counters{Done: 1})
	again, err := OpenCounterStore(path)
	if err != nil || again.Get("llm-api").Done != 1 {
		t.Errorf("a new file was not written: %v %+v", err, again.Get("llm-api"))
	}
}

// A queue without a store counts in memory, and a nil store does nothing.
func TestNilStoreIsHarmless(t *testing.T) {
	var s *CounterStore
	s.Put("x", Counters{Done: 1})
	if c := s.Get("x"); c.Done != 0 {
		t.Errorf("nil store returned %+v", c)
	}
	q := New(Config{ServiceName: "llm-api"})
	runJobs(t, q, 2, okAfter(time.Millisecond))
	if st := q.Stats(); st.TotalJobsDone != 2 {
		t.Errorf("done = %d, want 2", st.TotalJobsDone)
	}
}
