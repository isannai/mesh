package entities

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/isannai/mesh/pkg/pipeline"
)

// llmNodeData mirrors the per-node configuration for an LLM call.
//
// Endpoint defaults to "/v1/chat/completions" — the most common path —
// when omitted. Common LLM-specific params (model, temperature,
// max_tokens, top_p, stop, response_format) live in the freeform
// Params map and pass through unchanged into the request body.
type llmNodeData struct {
	Service  string         `json:"service"`            // optional, defaults to "llm-api"
	Endpoint string         `json:"endpoint,omitempty"` // optional, defaults to /v1/chat/completions
	Method   string         `json:"method,omitempty"`   // optional, defaults to POST
	Params   map[string]any `json:"params,omitempty"`   // model, temperature, max_tokens, ...
	Options  map[string]any `json:"options,omitempty"`  // overridable via "options" handle
	NodeID   string         `json:"nodeId,omitempty"`   // overridable via "node" handle
	WaitMode string         `json:"waitMode,omitempty"` // "sync" (default) | "" to skip ?wait=true
}

// LLM is the language-model AI entity. It targets OpenAI-compatible
// endpoints (chat/completions, completions, embeddings) on a provider
// node and forwards the response JSON downstream.
//
// Inputs:
//   - input    chat payload {messages: [...]} or a string prompt
//   - node     nodeSelectorNode result (provider routing + auth)
//   - options  optionsNode result merged into the request body
type LLM struct{}

func (LLM) Type() string { return "llmNode" }

func (LLM) Inputs() map[string]string {
	return map[string]string{
		"input":   "any",
		"node":    "object",
		"options": "object",
	}
}

func (LLM) Outputs() map[string]string { return map[string]string{"default": "json"} }

func (LLM) Execute(ctx context.Context, node pipeline.Node, ec *pipeline.ExecCtx) (any, error) {
	var d llmNodeData
	if len(node.Data) > 0 {
		if err := json.Unmarshal(node.Data, &d); err != nil {
			return nil, fmt.Errorf("llmNode: invalid data: %w", err)
		}
	}

	service := d.Service
	if service == "" {
		service = "llm-api"
	}
	endpoint := d.Endpoint
	if endpoint == "" {
		endpoint = "/v1/chat/completions"
	}

	resolvedID, selectorAuth := resolveNodeID(ec, d.NodeID, service)

	// Body assembly: substitute templates in params, then merge options.
	body := map[string]any{}
	if params := substituteParams(d.Params, ec.StepResults); params != nil {
		if m, ok := params.(map[string]any); ok {
			for k, v := range m {
				body[k] = v
			}
		}
	}
	for k, v := range resolveOptions(ec, d.Options) {
		body[k] = v
	}

	// Primary input → messages (chat) or prompt (completion).
	applyTextInput(ec, body)

	return callBroker(ctx, ec, aiCallRequest{
		NodeID:       resolvedID,
		Service:      service,
		Endpoint:     endpoint,
		Method:       d.Method,
		Body:         body,
		WaitMode:     d.WaitMode,
		SelectorAuth: selectorAuth,
	})
}
