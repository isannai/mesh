package queue

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/isannai/mesh/pkg/engine/manifest"
	"github.com/isannai/mesh/pkg/setup"
)

// ExternalMonitor tracks an external engine's in-flight request count by
// polling its Prometheus-style /metrics endpoint. ProcessFunc consults
// canDispatch() before forwarding to provide backpressure.
//
// The Mapping field maps logical metric names ("running_count") to the
// engine's actual Prometheus key (e.g. "vllm:num_requests_running"). It comes
// from the engine manifest's metrics.mapping section, allowing different
// engines (vLLM, TGI, Ollama) to expose differently-named metrics.
//
// External engines support continuous batching, so Capacity is typically the
// engine's own concurrency limit (e.g. 50 for a tuned vLLM). We poll /metrics
// to learn the engine's authoritative inflight count, then optimistically
// adjust during dispatch to bridge the gap until the next poll.
type ExternalMonitor struct {
	URL      string            // full metrics URL, post-template-expansion
	Capacity int               // dispatch hard limit (≤0 = unlimited)
	Mapping  map[string]string // logical → prometheus name (from manifest)
	Interval time.Duration     // poll period; default 1s

	inflight atomic.Int32
	client   *http.Client
}

// NewExternalMonitor builds a monitor from a service entry + its manifest.
// Capacity is typically passed in from BuildConfig's resolveConcurrency so
// service config can override the manifest default.
//
// Returns nil when the manifest has no metrics block — caller must guard
// before calling Run / canDispatch (the methods themselves are nil-safe).
func NewExternalMonitor(svc setup.ServiceEntry, m *manifest.Manifest, capacity int) *ExternalMonitor {
	if m == nil || m.Metrics == nil || m.Metrics.URL == "" {
		return nil
	}
	url := strings.ReplaceAll(m.Metrics.URL, "{addr}", svc.Addr)
	return &ExternalMonitor{
		URL:      url,
		Capacity: capacity,
		Mapping:  m.Metrics.Mapping,
		Interval: time.Second,
		client:   &http.Client{Timeout: 3 * time.Second},
	}
}

// Run periodically polls /metrics until ctx is canceled. Spawned in a
// goroutine alongside the queue Worker. Single tick on entry so Inflight
// is fresh by the time the first dispatch arrives.
func (e *ExternalMonitor) Run(ctx context.Context) {
	if e == nil || e.URL == "" {
		return
	}
	interval := e.Interval
	if interval <= 0 {
		interval = time.Second
	}
	_ = e.refresh(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = e.refresh(ctx)
		}
	}
}

// refresh hits /metrics once and updates inflight from running_count.
// Returns the first error encountered (HTTP / parse). The atomic value is
// only updated on success — transient failures don't zero it out.
func (e *ExternalMonitor) refresh(ctx context.Context) error {
	if e == nil || e.URL == "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.URL, nil)
	if err != nil {
		return err
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	runningKey := e.Mapping["running_count"]
	if runningKey == "" {
		return fmt.Errorf("external: no running_count mapping")
	}
	val, ok := parsePrometheusValue(body, runningKey)
	if !ok {
		return fmt.Errorf("external: metric %s not found", runningKey)
	}
	e.inflight.Store(int32(val))
	return nil
}

// CanDispatch reports whether another request can be sent without exceeding
// Capacity. Returns true for nil monitor or when Capacity ≤ 0 (unlimited).
func (e *ExternalMonitor) CanDispatch() bool {
	if e == nil || e.Capacity <= 0 {
		return true
	}
	return int(e.inflight.Load()) < e.Capacity
}

// Inflight returns the most recently observed in-flight count (0 for nil).
func (e *ExternalMonitor) Inflight() int32 {
	if e == nil {
		return 0
	}
	return e.inflight.Load()
}

// AddInflight optimistically adjusts the cached inflight count. Used by
// ProcessFunc to bridge the gap between dispatch and the next /metrics
// refresh — without this, a burst of dispatches all see "0 inflight" and
// over-commit before refresh catches up. Negative results are clamped to 0.
func (e *ExternalMonitor) AddInflight(delta int32) {
	if e == nil {
		return
	}
	n := e.inflight.Add(delta)
	if n < 0 {
		e.inflight.Store(0)
	}
}

// parsePrometheusValue scans a Prometheus exposition body and returns the
// gauge/counter value for the given metric name (labels ignored). Supports
// both "name value" and "name{labels} value" forms.
func parsePrometheusValue(body []byte, metric string) (float64, bool) {
	pattern := `(?m)^` + regexp.QuoteMeta(metric) + `(?:\{[^}]*\})?\s+([\d.eE+-]+)`
	re := regexp.MustCompile(pattern)
	m := re.FindSubmatch(body)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(string(m[1]), 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// MakeExternalProcess returns a ProcessFunc for external engines like vLLM.
// The processor:
//
//  1. Waits until monitor.CanDispatch() (or ctx cancellation)
//  2. Optimistically +1 inflight (corrected by next /metrics refresh)
//  3. Forwards the HTTP request like managed engines do (via MakeManagedProcess)
//  4. Always -1 inflight on exit (defer)
//
// Pass monitor=nil to skip backpressure entirely (useful for engines that
// declare no /metrics — capacity becomes manifest's concurrency only).
func MakeExternalProcess(svc setup.ServiceEntry, monitor *ExternalMonitor, opts DispatchOptions) ProcessFunc {
	forward := MakeManagedProcess(svc, opts)
	if monitor == nil {
		return forward
	}

	return func(ctx context.Context, job *Job) (int, string, []byte, error) {
		// Wait until the engine has room. Polls every 50ms; ctx-aware.
		for !monitor.CanDispatch() {
			select {
			case <-time.After(50 * time.Millisecond):
			case <-ctx.Done():
				return 0, "", nil, ctx.Err()
			}
		}

		monitor.AddInflight(1)
		defer monitor.AddInflight(-1)

		return forward(ctx, job)
	}
}
