package queue

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/isannai/mesh/pkg/setup"
)

// addrFromTestServer extracts host:port from an httptest.Server URL.
func addrFromTestServer(srv *httptest.Server) string {
	return strings.TrimPrefix(srv.URL, "http://")
}

func TestManagedProcessForward(t *testing.T) {
	var gotPath, gotHeader, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHeader = r.Header.Get("X-Test-Hint")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		w.Write([]byte("OK"))
	}))
	defer srv.Close()

	svc := setup.ServiceEntry{Name: "sd-api", Addr: addrFromTestServer(srv)}
	process := MakeManagedProcess(svc, DispatchOptions{})

	job := &Job{
		ID:          "abc",
		ServiceName: "sd-api",
		Path:        "/v1/images/generations",
		RequestBody: []byte(`{"prompt":"cat"}`),
		RequestHeader: http.Header{
			"X-Test-Hint": []string{"hello"},
		},
	}

	code, ct, body, err := process(context.Background(), job)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if code != 200 {
		t.Errorf("code = %d", code)
	}
	if ct != "text/plain" {
		t.Errorf("ct = %q", ct)
	}
	if string(body) != "OK" {
		t.Errorf("body = %q", string(body))
	}
	if gotPath != "/v1/images/generations" {
		t.Errorf("server path = %q", gotPath)
	}
	if gotHeader != "hello" {
		t.Errorf("forwarded header missing: %q", gotHeader)
	}
	if gotBody != `{"prompt":"cat"}` {
		t.Errorf("forwarded body = %q", gotBody)
	}
}

// TestManagedProcessStreaming feeds an OpenAI-style SSE response (content
// deltas + finish chunk + usage chunk) and checks the processor accumulates
// sentence chunks, reassembles a chat.completion result (content + usage), and
// stores message-excluded metadata on the job.
func TestManagedProcessStreaming(t *testing.T) {
	sse := []string{
		`{"id":"cmpl-1","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"Hello"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":" world."}}]}`,
		`{"choices":[{"index":0,"delta":{"content":" Bye!"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"total_tokens":42}}`,
		`[DONE]`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	defer srv.Close()

	svc := setup.ServiceEntry{Name: "llm-api", Addr: addrFromTestServer(srv)}
	process := MakeManagedProcess(svc, DispatchOptions{StreamPath: "choices[0].delta.content"})

	job := &Job{
		ID: "s1", ServiceName: "llm-api", Path: "/v1/chat/completions",
		RequestBody: []byte(`{"stream":true}`), Stream: true, ChunkMode: ChunkModeSentence,
	}

	code, ct, body, err := process(context.Background(), job)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if code != 200 {
		t.Errorf("code = %d", code)
	}
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("ct = %q, want application/json", ct)
	}
	// Result = reassembled chat.completion: content + usage + finish_reason.
	bs := string(body)
	if !strings.Contains(bs, `"content":"Hello world. Bye!"`) {
		t.Errorf("result content missing/wrong: %s", bs)
	}
	if !strings.Contains(bs, `"total_tokens":42`) || !strings.Contains(bs, `"finish_reason":"stop"`) {
		t.Errorf("result metadata missing: %s", bs)
	}
	// Sentence chunks — delimiters retained so join == raw content.
	want := []string{"Hello world. ", "Bye!"}
	if job.ChunkCount() != len(want) {
		t.Fatalf("chunk_count = %d, want %d", job.ChunkCount(), len(want))
	}
	for i, w := range want {
		if got, ok := job.ChunkAt(i); !ok || got != w {
			t.Errorf("chunk[%d] = %q,%v want %q", i, got, ok, w)
		}
	}
	// Metadata (EOF marker source): has usage + finish_reason, NO message content.
	meta := string(job.Meta())
	if !strings.Contains(meta, `"total_tokens":42`) || !strings.Contains(meta, `"finish_reason":"stop"`) {
		t.Errorf("meta missing usage/finish: %s", meta)
	}
	if strings.Contains(meta, `"content"`) || strings.Contains(meta, "Hello world") {
		t.Errorf("meta should exclude message content: %s", meta)
	}
}

// TestManagedProcessStreamingFallback: engine ignored stream:true and returned
// plain JSON — the whole body becomes a single chunk, returned verbatim.
func TestManagedProcessStreamingFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hi"}}]}`))
	}))
	defer srv.Close()

	svc := setup.ServiceEntry{Name: "llm-api", Addr: addrFromTestServer(srv)}
	process := MakeManagedProcess(svc, DispatchOptions{StreamPath: "choices[0].delta.content"})
	job := &Job{
		ID: "s2", Path: "/v1/chat/completions",
		RequestBody: []byte(`{}`), Stream: true, ChunkMode: ChunkModeSentence,
	}

	code, _, body, err := process(context.Background(), job)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if code != 200 {
		t.Errorf("code = %d", code)
	}
	if !strings.Contains(string(body), `"content":"hi"`) {
		t.Errorf("fallback body = %q", string(body))
	}
	if job.ChunkCount() != 1 {
		t.Errorf("fallback chunk_count = %d, want 1", job.ChunkCount())
	}
}

func TestManagedProcessOpenAIImageDecode(t *testing.T) {
	pngBytes := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A}
	encoded := base64.StdEncoding.EncodeToString(pngBytes)
	respJSON := []byte(`{"data":[{"b64_json":"` + encoded + `"}]}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(respJSON)
	}))
	defer srv.Close()

	svc := setup.ServiceEntry{Name: "sd-api", Addr: addrFromTestServer(srv)}
	process := MakeManagedProcess(svc, DispatchOptions{})

	job := &Job{ID: "x", Path: "/v1/images/generations"}
	code, ct, body, _ := process(context.Background(), job)
	if code != 200 {
		t.Fatalf("code = %d", code)
	}
	if ct != "image/png" {
		t.Errorf("expected ct=image/png after decode, got %q", ct)
	}
	if string(body) != string(pngBytes) {
		t.Errorf("body decoded incorrectly: got %x", body)
	}
}

func TestManagedProcessOpenAIDecodeDisabled(t *testing.T) {
	// JSON 그대로 전달돼야 함 (decode=false).
	respJSON := []byte(`{"data":[{"b64_json":"AAAA"}]}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(respJSON)
	}))
	defer srv.Close()

	off := false
	svc := setup.ServiceEntry{Name: "test", Addr: addrFromTestServer(srv)}
	process := MakeManagedProcess(svc, DispatchOptions{DecodeOpenAIImage: &off})

	_, ct, body, _ := process(context.Background(), &Job{Path: "/x"})
	if ct != "application/json" {
		t.Errorf("ct = %q (decode should be skipped)", ct)
	}
	if !json.Valid(body) {
		t.Error("body should still be valid JSON")
	}
}

func TestManagedProcessStorageSavesAndDropsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("PNGDATA"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	storage, _ := NewStorage(dir, time.Hour)

	svc := setup.ServiceEntry{Name: "sd-api", Addr: addrFromTestServer(srv)}
	process := MakeManagedProcess(svc, DispatchOptions{Storage: storage})

	job := &Job{ID: "abc123", ServiceName: "sd-api", Path: "/v1/images/generations"}
	code, ct, body, err := process(context.Background(), job)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if code != 200 || ct != "image/png" {
		t.Errorf("code/ct = %d / %q", code, ct)
	}
	if body != nil {
		t.Errorf("body should be nil after save, got %q", string(body))
	}
	if job.ResponseFile == "" {
		t.Error("ResponseFile not set")
	}
	if job.URL != "/outputs/sd-api_abc123.png" {
		t.Errorf("URL = %q", job.URL)
	}
}

func TestManagedProcessNoStorageKeepsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("STREAMED"))
	}))
	defer srv.Close()

	svc := setup.ServiceEntry{Name: "vllm", Addr: addrFromTestServer(srv)}
	process := MakeManagedProcess(svc, DispatchOptions{}) // no storage

	job := &Job{ID: "x", ServiceName: "vllm", Path: "/v1/chat"}
	_, _, body, _ := process(context.Background(), job)
	if string(body) != "STREAMED" {
		t.Errorf("body = %q", string(body))
	}
	if job.ResponseFile != "" {
		t.Errorf("ResponseFile should be empty, got %q", job.ResponseFile)
	}
}

func TestManagedProcessHTTPErrorPropagates(t *testing.T) {
	// 5xx 응답은 err 가 아니라 code/body 로 전달돼야 함 (queue.runJob 의 분기를
	// 위해). err 는 transport-level 오류만 표현.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		w.Write([]byte(`{"error":"oops"}`))
	}))
	defer srv.Close()

	svc := setup.ServiceEntry{Name: "x", Addr: addrFromTestServer(srv)}
	process := MakeManagedProcess(svc, DispatchOptions{})

	code, _, body, err := process(context.Background(), &Job{Path: "/x"})
	if err != nil {
		t.Errorf("transport err should be nil, got %v", err)
	}
	if code != 500 {
		t.Errorf("code = %d", code)
	}
	if !strings.Contains(string(body), "oops") {
		t.Errorf("body = %q", string(body))
	}
}

func TestManagedProcessNetworkError(t *testing.T) {
	// Connection refused — return err, not 5xx.
	svc := setup.ServiceEntry{Name: "x", Addr: "127.0.0.1:1"} // unreachable
	process := MakeManagedProcess(svc, DispatchOptions{})

	_, _, _, err := process(context.Background(), &Job{Path: "/x"})
	if err == nil {
		t.Error("expected network error")
	}
}

func TestManagedProcessContextCancel(t *testing.T) {
	// 서버가 응답 안 보내고 늘어지는 동안 ctx cancel → err.
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
		w.WriteHeader(200)
	}))
	defer srv.Close()
	defer close(block)

	svc := setup.ServiceEntry{Name: "x", Addr: addrFromTestServer(srv)}
	process := MakeManagedProcess(svc, DispatchOptions{})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, _, _, err := process(ctx, &Job{Path: "/x"})
	if err == nil {
		t.Error("expected context cancel error")
	}
}

func TestManagedProcessAddrFuncOverride(t *testing.T) {
	// AddrFunc 가 svc.Addr 무시하고 다른 주소 가리키는지.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	called := false
	svc := setup.ServiceEntry{Name: "x", Addr: "ignored:9999"}
	process := MakeManagedProcess(svc, DispatchOptions{
		AddrFunc: func(s setup.ServiceEntry) string {
			called = true
			return addrFromTestServer(srv)
		},
	})

	_, _, body, err := process(context.Background(), &Job{Path: "/x"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !called {
		t.Error("AddrFunc should be called")
	}
	if string(body) != "ok" {
		t.Errorf("body = %q", string(body))
	}
}

func TestManagedProcessThroughQueueWorker(t *testing.T) {
	// 통합 테스트: 실제 Queue+Worker 가 dispatcher 를 ProcessFunc 으로 사용.
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("done"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	storage, _ := NewStorage(dir, time.Hour)

	svc := setup.ServiceEntry{Name: "sd-api", Addr: addrFromTestServer(srv)}
	q := New(Config{ServiceName: "sd-api", Concurrency: 1})
	q.SetCleanupHook(storage.Cleanup)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Worker(ctx, MakeManagedProcess(svc, DispatchOptions{Storage: storage}))

	job, _ := q.Submit("/v1/images/generations", []byte(`{"prompt":"cat"}`), http.Header{})

	waitCtx, cn := context.WithTimeout(context.Background(), 2*time.Second)
	defer cn()
	done, err := q.Wait(waitCtx, job.ID)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if done.Status != StatusDone {
		t.Errorf("status = %s", done.Status)
	}
	if hits != 1 {
		t.Errorf("server hits = %d", hits)
	}
	if done.URL == "" {
		t.Error("URL not set after storage save")
	}
}
