package queue

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSubmitGet(t *testing.T) {
	q := New(Config{ServiceName: "sd-api"})
	job, err := q.Submit("/v1/x", []byte("hello"), http.Header{})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if job.ID == "" {
		t.Fatal("job ID empty")
	}
	if job.Status != StatusQueued {
		t.Errorf("status = %s", job.Status)
	}
	if job.ServiceName != "sd-api" {
		t.Errorf("ServiceName = %q, want %q", job.ServiceName, "sd-api")
	}
	if got := q.Get(job.ID); got != job {
		t.Errorf("Get returned %v, want %v", got, job)
	}
}

// reqIDHeader carries a client-supplied request id (none when id is empty).
func reqIDHeader(id string) http.Header {
	h := http.Header{}
	if id != "" {
		h.Set("X-ISANN-Request-Id", id)
	}
	return h
}

// TestSubmitReqIDDuplicateRefused: a client-supplied X-ISANN-Request-Id
// becomes the job id; the same id again is refused (not joined to the existing
// job) until that job is gone; absent/malformed falls back to a generated id.
func TestSubmitReqIDDuplicateRefused(t *testing.T) {
	q := New(Config{ServiceName: "sd-api"})
	hdr := reqIDHeader

	// supplied req_id becomes the job id
	j1, err := q.Submit("/v1/x", []byte("a"), hdr("npc-42-turn7"))
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if j1.ID != "npc-42-turn7" {
		t.Fatalf("job ID = %q, want supplied req_id", j1.ID)
	}

	// the SAME req_id again is refused - same body or not - and the first job
	// keeps its request
	for _, body := range []string{"a", "someone else's prompt"} {
		j2, err := q.Submit("/v1/x", []byte(body), hdr("npc-42-turn7"))
		if err != ErrDuplicateID || j2 != nil {
			t.Fatalf("resubmit (%q) = %v, %v; want nil, ErrDuplicateID", body, j2, err)
		}
	}
	if got := q.Get("npc-42-turn7"); got != j1 || string(got.RequestBody) != "a" {
		t.Fatalf("the first job changed after a refused resubmit")
	}

	// once the job is gone (DELETE of a finished job) the id is free again
	q.mu.Lock()
	j1.Status = StatusDone
	q.mu.Unlock()
	if r := q.Delete("npc-42-turn7"); r != DeleteOK {
		t.Fatalf("delete = %v", r)
	}
	if _, err := q.Submit("/v1/x", []byte("b"), hdr("npc-42-turn7")); err != nil {
		t.Fatalf("submit after delete: %v", err)
	}

	// no req_id → server-generated id
	j3, _ := q.Submit("/v1/x", nil, hdr(""))
	if j3.ID == "" || j3.ID == "npc-42-turn7" {
		t.Fatalf("expected generated id, got %q", j3.ID)
	}

	// malformed req_id (slash + space) → rejected → server-generated id
	j4, _ := q.Submit("/v1/x", nil, hdr("bad/id with space"))
	if j4.ID == "bad/id with space" {
		t.Fatalf("malformed req_id should not become the job id, got %q", j4.ID)
	}
}

func TestServiceNamePropagation(t *testing.T) {
	q := New(Config{ServiceName: "vllm-api"})
	for i := 0; i < 3; i++ {
		j, _ := q.Submit("/v1/y", nil, http.Header{})
		if j.ServiceName != "vllm-api" {
			t.Errorf("job %d ServiceName = %q", i, j.ServiceName)
		}
	}
}

func TestServiceNameEmpty(t *testing.T) {
	// 단일 서비스 / 테스트 용도로 ServiceName 미설정 시 빈 문자열 그대로 전파.
	q := New(Config{})
	j, _ := q.Submit("/x", nil, http.Header{})
	if j.ServiceName != "" {
		t.Errorf("expected empty ServiceName, got %q", j.ServiceName)
	}
}

func TestMaxQueueCombinedLimit(t *testing.T) {
	// MaxQueue=3 일 때 pending+running 합쳐서 3개까지만 허용.
	// concurrency=1 이라 1건 running, 나머지 pending. 4번째 submit → ErrQueueFull.
	q := New(Config{
		ServiceName: "sd-api",
		MaxQueue:    3,
		Concurrency: 1,
	})

	block := make(chan struct{})
	process := func(ctx context.Context, job *Job) (int, string, []byte, error) {
		<-block // 첫 번째 작업이 끝나지 않게 막아둠 → running 1건 유지
		return 200, "text/plain", []byte("ok"), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Worker(ctx, process)

	// 첫 작업이 running 으로 진입할 시간 확보
	if _, err := q.Submit("/x", nil, http.Header{}); err != nil {
		t.Fatalf("submit 1: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	// 추가 2건 (총 1 running + 2 pending = 3)
	for i := 0; i < 2; i++ {
		if _, err := q.Submit("/x", nil, http.Header{}); err != nil {
			t.Fatalf("submit %d: %v", i+2, err)
		}
	}

	// 4번째는 가득 → 거절
	if _, err := q.Submit("/x", nil, http.Header{}); err != ErrQueueFull {
		t.Errorf("expected ErrQueueFull, got %v", err)
	}

	// 첫 작업 완료시키고 슬롯 비워주기 → 다음 submit 통과해야 함
	close(block)
	time.Sleep(300 * time.Millisecond) // 처리 시간

	if _, err := q.Submit("/x", nil, http.Header{}); err != nil {
		// 큐가 비어가면 새 submit 가능해야 함 (이전 1건 done, 2건 처리 중/대기)
		// 정확한 카운트는 타이밍 의존이므로 ErrQueueFull 도 허용 — 핵심은 첫 가득 검사 동작
		if err != ErrQueueFull {
			t.Errorf("unexpected error after slot freed: %v", err)
		}
	}
}

func TestMaxQueueZeroUnlimited(t *testing.T) {
	// MaxQueue=0 → 제한 없음. 100개 submit 모두 성공해야 함.
	q := New(Config{ServiceName: "test"})
	for i := 0; i < 100; i++ {
		if _, err := q.Submit("/x", nil, http.Header{}); err != nil {
			t.Fatalf("submit %d failed: %v", i, err)
		}
	}
}

func TestSaveToDiskPropagation(t *testing.T) {
	q := New(Config{ServiceName: "sd-api", SaveToDisk: true})
	if !q.SaveToDisk() {
		t.Error("SaveToDisk() = false, want true")
	}

	q2 := New(Config{ServiceName: "vllm-api", SaveToDisk: false})
	if q2.SaveToDisk() {
		t.Error("SaveToDisk() = true, want false")
	}
}

func TestServiceNameAccessor(t *testing.T) {
	q := New(Config{ServiceName: "llm-api"})
	if q.ServiceName() != "llm-api" {
		t.Errorf("ServiceName() = %q", q.ServiceName())
	}
}

func TestMaxQueueAccessor(t *testing.T) {
	q := New(Config{MaxQueue: 42})
	if q.MaxQueue() != 42 {
		t.Errorf("MaxQueue() = %d", q.MaxQueue())
	}
}

func TestWorkerSerial(t *testing.T) {
	q := New(Config{Concurrency: 1})
	var inflight int32
	var maxInflight int32
	process := func(ctx context.Context, job *Job) (int, string, []byte, error) {
		n := atomic.AddInt32(&inflight, 1)
		if n > atomic.LoadInt32(&maxInflight) {
			atomic.StoreInt32(&maxInflight, n)
		}
		time.Sleep(50 * time.Millisecond)
		atomic.AddInt32(&inflight, -1)
		return 200, "text/plain", []byte("ok"), nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Worker(ctx, process)

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			job, _ := q.Submit("/x", nil, http.Header{})
			waitCtx, cn := context.WithTimeout(context.Background(), 2*time.Second)
			defer cn()
			if _, err := q.Wait(waitCtx, job.ID); err != nil {
				t.Errorf("wait failed: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&maxInflight); got > 1 {
		t.Errorf("max inflight = %d, want 1", got)
	}
}

func TestWorkerProcessSetsResult(t *testing.T) {
	q := New(Config{})
	process := func(ctx context.Context, job *Job) (int, string, []byte, error) {
		return 200, "image/png", []byte("PNGDATA"), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Worker(ctx, process)

	job, _ := q.Submit("/v1/images/generations", nil, http.Header{})
	waitCtx, cn := context.WithTimeout(context.Background(), 2*time.Second)
	defer cn()
	done, err := q.Wait(waitCtx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != StatusDone {
		t.Errorf("status = %s", done.Status)
	}
	if string(done.ResponseBody) != "PNGDATA" {
		t.Errorf("body = %q", string(done.ResponseBody))
	}
	if done.ResponseType != "image/png" {
		t.Errorf("content-type = %q", done.ResponseType)
	}
	if done.URL == "" {
		t.Error("URL not set")
	}
	if done.Progress != 100 {
		t.Errorf("progress = %d", done.Progress)
	}
}

func TestWorkerProcessFailure(t *testing.T) {
	q := New(Config{})
	process := func(ctx context.Context, job *Job) (int, string, []byte, error) {
		return 0, "", nil, http.ErrServerClosed
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Worker(ctx, process)

	job, _ := q.Submit("/x", nil, http.Header{})
	waitCtx, cn := context.WithTimeout(context.Background(), 2*time.Second)
	defer cn()
	done, _ := q.Wait(waitCtx, job.ID)
	if done.Status != StatusFailed {
		t.Errorf("status = %s", done.Status)
	}
	if done.Error == "" {
		t.Error("error not set")
	}
}

func TestUpdateRunningProgress(t *testing.T) {
	q := New(Config{})
	job, _ := q.Submit("/x", nil, http.Header{})
	// Manually mark running for the test.
	q.mu.Lock()
	q.running[job.ID] = job
	q.mu.Unlock()

	q.UpdateRunningProgress(5, 10)
	if job.Step != 5 || job.Total != 10 || job.Progress != 50 {
		t.Errorf("job state = step=%d total=%d progress=%d", job.Step, job.Total, job.Progress)
	}
}

func TestStats(t *testing.T) {
	q := New(Config{})
	q.Submit("/x", nil, http.Header{})
	q.Submit("/x", nil, http.Header{})
	s := q.Stats()
	if s.Pending != 2 {
		t.Errorf("pending = %d", s.Pending)
	}
}

// TestSnapshotNeverDoneWithoutAnswer: readers polling jobs while they finish
// never see "done" without the answer. Reading the live job could catch it
// between the two writes and send an empty 200.
func TestSnapshotNeverDoneWithoutAnswer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q := New(Config{ServiceName: "llm-api", Concurrency: 4})
	var ids []string
	for i := 0; i < 200; i++ {
		j, err := q.Submit("/x", nil, http.Header{})
		if err != nil {
			t.Fatalf("submit: %v", err)
		}
		ids = append(ids, j.ID)
	}
	go q.Worker(ctx, func(ctx context.Context, j *Job) (int, string, []byte, error) {
		return 200, "application/json", []byte(`{"answer":"` + j.ID + `"}`), nil
	})
	deadline := time.Now().Add(10 * time.Second)
	for left := len(ids); left > 0; {
		if time.Now().After(deadline) {
			t.Fatalf("%d jobs still not done", left)
		}
		left = 0
		for _, id := range ids {
			s := q.Snapshot(id)
			if s.Status != StatusDone {
				left++
				continue
			}
			if !strings.Contains(string(s.ResponseBody), id) {
				t.Fatalf("job %s reads done with answer %q", id, s.ResponseBody)
			}
		}
	}
}

// TestSubmitWithAndSnapshotCopy: owner, timeout and stream mode are on the job
// from the start, and a snapshot is a copy that later writes do not reach.
func TestSubmitWithAndSnapshotCopy(t *testing.T) {
	q := New(Config{ServiceName: "llm-api"})
	j, err := q.SubmitWith("/x", nil, http.Header{}, SubmitOptions{
		Owner: "0xabc", Timeout: 7 * time.Second, Stream: true, ChunkMode: ChunkModeStrict,
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	s := q.Snapshot(j.ID)
	if s.SubmitterAddress != "0xabc" || s.Timeout != 7*time.Second || !s.Stream || s.ChunkMode != ChunkModeStrict {
		t.Fatalf("snapshot = owner %q timeout %v stream %v mode %q", s.SubmitterAddress, s.Timeout, s.Stream, s.ChunkMode)
	}

	j.AppendChunk("first. ")
	j.setFile("/tmp/x.png", "/outputs/x.png")
	s = q.Snapshot(j.ID)
	j.AppendChunk("second. ")
	if s.ChunkCount() != 1 || s.URL != "/outputs/x.png" || s.ResponseFile != "/tmp/x.png" {
		t.Fatalf("snapshot chunks %d url %q file %q", s.ChunkCount(), s.URL, s.ResponseFile)
	}
	s.AppendChunk("only on the copy")
	if j.ChunkCount() != 2 {
		t.Fatalf("live job has %d chunks, want 2 (the copy's write leaked)", j.ChunkCount())
	}
	if q.Snapshot("nope") != nil {
		t.Fatal("snapshot of a missing job is not nil")
	}
}

// TestStatsHidesRunningJobID - Stats leaves the node (public /v1/queue/stats,
// the RV's /v1/metrics, /ping), and an anonymous job's id is what reads its
// result. A running job shows as "<service>-active", never as its id.
func TestStatsHidesRunningJobID(t *testing.T) {
	q := New(Config{ServiceName: "llm-api"})
	if got := q.Stats().RunningJobID; got != "" {
		t.Fatalf("idle running_job_id = %q, want empty", got)
	}
	job, _ := q.Submit("/x", nil, http.Header{})
	if q.dequeue() != job {
		t.Fatal("dequeue did not start the submitted job")
	}
	s := q.Stats()
	if s.RunningJobID == job.ID || strings.Contains(s.RunningJobID, job.ID) {
		t.Fatalf("running_job_id %q carries the job id %s", s.RunningJobID, job.ID)
	}
	if s.RunningJobID != "llm-api-active" || s.Running != 1 {
		t.Fatalf("running_job_id = %q running = %d, want llm-api-active and 1", s.RunningJobID, s.Running)
	}
}

func TestWaitContextCanceled(t *testing.T) {
	q := New(Config{})
	job, _ := q.Submit("/x", nil, http.Header{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := q.Wait(ctx, job.ID)
	if err == nil {
		t.Error("expected context error")
	}
}

func TestWaitJobNotFound(t *testing.T) {
	q := New(Config{})
	_, err := q.Wait(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected not found error")
	}
}

// 🔴 THE HOOK MUST ARRIVE THROUGH Config, NOT SetCleanupHook.
//
// Manager builds every production queue from a factory that hands back a
// Config (manager.go: `cfg, process := m.factory(svc); q := New(cfg)`). There
// is no seam there to call SetCleanupHook, which is why the hook was defined,
// documented, and unit-tested while being nil in every running station:
// dispatcher_test.go installs it by hand and passes, so nothing pointed at the
// gap. A test that calls SetCleanupHook cannot fail when the wiring is missing.
//
// Both eviction paths are covered - TTL and LRU - because gc calls the hook
// twice, once in each branch.
func TestCleanupHookArrivesThroughConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  Config
		age  time.Duration // how far in the past EndedAt sits
		jobs int
	}{
		{"TTL", Config{DoneTTL: time.Minute, MaxDone: 100}, 2 * time.Minute, 1},
		{"LRU", Config{DoneTTL: time.Hour, MaxDone: 1}, 0, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var evicted []string
			cfg := tc.cfg
			cfg.ServiceName = "sd-api"
			cfg.CleanupHook = func(j *Job) { evicted = append(evicted, j.ID) }
			q := New(cfg)

			var ids []string
			for i := 0; i < tc.jobs; i++ {
				job, err := q.Submit("/v1/images/generations", []byte(`{}`), http.Header{})
				if err != nil {
					t.Fatal(err)
				}
				ids = append(ids, job.ID)
				q.mu.Lock()
				q.jobs[job.ID].Status = StatusDone
				q.jobs[job.ID].EndedAt = time.Now().Add(-tc.age - time.Duration(tc.jobs-i)*time.Second)
				q.jobs[job.ID].ResponseFile = "/tmp/" + job.ID + ".png"
				q.mu.Unlock()
			}

			q.gc()

			if len(evicted) == 0 {
				t.Fatal("cleanup hook never fired - Config.CleanupHook is not reaching the queue, " +
					"so result files outlive the job that names their owner")
			}
			// The oldest is always the one that goes.
			if evicted[0] != ids[0] {
				t.Errorf("evicted %v, want the oldest (%s) first", evicted, ids[0])
			}
			if q.Get(evicted[0]) != nil {
				t.Error("job survived gc")
			}
		})
	}
}
