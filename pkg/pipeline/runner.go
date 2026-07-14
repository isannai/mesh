package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/isannai/mesh/pkg/glog"
)

// Runner executes pipeline graphs. It holds the entity registry and
// shared transport / node caller used by entities that call into the
// broker (e.g., aiService).
type Runner struct {
	registry   *Registry
	httpClient *http.Client
	baseURL    string
	nodeCaller NodeCaller
	log        *glog.Logger
}

// NewRunner constructs a Runner. baseURL is kept as a legacy fallback for
// entities that still use HTTPClient + URL composition; new AI entities
// should go through ExecCtx.NodeCaller (set via SetNodeCaller) to bypass
// the HTTP loopback entirely.
func NewRunner(reg *Registry, baseURL string, log *glog.Logger) *Runner {
	return &Runner{
		registry: reg,
		baseURL:  baseURL,
		log:      log,
		httpClient: &http.Client{
			Timeout: 0, // no global timeout — long-running AI calls OK
		},
	}
}

// ProgressCallback is invoked after each step completes. stepID is the
// node that just finished; partial is the live stepResults map. Callers
// must not modify partial (it is the runner's internal state).
type ProgressCallback func(stepID string, result StepResult, partial map[string]any)

// Run executes the pipeline graph. onProgress may be nil when progress
// tracking is not needed (pure sync calls).
//
// When validation fails, Run returns early with ValidationErrors set and
// no steps executed. When a step fails, Run halts execution and returns
// with Error set; earlier results are preserved in StepResults.
func (r *Runner) Run(ctx context.Context, g *Graph, onProgress ProgressCallback) *ExecuteResponse {
	// Validate up front — cheap, catches most misconfiguration.
	if verrs := validateGraph(g, r.registry); len(verrs) > 0 {
		return &ExecuteResponse{
			ValidationErrors: verrs,
			StepResults:      map[string]any{},
			Steps:            []StepResult{},
			Error:            "graph validation failed",
		}
	}

	order := topoSort(g.Nodes, g.Edges)
	stepResults := make(map[string]any, len(order))
	steps := make([]StepResult, 0, len(order))

	nodeByID := make(map[string]Node, len(g.Nodes))
	for _, n := range g.Nodes {
		nodeByID[n.ID] = n
	}

	for _, nodeID := range order {
		if err := ctx.Err(); err != nil {
			return &ExecuteResponse{
				StepResults: stepResults,
				Steps:       steps,
				Error:       fmt.Sprintf("cancelled: %v", err),
			}
		}

		node, ok := nodeByID[nodeID]
		if !ok {
			continue
		}

		entity := r.registry.Get(node.Type)
		if entity == nil {
			// Unknown type → mark skipped but continue.
			step := StepResult{
				ID:     nodeID,
				Type:   node.Type,
				Status: "skipped",
				Reason: "no-entity",
			}
			steps = append(steps, step)
			if onProgress != nil {
				onProgress(nodeID, step, stepResults)
			}
			continue
		}

		ec := r.buildExecCtx(g, stepResults, nodeID)

		started := time.Now()
		result, err := entity.Execute(ctx, node, ec)
		durationMs := time.Since(started).Milliseconds()

		if err != nil {
			step := StepResult{
				ID:         nodeID,
				Type:       node.Type,
				Status:     "error",
				DurationMs: durationMs,
				Params:     redact(rawDecode(node.Data)),
				Inputs:     redactInputs(ec.InputsByHandle),
				Error:      err.Error(),
			}
			steps = append(steps, step)
			if onProgress != nil {
				onProgress(nodeID, step, stepResults)
			}
			return &ExecuteResponse{
				StepResults: stepResults,
				Steps:       steps,
				Error:       fmt.Sprintf("step %q (%s) failed: %v", nodeID, node.Type, err),
			}
		}

		stepResults[nodeID] = result
		step := StepResult{
			ID:         nodeID,
			Type:       node.Type,
			Status:     "done",
			DurationMs: durationMs,
			Params:     redact(rawDecode(node.Data)),
			Inputs:     redactInputs(ec.InputsByHandle),
			Output:     redact(result),
		}
		steps = append(steps, step)
		if onProgress != nil {
			onProgress(nodeID, step, stepResults)
		}
	}

	return &ExecuteResponse{
		StepResults: stepResults,
		Steps:       steps,
	}
}

// buildExecCtx assembles the per-node context: primary input (first
// non-config edge), plus all incoming edges grouped by target handle.
func (r *Runner) buildExecCtx(g *Graph, stepResults map[string]any, nodeID string) *ExecCtx {
	var primary any
	inputsByHandle := make(map[string][]InputItem)
	for _, e := range g.Edges {
		if e.Target != nodeID {
			continue
		}
		handle := edgeTargetHandle(e)
		val := stepResults[e.Source]
		inputsByHandle[handle] = append(inputsByHandle[handle], InputItem{
			FromID: e.Source,
			Value:  val,
		})
		// Primary input: first non-config edge
		if primary == nil && !isConfigHandle(e.TargetHandle) {
			primary = val
		}
	}
	return &ExecCtx{
		Graph:          g,
		StepResults:    stepResults,
		InputData:      primary,
		InputsByHandle: inputsByHandle,
		AuthHeaders:    g.AuthHeaders,
		NetworkNodes:   g.NetworkNodes,
		HTTPClient:     r.httpClient,
		BaseURL:        r.baseURL,
		NodeCaller:     r.nodeCaller,
		Logger:         r.log,
	}
}

// rawDecode turns a json.RawMessage into a generic any for redaction.
// Returns the raw string if decode fails.
func rawDecode(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return string(raw)
	}
	return out
}

// redact trims large values so the steps summary stays compact. Full
// untrimmed values remain in StepResults for downstream consumers.
func redact(v any) any {
	if v == nil {
		return nil
	}
	switch x := v.(type) {
	case string:
		if len(x) > 12 && (stringHasPrefix(x, "data:image/")) {
			return fmt.Sprintf("[image base64, %d chars]", len(x))
		}
		if len(x) > 300 {
			return x[:300] + "…"
		}
		return x
	case []any:
		if len(x) > 20 {
			return fmt.Sprintf("[array length=%d]", len(x))
		}
		out := make([]any, len(x))
		for i, el := range x {
			out[i] = redact(el)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = redact(val)
		}
		return out
	}
	return v
}

// redactInputs converts InputsByHandle to a JSON-friendly shape with redacted values.
func redactInputs(in map[string][]InputItem) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for handle, items := range in {
		arr := make([]map[string]any, len(items))
		for i, it := range items {
			arr[i] = map[string]any{
				"fromId": it.FromID,
				"value":  redact(it.Value),
			}
		}
		out[handle] = arr
	}
	return out
}

func stringHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
