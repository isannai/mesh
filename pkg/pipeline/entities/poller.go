package entities

import (
	"context"

	"github.com/isannai/mesh/pkg/pipeline"
)

// Poller is a no-op stub kept for graph compatibility. The aiService
// entity already calls provider services with `?wait=true` so the result
// is fully resolved before flowing downstream — there is nothing to poll.
//
// This entity exists so legacy graphs containing pollerNode references
// remain valid; Execute simply forwards its input untouched.
type Poller struct{}

func (Poller) Type() string               { return "pollerNode" }
func (Poller) Inputs() map[string]string  { return map[string]string{"input": "any"} }
func (Poller) Outputs() map[string]string { return map[string]string{"default": "any"} }

func (Poller) Execute(_ context.Context, _ pipeline.Node, ec *pipeline.ExecCtx) (any, error) {
	return ec.InputData, nil
}
