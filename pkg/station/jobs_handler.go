// Phase 7 of the queue migration (docs/TODO/queue-migration-phases.md):
// HTTP handlers exposing the Provider's QueueManager + Storage to broker.
//
// These handlers are isolated and testable today. Wiring them into Provider's
// QUIC stream dispatcher (so broker requests actually flow through them) is
// the follow-up step — see queue-migration-phases.md Phase 7 final notes.
package station

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/isannai/mesh/pkg/auth"
	"github.com/isannai/mesh/pkg/engine/manifest"
	"github.com/isannai/mesh/pkg/station/queue"
	"github.com/isannai/mesh/pkg/setup"
)

// clampTimeoutSecs parses a ?timeout=<seconds> value and clamps it to a sane
// range. Returns 0 (= no override, use the request context as-is) for an
// empty / invalid / non-positive value. Shared convention with the isannd
// infer proxy so a sync (wait=true) submit can extend past the default deadline.
func clampTimeoutSecs(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return 0
	}
	if n > 600 {
		return 600 // 10 min ceiling
	}
	return n
}

// JobsHandler answers /v1/jobs* and /outputs/* requests against a
// QueueManager + Storage pair. Construct one per Provider instance.
type JobsHandler struct {
	mgr     *queue.Manager
	storage *queue.Storage

	// services is the live service set; swapped by SetServices on reload
	// (auto-wire), read under svcMu by lookupService on each submit.
	svcMu    sync.RWMutex
	services []setup.ServiceEntry

	// apiFor resolves a service's manifest api block (M1). Used at submit time
	// to map generic run-params onto the engine body, select a run variant by
	// path, gate the path against the allowlist, and apply any wire encoding.
	// nil disables all of that (tests / legacy callers that send the engine
	// body verbatim).
	apiFor func(service string) *manifest.APISpec

	// engineDown reports whether a service's engine is currently unable to
	// serve (lifecycle phase stopped/stopping). Submissions to a down engine
	// fail fast (503) instead of queuing a job that can only fail at dispatch.
	// nil disables the gate (tests). docs/TODO/isann-cli-phase3.md.
	engineDown func(service string) bool
}

// NewJobsHandler builds a handler. Pass nil services for tests; production
// callers pass p.Cfg.Services so /v1/jobs can resolve the target service
// entry by name. apiFor resolves the manifest api block for generic-param
// mapping / path gating / wire encoding (pass nil to disable — the body is
// then forwarded verbatim).
func NewJobsHandler(mgr *queue.Manager, storage *queue.Storage, services []setup.ServiceEntry, apiFor func(string) *manifest.APISpec, engineDown func(string) bool) *JobsHandler {
	return &JobsHandler{mgr: mgr, storage: storage, services: services, apiFor: apiFor, engineDown: engineDown}
}

// apiSpec resolves the manifest api block for a service, or nil.
func (h *JobsHandler) apiSpec(service string) *manifest.APISpec {
	if h.apiFor == nil {
		return nil
	}
	return h.apiFor(service)
}

// Register attaches all queue-related routes to mux. Idempotent: callers
// must use a fresh ServeMux. Routes:
//
//	POST /v1/jobs                — submit job (returns 202 + job_id, or 429)
//	GET  /v1/jobs/{id}           — status JSON
//	GET  /v1/jobs/{id}/result    — body or file stream
//	GET  /outputs/{filename}     — disk file stream (Storage-backed)
//	GET  /v1/queue/stats?service=NAME — single-service queue stats
func (h *JobsHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/v1/jobs", h.handleSubmit)        // POST
	mux.HandleFunc("/v1/jobs/", h.handleByID)         // GET (id, id/result)
	mux.HandleFunc("/outputs/", h.handleOutputs)      // GET
	mux.HandleFunc("/v1/queue/stats", h.handleStats)  // GET
}

// submitRequest is the POST /v1/jobs body shape.
type submitRequest struct {
	Service string          `json:"service"`        // required
	Path    string          `json:"path,omitempty"` // optional override; defaults to "/v1/inference"
	Params  json.RawMessage `json:"params"`         // engine-shaped body, forwarded verbatim (legacy/broker)
	Run     json.RawMessage `json:"run,omitempty"`  // generic run-params (by CLI name); mapped via api.run (M1)
	Wait    bool            `json:"wait,omitempty"` // sync mode (block until done)

	// Stream = sentence-chunk streaming (async pattern): the worker reads the
	// engine's SSE stream and accumulates sentence chunks on the job; the
	// client polls status (chunk_count) and fetches chunks. Returns job_id at
	// once like a normal async submit. See docs/TODO/infer-streaming.md.
	Stream    bool   `json:"stream,omitempty"`
	ChunkMode string `json:"chunk_mode,omitempty"` // strict | sentence | low_latency ("" = default)
}

// submitResponse is the 202 success body.
type submitResponse struct {
	JobID      string `json:"job_id"`
	Service    string `json:"service"`
	Position   int    `json:"position"`
	QueueDepth int    `json:"queue_depth"`
	QueueMax   int    `json:"queue_max"`
}

// errorResponse is the 4xx/5xx body.
type errorResponse struct {
	Error      string `json:"error"`
	QueueDepth int    `json:"queue_depth,omitempty"`
	QueueMax   int    `json:"queue_max,omitempty"`
}

// handleSubmitForService is handleSubmit's variant for callers that have
// already resolved the target service via the URL (e.g. /svc/<name>/v1/jobs).
// Overrides the body's service field with svcName so callers don't have to
// repeat the name in the body. The body's other fields (path, params, wait)
// are honored as usual.
func (h *JobsHandler) handleSubmitForService(w http.ResponseWriter, r *http.Request, svcName string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "read body: " + err.Error()})
		return
	}
	// Parse loosely, overwrite service, re-serialize. Empty body → minimal req
	// with just the service set; downstream defaults handle path = /v1/inference.
	var raw map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &raw); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON: " + err.Error()})
			return
		}
	}
	if raw == nil {
		raw = map[string]any{}
	}
	raw["service"] = svcName
	patched, _ := json.Marshal(raw)
	r.Body = io.NopCloser(strings.NewReader(string(patched)))
	r.ContentLength = int64(len(patched))
	h.handleSubmit(w, r)
}

// handleSubmit handles POST /v1/jobs.
func (h *JobsHandler) handleSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "read body: " + err.Error()})
		return
	}
	var req submitRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON: " + err.Error()})
		return
	}
	if req.Service == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "service required"})
		return
	}
	svc, ok := h.lookupService(req.Service)
	if !ok {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "service not found: " + req.Service})
		return
	}
	// Fail fast when the engine is known to be down (lifecycle phase
	// stopped/stopping) — otherwise we'd queue a job that can only fail at
	// dispatch. Reads the watcher's cached phase, so no probe wakes a stopped
	// WSL/engine. docs/TODO/isann-cli-phase3.md.
	if h.engineDown != nil && h.engineDown(svc.Name) {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{
			Error: "engine not running: " + svc.Name + " — start it with 'isann docker start <engine>' and wait until ready",
		})
		return
	}
	q := h.mgr.GetOrCreate(svc)

	api := h.apiSpec(req.Service)

	// Path allowlist: a caller-supplied engine path must be declared in the
	// service manifest (a proxy_route, the default run, or a run variant).
	// Blocks a submission from forwarding to an arbitrary engine URL. Skipped
	// when no manifest is wired (tests submit the body verbatim).
	if req.Path != "" && api != nil && !api.AllowsPath(req.Path) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "path not allowed for service " + req.Service + ": " + req.Path})
		return
	}

	jobPath := req.Path
	if jobPath == "" {
		jobPath = "/v1/inference"
	}
	jobBody := []byte(req.Params)
	submitHeader := r.Header.Clone()

	// M1: generic run-params → engine body via the manifest's api.run template.
	// The CLI submits {"run": {...}} (params by CLI name); the door maps them
	// onto the engine's request shape and endpoint, so a new engine = manifest,
	// no isann patch (docs/TODO/isann-cli-phase3.md).
	if len(req.Run) > 0 {
		if api == nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "service has no api.run schema: " + req.Service})
			return
		}
		rs := api.RunFor(req.Path) // variant by submit path, else default run
		if rs == nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "service has no api.run schema: " + req.Service})
			return
		}
		var runParams map[string]any
		if err := json.Unmarshal(req.Run, &runParams); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid run params: " + err.Error()})
			return
		}
		mapped, err := rs.BuildBody(runParams)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
			return
		}
		// extra_args: relocate body fields into the prompt for engines that
		// ignore top-level params (sd.cpp). No-op when not declared.
		if rs.ExtraArgs != nil {
			var bm map[string]any
			if json.Unmarshal(mapped, &bm) == nil {
				rs.ExtraArgs.Apply(bm)
				if b, e := json.Marshal(bm); e == nil {
					mapped = b
				}
			}
		}
		jobBody = mapped
		jobPath = rs.Path

		// Wire encoding at the engine edge: transcode the JSON body into the
		// engine's native format (e.g. multipart for sd.cpp img2img /v1/images/
		// edits) when the target route declares one. Params stay JSON everywhere
		// upstream (preset injection / run-mapping / extra_args all operate on
		// JSON) — the non-JSON serialization lives ONLY here. The dispatcher then
		// forwards the encoded body + Content-Type verbatim. See
		// docs/confirm/20260704/sd-img2img-queue-plan.md.
		if enc := api.EncodingFor(rs.Path); enc != nil {
			encoded, ctype, eerr := encodeEngineBody(jobBody, enc)
			if eerr != nil {
				writeJSON(w, http.StatusBadRequest, errorResponse{Error: "encode " + enc.Type + " body: " + eerr.Error()})
				return
			}
			jobBody = encoded
			submitHeader.Set("Content-Type", ctype)
		}
	}

	// Stream mode: ensure the engine sees stream:true (so it emits SSE), and
	// mark the job for sentence-chunk accumulation before it enters the queue.
	var job *queue.Job
	if req.Stream {
		jobBody = ensureStreamFlag(jobBody)
		job, err = q.SubmitStream(jobPath, jobBody, submitHeader, req.ChunkMode)
	} else {
		job, err = q.Submit(jobPath, jobBody, submitHeader)
	}
	if job != nil {
		// Record the wallet-authenticated submitter (empty for anonymous).
		// Identity is recovered directly from the IANN signature here (M0,
		// docs/TODO/isann-cli-phase3.md) — the broker-direct CLI path has no
		// broker to vouch via X-Caller-Address, so the door recovers itself.
		job.SubmitterAddress = recoverCaller(r)
		// ?timeout=<sec> also bounds the engine call itself (not just this
		// handler's wait): on expiry the worker cancels the engine connection
		// so a stalled call can't wedge the queue. Absent → service default
		// (docs/bugs/2026-07-30-queue-worker-wedge-on-stream-stall.md).
		if secs := clampTimeoutSecs(r.URL.Query().Get("timeout")); secs > 0 {
			job.Timeout = time.Duration(secs) * time.Second
		}
	}
	if err == queue.ErrQueueFull {
		stats := q.Stats()
		writeJSON(w, http.StatusTooManyRequests, errorResponse{
			Error:      "queue_full",
			QueueDepth: stats.Pending + stats.Running,
			QueueMax:   q.MaxQueue(),
		})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error()})
		return
	}

	// Stream jobs always detach (async pattern) — the client polls status /
	// chunk and fetches result on done, so -wait is ignored for them.
	if req.Wait && !req.Stream {
		// ?timeout=<sec> bounds how long this sync wait blocks — a long image gen
		// needs more than the caller's default. Absent → the bare request context
		// (client disconnect still cancels). On expiry q.Wait returns ctx.Err →
		// a clean 504 below instead of the connection just dropping.
		waitCtx := r.Context()
		if secs := clampTimeoutSecs(r.URL.Query().Get("timeout")); secs > 0 {
			var cancel context.CancelFunc
			waitCtx, cancel = context.WithTimeout(waitCtx, time.Duration(secs)*time.Second)
			defer cancel()
		}
		done, werr := q.Wait(waitCtx, job.ID)
		if werr != nil {
			writeJSON(w, http.StatusGatewayTimeout, errorResponse{Error: werr.Error()})
			return
		}
		// A failed job carries its message on job.Error (ResponseCode/Body stay
		// unset), so surface it as an error — the SAME shape the /result GET
		// returns — instead of an empty 200 a sync caller can't tell from success.
		if done.Status == queue.StatusFailed {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: done.Error})
			return
		}
		// Stream the actual result body back (status code from upstream).
		ct := done.ResponseType
		if ct == "" {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)
		// Echo job_id so callers using ?wait=true can correlate / cleanup later.
		w.Header().Set("X-Job-ID", job.ID)
		consume := r.URL.Query().Get("consume") == "true"
		if done.ResponseFile != "" {
			http.ServeFile(w, r, done.ResponseFile)
		} else {
			if done.ResponseCode > 0 {
				w.WriteHeader(done.ResponseCode)
			}
			_, _ = w.Write(done.ResponseBody)
		}
		if consume {
			h.deleteJob(job.ID)
		}
		return
	}

	stats := q.Stats()
	writeJSON(w, http.StatusAccepted, submitResponse{
		JobID:      job.ID,
		Service:    svc.Name,
		Position:   job.Position,
		QueueDepth: stats.Pending + stats.Running,
		QueueMax:   q.MaxQueue(),
	})
}

// handleByID handles /v1/jobs/{id}, /v1/jobs/{id}/result, and
// DELETE /v1/jobs/{id}. (ServeMux dispatches by prefix only, so all
// methods land here.)
func (h *JobsHandler) handleByID(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		h.handleDelete(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/v1/jobs/")
	if rest == "" {
		http.NotFound(w, r)
		return
	}
	wantResult := false
	wantChunk := false
	switch {
	case strings.HasSuffix(rest, "/result"):
		wantResult = true
		rest = strings.TrimSuffix(rest, "/result")
	case strings.HasSuffix(rest, "/chunk"):
		wantChunk = true
		rest = strings.TrimSuffix(rest, "/chunk")
	}

	// Search every queue for the job ID — JobIDs are globally unique enough
	// (12 hex chars) that scanning O(N services) is fine at our scale.
	job, _ := h.lookupJob(rest)
	if job == nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "job not found"})
		return
	}

	if !authorizeJob(r, job) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
		return
	}

	if wantChunk {
		h.serveChunk(w, r, job)
		return
	}

	if !wantResult {
		writeJSON(w, http.StatusOK, job)
		return
	}

	consume := r.URL.Query().Get("consume") == "true"

	// /result — body or file stream.
	if job.Status != queue.StatusDone && job.Status != queue.StatusFailed {
		writeJSON(w, http.StatusAccepted, job) // not done yet — consume is ignored
		return
	}
	if job.Status == queue.StatusFailed {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: job.Error})
		if consume {
			h.deleteJob(job.ID)
		}
		return
	}
	ct := job.ResponseType
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	if job.ResponseFile != "" {
		http.ServeFile(w, r, job.ResponseFile)
	} else {
		_, _ = w.Write(job.ResponseBody)
	}
	if consume {
		h.deleteJob(job.ID)
	}
}

// handleDelete handles DELETE /v1/jobs/{id}. Removes a finished
// (done/failed) job from the queue and triggers the cleanup hook so
// disk artifacts are deleted. Queued/running jobs return 409 because
// there is no per-job cancel mechanism today.
func (h *JobsHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/jobs/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	// Ownership check before evicting. lookupJob also covers the
	// not-found path so we don't leak existence to non-owners.
	job, _ := h.lookupJob(id)
	if job == nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "job not found"})
		return
	}
	if !authorizeJob(r, job) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
		return
	}
	switch h.deleteJob(id) {
	case queue.DeleteOK:
		w.WriteHeader(http.StatusNoContent)
	case queue.DeleteNotFound:
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "job not found"})
	case queue.DeleteStillActive:
		writeJSON(w, http.StatusConflict, errorResponse{Error: "cannot delete queued or running job"})
	}
}

// recoverCaller derives the submitter's wallet identity by recovering it
// directly from the request's IANN signature ("Authorization: ISANN <sig>"
// + "X-ISANN-Message"). The receiving door is the single source of truth —
// it does not trust any forwarded header (M0, docs/TODO/isann-cli-phase3.md).
//
// X-Caller-Address is intentionally never consulted: identity must be proven
// by a signature on the same request, so it cannot be spoofed by a header.
// Returns "" for anonymous requests (open-mode / free tier) and for any
// request whose signature is missing or invalid.
func recoverCaller(r *http.Request) string {
	sig := strings.TrimPrefix(r.Header.Get("Authorization"), "ISANN ")
	message := r.Header.Get("X-ISANN-Message")
	if sig == "" || message == "" {
		return ""
	}
	addr, err := auth.RecoverAddress(message, sig)
	if err != nil {
		return ""
	}
	return addr
}

// authorizeJob enforces per-job ownership. Returns true when the caller
// is allowed to fetch/delete the job's result.
//
//   - job.SubmitterAddress == ""  → anonymous submission, free tier
//     (anyone can read with the jobid). Returns true.
//   - job.SubmitterAddress != ""  → caller must match. Identity is recovered
//     from the IANN signature (recoverCaller) — the same source used at
//     submit — so the wallet that queued the job can fetch/delete it.
func authorizeJob(r *http.Request, job *queue.Job) bool {
	if job.SubmitterAddress == "" {
		return true
	}
	return strings.EqualFold(recoverCaller(r), job.SubmitterAddress)
}

// serveChunk answers GET /v1/jobs/{id}/chunk?index=n for streaming jobs: it
// returns the n-th completed sentence chunk as JSON. While the job is still
// running and the chunk hasn't been produced yet, returns 202 with the current
// chunk_count so the client can poll; once the job is done, an out-of-range
// index is 404. See docs/TODO/infer-streaming.md (M4/req 6).
// serveChunk answers GET /v1/jobs/{id}/chunk?index=n for streaming jobs.
//
// Indices 0..N-1 are sentence chunks (content). Index N — the (N+1)-th, only
// after the job is done — is the EOF marker: the message-excluded metadata
// (finish_reason / usage / model …) with "eof":true. So a sequential SDK
// next() reads 0,1,2,… and stops when it sees eof, without knowing N in
// advance. chunk_count keeps growing while the job runs, so index==count is
// "pending" (more sentences may come, or it becomes EOF once done), never EOF
// mid-stream. See docs/TODO/infer-streaming.md (M8).
func (h *JobsHandler) serveChunk(w http.ResponseWriter, r *http.Request, job *queue.Job) {
	idx, err := strconv.Atoi(r.URL.Query().Get("index"))
	if err != nil || idx < 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid index (want a non-negative integer)"})
		return
	}
	count := job.ChunkCount()
	done := job.Status == queue.StatusDone || job.Status == queue.StatusFailed

	// Content chunk.
	if content, ok := job.ChunkAt(idx); ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"job_id":      job.ID,
			"index":       idx,
			"content":     content,
			"chunk_count": count,
			"done":        done,
			"eof":         false,
		})
		return
	}

	// idx >= count — not (yet) a content chunk.
	if !done {
		// More sentences may still arrive, or this index becomes the EOF marker
		// once the job finishes — poll the same index again.
		writeJSON(w, http.StatusAccepted, map[string]any{"status": "pending", "chunk_count": count})
		return
	}
	// Done: index == count is the EOF marker (the (N+1)-th chunk) carrying the
	// message-excluded metadata; beyond that is out of range.
	if idx == count {
		var m map[string]any
		if meta := job.Meta(); len(meta) > 0 {
			_ = json.Unmarshal(meta, &m)
		}
		if m == nil {
			m = map[string]any{}
		}
		m["job_id"] = job.ID
		m["index"] = idx
		m["chunk_count"] = count
		m["done"] = true
		m["eof"] = true
		writeJSON(w, http.StatusOK, m)
		return
	}
	writeJSON(w, http.StatusNotFound, errorResponse{Error: "chunk index out of range"})
}

// ensureStreamFlag forces "stream": true into a JSON-object engine body so the
// engine emits SSE for a stream-mode job, and "stream_options.include_usage"
// so the final SSE chunk carries usage (OpenAI-compatible: llama-server /
// vLLM) — that usage is what surfaces in the reassembled result + the EOF
// chunk's metadata. Non-object / unparseable bodies are returned unchanged.
func ensureStreamFlag(body []byte) []byte {
	var m map[string]any
	if len(body) > 0 {
		if json.Unmarshal(body, &m) != nil {
			return body // not a JSON object — leave as-is
		}
	}
	if m == nil {
		m = map[string]any{}
	}
	m["stream"] = true
	if _, ok := m["stream_options"]; !ok {
		m["stream_options"] = map[string]any{"include_usage": true}
	}
	if b, err := json.Marshal(m); err == nil {
		return b
	}
	return body
}

// deleteJob scans all queues, deleting on the first match. Returns
// DeleteNotFound when no queue holds the id.
func (h *JobsHandler) deleteJob(id string) queue.DeleteResult {
	for _, name := range h.mgr.Names() {
		q := h.mgr.Get(name)
		if q == nil {
			continue
		}
		if res := q.Delete(id); res != queue.DeleteNotFound {
			return res
		}
	}
	return queue.DeleteNotFound
}

// handleOutputs serves /outputs/{filename}. Tries the on-disk Storage
// directory first; falls back to looking the job up by ID and streaming
// its in-memory ResponseBody when storage isn't configured (or the file
// is missing — e.g. cleaned up after TTL while the broker still has
// the URL cached).
func (h *JobsHandler) handleOutputs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/outputs/")
	name = path.Clean(name)
	if name == "" || name == "/" || strings.Contains(name, "..") {
		http.Error(w, "invalid filename", http.StatusBadRequest)
		return
	}

	consume := r.URL.Query().Get("consume") == "true"

	// Extract job ID from filename ({service}_{jobID}.{ext} per storage.Save).
	jobIDOf := func(n string) string {
		if dot := strings.LastIndexByte(n, '.'); dot > 0 {
			n = n[:dot]
		}
		if u := strings.LastIndexByte(n, '_'); u >= 0 {
			return n[u+1:]
		}
		return n
	}

	// Resolve owner first — both disk and memory paths need it.
	// We look the job up by id derived from the filename. If the queue
	// no longer holds the job (TTL evicted) we treat the artifact as
	// anonymous/free-tier (no ownership info available).
	id := jobIDOf(name)
	if job, _ := h.lookupJob(id); job != nil && !authorizeJob(r, job) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
		return
	}

	// Disk path: serve directly when storage + the file both exist.
	if h.storage != nil {
		full := path.Join(h.storage.Dir, name)
		// http.ServeFile would 404 on missing files which is fine, but
		// we want to fall through to the in-memory lookup below for
		// jobs that finished before storage was wired (or after TTL
		// cleaned them up). Probe with os.Stat first.
		if _, err := os.Stat(full); err == nil {
			http.ServeFile(w, r, full)
			if consume {
				h.deleteJob(id)
			}
			return
		}
	}

	// Memory fallback: serve ResponseBody from the queue.
	job, _ := h.lookupJob(id)
	if job == nil {
		http.Error(w, "result not found", http.StatusNotFound)
		return
	}
	if job.Status != queue.StatusDone {
		http.Error(w, "job not done", http.StatusConflict)
		return
	}
	ct := job.ResponseType
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	if len(job.ResponseBody) > 0 {
		_, _ = w.Write(job.ResponseBody)
	} else if job.ResponseFile != "" {
		http.ServeFile(w, r, job.ResponseFile)
	} else {
		http.Error(w, "result body unavailable", http.StatusGone)
		return
	}
	if consume {
		h.deleteJob(id)
	}
}

// handleStats handles GET /v1/queue/stats?service=NAME.
func (h *JobsHandler) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := r.URL.Query().Get("service")
	if name == "" {
		// No name → AllStats snapshot.
		writeJSON(w, http.StatusOK, h.mgr.AllStats())
		return
	}
	writeJSON(w, http.StatusOK, h.mgr.Stats(name))
}

// lookupService returns the ServiceEntry by name. False when not found.
func (h *JobsHandler) lookupService(name string) (setup.ServiceEntry, bool) {
	h.svcMu.RLock()
	defer h.svcMu.RUnlock()
	for _, s := range h.services {
		if s.Name == name {
			return s, true
		}
	}
	return setup.ServiceEntry{}, false
}

// SetServices swaps the live service set (auto-wire reload). In-flight jobs are
// unaffected — only future name→service resolutions see the new set.
func (h *JobsHandler) SetServices(svcs []setup.ServiceEntry) {
	h.svcMu.Lock()
	h.services = svcs
	h.svcMu.Unlock()
}

// lookupJob scans all queues for a job by ID.
func (h *JobsHandler) lookupJob(id string) (*queue.Job, string) {
	for _, name := range h.mgr.Names() {
		q := h.mgr.Get(name)
		if q == nil {
			continue
		}
		if j := q.Get(id); j != nil {
			return j, name
		}
	}
	return nil, ""
}

// writeJSON serializes payload as JSON with the given status code.
func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}
