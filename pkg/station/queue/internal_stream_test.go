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
