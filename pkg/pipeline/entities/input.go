// Package entities contains GLink pipeline entity implementations.
// Each file defines one Entity that handles a specific node.type.
package entities

import (
	"context"
	"encoding/json"

	"github.com/isannai/mesh/pkg/pipeline"
)

// inputNodeData mirrors the JS input node's data.params shape.
// Either Value or Prompt is used; Value takes precedence.
type inputNodeData struct {
	Params struct {
		Value  string `json:"value"`
		Prompt string `json:"prompt"`
	} `json:"params"`
}

// Input is a constant source node — emits the configured value/prompt.
type Input struct{}

func (Input) Type() string                 { return "inputNode" }
func (Input) Inputs() map[string]string    { return map[string]string{} }
func (Input) Outputs() map[string]string   { return map[string]string{"default": "text"} }

func (Input) Execute(_ context.Context, node pipeline.Node, _ *pipeline.ExecCtx) (any, error) {
	if len(node.Data) == 0 {
		return "", nil
	}
	var d inputNodeData
	_ = json.Unmarshal(node.Data, &d) // best-effort; missing params is fine
	if d.Params.Value != "" {
		return d.Params.Value, nil
	}
	return d.Params.Prompt, nil
}
