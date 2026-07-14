package queue

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/isannai/mesh/pkg/setup"
)

func newTestManager(t *testing.T) (*Manager, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	m := NewManager(ctx, func(svc setup.ServiceEntry) (Config, ProcessFunc) {
		return Config{
			ServiceName: svc.Name,
			Concurrency: 1,
			MaxQueue:    5,
		}, nil // nil process — Submit/Stats only, no worker
	})
	return m, cancel
}

func TestManagerGetOrCreateLazy(t *testing.T) {
	m, cancel := newTestManager(t)
	defer cancel()

	if got := m.Get("sd-api"); got != nil {
		t.Fatal("Get before create returned non-nil")
	}

	q := m.GetOrCreate(setup.ServiceEntry{Name: "sd-api"})
	if q == nil {
		t.Fatal("GetOrCreate returned nil")
	}
	if q.ServiceName() != "sd-api" {
		t.Errorf("ServiceName = %q", q.ServiceName())
	}

	// Subsequent call returns same pointer.
	q2 := m.GetOrCreate(setup.ServiceEntry{Name: "sd-api"})
	if q != q2 {
		t.Error("repeated GetOrCreate returned different pointers")
	}

	// Get also returns the same.
	if got := m.Get("sd-api"); got != q {
		t.Error("Get returned different pointer than GetOrCreate")
	}
}

func TestManagerMultiServiceIsolation(t *testing.T) {
	m, cancel := newTestManager(t)
	defer cancel()

	sd := m.GetOrCreate(setup.ServiceEntry{Name: "sd-api"})
	llm := m.GetOrCreate(setup.ServiceEntry{Name: "llm-api"})
	vllm := m.GetOrCreate(setup.ServiceEntry{Name: "vllm-api"})

	if sd == llm || llm == vllm || sd == vllm {
		t.Error("different services share queue instance")
	}
	if m.Len() != 3 {
		t.Errorf("Len = %d, want 3", m.Len())
	}

	// Submit to sd-api fills its pending; llm-api should remain empty.
	for i := 0; i < 5; i++ {
		if _, err := sd.Submit("/x", nil, http.Header{}); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}
	// 6th submit on sd-api should fail (max=5).
	if _, err := sd.Submit("/x", nil, http.Header{}); err != ErrQueueFull {
		t.Errorf("expected ErrQueueFull on sd-api, got %v", err)
	}
	// llm-api unaffected.
	if _, err := llm.Submit("/x", nil, http.Header{}); err != nil {
		t.Errorf("llm-api submit failed despite sd-api being full: %v", err)
	}
}

func TestManagerStats(t *testing.T) {
	m, cancel := newTestManager(t)
	defer cancel()

	// Stats on a non-existent queue returns zero Stats.
	if s := m.Stats("nope"); s.Pending != 0 || s.Running != 0 {
		t.Errorf("Stats on missing queue returned %+v", s)
	}

	q := m.GetOrCreate(setup.ServiceEntry{Name: "sd-api"})
	for i := 0; i < 3; i++ {
		q.Submit("/x", nil, http.Header{})
	}

	s := m.Stats("sd-api")
	if s.Pending != 3 {
		t.Errorf("Stats.Pending = %d, want 3", s.Pending)
	}
}

func TestManagerAllStats(t *testing.T) {
	m, cancel := newTestManager(t)
	defer cancel()

	a := m.GetOrCreate(setup.ServiceEntry{Name: "a"})
	b := m.GetOrCreate(setup.ServiceEntry{Name: "b"})
	a.Submit("/x", nil, http.Header{})
	a.Submit("/x", nil, http.Header{})
	b.Submit("/x", nil, http.Header{})

	all := m.AllStats()
	if len(all) != 2 {
		t.Fatalf("AllStats returned %d entries", len(all))
	}
	if all["a"].Pending != 2 {
		t.Errorf("a.Pending = %d", all["a"].Pending)
	}
	if all["b"].Pending != 1 {
		t.Errorf("b.Pending = %d", all["b"].Pending)
	}
}

func TestManagerNames(t *testing.T) {
	m, cancel := newTestManager(t)
	defer cancel()

	m.GetOrCreate(setup.ServiceEntry{Name: "x"})
	m.GetOrCreate(setup.ServiceEntry{Name: "y"})

	names := m.Names()
	if len(names) != 2 {
		t.Fatalf("Names returned %d entries", len(names))
	}
	have := map[string]bool{names[0]: true, names[1]: true}
	if !have["x"] || !have["y"] {
		t.Errorf("Names = %v, want [x y]", names)
	}
}

func TestManagerConcurrentGetOrCreate(t *testing.T) {
	// 다수 goroutine 이 동시에 같은 service 의 GetOrCreate 를 호출해도 단 하나의
	// Queue 인스턴스만 만들어져야 함 (double-check locking 정확성 검증).
	m, cancel := newTestManager(t)
	defer cancel()

	var firstPtr atomic.Value
	var wg sync.WaitGroup
	const N = 50
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q := m.GetOrCreate(setup.ServiceEntry{Name: "racy"})
			if firstPtr.Load() == nil {
				firstPtr.CompareAndSwap(nil, q)
				return
			}
			if got := firstPtr.Load().(*Queue); got != q {
				t.Errorf("got different queue instance under race")
			}
		}()
	}
	wg.Wait()

	if m.Len() != 1 {
		t.Errorf("Len after race = %d, want 1", m.Len())
	}
}

func TestManagerFactoryStartsWorker(t *testing.T) {
	// Factory 가 ProcessFunc 을 반환하면 worker 가 자동 시작되어야 함.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var processed int32
	process := func(ctx context.Context, job *Job) (int, string, []byte, error) {
		atomic.AddInt32(&processed, 1)
		return 200, "text/plain", []byte("ok"), nil
	}
	factory := func(svc setup.ServiceEntry) (Config, ProcessFunc) {
		return Config{ServiceName: svc.Name, Concurrency: 1}, process
	}
	m := NewManager(ctx, factory)

	q := m.GetOrCreate(setup.ServiceEntry{Name: "sd-api"})
	job, _ := q.Submit("/x", nil, http.Header{})

	waitCtx, cn := context.WithTimeout(context.Background(), 2*time.Second)
	defer cn()
	if _, err := q.Wait(waitCtx, job.ID); err != nil {
		t.Fatalf("worker did not run: %v", err)
	}
	if atomic.LoadInt32(&processed) != 1 {
		t.Errorf("processed count = %d", atomic.LoadInt32(&processed))
	}
}

func TestManagerNilFactoryFallback(t *testing.T) {
	// nil factory 도 안전하게 동작 (default factory 가 svc.Name 만 채움).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := NewManager(ctx, nil)
	q := m.GetOrCreate(setup.ServiceEntry{Name: "stub"})
	if q.ServiceName() != "stub" {
		t.Errorf("ServiceName = %q", q.ServiceName())
	}
	// Submit 도 정상 (worker 만 안 돌아감).
	if _, err := q.Submit("/x", nil, http.Header{}); err != nil {
		t.Errorf("submit failed: %v", err)
	}
}
