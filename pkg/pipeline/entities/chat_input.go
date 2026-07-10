package entities

import (
	"context"
	"encoding/json"

	"github.com/isannai/mesh/pkg/pipeline"
)

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatInputData struct {
	Messages []chatMessage `json:"messages"`
}

// ChatInput emits {messages: [{role, content}, ...]} for downstream LLM nodes.
// Empty-content messages are dropped.
type ChatInput struct{}

func (ChatInput) Type() string               { return "chatInputNode" }
func (ChatInput) Inputs() map[string]string  { return map[string]string{} }
func (ChatInput) Outputs() map[string]string { return map[string]string{"default": "json"} }

func (ChatInput) Execute(_ context.Context, node pipeline.Node, _ *pipeline.ExecCtx) (any, error) {
	// Returns []any / map[string]any so downstream LLM entity can type-assert
	// via .([]any) — a []chatMessage would fail that cast.
	if len(node.Data) == 0 {
		return map[string]any{"messages": []any{}}, nil
	}
	var d chatInputData
	_ = json.Unmarshal(node.Data, &d)
	out := make([]any, 0, len(d.Messages))
	for _, m := range d.Messages {
		if m.Content == "" {
			continue
		}
		out = append(out, map[string]any{
			"role":    m.Role,
			"content": m.Content,
		})
	}
	return map[string]any{"messages": out}, nil
}
