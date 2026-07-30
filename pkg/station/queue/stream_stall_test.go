package queue

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/isannai/mesh/pkg/setup"
)

// TestStreamStallTimesOut verifies the fix for the queue-wide wedge
// (docs/bugs/2026-07-30-queue-worker-wedge-on-stream-stall.md): an engine SSE
// stream that emits a chunk and then stalls (no [DONE], connection left open)
// must NOT block the worker forever. With an idle timeout configured, the
// processor cancels the stalled call on its own — no station restart / ctx
// cancel needed — and returns an error so the worker slot is freed.
func TestStreamStallTimesOut(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		if fl != nil {
			fl.Flush()
		}
		<-release // stall: hold the connection open, never send [DONE] or close
	}))
	defer srv.Close()
	defer close(release)

	svc := setup.ServiceEntry{Name: "llm", Addr: strings.TrimPrefix(srv.URL, "http://")}
	proc := MakeManagedProcess(svc, DispatchOptions{
		StreamPath: "choices[0].delta.content",
		Timeout:    300 * time.Millisecond, // idle bound
	})
	job := &Job{ID: "j1", Path: "/v1/chat/completions", Stream: true, RequestBody: []byte("{}")}

	type result struct {
		err error
	}
	res := make(chan result, 1)
	go func() {
		_, _, _, err := proc(context.Background(), job)
		res <- result{err}
	}()

	select {
	case r := <-res:
		// Must have self-recovered via the idle timeout, reporting an error.
		if r.err == nil {
			t.Fatal("stalled stream returned success — expected an idle-timeout error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("processor did not return on its own — idle timeout not firing (wedge still present)")
	}
}

// TestStreamProgressNotKilled verifies the idle timeout does NOT kill a
// healthy, progressing stream: chunks arriving faster than the idle window keep
// resetting the watchdog, so a long generation completes normally.
func TestStreamProgressNotKilled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		// 12 chunks * 100ms ≈ 1.2s total — well past the 300ms idle window, but
		// each gap (100ms) is under it, so the stream must not be cut.
		for i := 0; i < 12; i++ {
			io.WriteString(w, fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"content\":\"tok%d \"}}]}\n\n", i))
			if fl != nil {
				fl.Flush()
			}
			time.Sleep(100 * time.Millisecond)
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
		Timeout:    300 * time.Millisecond, // idle bound < the 1.2s total gen
	})
	job := &Job{ID: "j2", Path: "/v1/chat/completions", Stream: true, RequestBody: []byte("{}")}

	type result struct {
		body []byte
		err  error
	}
	res := make(chan result, 1)
	go func() {
		_, _, body, err := proc(context.Background(), job)
		res <- result{body, err}
	}()

	select {
	case r := <-res:
		if r.err != nil {
			t.Fatalf("progressing stream was killed by idle timeout: %v", r.err)
		}
		if !strings.Contains(string(r.body), "tok11") {
			t.Fatalf("expected full generation in result, got: %s", string(r.body))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("processor did not complete a progressing stream")
	}
}
