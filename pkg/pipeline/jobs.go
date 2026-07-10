package pipeline

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sort"
	"sync"
	"time"
)

// Job is one async pipeline execution. Fields are updated by a goroutine
// while the pipeline runs — readers must take the JobStore mutex or use
// the Snapshot() helper for a consistent view.
type Job struct {
	ID          string
	Status      string // queued | running | done | failed | cancelled
	Graph       *Graph
	StepResults map[string]any
	Steps       []StepResult
	CurrentStep string
	Progress    int // completed / total (0-100)
	Error       string
	CreatedAt   time.Time
	StartedAt   time.Time
	EndedAt     time.Time

	cancel context.CancelFunc
	doneCh chan struct{}
}

// JobSnapshot is an immutable view of a Job's fields, suitable for JSON
// encoding in API responses. It does not include the cancel function or
// doneCh — those are internal.
type JobSnapshot struct {
	ID          string         `json:"id"`
	Status      string         `json:"status"`
	CurrentStep string         `json:"current,omitempty"`
	Progress    int            `json:"progress"`
	StepResults map[string]any `json:"stepResults"`
	Steps       []StepResult   `json:"steps"`
	Error       string         `json:"error,omitempty"`
	CreatedAt   time.Time      `json:"createdAt"`
	StartedAt   time.Time      `json:"startedAt,omitempty"`
	EndedAt     time.Time      `json:"endedAt,omitempty"`
}

// Snapshot returns a copy of the job's visible state (map slices copied).
func (j *Job) Snapshot() JobSnapshot {
	stepsCopy := make([]StepResult, len(j.Steps))
	copy(stepsCopy, j.Steps)
	resCopy := make(map[string]any, len(j.StepResults))
	for k, v := range j.StepResults {
		resCopy[k] = v
	}
	return JobSnapshot{
		ID:          j.ID,
		Status:      j.Status,
		CurrentStep: j.CurrentStep,
		Progress:    j.Progress,
		StepResults: resCopy,
		Steps:       stepsCopy,
		Error:       j.Error,
		CreatedAt:   j.CreatedAt,
		StartedAt:   j.StartedAt,
		EndedAt:     j.EndedAt,
	}
}

// Done returns a channel closed when the job finishes (done/failed/cancelled).
func (j *Job) Done() <-chan struct{} { return j.doneCh }

// JobStoreConfig tunes retention. Zero values use sensible defaults.
type JobStoreConfig struct {
	MaxDone int           // LRU cap for completed jobs (default 100)
	DoneTTL time.Duration // how long to keep completed jobs (default 30min)
	GCEvery time.Duration // gc interval (default 1min)
}

func (c JobStoreConfig) withDefaults() JobStoreConfig {
	if c.MaxDone <= 0 {
		c.MaxDone = 100
	}
	if c.DoneTTL <= 0 {
		c.DoneTTL = 30 * time.Minute
	}
	if c.GCEvery <= 0 {
		c.GCEvery = time.Minute
	}
	return c
}

// JobStore is a goroutine-safe async pipeline job store.
type JobStore struct {
	cfg  JobStoreConfig
	mu   sync.Mutex
	jobs map[string]*Job
	stop chan struct{}
}

// NewJobStore constructs a JobStore and starts its background GC loop.
// Callers should Close() it during shutdown to stop the GC goroutine.
func NewJobStore(cfg JobStoreConfig) *JobStore {
	s := &JobStore{
		cfg:  cfg.withDefaults(),
		jobs: make(map[string]*Job),
		stop: make(chan struct{}),
	}
	go s.gcLoop()
	return s
}

// Close stops the background GC goroutine. Safe to call multiple times.
func (s *JobStore) Close() {
	select {
	case <-s.stop:
		return
	default:
		close(s.stop)
	}
}

// Submit creates a queued job, starts a goroutine to execute it, and
// returns immediately. The caller's ctx controls cancellation of the job
// (a derived context is passed to runner.Run).
func (s *JobStore) Submit(ctx context.Context, g *Graph, runner *Runner) *Job {
	jobCtx, cancel := context.WithCancel(context.Background())
	// ctx cancellation also cancels the job
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-jobCtx.Done():
		}
	}()

	total := len(g.Nodes)
	job := &Job{
		ID:          generateID(),
		Status:      "queued",
		Graph:       g,
		StepResults: make(map[string]any),
		Steps:       []StepResult{},
		CreatedAt:   time.Now(),
		cancel:      cancel,
		doneCh:      make(chan struct{}),
	}

	s.mu.Lock()
	s.jobs[job.ID] = job
	s.mu.Unlock()

	go func() {
		defer close(job.doneCh)
		defer cancel()

		s.mu.Lock()
		job.Status = "running"
		job.StartedAt = time.Now()
		s.mu.Unlock()

		onProgress := func(stepID string, result StepResult, partial map[string]any) {
			s.mu.Lock()
			job.CurrentStep = stepID
			job.Steps = append(job.Steps, result)
			if total > 0 {
				job.Progress = len(job.Steps) * 100 / total
				if job.Progress > 100 {
					job.Progress = 100
				}
			}
			// mirror partial results (copy, since it may be mutated further)
			for k, v := range partial {
				job.StepResults[k] = v
			}
			s.mu.Unlock()
		}

		resp := runner.Run(jobCtx, g, onProgress)

		s.mu.Lock()
		defer s.mu.Unlock()
		job.EndedAt = time.Now()
		// Overwrite with the runner's final maps/slices so we have the full state.
		if resp.StepResults != nil {
			job.StepResults = resp.StepResults
		}
		if resp.Steps != nil {
			job.Steps = resp.Steps
		}
		job.CurrentStep = ""

		switch {
		case jobCtx.Err() == context.Canceled:
			job.Status = "cancelled"
			if job.Error == "" {
				job.Error = "cancelled"
			}
		case resp.Error != "" || len(resp.ValidationErrors) > 0:
			job.Status = "failed"
			if resp.Error != "" {
				job.Error = resp.Error
			} else {
				job.Error = "validation failed"
			}
		default:
			job.Status = "done"
			if total > 0 {
				job.Progress = 100
			}
		}
	}()

	return job
}

// Get returns a pointer to the job (callers must not mutate) or nil.
func (s *JobStore) Get(id string) *Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jobs[id]
}

// Snapshot returns a JSON-friendly view of the job's current state.
func (s *JobStore) Snapshot(id string) (JobSnapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return JobSnapshot{}, false
	}
	return j.Snapshot(), true
}

// Wait blocks until the job finishes or ctx is cancelled.
func (s *JobStore) Wait(ctx context.Context, id string) (*Job, error) {
	s.mu.Lock()
	job := s.jobs[id]
	s.mu.Unlock()
	if job == nil {
		return nil, ErrJobNotFound
	}
	select {
	case <-job.doneCh:
		return job, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Cancel aborts a running job. Returns true when the job existed and was
// in a cancellable state (queued/running).
func (s *JobStore) Cancel(id string) bool {
	s.mu.Lock()
	job := s.jobs[id]
	s.mu.Unlock()
	if job == nil {
		return false
	}
	if job.cancel != nil {
		job.cancel()
	}
	return true
}

// List returns snapshots of all known jobs (live + done).
func (s *JobStore) List() []JobSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]JobSnapshot, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, j.Snapshot())
	}
	return out
}

// gcLoop runs gc periodically until Close().
func (s *JobStore) gcLoop() {
	t := time.NewTicker(s.cfg.GCEvery)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			s.gc()
		}
	}
}

// gc evicts expired/over-cap completed jobs.
func (s *JobStore) gc() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	type entry struct {
		id   string
		when time.Time
	}
	var finished []entry
	for id, j := range s.jobs {
		if isTerminal(j.Status) {
			finished = append(finished, entry{id, j.EndedAt})
		}
	}

	// TTL evict
	for _, e := range finished {
		j := s.jobs[e.id]
		if j == nil {
			continue
		}
		if !j.EndedAt.IsZero() && now.Sub(j.EndedAt) > s.cfg.DoneTTL {
			delete(s.jobs, e.id)
		}
	}

	// LRU trim — rebuild finished list after TTL evict
	finished = finished[:0]
	for id, j := range s.jobs {
		if isTerminal(j.Status) {
			finished = append(finished, entry{id, j.EndedAt})
		}
	}
	if len(finished) <= s.cfg.MaxDone {
		return
	}
	sort.Slice(finished, func(i, j int) bool {
		return finished[i].when.Before(finished[j].when)
	})
	excess := len(finished) - s.cfg.MaxDone
	for i := 0; i < excess; i++ {
		delete(s.jobs, finished[i].id)
	}
}

func isTerminal(status string) bool {
	return status == "done" || status == "failed" || status == "cancelled"
}

// generateID returns a 12-char hex job id.
func generateID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ErrJobNotFound signals a missing job id.
type pipelineError string

func (e pipelineError) Error() string { return string(e) }

const (
	ErrJobNotFound pipelineError = "pipeline: job not found"
)
