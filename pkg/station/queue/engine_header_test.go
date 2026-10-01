package queue

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/isannai/mesh/pkg/setup"
)

// callerCredentials are the headers an inference request reaches the station
// with that belong to the caller, not the request: the signature, its message,
// and a protected node's entry token.
var callerCredentials = http.Header{
	"Authorization":      {"ISANN 0xsig"},
	"X-Isann-Message":    {"bWVzc2FnZQ=="},
	"X-Isann-Credential": {"ianacc_token"},
	"X-Isann-Request-Id": {"req-1"},
}

// SEC-17: a queued job went to the engine with every header the caller sent,
// signature and entry token included. The engine is a third-party image that
// may log them.
func TestQueuedJobReachesTheEngineWithoutCallerCredentials(t *testing.T) {
	var got http.Header
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(200)
	}))
	defer engine.Close()

	hdr := http.Header{"Content-Type": {"application/json"}, "X-Test-Hint": {"hello"}}
	for k, v := range callerCredentials {
		hdr[k] = v
	}
	process := MakeManagedProcess(setup.ServiceEntry{Name: "llm-api", Addr: addrFromTestServer(engine)}, DispatchOptions{})
	if _, _, _, err := process(context.Background(), &Job{ID: "j1", ServiceName: "llm-api", Path: "/v1/chat/completions",
		RequestBody: []byte(`{}`), RequestHeader: hdr}); err != nil {
		t.Fatal(err)
	}

	for k := range callerCredentials {
		if v := got.Get(k); v != "" {
			t.Errorf("engine received %s: %q", k, v)
		}
	}
	if got.Get("X-Test-Hint") != "hello" || got.Get("Content-Type") != "application/json" {
		t.Errorf("the request's own headers did not arrive: %v", got)
	}
}
