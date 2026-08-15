package probe

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/isannai/mesh/pkg/engine/manifest"
	"github.com/isannai/mesh/pkg/rvnodes"
)

// fakeNode stands in for isannd's /node/<id>/… proxy: run-schema, submit,
// status, result.
type fakeNode struct {
	schemaHits int
	sessions   []string
	submits    []map[string]any
	submitCode int
	statuses   []string // consumed one per status poll; last value repeats
}

func (f *fakeNode) handler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.sessions = append(f.sessions, r.Header.Get("X-ISANN-Session"))
		switch {
		case strings.HasSuffix(r.URL.Path, "/run-schema"):
			f.schemaHits++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"default": manifest.RunSpec{
					Path: "/v1/completions",
					Params: []manifest.RunParam{
						{Name: "prompt", Type: "string", Required: true},
						{Name: "max_tokens", Type: "int"},
						{Name: "temperature", Type: "float"},
						{Name: "stop", Type: "string"},
					},
				},
				"variants": []any{},
			})
		case strings.HasSuffix(r.URL.Path, "/v1/jobs") && r.Method == http.MethodPost:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.submits = append(f.submits, body)
			code := f.submitCode
			if code == 0 {
				code = http.StatusAccepted
			}
			w.WriteHeader(code)
			if code == http.StatusAccepted {
				_ = json.NewEncoder(w).Encode(map[string]any{"job_id": "job-1", "queue_max": 8})
			}
		case strings.HasSuffix(r.URL.Path, "/result"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"text": " Paris"}},
				"usage":   map[string]any{"prompt_tokens": 40, "completion_tokens": 2},
			})
		default: // status poll
			st := "done"
			if len(f.statuses) > 0 {
				st = f.statuses[0]
				if len(f.statuses) > 1 {
					f.statuses = f.statuses[1:]
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": st})
		}
	})
}

func testTarget() Target {
	n := rvnodes.Node{ID: "0xabc", Addr: "203.0.113.1:7443", AuthMode: "public"}
	return Target{Node: n, Service: rvnodes.Service{Name: "llm-api", Engine: "llama"}, Slash24: "203.0.113"}
}

func testQuestion() Question {
	return Question{
		Category: CatMath, Q: "What is 12 + 7?", Draft: "19", Fewshot: mathFewshot,
	}
}

func TestSubmitAndCollect(t *testing.T) {
	fake := &fakeNode{statuses: []string{"queued", "running", "done"}}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	f := NewFirer(srv.URL)
	res, err := f.Submit(testTarget(), testQuestion())
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != OutcomeSubmitted || res.JobID != "job-1" {
		t.Fatalf("submit = %+v", res)
	}

	got, err := f.Collect("0xabc", "job-1", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != OutcomeAnswered || got.Answer != "Paris" {
		t.Fatalf("collect = %+v", got)
	}
	if got.CompletionTokens != 2 || got.PromptTokens != 40 {
		t.Errorf("token counts lost: %+v", got)
	}
}

// 🔴 The probe must arrive anonymously: a public node's free tier is what is
// being exercised, and isannd turns a session token into a wallet signature on
// the way out, which would change what is measured.
func TestFireCarriesNoSession(t *testing.T) {
	fake := &fakeNode{}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	f := NewFirer(srv.URL)
	if _, err := f.Submit(testTarget(), testQuestion()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Collect("0xabc", "job-1", time.Second); err != nil {
		t.Fatal(err)
	}
	for i, s := range fake.sessions {
		if s != "" {
			t.Fatalf("request %d carried a session header %q", i, s)
		}
	}
}

// Parameter names come from the schema. The measured settings must land, and
// max_tokens must NOT be shrunk to 4-8 — that truncates multi-token proper
// nouns and fails honest nodes.
func TestSubmitUsesSchemaParams(t *testing.T) {
	fake := &fakeNode{}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	f := NewFirer(srv.URL)
	if _, err := f.Submit(testTarget(), testQuestion()); err != nil {
		t.Fatal(err)
	}
	if len(fake.submits) != 1 {
		t.Fatalf("submits = %d", len(fake.submits))
	}
	run, _ := fake.submits[0]["run"].(map[string]any)
	if run == nil {
		t.Fatalf("no run block: %+v", fake.submits[0])
	}
	prompt, _ := run["prompt"].(string)
	if !strings.HasSuffix(prompt, "A:") || !strings.HasPrefix(prompt, "Give only the answer.") {
		t.Errorf("prompt not assembled: %q", prompt)
	}
	if n, _ := run["max_tokens"].(float64); int(n) != probeMaxTokens {
		t.Errorf("max_tokens = %v, want %d", run["max_tokens"], probeMaxTokens)
	}
	if int(probeMaxTokens) < 16 {
		t.Error("max_tokens must stay generous — the stop sequence makes answers short")
	}
	if fake.submits[0]["service"] != "llm-api" {
		t.Errorf("service = %v", fake.submits[0]["service"])
	}
}

// An engine that names things differently must still work; only the values are
// ours, not the keys.
func TestBuildRunFallsBackToRequiredString(t *testing.T) {
	rs := &manifest.RunSpec{Params: []manifest.RunParam{
		{Name: "input_text", Type: "string", Required: true},
	}}
	run := buildRun(rs, testQuestion())
	if run == nil || run["input_text"] == nil {
		t.Fatalf("run = %+v", run)
	}
	if _, ok := run["max_tokens"]; ok {
		t.Error("undeclared parameter was sent anyway")
	}

	// Nothing to put the question in ⇒ no shot, rather than a malformed one.
	if got := buildRun(&manifest.RunSpec{}, testQuestion()); got != nil {
		t.Errorf("want nil when no prompt parameter exists, got %+v", got)
	}
}

// 429 means the node is alive and busy. Recording it as a refusal would make a
// working node look dead.
func TestSubmitQueueFullIsNotAnError(t *testing.T) {
	fake := &fakeNode{submitCode: http.StatusTooManyRequests}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	res, err := NewFirer(srv.URL).Submit(testTarget(), testQuestion())
	if err != nil {
		t.Fatalf("429 should not be an error: %v", err)
	}
	if res.Outcome != OutcomeQueueFull {
		t.Errorf("outcome = %q, want %q", res.Outcome, OutcomeQueueFull)
	}
}

// A node that accepts and never finishes is a timeout, which is a different
// statement from a refusal.
func TestCollectTimeout(t *testing.T) {
	fake := &fakeNode{statuses: []string{"running"}}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	got, err := NewFirer(srv.URL).Collect("0xabc", "job-1", 700*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != OutcomeTimeout {
		t.Errorf("outcome = %q, want %q", got.Outcome, OutcomeTimeout)
	}
}

// The schema is fetched once per node+service; two nodes running the same
// engine may still be on different versions, so the cache key includes both.
func TestRunSchemaCachedPerNodeAndService(t *testing.T) {
	fake := &fakeNode{}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	f := NewFirer(srv.URL)

	for i := 0; i < 3; i++ {
		if _, err := f.RunSchema("0xabc", "llm-api"); err != nil {
			t.Fatal(err)
		}
	}
	if fake.schemaHits != 1 {
		t.Errorf("schema fetched %d times, want 1", fake.schemaHits)
	}
	if _, err := f.RunSchema("0xdef", "llm-api"); err != nil {
		t.Fatal(err)
	}
	if fake.schemaHits != 2 {
		t.Errorf("a different node must fetch its own schema, hits=%d", fake.schemaHits)
	}
}

// Node ids reach the prober bare; the station role prefix is what isannd routes on.
func TestNodeBaseAddsRolePrefix(t *testing.T) {
	f := NewFirer("http://127.0.0.1:8443")
	if got := f.nodeBase("0xabc"); got != "http://127.0.0.1:8443/node/s:0xabc" {
		t.Errorf("nodeBase = %q", got)
	}
	if got := f.nodeBase("c:0xabc"); got != "http://127.0.0.1:8443/node/c:0xabc" {
		t.Errorf("existing prefix must be kept: %q", got)
	}
}
