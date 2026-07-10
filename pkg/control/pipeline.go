package control

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/isannai/mesh/pkg/pipeline"
)

// handlePipelineExecute handles POST /v1/pipeline/execute.
//
// Default mode is async: a job is queued and the handler returns 202 with
// {job_id, status}. Clients then poll /v1/pipeline/jobs/{id} for progress
// and /v1/pipeline/jobs/{id}/result for the final response.
//
// Pass ?wait=true to run synchronously and receive the full ExecuteResponse
// inline. Long pipelines may exceed proxy/HTTP timeouts in this mode — use
// async unless the graph is known to be fast.
func (b *Broker) handlePipelineExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var graph pipeline.Graph
	if err := json.NewDecoder(r.Body).Decode(&graph); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	wait := r.URL.Query().Get("wait") == "true"
	w.Header().Set("Content-Type", "application/json")

	if wait {
		// Sync: block until pipeline completes.
		resp := b.pipelineRunner.Run(r.Context(), &graph, nil)
		if resp.Error != "" || len(resp.ValidationErrors) > 0 {
			w.WriteHeader(http.StatusUnprocessableEntity)
		}
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	// Async (default): queue the job, return 202 + job_id.
	// Use background context so the job survives client disconnect.
	job := b.pipelineJobs.Submit(r.Context(), &graph, b.pipelineRunner)
	w.Header().Set("X-Job-Id", job.ID)
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"job_id": job.ID,
		"status": job.Status,
	})
}

// handlePipelineJobs lists known async jobs (active + completed within TTL).
func (b *Broker) handlePipelineJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(b.pipelineJobs.List())
}

// handlePipelineJobByID dispatches /v1/pipeline/jobs/{id} and {id}/result.
//
//	GET    /v1/pipeline/jobs/{id}         — current snapshot (status + partial results)
//	GET    /v1/pipeline/jobs/{id}/result  — final ExecuteResponse (409 when not done)
//	DELETE /v1/pipeline/jobs/{id}         — cancel
func (b *Broker) handlePipelineJobByID(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/pipeline/jobs/")
	if rest == "" {
		// Trailing slash without id → list
		b.handlePipelineJobs(w, r)
		return
	}
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	suffix := ""
	if len(parts) > 1 {
		suffix = parts[1]
	}

	switch r.Method {
	case http.MethodGet:
		snap, ok := b.pipelineJobs.Snapshot(id)
		if !ok {
			http.Error(w, "job not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch suffix {
		case "":
			_ = json.NewEncoder(w).Encode(snap)
		case "result":
			if snap.Status != "done" && snap.Status != "failed" && snap.Status != "cancelled" {
				http.Error(w, "job not finished", http.StatusConflict)
				return
			}
			_ = json.NewEncoder(w).Encode(pipeline.ExecuteResponse{
				StepResults: snap.StepResults,
				Steps:       snap.Steps,
				Error:       snap.Error,
			})
		default:
			http.Error(w, "unknown sub-path", http.StatusNotFound)
		}

	case http.MethodDelete:
		if !b.pipelineJobs.Cancel(id) {
			http.Error(w, "job not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "cancelled"})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handlePipelineEntities lists all registered entity types and their I/O schema.
// Used by the web UI to populate the node palette.
func (b *Broker) handlePipelineEntities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(b.pipelineRegistry.Describe())
}

// pipelineSelfBaseURL returns the broker's own base URL. Kept for legacy
// ExecCtx.BaseURL compatibility; AI entities now use ExecCtx.NodeCaller to
// reach provider services directly (no HTTP loopback), so the actual
// scheme/host here is not used by the primary call path.
func (b *Broker) pipelineSelfBaseURL() string {
	scheme := "http"
	if b.Cfg.TLS.Enabled {
		scheme = "https"
	}
	addr := b.Cfg.ListenAddr
	if addr == "" {
		addr = ":8080"
	}
	if strings.HasPrefix(addr, ":") {
		return scheme + "://127.0.0.1" + addr
	}
	if strings.HasPrefix(addr, "0.0.0.0:") {
		return scheme + "://127.0.0.1" + strings.TrimPrefix(addr, "0.0.0.0")
	}
	return scheme + "://" + addr
}

