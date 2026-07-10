package entities

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/isannai/mesh/pkg/pipeline"
)

// sdNodeData is the per-node configuration for a Stable Diffusion call.
//
// When Endpoint is empty, the runner picks one based on connected handles:
//   - image handle present → "/v1/images/edits" (img2img / inpaint)
//   - else                 → "/v1/images/generations" (txt2img)
//
// SD-specific params (size, steps, cfg_scale, seed, n, strength) live in
// the freeform Params map and pass through unchanged.
type sdNodeData struct {
	Service  string         `json:"service"`            // optional, defaults to "sd-api"
	Endpoint string         `json:"endpoint,omitempty"` // optional, auto-detected from handles
	Method   string         `json:"method,omitempty"`   // optional, defaults to POST
	Params   map[string]any `json:"params,omitempty"`   // size, steps, cfg_scale, seed, ...
	Options  map[string]any `json:"options,omitempty"`  // overridable via "options" handle
	NodeID   string         `json:"nodeId,omitempty"`   // overridable via "node" handle
	WaitMode string         `json:"waitMode,omitempty"` // "sync" (default) | "" to skip ?wait=true
}

// SD is the Stable Diffusion AI entity. It targets OpenAI-compatible image
// endpoints on a provider node, auto-routing between txt2img, img2img, and
// inpaint based on which input handles are wired.
//
// Inputs:
//   - input    prompt text
//   - node     nodeSelectorNode result (provider routing + auth)
//   - options  optionsNode result merged into the request body
//   - image    img2img source image (auto switches endpoint to /v1/images/edits)
//   - mask     inpaint mask (only used when image is also present)
type SD struct{}

func (SD) Type() string { return "sdNode" }

func (SD) Inputs() map[string]string {
	return map[string]string{
		"input":   "any",
		"node":    "object",
		"options": "object",
		"image":   "image",
		"mask":    "image",
	}
}

func (SD) Outputs() map[string]string { return map[string]string{"default": "json"} }

func (SD) Execute(ctx context.Context, node pipeline.Node, ec *pipeline.ExecCtx) (any, error) {
	var d sdNodeData
	if len(node.Data) > 0 {
		if err := json.Unmarshal(node.Data, &d); err != nil {
			return nil, fmt.Errorf("sdNode: invalid data: %w", err)
		}
	}

	service := d.Service
	if service == "" {
		service = "sd-api"
	}

	resolvedID, selectorAuth := resolveNodeID(ec, d.NodeID, service)

	// Body assembly.
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

	// Prompt input.
	applyTextInput(ec, body)

	// Auto endpoint + image/mask attachment.
	endpoint := d.Endpoint
	if items := ec.InputsByHandle["image"]; len(items) > 0 && items[0].Value != nil {
		if endpoint == "" {
			endpoint = "/v1/images/edits"
		}
		body["image"] = items[0].Value
		if maskItems := ec.InputsByHandle["mask"]; len(maskItems) > 0 && maskItems[0].Value != nil {
			body["mask"] = maskItems[0].Value
		}
	} else if endpoint == "" {
		endpoint = "/v1/images/generations"
	}

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
