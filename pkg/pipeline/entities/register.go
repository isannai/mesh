package entities

import "github.com/isannai/mesh/pkg/pipeline"

// RegisterBuiltins installs every shipped entity into the registry.
// Call once at startup (e.g., from broker.New).
func RegisterBuiltins(reg *pipeline.Registry) {
	reg.Register(Input{})
	reg.Register(Output{})
	reg.Register(ChatInput{})
	reg.Register(Options{})
	reg.Register(Transform{})
	reg.Register(NodeSelector{})
	reg.Register(LLM{})
	reg.Register(SD{})
	reg.Register(Poller{})
}
