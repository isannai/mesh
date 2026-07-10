package entities

import (
	"context"

	"github.com/isannai/mesh/pkg/pipeline"
)

// Output is a sink node — emits its primary upstream input unchanged.
// Used to mark a graph's terminal value for client display.
type Output struct{}

func (Output) Type() string               { return "outputNode" }
func (Output) Inputs() map[string]string  { return map[string]string{"input": "any"} }
func (Output) Outputs() map[string]string { return map[string]string{"default": "any"} }

func (Output) Execute(_ context.Context, _ pipeline.Node, ec *pipeline.ExecCtx) (any, error) {
	return ec.InputData, nil
}
