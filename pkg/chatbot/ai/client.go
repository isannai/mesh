// Package ai defines the seam between the bot and the wrapped AI API.
//
// The telegram layer depends ONLY on the Client interface here — never on the
// transport, the endpoint, or the EOA-signature credential. That keeps AI
// integration additive: a future httpClient implements Client and main.go swaps
// NopClient -> httpClient in a single line, leaving handlers untouched.
package ai

import (
	"context"
	"time"
)

// ChatRequest is a single prompt sent to the AI API. The harness and tool
// execution happen inside the AI server, so the bot only sends the prompt and
// receives the result.
type ChatRequest struct {
	Prompt string
	Meta   map[string]string // optional; no transport/crypto detail leaks here
}

// ChatResponse is the AI API's reply.
type ChatResponse struct {
	Text string
}

// Client is what the telegram handlers are injected with.
type Client interface {
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
}

// NopClient is the Milestone 1 stub used until the real AI client is wired.
//
// It simulates "thinking" latency so the typing/placeholder indicator is
// visible, then echoes the prompt back. The real AI client will replace this
// and provide a real answer (with its own natural latency).
type NopClient struct{}

func (NopClient) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	select {
	case <-time.After(2 * time.Second): // 추론 가장: 2초 딜레이
	case <-ctx.Done():
		return ChatResponse{}, ctx.Err()
	}
	return ChatResponse{Text: req.Prompt}, nil
}
