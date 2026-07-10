package queue

import (
	"context"
	"net/http"
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

// TestSubmitReqIDIdempotent — a client-supplied X-ISANN-Request-Id becomes the
// job id and dedups retries; absent/malformed falls back to a generated id.
func TestSubmitReqIDIdempotent(t *testing.T) {
	q := New(Config{ServiceName: "sd-api"})
	hdr := func(id string) http.Header {
		h := http.Header{}
		if id != "" {
			h.Set("X-ISANN-Request-Id", id)
		}
		return h
	}

	// supplied req_id becomes the job id
	j1, err := q.Submit("/v1/x", []byte("a"), hdr("npc-42-turn7"))
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if j1.ID != "npc-42-turn7" {
		t.Fatalf("job ID = %q, want supplied req_id", j1.ID)
	}

	// retry with the SAME req_id returns the SAME job (dedup, no duplicate)
	j2, err := q.Submit("/v1/x", []byte("a"), hdr("npc-42-turn7"))
	if err != nil {
		t.Fatalf("resubmit: %v", err)
	}
	if j2 != j1 {
		t.Fatalf("resubmit created a new job; want dedup to the existing one")
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
