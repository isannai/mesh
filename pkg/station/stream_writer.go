package station

import (
	"fmt"
	"net/http"

	"github.com/daesob/http3proxy/pkg/glog"
	"github.com/quic-go/quic-go"
)

// streamResponseWriter adapts a QUIC stream to http.ResponseWriter so handlers
// built on net/http (e.g. JobsHandler) can serve provider control-plane
// requests directly. The underlying transport is HTTP/1.1 over the QUIC
// stream — same wire format provider already uses elsewhere via
// writeHTTPResponse, just routed through the standard ResponseWriter API.
//
// Behavior notes:
//   - Header() is mutable until the first Write/WriteHeader; after that the
//     status line + headers are flushed and further changes are ignored.
//   - Write() implicitly writes a 200 status if WriteHeader was not called,
//     matching net/http server semantics. Caller is responsible for setting
//     Content-Type before the first Write when known.
//   - Content-Length must be set explicitly by the handler when known; if
//     omitted, the response is read until stream EOF on the client side.
//     handlers that buffer (writeJSON, http.ServeFile) already do this.
type streamResponseWriter struct {
	stream  quic.Stream
	headers http.Header
	code    int
	written bool
}

func newStreamResponseWriter(s quic.Stream) *streamResponseWriter {
	return &streamResponseWriter{
		stream:  s,
		headers: make(http.Header),
		code:    http.StatusOK,
	}
}

func (w *streamResponseWriter) Header() http.Header {
	return w.headers
}

func (w *streamResponseWriter) WriteHeader(code int) {
	if w.written {
		return
	}
	w.code = code
	w.written = true
	fmt.Fprintf(w.stream, "HTTP/1.1 %d %s\r\n", code, http.StatusText(code))
	_ = w.headers.Write(w.stream)
	fmt.Fprint(w.stream, "\r\n")
}

func (w *streamResponseWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	return w.stream.Write(b)
}

// serveJobsHandler dispatches a parsed broker request to the JobsHandler
// using a stream-backed http.ResponseWriter. The forwardPath argument is
// the path JobsHandler should see (e.g. /v1/jobs after stripping the
// /provider prefix used in the broker → provider routing layer).
//
// When the queue subsystem hasn't been initialized (legacy mode where
// engine-runner still owns the queue) this returns 503 so callers can fall
// back gracefully — broker frontend may run ahead of provider deployment.
func (p *Provider) serveJobsHandler(stream quic.Stream, req *http.Request, forwardPath string) {
	w := newStreamResponseWriter(stream)
	if p.jobsHandler == nil {
		p.Log.Log(glog.Debug, "[station] jobs handler not initialized — queue subsystem disabled")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"queue_subsystem_disabled"}`))
		return
	}
	// Rewrite path so JobsHandler's internal routing (which expects
	// /v1/jobs, /v1/jobs/{id}, /outputs/{name}, /v1/queue/stats) matches.
	req.URL.Path = forwardPath

	switch {
	case forwardPath == "/v1/jobs":
		p.jobsHandler.handleSubmit(w, req)
	case forwardPath == "/v1/queue/stats":
		p.jobsHandler.handleStats(w, req)
	case len(forwardPath) > len("/v1/jobs/") && forwardPath[:len("/v1/jobs/")] == "/v1/jobs/":
		p.jobsHandler.handleByID(w, req)
	case len(forwardPath) > len("/outputs/") && forwardPath[:len("/outputs/")] == "/outputs/":
		p.jobsHandler.handleOutputs(w, req)
	default:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
	}
}
