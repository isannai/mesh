package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPOptions configures the real AI client. main.go fills it from the AI config.
type HTTPOptions struct {
	Endpoint string   // agent run URL (no ?stream)
	Engine   string   // e.g. "llama"
	Preset   string   // server-side inference preset (options + system prompt)
	Nodes    []string // sent as nodes:[...]
	MaxTurns int
	Stream   bool // true -> ?stream=1 (NDJSON), false -> ?stream=0 (single JSON)
	Timeout  time.Duration
	// Credential is loaded but NOT sent yet — auth is intentionally not wired.
	Credential string
}

// HTTPClient talks to the wrapped AI agent API. It implements Client.
type HTTPClient struct {
	opts HTTPOptions
	httpc *http.Client
}

// NewHTTPClient builds an AI client from opts.
func NewHTTPClient(opts HTTPOptions) *HTTPClient {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &HTTPClient{opts: opts, httpc: &http.Client{Timeout: timeout}}
}

// agentRequest is the POST body for /internal/api/agent/run.
type agentRequest struct {
	Prompt   string   `json:"prompt"`
	Engine   string   `json:"engine"`
	Preset   string   `json:"preset"`
	Nodes    []string `json:"nodes"`
	MaxTurns int      `json:"max_turns"`
}

// streamEvent is one NDJSON line in stream mode.
type streamEvent struct {
	Event  string `json:"event"`
	Answer string `json:"answer"`
	Error  string `json:"error"`
	Detail string `json:"detail"`
}

// onceResponse is the single-JSON body in non-stream mode.
type onceResponse struct {
	Answer string `json:"answer"`
}

// Chat sends the prompt to the agent API and returns the final answer.
func (c *HTTPClient) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	payload, err := json.Marshal(agentRequest{
		Prompt:   req.Prompt,
		Engine:   c.opts.Engine,
		Preset:   c.opts.Preset,
		Nodes:    c.opts.Nodes,
		MaxTurns: c.opts.MaxTurns,
	})
	if err != nil {
		return ChatResponse{}, fmt.Errorf("encode request: %w", err)
	}

	stream := "0"
	if c.opts.Stream {
		stream = "1"
	}
	url := c.opts.Endpoint + "?stream=" + stream

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return ChatResponse{}, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	// NOTE: auth (Credential) is intentionally not attached yet.

	resp, err := c.httpc.Do(httpReq)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("ai request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return ChatResponse{}, fmt.Errorf("ai status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	if c.opts.Stream {
		return parseStream(resp.Body)
	}
	return parseOnce(resp.Body)
}

// parseStream reads NDJSON lines and returns the answer from the "done" event.
func parseStream(r io.Reader) (ChatResponse, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // allow long lines
	answer := ""
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev streamEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue // skip non-JSON / keepalive lines
		}
		switch ev.Event {
		case "error":
			return ChatResponse{}, fmt.Errorf("ai error: %s", firstNonEmpty(ev.Error, ev.Detail, "unknown"))
		case "done":
			answer = ev.Answer
		}
	}
	if err := sc.Err(); err != nil {
		return ChatResponse{}, fmt.Errorf("read stream: %w", err)
	}
	if answer == "" {
		return ChatResponse{}, fmt.Errorf("ai stream ended without an answer")
	}
	return ChatResponse{Text: answer}, nil
}

// parseOnce decodes the single-JSON response and returns the answer.
func parseOnce(r io.Reader) (ChatResponse, error) {
	var out onceResponse
	if err := json.NewDecoder(r).Decode(&out); err != nil {
		return ChatResponse{}, fmt.Errorf("decode response: %w", err)
	}
	if out.Answer == "" {
		return ChatResponse{}, fmt.Errorf("ai response had no answer")
	}
	return ChatResponse{Text: out.Answer}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
