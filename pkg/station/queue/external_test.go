package queue

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daesob/http3proxy/pkg/engine/manifest"
	"github.com/daesob/http3proxy/pkg/setup"
)

// vllmManifest builds a manifest similar to vllm.json for tests.
func vllmManifest(metricsURL string) *manifest.Manifest {
	return &manifest.Manifest{
		Metrics: &manifest.MetricsSpec{
			Type: "prometheus",
			URL:  metricsURL,
			Mapping: map[string]string{
				"queue_depth":   "vllm:num_requests_waiting",
				"running_count": "vllm:num_requests_running",
			},
		},
	}
}

func TestParsePrometheusValueSimple(t *testing.T) {
	body := []byte("vllm:num_requests_running 12\nvllm:num_requests_waiting 3\n")
	v, ok := parsePrometheusValue(body, "vllm:num_requests_running")
	if !ok || v != 12 {
		t.Errorf("got %v ok=%v", v, ok)
	}
}

func TestParsePrometheusValueWithLabels(t *testing.T) {
	body := []byte(`vllm:num_requests_running{model="llama-3"} 7.5` + "\n")
	v, ok := parsePrometheusValue(body, "vllm:num_requests_running")
	if !ok || v != 7.5 {
		t.Errorf("got %v ok=%v", v, ok)
	}
}

func TestParsePrometheusValueMissing(t *testing.T) {
	body := []byte("other_metric 99\n")
	if _, ok := parsePrometheusValue(body, "missing"); ok {
		t.Error("expected ok=false")
	}
}

func TestParsePrometheusValueScientific(t *testing.T) {
	body := []byte("metric_x 1.5e+02\n")
	v, ok := parsePrometheusValue(body, "metric_x")
	if !ok || v != 150 {
		t.Errorf("got %v", v)
	}
}

func TestExternalMonitorRefresh(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "vllm:num_requests_running 12")
		fmt.Fprintln(w, "vllm:num_requests_waiting 4")
	}))
	defer srv.Close()

	svc := setup.ServiceEntry{Name: "vllm-api", Addr: strings.TrimPrefix(srv.URL, "http://")}
	mon := NewExternalMonitor(svc, vllmManifest("http://{addr}/metrics"), 50)
	if mon == nil {
		t.Fatal("NewExternalMonitor returned nil despite metrics being set")
	}

	if err := mon.refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if mon.Inflight() != 12 {
		t.Errorf("Inflight = %d, want 12", mon.Inflight())
	}
}

func TestExternalMonitorRefreshHTTPError(t *testing.T) {
	mon := &ExternalMonitor{
		URL:      "http://127.0.0.1:1/metrics", // unreachable
		Capacity: 50,
		Mapping:  map[string]string{"running_count": "x"},
		client:   &http.Client{Timeout: 100 * time.Millisecond},
	}
	if err := mon.refresh(context.Background()); err == nil {
		t.Error("expected refresh error")
	}
	// Inflight unchanged on error.
	if mon.Inflight() != 0 {
		t.Errorf("Inflight should remain 0, got %d", mon.Inflight())
	}
}

func TestExternalMonitorNilFromManifest(t *testing.T) {
	// Manifest without metrics → nil monitor.
	mon := NewExternalMonitor(setup.ServiceEntry{Name: "x"}, &manifest.Manifest{}, 50)
	if mon != nil {
		t.Error("expected nil monitor when metrics absent")
	}
}

func TestExternalMonitorCanDispatch(t *testing.T) {
	mon := &ExternalMonitor{Capacity: 50}
	mon.inflight.Store(49)
	if !mon.CanDispatch() {
		t.Error("49/50 should allow dispatch")
	}
	mon.inflight.Store(50)
	if mon.CanDispatch() {
		t.Error("50/50 should reject")
	}
	mon.inflight.Store(60) // could happen between refreshes
	if mon.CanDispatch() {
		t.Error("over-capacity should reject")
	}
}

func TestExternalMonitorCanDispatchNil(t *testing.T) {
	var mon *ExternalMonitor
	if !mon.CanDispatch() {
		t.Error("nil monitor should allow")
	}
}

func TestExternalMonitorCanDispatchUnlimited(t *testing.T) {
	mon := &ExternalMonitor{Capacity: 0}
	mon.inflight.Store(9999)
	if !mon.CanDispatch() {
		t.Error("Capacity=0 means unlimited")
	}
}

func TestExternalMonitorAddInflight(t *testing.T) {
	mon := &ExternalMonitor{}
	mon.AddInflight(3)
	if mon.Inflight() != 3 {
		t.Errorf("got %d", mon.Inflight())
	}
	mon.AddInflight(-1)
	if mon.Inflight() != 2 {
		t.Errorf("got %d", mon.Inflight())
	}
	mon.AddInflight(-100) // would underflow without clamp
	if mon.Inflight() != 0 {
		t.Errorf("expected clamp to 0, got %d", mon.Inflight())
	}
}

func TestExternalMonitorRunStopsOnCancel(t *testing.T) {
	hits := atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprintln(w, "vllm:num_requests_running 5")
	}))
	defer srv.Close()

	mon := &ExternalMonitor{
		URL:      srv.URL,
		Capacity: 50,
		Mapping:  map[string]string{"running_count": "vllm:num_requests_running"},
		Interval: 30 * time.Millisecond,
		client:   &http.Client{Timeout: time.Second},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		mon.Run(ctx)
		close(done)
	}()

	time.Sleep(120 * time.Millisecond) // ~3-4 polls
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop on cancel")
	}

	if mon.Inflight() != 5 {
		t.Errorf("Inflight should reflect last poll, got %d", mon.Inflight())
	}
	if hits.Load() < 1 {
		t.Errorf("expected at least 1 hit, got %d", hits.Load())
	}
}

func TestMakeExternalProcessForwards(t *testing.T) {
	// Engine endpoint
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("answered"))
	}))
	defer engine.Close()

	mon := &ExternalMonitor{Capacity: 10} // empty mapping ok — CanDispatch only
	svc := setup.ServiceEntry{Name: "vllm-api", Addr: strings.TrimPrefix(engine.URL, "http://")}
	process := MakeExternalProcess(svc, mon, DispatchOptions{})

	code, _, body, err := process(context.Background(), &Job{Path: "/v1/completions"})
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if code != 200 || string(body) != "answered" {
		t.Errorf("code/body = %d / %q", code, string(body))
	}
}

func TestMakeExternalProcessNilMonitor(t *testing.T) {
	// nil monitor → forward without backpressure (acts like managed).
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer engine.Close()

	svc := setup.ServiceEntry{Name: "x", Addr: strings.TrimPrefix(engine.URL, "http://")}
	process := MakeExternalProcess(svc, nil, DispatchOptions{})

	_, _, body, err := process(context.Background(), &Job{Path: "/x"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if string(body) != "ok" {
		t.Errorf("body = %q", string(body))
	}
}

func TestMakeExternalProcessAddsInflight(t *testing.T) {
	// 처리 중 inflight=+1, 끝나면 -1 로 복원.
	hold := make(chan struct{})
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-hold
		w.Write([]byte("done"))
	}))
	defer engine.Close()

	mon := &ExternalMonitor{Capacity: 10}
	svc := setup.ServiceEntry{Name: "x", Addr: strings.TrimPrefix(engine.URL, "http://")}
	process := MakeExternalProcess(svc, mon, DispatchOptions{})

	go process(context.Background(), &Job{Path: "/x"})

	// 잠깐 기다린 뒤 inflight 확인 — 1 이어야 함.
	time.Sleep(50 * time.Millisecond)
	if mon.Inflight() != 1 {
		t.Errorf("inflight during process = %d, want 1", mon.Inflight())
	}

	close(hold)
	time.Sleep(80 * time.Millisecond)
	if mon.Inflight() != 0 {
		t.Errorf("inflight after process = %d, want 0", mon.Inflight())
	}
}

func TestMakeExternalProcessBackpressure(t *testing.T) {
	// 이미 capacity 도달 상태에서 호출 → 풀릴 때까지 대기.
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer engine.Close()

	mon := &ExternalMonitor{Capacity: 2}
	mon.inflight.Store(2) // 가득

	svc := setup.ServiceEntry{Name: "x", Addr: strings.TrimPrefix(engine.URL, "http://")}
	process := MakeExternalProcess(svc, mon, DispatchOptions{})

	started := atomic.Bool{}
	finished := atomic.Bool{}
	go func() {
		started.Store(true)
		process(context.Background(), &Job{Path: "/x"})
		finished.Store(true)
	}()

	// 200ms 동안엔 dispatch 안 됨 (대기 중).
	time.Sleep(200 * time.Millisecond)
	if !started.Load() {
		t.Fatal("goroutine should have started")
	}
	if finished.Load() {
		t.Error("process should still be waiting (capacity full)")
	}

	// inflight 를 1 로 줄여주면 풀림.
	mon.AddInflight(-1)
	time.Sleep(150 * time.Millisecond)
	if !finished.Load() {
		t.Error("process should have proceeded after capacity freed")
	}
}

func TestMakeExternalProcessContextCancelDuringWait(t *testing.T) {
	// capacity full 상태에서 ctx cancel → ctx.Err() 반환.
	mon := &ExternalMonitor{Capacity: 1}
	mon.inflight.Store(1) // 가득

	svc := setup.ServiceEntry{Name: "x", Addr: "ignored:0"}
	process := MakeExternalProcess(svc, mon, DispatchOptions{})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, _, _, err := process(ctx, &Job{Path: "/x"})
	if err == nil {
		t.Error("expected ctx error")
	}
}
