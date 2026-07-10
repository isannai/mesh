// Package pipeline is GLink's DAG-based pipeline runner. It parses a graph
// description (nodes + edges), validates it, topologically sorts the nodes,
// and dispatches each node to a registered Entity for execution.
//
// The package is self-contained and knows nothing about the broker's HTTP
// layer. Integration with broker happens through pkg/control/pipeline.go
// which constructs the Runner, JobStore and exposes /v1/pipeline/* routes.
package pipeline

import (
	"encoding/json"
)

// Node is one vertex of the pipeline graph. Data carries entity-specific
// configuration and is parsed by the target entity (each entity defines
// its own internal data struct).
//
// Additional fields sent by React Flow (position, width, selected, ...)
// are silently ignored by encoding/json — the runner needs only id/type/data.
type Node struct {
	ID   string          `json:"id"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// Edge connects two node handles. Empty SourceHandle/TargetHandle default
// to "default" / "input" respectively when resolved by the runner.
type Edge struct {
	Source       string `json:"source"`
	Target       string `json:"target"`
	SourceHandle string `json:"sourceHandle,omitempty"`
	TargetHandle string `json:"targetHandle,omitempty"`
}

// Graph is the top-level request body for pipeline execution. NetworkNodes
// carries the current known provider node directory (for nodeSelector) and
// AuthHeaders provides global signing headers forwarded to provider calls
// when a node selector doesn't supply its own.
type Graph struct {
	Nodes        []Node            `json:"nodes"`
	Edges        []Edge            `json:"edges"`
	NetworkNodes []any             `json:"networkNodes,omitempty"`
	AuthHeaders  map[string]string `json:"authHeaders,omitempty"`
}

// StepResult summarizes the outcome of a single node execution. Output is
// redacted (long strings/arrays trimmed) for the steps summary; the full
// result lives in ExecuteResponse.StepResults keyed by node ID.
type StepResult struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Status     string         `json:"status"` // done | error | skipped
	DurationMs int64          `json:"durationMs"`
	Params     any            `json:"params,omitempty"`
	Inputs     map[string]any `json:"inputs,omitempty"`
	Output     any            `json:"output,omitempty"`
	Error      string         `json:"error,omitempty"`
	Reason     string         `json:"reason,omitempty"` // set when status=skipped
}

// ExecuteResponse is the final pipeline result. When ValidationErrors is
// non-empty the pipeline did not run and StepResults/Steps are empty.
// When Error is set, one step failed and execution halted — StepResults
// may contain partial results from earlier steps.
type ExecuteResponse struct {
	StepResults      map[string]any `json:"stepResults"`
	Steps            []StepResult   `json:"steps"`
	Error            string         `json:"error,omitempty"`
	ValidationErrors []string       `json:"validationErrors,omitempty"`
}

// edgeTargetHandle returns the effective target handle name (default "input").
func edgeTargetHandle(e Edge) string {
	if e.TargetHandle == "" {
		return "input"
	}
	return e.TargetHandle
}

// edgeSourceHandle returns the effective source handle name (default "default").
func edgeSourceHandle(e Edge) string {
	if e.SourceHandle == "" {
		return "default"
	}
	return e.SourceHandle
}

// isConfigHandle reports handles that don't contribute to execution ordering
// (node selector / options dependencies are data injections, not flow).
func isConfigHandle(h string) bool {
	return h == "node" || h == "options"
}
