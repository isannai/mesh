package queue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/isannai/mesh/pkg/glog"
)

// Config tunes a single Queue instance (one service). Zero values fall back
// to safe defaults. ServiceName identifies the owning service — every Job
// submitted to this queue inherits the name (used by Phase 2 multi-queue
// routing and Phase 7 HTTP handler).
type Config struct {
	ServiceName string            // owning service name (sd-api, llm-api, vllm-api, ...)
	Concurrency int               // simultaneous workers (default 1)
	MaxQueue    int               // pending+running combined cap; 0 = unlimited (default 0)
	MaxDone     int               // LRU cap for done/failed jobs (default 100)
	DoneTTL     time.Duration     // retention for done/failed jobs (default 1h)
	SaveToDisk  bool              // when true, processor persists results to disk and drops in-memory body (Phase 4)
	Disabled    bool              // when true, this service bypasses the queue entirely — Submit/dequeue/runJob never run
	Events      *glog.EventWriter // optional — emits job.received/started/completed/failed

	// OnJobChange fires after every job state transition (queued / started
	// / completed / failed). The provider wires this to NotifyJobChange so
	// the heartbeat loop flushes immediately. Called outside the queue
	// lock — implementations must be non-blocking (channel send with
	// default branch).
	//
	// event:   "received" | "started" | "completed" | "failed"
	// jobID:   발화 대상 job의 ID
	// pending: 큐 대기 중 작업 수 (event 발생 직후)
	// running: 실행 중 작업 수 (completed/failed는 본인 제외 후)
	OnJobChange func(serviceName, event, jobID string, pending, running int)
}

// ErrQueueFull is returned by Submit when the queue's combined pending+running
// count would exceed MaxQueue.
var ErrQueueFull = fmt.Errorf("queue full")

func (c Config) withDefaults() Config {
	if c.Concurrency <= 0 {
		c.Concurrency = 1
	}
	if c.MaxDone <= 0 {
		c.MaxDone = 100
	}
	// DoneTTL: 0 means "evict on next gc tick" (≈1min after completion) —
	// used by streaming engines like vLLM where the client has already
	// consumed the result by the time the job finishes. Negative is clamped
	// to 0. Positive durations are respected as-is. Production callers go
	// through BuildConfig which merges manifest + service config; tests pass
	// what they need explicitly.
	if c.DoneTTL < 0 {
		c.DoneTTL = 0
	}
	return c
}

// Stats is the response shape for /v1/queue/stats. Field names mirror sd-api
// for broker compatibility.
type Stats struct {
	Pending          int     `json:"pending"`
	Running          int     `json:"running,omitempty"`
	RunningJobID     string  `json:"running_job_id"`
	EstimatedWaitSec int     `json:"estimated_wait_sec"`
	TotalJobsDone    int64   `json:"total_jobs_done"`
	TotalJobsFailed  int64   `json:"total_jobs_failed,omitempty"`
	AvgJobSec        float64 `json:"avg_job_sec"`
}

// ProcessFunc forwards a job to the underlying engine and returns its
// response. Implementations must respect ctx cancellation.
type ProcessFunc func(ctx context.Context, job *Job) (code int, contentType string, body []byte, err error)

// Queue is a goroutine-safe in-memory FIFO job queue scoped to a single
// service. Phase 2's QueueManager owns one Queue instance per service.
type Queue struct {
	cfg     Config
	mu      sync.Mutex
	jobs    map[string]*Job
	pending []string // job IDs in FIFO order
	running map[string]*Job

	totalDone   int64
	totalFailed int64
	durations   []time.Duration // recent durations for avg calculation

	cleanupHook func(*Job) // optional callback when a job is evicted
	events      *glog.EventWriter
	onJobChange func(serviceName, event, jobID string, pending, running int)
}

// SetCleanupHook installs a callback invoked just before a job is removed
// from the queue (TTL or LRU). Phase 4's storage layer uses this to delete
// on-disk result files.
func (q *Queue) SetCleanupHook(fn func(*Job)) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.cleanupHook = fn
}

// New creates a Queue. Call Worker in a goroutine to start processing.
func New(cfg Config) *Queue {
	return &Queue{
		cfg:         cfg.withDefaults(),
		jobs:        make(map[string]*Job),
		running:     make(map[string]*Job),
		events:      cfg.Events,
		onJobChange: cfg.OnJobChange,
	}
}

// ServiceName returns the configured owning service name. Empty string when
// not set (single-tenant mode, mainly for tests).
func (q *Queue) ServiceName() string { return q.cfg.ServiceName }

// MaxQueue returns the configured combined cap (0 = unlimited).
func (q *Queue) MaxQueue() int { return q.cfg.MaxQueue }

// IsDisabled reports whether this queue is bypassed entirely. Callers
// should reverse-proxy requests directly to the underlying service when
// true, skipping Submit/Wait. Useful for streaming services (webdav,
// terminal) registered through the same manifest path as queued engines.
func (q *Queue) IsDisabled() bool { return q.cfg.Disabled }

// SaveToDisk reports whether processors for this queue should persist
// results to disk (used by Phase 4 storage helpers).
func (q *Queue) SaveToDisk() bool { return q.cfg.SaveToDisk }

// Submit creates a new Job and enqueues it. The returned Job is safe to
// inspect (it is the same instance the worker mutates), but callers should
// not modify it directly — read fields under Get() or wait via Done().
func (q *Queue) Submit(path string, body []byte, header http.Header) (*Job, error) {
	return q.submit(path, body, header, nil)
}

// SubmitStream is Submit for sentence-chunk streaming jobs: it marks Stream and
// the segmenter mode on the job BEFORE it enters the queue, so a worker can
// never dequeue it in buffer mode (avoids a Stream-flag race). chunkMode ""
// uses the segmenter default. See docs/TODO/infer-streaming.md.
func (q *Queue) SubmitStream(path string, body []byte, header http.Header, chunkMode string) (*Job, error) {
	return q.submit(path, body, header, func(j *Job) {
		j.Stream = true
		j.ChunkMode = chunkMode
	})
}

// submit is the shared core of Submit/SubmitStream. init (nil for plain Submit)
// runs on the freshly-built job before it enters the pending queue.
func (q *Queue) submit(path string, body []byte, header http.Header, init func(*Job)) (*Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	// req_id (client-supplied idempotency key, X-ISANN-Request-Id). When present
	// the job is keyed by it, so a retry with the same req_id returns the
	// existing job instead of running a duplicate — this is what survives a lost
	// submit-ack / mobile reconnect. Absent or malformed → server-generated id
	// (legacy behaviour). Dedup is checked BEFORE the capacity gate so a retry
	// of an accepted job is never rejected as "queue full".
	id := sanitizeReqID(header.Get("X-ISANN-Request-Id"))
	if id != "" {
		if existing, ok := q.jobs[id]; ok {
			return existing, nil // idempotent: same req_id → same job
		}
	} else {
		id = generateID()
	}

	if q.cfg.MaxQueue > 0 && len(q.pending)+len(q.running) >= q.cfg.MaxQueue {
		return nil, ErrQueueFull
	}

	job := &Job{
		ID:            id,
		ServiceName:   q.cfg.ServiceName,
		Status:        StatusQueued,
		Path:          path,
		RequestBody:   body,
		RequestHeader: header.Clone(),
		CreatedAt:     time.Now(),
		doneCh:        make(chan struct{}),
	}
	if init != nil {
		init(job)
	}
	q.jobs[job.ID] = job
	q.pending = append(q.pending, job.ID)
	job.Position = len(q.pending) - 1

	if q.events != nil {
		q.events.Emit("job.received", map[string]any{
			"id":      job.ID,
			"service": q.cfg.ServiceName,
			"path":    path,
		})
	}
	if q.onJobChange != nil {
		q.onJobChange(q.cfg.ServiceName, "received", job.ID, len(q.pending), len(q.running))
	}
	return job, nil
}

// Get returns a snapshot pointer of the job by ID. Callers should treat the
// result as read-only — do not write fields. Returns nil if not found.
func (q *Queue) Get(id string) *Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.jobs[id]
}

// Wait blocks until the job reaches done/failed or ctx is canceled.
func (q *Queue) Wait(ctx context.Context, id string) (*Job, error) {
	q.mu.Lock()
	job := q.jobs[id]
	q.mu.Unlock()
	if job == nil {
		return nil, fmt.Errorf("queue: job %s not found", id)
	}
	select {
	case <-job.doneCh:
		return job, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Stats returns aggregate metrics suitable for /v1/queue/stats.
func (q *Queue) Stats() Stats {
	q.mu.Lock()
	defer q.mu.Unlock()

	var avg float64
	if len(q.durations) > 0 {
		var sum time.Duration
		for _, d := range q.durations {
			sum += d
		}
		avg = sum.Seconds() / float64(len(q.durations))
	}

	estWait := 0
	if avg > 0 && q.cfg.Concurrency > 0 {
		estWait = int(float64(len(q.pending)) * avg / float64(q.cfg.Concurrency))
	}

	var runningID string
	for id := range q.running {
		runningID = id
		break
	}

	return Stats{
		Pending:          len(q.pending),
		Running:          len(q.running),
		RunningJobID:     runningID,
		EstimatedWaitSec: estWait,
		TotalJobsDone:    q.totalDone,
		TotalJobsFailed:  q.totalFailed,
		AvgJobSec:        avg,
	}
}

// UpdateRunningProgress applies a parser progress event to running jobs.
// When concurrency=1 (the only mode that supports accurate mapping) the
// single running job receives the update. Multi-concurrency falls back to
// updating all running jobs (best effort).
func (q *Queue) UpdateRunningProgress(step, total int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, job := range q.running {
		job.Step = step
		job.Total = total
		if total > 0 {
			job.Progress = step * 100 / total
			if job.Progress > 100 {
				job.Progress = 100
			}
		}
	}
}

// UpdateJobProgress targets a specific running job by ID. Returns false
// when the job isn't currently running (already done/failed, evicted, or
// belongs to a different queue). Used by the engine-runner → provider
// progress callback so updates can't bleed across services that happen
// to share a queue manager.
func (q *Queue) UpdateJobProgress(jobID string, step, total int) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	job, ok := q.running[jobID]
	if !ok {
		return false
	}
	job.Step = step
	job.Total = total
	if total > 0 {
		job.Progress = step * 100 / total
		if job.Progress > 100 {
			job.Progress = 100
		}
	}
	return true
}

// Worker processes pending jobs until ctx is canceled. Run in a goroutine.
// `process` should forward to the engine; events feeds parser progress.
func (q *Queue) Worker(ctx context.Context, process ProcessFunc) {
	sem := make(chan struct{}, q.cfg.Concurrency)
	gcTicker := time.NewTicker(time.Minute)
	defer gcTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-gcTicker.C:
			q.gc()
			continue
		default:
		}

		// Acquire a worker slot BEFORE dequeuing. dequeue() moves a job into
		// q.running, so pulling one while all slots are busy would count a job
		// that is merely waiting for a slot as "running" — inflating
		// running_count above Concurrency (e.g. 2 with Concurrency=1, most
		// visibly when the in-flight job is stuck on an offline engine).
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return
		}

		job := q.dequeue()
		if job == nil {
			<-sem // release the slot we grabbed; nothing to run
			// nothing to do — back off briefly
			select {
			case <-ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}

		go func(j *Job) {
			defer func() { <-sem }()
			q.runJob(ctx, j, process)
		}(job)
	}
}

// dequeue pops the next pending job and marks it running. Returns nil when
// the pending queue is empty. Emits job.started + onJobChange after the
// lock is released so callers do not block under q.mu.
func (q *Queue) dequeue() *Job {
	q.mu.Lock()
	if len(q.pending) == 0 {
		q.mu.Unlock()
		return nil
	}
	id := q.pending[0]
	q.pending = q.pending[1:]
	job := q.jobs[id]
	if job == nil {
		q.mu.Unlock()
		return nil
	}
	job.Status = StatusRunning
	job.Position = 0
	job.StartedAt = time.Now()
	q.running[id] = job
	// Reflow positions for the rest of the queue.
	for i, pid := range q.pending {
		if p := q.jobs[pid]; p != nil {
			p.Position = i
		}
	}
	events := q.events
	onChange := q.onJobChange
	serviceName := q.cfg.ServiceName
	jobID := job.ID
	pending := len(q.pending)
	running := len(q.running)
	q.mu.Unlock()

	if events != nil {
		events.Emit("job.started", map[string]any{
			"id":      jobID,
			"service": serviceName,
		})
	}
	if onChange != nil {
		onChange(serviceName, "started", jobID, pending, running)
	}
	return job
}

// runJob calls process and records the outcome. Always closes doneCh and
// updates aggregate metrics. Cleanup (running 제거 + doneCh close) happens
// BEFORE the onChange/events callback so that completed/failed depth values
// reflect "본인 제외" — i.e. pending=0 + running=0 → depth=0 (idle) when
// no other jobs are queued.
func (q *Queue) runJob(ctx context.Context, job *Job, process ProcessFunc) {
	code, ct, body, err := process(ctx, job)

	q.mu.Lock()
	if err != nil {
		job.Status = StatusFailed
		job.Error = err.Error()
		q.totalFailed++
	} else {
		job.Status = StatusDone
		job.Progress = 100
		if job.Total > 0 {
			job.Step = job.Total
		}
		job.ResponseCode = code
		job.ResponseType = ct
		job.ResponseBody = body
		// process() may already have set URL (when SaveToDisk persisted to disk).
		// Only fall back to the bare ID when the processor did not set one.
		if job.URL == "" {
			job.URL = "/outputs/" + job.ID
		}
		q.totalDone++
	}

	// Cleanup BEFORE callbacks so depth = pending+running excludes self.
	delete(q.running, job.ID)
	job.EndedAt = time.Now()
	dur := job.EndedAt.Sub(job.StartedAt)
	q.durations = append(q.durations, dur)
	if len(q.durations) > 20 {
		q.durations = q.durations[1:]
	}
	close(job.doneCh)

	events := q.events
	onChange := q.onJobChange
	serviceName := q.cfg.ServiceName
	jobID := job.ID
	pending := len(q.pending)
	running := len(q.running)

	var event string
	var emitPayload map[string]any
	if job.Status == StatusFailed {
		event = "failed"
		emitPayload = map[string]any{
			"id":      jobID,
			"service": serviceName,
			"error":   job.Error,
		}
	} else {
		event = "completed"
		emitPayload = map[string]any{
			"id":          jobID,
			"service":     serviceName,
			"duration_ms": time.Since(job.StartedAt).Milliseconds(),
		}
	}
	q.mu.Unlock()

	if events != nil {
		events.Emit("job."+event, emitPayload)
	}
	if onChange != nil {
		onChange(serviceName, event, jobID, pending, running)
	}
}

// gc evicts expired done/failed jobs and trims to MaxDone.
func (q *Queue) gc() {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now()
	type entry struct {
		id  string
		end time.Time
	}
	var finished []entry
	for id, job := range q.jobs {
		if job.Status == StatusDone || job.Status == StatusFailed {
			finished = append(finished, entry{id, job.EndedAt})
		}
	}

	// TTL evict
	for _, e := range finished {
		if now.Sub(e.end) > q.cfg.DoneTTL {
			if j, ok := q.jobs[e.id]; ok && q.cleanupHook != nil {
				q.cleanupHook(j)
			}
			delete(q.jobs, e.id)
		}
	}

	// LRU trim
	if len(q.jobs) > q.cfg.MaxDone {
		var rest []entry
		for id, job := range q.jobs {
			if job.Status == StatusDone || job.Status == StatusFailed {
				rest = append(rest, entry{id, job.EndedAt})
			}
		}
		// Sort oldest first
		for i := 1; i < len(rest); i++ {
			for j := i; j > 0 && rest[j-1].end.After(rest[j].end); j-- {
				rest[j-1], rest[j] = rest[j], rest[j-1]
			}
		}
		excess := len(q.jobs) - q.cfg.MaxDone
		for i := 0; i < excess && i < len(rest); i++ {
			if j, ok := q.jobs[rest[i].id]; ok && q.cleanupHook != nil {
				q.cleanupHook(j)
			}
			delete(q.jobs, rest[i].id)
		}
	}
}

// DeleteResult tells the caller why Delete did or did not act.
type DeleteResult int

const (
	DeleteOK          DeleteResult = iota // evicted
	DeleteNotFound                        // no such job
	DeleteStillActive                     // queued or running — cannot delete (no per-job cancel)
)

// Delete evicts a finished (done/failed) job from the queue. queued and
// running jobs are not deletable because there is no per-job cancel
// mechanism today — caller should treat StillActive as 409 Conflict.
func (q *Queue) Delete(id string) DeleteResult {
	q.mu.Lock()
	defer q.mu.Unlock()

	job, ok := q.jobs[id]
	if !ok {
		return DeleteNotFound
	}
	if job.Status != StatusDone && job.Status != StatusFailed {
		return DeleteStillActive
	}
	if q.cleanupHook != nil {
		q.cleanupHook(job)
	}
	delete(q.jobs, id)
	return DeleteOK
}

// generateID returns a hex-encoded random job id (12 chars).
func generateID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// sanitizeReqID validates a client-supplied request id (idempotency key) for
// use as the job id. It appears verbatim in /v1/jobs/{id} paths and as a map
// key, so only a bounded URL-safe token is accepted; anything else returns ""
// so Submit falls back to a generated id. Charset: [A-Za-z0-9._-], 1..128.
func sanitizeReqID(s string) string {
	if len(s) == 0 || len(s) > 128 {
		return ""
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '.' || c == '-' || c == '_'
		if !ok {
			return ""
		}
	}
	return s
}
