package entities

import (
	"context"
	"encoding/json"

	"github.com/isannai/mesh/pkg/pipeline"
)

type optionsData struct {
	Options map[string]any `json:"options"`
}

// Options emits a filtered map of generation parameters that downstream AI
// nodes pick up via the "options" handle. Empty strings, nulls, and
// "seed: -1" (= random sentinel) are stripped.
type Options struct{}

func (Options) Type() string               { return "optionsNode" }
func (Options) Inputs() map[string]string  { return map[string]string{} }
func (Options) Outputs() map[string]string { return map[string]string{"default": "object"} }

func (Options) Execute(_ context.Context, node pipeline.Node, _ *pipeline.ExecCtx) (any, error) {
	if len(node.Data) == 0 {
		return map[string]any{}, nil
	}
	var d optionsData
	_ = json.Unmarshal(node.Data, &d)

	out := make(map[string]any, len(d.Options))
	for k, v := range d.Options {
		if v == nil {
			continue
		}
		if s, ok := v.(string); ok && s == "" {
			continue
		}
		// seed = -1 means "random" — omit so engine generates a fresh seed.
		if k == "seed" {
			if f, ok := v.(float64); ok && f == -1 {
				continue
			}
			if i, ok := v.(int); ok && i == -1 {
				continue
			}
		}
		out[k] = v
	}
	return out, nil
}
