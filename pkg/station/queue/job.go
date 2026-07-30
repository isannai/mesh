// Package queue implements an in-memory async job queue owned by Provider.
//
// Each incoming POST to a Provider's /v1/jobs endpoint is wrapped as a Job,
// queued FIFO per service, and processed by a worker that forwards the
// request to the underlying engine (engine-runner or external like vLLM).
// Clients receive a job_id immediately and can poll /v1/jobs/{id} for
// status and /v1/jobs/{id}/result (or /outputs/{filename}) for the final
// response body.
//
// HTTP session disconnect does not cancel the job — the worker continues in
// the background, and the result is preserved until the LRU/TTL evicts it.
//
// This package is a Phase 1 fork of pkg/engine/queue with these additions:
//   - Job.ServiceName — which service owns this job
//   - Config.ServiceName / SaveToDisk — per-queue identity and storage policy
//   - Config.MaxQueue (replacing MaxPending) — pending+running combined limit
package queue

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// JobStatus mirrors sd-api's status vocabulary for broker compatibility.
type JobStatus string

const (
	StatusQueued    JobStatus = "queued"
	StatusPreparing JobStatus = "preparing"
	StatusRunning   JobStatus = "running"
	StatusDone      JobStatus = "done"
	StatusFailed    JobStatus = "failed"
)

// Job is a single queued request. JSON tags stay wire-compatible with
// engine-runner's Job so existing broker/poller code keeps working when
// URLs swap from engine-runner to Provider.
type Job struct {
	ID          string    `json:"job_id"`
	ServiceName string    `json:"service,omitempty"` // owning service queue (Phase 2 dispatcher key)
	Status      JobStatus `json:"status"`
	Position    int       `json:"position,omitempty"`

	// Progress fields are flat (sd-api convention), not nested.
	Progress int `json:"progress"`
	Step     int `json:"step"`
	Total    int `json:"total"`

	// URL points to the result download path. Set when status=done.
	// Format: /outputs/{filename} (sd-api compatible).
	URL string `json:"url,omitempty"`

	// SubmitterAddress is the EOA address of the wallet-authenticated client
	// that submitted this job, recovered directly from the request's IANN
	// signature by the receiving door (recoverCaller — M0). Empty when the
	// submission was anonymous (free tier — no ownership gating). Used by
	// /v1/jobs/{id}*, /outputs/{filename}, and DELETE handlers to authorize
	// fetch/delete operations.
	SubmitterAddress string `json:"submitter_address,omitempty"`

	Error string `json:"error,omitempty"`

	// Internal fields (not serialized).
	Path          string        `json:"-"` // POST path the request was submitted to
	RequestBody   []byte        `json:"-"`
	RequestHeader http.Header   `json:"-"`
	ResponseBody  []byte        `json:"-"` // populated when no disk save; else empty
	ResponseFile  string        `json:"-"` // disk path when SaveToDisk=true
	ResponseCode  int           `json:"-"`
	ResponseType  string        `json:"-"` // Content-Type
	CreatedAt     time.Time     `json:"-"`
	StartedAt     time.Time     `json:"-"`
	EndedAt       time.Time     `json:"-"`
	doneCh        chan struct{} `json:"-"`

	// Streaming (sentence-chunked) fields. Set at submit time; the processor
	// reads the engine's SSE stream and appends completed sentences as chunks.
	// See docs/TODO/infer-streaming.md.
	Stream    bool   `json:"-"` // true = stream-chunk mode (processor accumulates sentence chunks)
	ChunkMode string `json:"-"` // segmenter mode: strict | sentence | low_latency (empty = default)

	// Timeout overrides the service default engine-call timeout for this job
	// (from the submit request's ?timeout=). Zero = use the service default.
	// Streaming jobs treat it as idle (no-chunk) time; buffered as total time.
	Timeout time.Duration `json:"-"`

	chunkMu sync.Mutex `json:"-"`
	chunks  []string   `json:"-"` // completed sentence chunks, in order
	meta    []byte     `json:"-"` // message-excluded metadata JSON (usage/finish_reason/…), set at stream completion
}

// Done returns a channel closed when the job reaches done/failed.
func (j *Job) Done() <-chan struct{} { return j.doneCh }

// AppendChunk records a completed sentence chunk (streaming mode). Safe for
// concurrent use with ChunkCount/ChunkAt/MarshalJSON.
func (j *Job) AppendChunk(s string) {
	j.chunkMu.Lock()
	j.chunks = append(j.chunks, s)
	j.chunkMu.Unlock()
}

// ChunkCount returns the number of completed sentence chunks so far.
func (j *Job) ChunkCount() int {
	j.chunkMu.Lock()
	defer j.chunkMu.Unlock()
	return len(j.chunks)
}

// ChunkAt returns the i-th completed chunk (0-based). ok=false when i is out
// of the current range (not produced yet, or beyond the final count).
func (j *Job) ChunkAt(i int) (string, bool) {
	j.chunkMu.Lock()
	defer j.chunkMu.Unlock()
	if i < 0 || i >= len(j.chunks) {
		return "", false
	}
	return j.chunks[i], true
}

// SetMeta stores the message-excluded metadata JSON (usage / finish_reason /
// model …) captured at stream completion. Read by the chunk endpoint's EOF
// marker (index -1).
func (j *Job) SetMeta(b []byte) {
	j.chunkMu.Lock()
	j.meta = b
	j.chunkMu.Unlock()
}

// Meta returns the metadata JSON set by SetMeta, or nil for a non-streaming job.
func (j *Job) Meta() []byte {
	j.chunkMu.Lock()
	defer j.chunkMu.Unlock()
	return j.meta
}

// MarshalJSON adds chunk_count to a streaming job's serialized form — always
// present for stream jobs (even 0, so a poller sees it from the first tick) and
// entirely absent for non-streaming jobs (unchanged wire). Pointer receiver —
// callers marshal *Job (handleByID passes the pointer). The count is read under
// chunkMu so it stays consistent with concurrent AppendChunk.
func (j *Job) MarshalJSON() ([]byte, error) {
	type alias Job // sheds MarshalJSON to avoid recursion; unexported fields are skipped by encoding/json
	if !j.Stream {
		return json.Marshal((*alias)(j))
	}
	j.chunkMu.Lock()
	n := len(j.chunks)
	j.chunkMu.Unlock()
	return json.Marshal(&struct {
		*alias
		ChunkCount int `json:"chunk_count"`
	}{(*alias)(j), n})
}
