package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPClientChat(t *testing.T) {
	const want = "안녕하세요! 어떻게 도와드릴까요?"

	tests := []struct {
		name   string
		stream bool
		body   string // server response
	}{
		{
			name:   "non-stream single JSON",
			stream: false,
			body:   `{"answer":"안녕하세요! 어떻게 도와드릴까요?","turns":1}`,
		},
		{
			name:   "stream NDJSON",
			stream: true,
			body:   "{\"event\":\"started\"}\n{\"event\":\"done\",\"answer\":\"안녕하세요! 어떻게 도와드릴까요?\",\"turns\":1}\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotReq agentRequest
			var gotStream string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotStream = r.URL.Query().Get("stream")
				b, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(b, &gotReq)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer srv.Close()

			c := NewHTTPClient(HTTPOptions{
				Endpoint: srv.URL,
				Engine:   "llama",
				Preset:   "default",
				Nodes:    []string{"P:0xabc"},
				MaxTurns: 4,
				Stream:   tt.stream,
			})

			resp, err := c.Chat(context.Background(), ChatRequest{Prompt: "안녕"})
			if err != nil {
				t.Fatalf("Chat error: %v", err)
			}
			if resp.Text != want {
				t.Errorf("answer = %q, want %q", resp.Text, want)
			}

			// Verify request shape.
			if gotReq.Prompt != "안녕" {
				t.Errorf("prompt = %q, want 안녕", gotReq.Prompt)
			}
			if gotReq.Engine != "llama" || gotReq.Preset != "default" || gotReq.MaxTurns != 4 {
				t.Errorf("body params wrong: %+v", gotReq)
			}
			if len(gotReq.Nodes) != 1 || gotReq.Nodes[0] != "P:0xabc" {
				t.Errorf("nodes = %v, want [P:0xabc]", gotReq.Nodes)
			}
			wantStream := "0"
			if tt.stream {
				wantStream = "1"
			}
			if gotStream != wantStream {
				t.Errorf("stream query = %q, want %q", gotStream, wantStream)
			}
		})
	}
}

func TestHTTPClientErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "boom")
	}))
	defer srv.Close()

	c := NewHTTPClient(HTTPOptions{Endpoint: srv.URL})
	if _, err := c.Chat(context.Background(), ChatRequest{Prompt: "hi"}); err == nil {
		t.Fatal("expected error on 500 status, got nil")
	}
}
