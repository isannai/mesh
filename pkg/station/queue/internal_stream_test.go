package queue

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/isannai/mesh/pkg/setup"
)

// TestBufferedClientInternalStreamsAndReassembles verifies §3-3: a streamable
// engine (StreamPath set) is consumed as a stream internally even when the
// client asked for a buffered response (job.Stream=false). The engine must see
// stream:true, and the worker must reassemble a faithful non-streaming
// chat.completion — including tool_calls merged from deltas — as the result.
func TestBufferedClientInternalStreamsAndReassembles(t *testing.T) {
	var sawStreamTrue bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The engine should have been asked to stream even though the client
		// requested buffered — forceStreamFlag injects it.
		reqBody, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(reqBody, &m)
		if s, ok := m["stream"].(bool); ok && s {
			sawStreamTrue = true
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		chunks := []string{
			`{"choices":[{"index":0,"delta":{"role":"assistant","content":"","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"seoul\"}"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"total_tokens":42}}`,
		}
		for _, c := range chunks {
			io.WriteString(w, "data: "+c+"\n\n")
			if fl != nil {
				fl.Flush()
			}
		}
		io.WriteString(w, "data: [DONE]\n\n")
		if fl != nil {
			fl.Flush()
		}
	}))
	defer srv.Close()

	svc := setup.ServiceEntry{Name: "llm", Addr: strings.TrimPrefix(srv.URL, "http://")}
	proc := MakeManagedProcess(svc, DispatchOptions{
		StreamPath: "choices[0].delta.content",
		Timeout:    2 * time.Second,
	})
	// Buffered client: Stream=false. Internal streaming must still kick in.
	job := &Job{ID: "j1", Path: "/v1/chat/completions", Stream: false, RequestBody: []byte(`{"model":"x"}`)}

	code, ct, body, err := proc(context.Background(), job)
	if err != nil {
		t.Fatalf("proc error: %v", err)
	}
	if !sawStreamTrue {
		t.Fatal("engine did not receive stream:true — forceStreamFlag not applied for buffered client")
	}
	if code != http.StatusOK || !strings.Contains(ct, "json") {
		t.Fatalf("unexpected code/ct: %d %q", code, ct)
	}

	// Result must be a reassembled chat.completion with the tool_calls intact.
	var res map[string]any
	if e := json.Unmarshal(body, &res); e != nil {
		t.Fatalf("result not JSON: %v — %s", e, string(body))
	}
	choices, _ := res["choices"].([]any)
	if len(choices) == 0 {
		t.Fatalf("no choices in result: %s", string(body))
	}
	ch0, _ := choices[0].(map[string]any)
	msg, _ := ch0["message"].(map[string]any)
	if msg == nil {
		t.Fatalf("no message in choice: %s", string(body))
	}
	tcs, _ := msg["tool_calls"].([]any)
	if len(tcs) != 1 {
		t.Fatalf("expected 1 tool_call, got %d: %s", len(tcs), string(body))
	}
	tc0, _ := tcs[0].(map[string]any)
	fn, _ := tc0["function"].(map[string]any)
	if fn["name"] != "get_weather" {
		t.Errorf("tool name = %v, want get_weather", fn["name"])
	}
	if fn["arguments"] != `{"city":"seoul"}` {
		t.Errorf("tool arguments = %v, want {\"city\":\"seoul\"}", fn["arguments"])
	}
	if tc0["id"] != "call_1" {
		t.Errorf("tool id = %v, want call_1", tc0["id"])
	}
	// usage should survive too.
	if usage, _ := res["usage"].(map[string]any); usage == nil || usage["total_tokens"] == nil {
		t.Errorf("usage not carried through: %s", string(body))
	}
}

// A /v1/completions job streams text (choices[0].text), not chat deltas. The
// station reads its tokens from there, and the result is a text_completion
// carrying the whole text; before, every token was missed and the answer came
// back empty while its usage was still billed.
func TestCompletionsJobKeepsItsText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, c := range []string{
			`{"id":"cmpl-1","object":"text_completion","model":"m","choices":[{"index":0,"text":"Once upon ","finish_reason":null}]}`,
			`{"id":"cmpl-1","object":"text_completion","model":"m","choices":[{"index":0,"text":"a time.","finish_reason":null}]}`,
			`{"id":"cmpl-1","object":"text_completion","model":"m","choices":[{"index":0,"text":"","finish_reason":"stop"}]}`,
			`{"id":"cmpl-1","object":"text_completion","model":"m","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`,
		} {
			io.WriteString(w, "data: "+c+"\n\n")
		}
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	svc := setup.ServiceEntry{Name: "llm", Addr: strings.TrimPrefix(srv.URL, "http://")}
	proc := MakeManagedProcess(svc, DispatchOptions{StreamPath: "choices[0].delta.content", Timeout: 2 * time.Second})
	job := &Job{ID: "c1", Path: "/v1/completions", Stream: true, RequestBody: []byte(`{"prompt":"Tell me"}`)}

	code, ct, body, err := proc(context.Background(), job)
	if err != nil || code != http.StatusOK || !strings.Contains(ct, "json") {
		t.Fatalf("proc: %d %q %v", code, ct, err)
	}
	var res struct {
		Object  string `json:"object"`
		Choices []struct {
			Text         string `json:"text"`
			FinishReason string `json:"finish_reason"`
			Message      any    `json:"message"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("result %s: %v", body, err)
	}
	if res.Object != "text_completion" || len(res.Choices) != 1 || res.Choices[0].Text != "Once upon a time." ||
		res.Choices[0].FinishReason != "stop" || res.Choices[0].Message != nil || res.Usage.TotalTokens != 7 {
		t.Fatalf("result %s, want a text_completion with the whole text, stop, usage 7", body)
	}

	var chunks strings.Builder
	for i := 0; i < job.ChunkCount(); i++ {
		c, _ := job.ChunkAt(i)
		chunks.WriteString(c)
	}
	if chunks.String() != "Once upon a time." {
		t.Errorf("chunks %q, want the text", chunks.String())
	}
	var meta map[string]any
	if err := json.Unmarshal(job.Meta(), &meta); err != nil || meta["object"] != "text_completion" {
		t.Errorf("EOF metadata %s, want a text_completion", job.Meta())
	}
	if ch, _ := meta["choices"].([]any); len(ch) != 1 || ch[0].(map[string]any)["text"] != nil {
		t.Errorf("EOF metadata %s carries the text", job.Meta())
	}
}
