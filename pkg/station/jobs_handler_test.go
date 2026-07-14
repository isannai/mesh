package station

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/isannai/mesh/pkg/station/queue"
	"github.com/isannai/mesh/pkg/setup"
)

// stubFactory builds a Manager that forwards to a single in-memory mock
// engine — used to exercise JobsHandler end-to-end without spawning anything.
func stubFactory(engine *httptest.Server) queue.Factory {
	return func(svc setup.ServiceEntry) (queue.Config, queue.ProcessFunc) {
		cfg := queue.Config{ServiceName: svc.Name, Concurrency: 1, MaxQueue: 5}
		// Override addr to point at the mock engine.
		fixed := svc
		fixed.Addr = strings.TrimPrefix(engine.URL, "http://")
		return cfg, queue.MakeManagedProcess(fixed, queue.DispatchOptions{})
	}
}

func newTestHandler(t *testing.T, engine *httptest.Server) (*JobsHandler, *httptest.Server) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mgr := queue.NewManager(ctx, stubFactory(engine))
	services := []setup.ServiceEntry{{Name: "sd-api", Addr: "ignored"}}
	h := NewJobsHandler(mgr, nil, services, nil, nil)
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return h, srv
}

// streamStubFactory points the queue at an SSE engine and wires StreamPath so
// stream-mode jobs accumulate sentence chunks (M3 + M4).
func streamStubFactory(engine *httptest.Server) queue.Factory {
	return func(svc setup.ServiceEntry) (queue.Config, queue.ProcessFunc) {
		cfg := queue.Config{ServiceName: svc.Name, Concurrency: 1, MaxQueue: 5}
		fixed := svc
		fixed.Addr = strings.TrimPrefix(engine.URL, "http://")
		return cfg, queue.MakeManagedProcess(fixed, queue.DispatchOptions{StreamPath: "choices[0].delta.content"})
	}
}

func newStreamTestHandler(t *testing.T, engine *httptest.Server) *httptest.Server {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mgr := queue.NewManager(ctx, streamStubFactory(engine))
	h := NewJobsHandler(mgr, nil, []setup.ServiceEntry{{Name: "sd-api", Addr: "ignored"}}, nil, nil)
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestJobsHandlerStreamChunks drives a stream-mode submit end-to-end: the
// worker reads the engine SSE, accumulates sentence chunks, and the client
// polls status + fetches chunks + result.
func TestJobsHandlerStreamChunks(t *testing.T) {
	sse := []string{
		`{"choices":[{"index":0,"delta":{"content":"Hello"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":" world."}}]}`,
		`{"choices":[{"index":0,"delta":{"content":" Bye!"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"total_tokens":42}}`,
		`[DONE]`,
	}
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl, _ := w.(http.Flusher)
		for _, c := range sse {
			_, _ = w.Write([]byte("data: " + c + "\n\n"))
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	defer engine.Close()

	srv := newStreamTestHandler(t, engine)

	body, _ := json.Marshal(submitRequest{Service: "sd-api", Params: json.RawMessage(`{"prompt":"x"}`), Stream: true, ChunkMode: "sentence"})
	resp, err := http.Post(srv.URL+"/v1/jobs", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if resp.StatusCode != http.StatusAccepted {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("submit status = %d, body = %s", resp.StatusCode, buf)
	}
	var sr submitResponse
	json.NewDecoder(resp.Body).Decode(&sr)
	resp.Body.Close()
	if sr.JobID == "" {
		t.Fatal("no job id")
	}

	// Poll status until done.
	var status struct {
		Status     string `json:"status"`
		ChunkCount int    `json:"chunk_count"`
	}
	for i := 0; i < 300; i++ {
		r, _ := http.Get(srv.URL + "/v1/jobs/" + sr.JobID)
		json.NewDecoder(r.Body).Decode(&status)
		r.Body.Close()
		if status.Status == "done" || status.Status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status.Status != "done" {
		t.Fatalf("final status = %q", status.Status)
	}
	if status.ChunkCount != 2 {
		t.Fatalf("chunk_count = %d, want 2", status.ChunkCount)
	}

	// chunk index 0 → content, eof:false.
	r, _ := http.Get(srv.URL + "/v1/jobs/" + sr.JobID + "/chunk?index=0")
	if r.StatusCode != 200 {
		t.Fatalf("chunk 0 status = %d", r.StatusCode)
	}
	var c struct {
		Index   int    `json:"index"`
		Content string `json:"content"`
		EOF     bool   `json:"eof"`
	}
	json.NewDecoder(r.Body).Decode(&c)
	r.Body.Close()
	if c.Content != "Hello world." || c.EOF {
		t.Fatalf("chunk0 = %+v", c)
	}

	// index == count (2) → EOF marker: eof:true + metadata (usage), no content.
	r2, _ := http.Get(srv.URL + "/v1/jobs/" + sr.JobID + "/chunk?index=2")
	eb, _ := io.ReadAll(r2.Body)
	r2.Body.Close()
	if r2.StatusCode != 200 {
		t.Fatalf("eof chunk status = %d (%s)", r2.StatusCode, eb)
	}
	if !strings.Contains(string(eb), `"eof":true`) || !strings.Contains(string(eb), `"total_tokens":42`) {
		t.Fatalf("eof marker = %s", eb)
	}

	// Beyond the EOF marker → 404.
	r3, _ := http.Get(srv.URL + "/v1/jobs/" + sr.JobID + "/chunk?index=3")
	r3.Body.Close()
	if r3.StatusCode != http.StatusNotFound {
		t.Fatalf("oob chunk status = %d, want 404", r3.StatusCode)
	}

	// Result = reassembled JSON: full content + usage metadata.
	r4, _ := http.Get(srv.URL + "/v1/jobs/" + sr.JobID + "/result")
	full, _ := io.ReadAll(r4.Body)
	r4.Body.Close()
	if !strings.Contains(string(full), `"content":"Hello world. Bye!"`) || !strings.Contains(string(full), `"total_tokens":42`) {
		t.Fatalf("result = %s", full)
	}
}

func TestServeChunk(t *testing.T) {
	h := &JobsHandler{}

	// Running job: an index beyond the current count → 202 pending (more may
	// come, or it becomes the EOF marker once done).
	running := &queue.Job{ID: "r", Status: queue.StatusRunning}
	running.AppendChunk("first.")
	rec := httptest.NewRecorder()
	h.serveChunk(rec, httptest.NewRequest("GET", "/v1/jobs/r/chunk?index=1", nil), running)
	if rec.Code != http.StatusAccepted {
		t.Errorf("pending code = %d, want 202", rec.Code)
	}

	// Done job with 2 sentence chunks + captured metadata.
	done := &queue.Job{ID: "d", Status: queue.StatusDone}
	done.AppendChunk("one.")
	done.AppendChunk("two.")
	done.SetMeta([]byte(`{"usage":{"total_tokens":5},"choices":[{"finish_reason":"stop"}]}`))

	// Content chunk 0 → 200, eof:false.
	rec = httptest.NewRecorder()
	h.serveChunk(rec, httptest.NewRequest("GET", "/v1/jobs/d/chunk?index=0", nil), done)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"content":"one."`) || !strings.Contains(rec.Body.String(), `"eof":false`) {
		t.Errorf("content chunk: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// index == count (2) → EOF marker: eof:true + metadata, no content.
	rec = httptest.NewRecorder()
	h.serveChunk(rec, httptest.NewRequest("GET", "/v1/jobs/d/chunk?index=2", nil), done)
	eb := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("eof code = %d (%s)", rec.Code, eb)
	}
	if !strings.Contains(eb, `"eof":true`) || !strings.Contains(eb, `"total_tokens":5`) || !strings.Contains(eb, `"finish_reason":"stop"`) {
		t.Errorf("eof marker missing fields: %s", eb)
	}
	if strings.Contains(eb, `"content"`) {
		t.Errorf("eof marker must not carry content: %s", eb)
	}

	// index beyond the EOF marker → 404.
	rec = httptest.NewRecorder()
	h.serveChunk(rec, httptest.NewRequest("GET", "/v1/jobs/d/chunk?index=3", nil), done)
	if rec.Code != http.StatusNotFound {
		t.Errorf("oob code = %d, want 404", rec.Code)
	}

	// Bad / negative index → 400.
	for _, q := range []string{"index=abc", "index=-1"} {
		rec = httptest.NewRecorder()
		h.serveChunk(rec, httptest.NewRequest("GET", "/v1/jobs/d/chunk?"+q, nil), done)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s code = %d, want 400", q, rec.Code)
		}
	}
}

func TestEnsureStreamFlag(t *testing.T) {
	out := ensureStreamFlag([]byte(`{"prompt":"x"}`))
	var m map[string]any
	if json.Unmarshal(out, &m) != nil || m["stream"] != true {
		t.Errorf("ensureStreamFlag = %s", out)
	}
	// stream_options.include_usage injected so the final SSE chunk carries usage.
	if so, ok := m["stream_options"].(map[string]any); !ok || so["include_usage"] != true {
		t.Errorf("stream_options.include_usage missing: %s", out)
	}
	// Empty body → {"stream":true, stream_options:...}.
	out = ensureStreamFlag(nil)
	m = nil
	if json.Unmarshal(out, &m) != nil || m["stream"] != true {
		t.Errorf("ensureStreamFlag(nil) = %s", out)
	}
}

func TestJobsHandlerSubmitAccepted(t *testing.T) {
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("done"))
	}))
	defer engine.Close()

	_, srv := newTestHandler(t, engine)

	body, _ := json.Marshal(submitRequest{Service: "sd-api", Params: json.RawMessage(`{"prompt":"x"}`)})
	resp, err := http.Post(srv.URL+"/v1/jobs", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, string(buf))
	}
	var got submitResponse
	json.NewDecoder(resp.Body).Decode(&got)
	if got.JobID == "" {
		t.Error("JobID empty")
	}
	if got.Service != "sd-api" {
		t.Errorf("service = %q", got.Service)
	}
}

func TestJobsHandlerServiceNotFound(t *testing.T) {
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer engine.Close()
	_, srv := newTestHandler(t, engine)

	body, _ := json.Marshal(submitRequest{Service: "nope", Params: json.RawMessage(`{}`)})
	resp, _ := http.Post(srv.URL+"/v1/jobs", "application/json", strings.NewReader(string(body)))
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestJobsHandlerWaitMode(t *testing.T) {
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("hello"))
	}))
	defer engine.Close()
	_, srv := newTestHandler(t, engine)

	body, _ := json.Marshal(submitRequest{
		Service: "sd-api",
		Params:  json.RawMessage(`{}`),
		Wait:    true,
	})
	resp, err := http.Post(srv.URL+"/v1/jobs", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	bs, _ := io.ReadAll(resp.Body)
	if string(bs) != "hello" {
		t.Errorf("body = %q", string(bs))
	}
	if resp.Header.Get("Content-Type") != "text/plain" {
		t.Errorf("ct = %q", resp.Header.Get("Content-Type"))
	}
}

// TestJobsHandlerWaitModeFailed — a wait:true submit whose job FAILS must return
// an error response carrying job.Error, not an empty 200. (A failed job leaves
// ResponseCode/Body unset, so without the explicit StatusFailed check the sync
// caller couldn't tell failure from success.) The engine is closed before submit
// so the forward errors → StatusFailed.
func TestJobsHandlerWaitModeFailed(t *testing.T) {
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	_, srv := newTestHandler(t, engine)
	engine.Close() // engine down → the job's forward call errors → StatusFailed

	body, _ := json.Marshal(submitRequest{
		Service: "sd-api",
		Params:  json.RawMessage(`{}`),
		Wait:    true,
	})
	resp, err := http.Post(srv.URL+"/v1/jobs", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	var er errorResponse
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if er.Error == "" {
		t.Error("wait:true failed job must surface job.Error, got empty")
	}
}

func TestJobsHandlerByID(t *testing.T) {
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("payload"))
	}))
	defer engine.Close()
	h, srv := newTestHandler(t, engine)

	// Submit
	body, _ := json.Marshal(submitRequest{Service: "sd-api", Params: json.RawMessage(`{}`)})
	resp, _ := http.Post(srv.URL+"/v1/jobs", "application/json", strings.NewReader(string(body)))
	var sr submitResponse
	json.NewDecoder(resp.Body).Decode(&sr)
	resp.Body.Close()

	// Wait for completion (poll Manager directly)
	q := h.mgr.Get("sd-api")
	wctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := q.Wait(wctx, sr.JobID); err != nil {
		t.Fatalf("wait: %v", err)
	}

	// GET status
	r2, _ := http.Get(srv.URL + "/v1/jobs/" + sr.JobID)
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusOK {
		t.Errorf("status = %d", r2.StatusCode)
	}

	// GET result
	r3, _ := http.Get(srv.URL + "/v1/jobs/" + sr.JobID + "/result")
	defer r3.Body.Close()
	bs, _ := io.ReadAll(r3.Body)
	if string(bs) != "payload" {
		t.Errorf("result body = %q", string(bs))
	}
}

func TestJobsHandlerStats(t *testing.T) {
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer engine.Close()
	h, srv := newTestHandler(t, engine)

	// Trigger queue creation
	h.mgr.GetOrCreate(setup.ServiceEntry{Name: "sd-api"})

	resp, _ := http.Get(srv.URL + "/v1/queue/stats?service=sd-api")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
	var got queue.Stats
	json.NewDecoder(resp.Body).Decode(&got)
	// Empty queue — fields just need to decode cleanly.
	_ = got
}

func TestJobsHandlerOutputsMissingFallsBackTo404(t *testing.T) {
	// No storage, no job with this ID → /outputs/{name} should fall through
	// to 404 (was 503 before the in-memory fallback was added).
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer engine.Close()
	_, srv := newTestHandler(t, engine)

	resp, _ := http.Get(srv.URL + "/outputs/anything.png")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (no storage, no in-memory job)", resp.StatusCode)
	}
}

func TestJobsHandlerOutputsTraversalGuarded(t *testing.T) {
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer engine.Close()

	// Build a handler with a real storage to test traversal blocking.
	dir := t.TempDir()
	storage, _ := queue.NewStorage(dir, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr := queue.NewManager(ctx, stubFactory(engine))
	h := NewJobsHandler(mgr, storage, nil, nil, nil)
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// /outputs/../etc/passwd should be blocked.
	resp, _ := http.Get(srv.URL + "/outputs/..%2F..%2Fetc%2Fpasswd")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("traversal should not succeed")
	}
}
