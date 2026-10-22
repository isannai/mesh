package station

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/isannai/mesh/pkg/setup"
	"github.com/isannai/mesh/pkg/tunnel"
)

// A standard call (/svc/<name>/v1/chat/completions) to another node used to ignore
// ?timeout=: the station passed the query on to the engine and then waited without a
// limit, so ?timeout=2 on a long answer held the connection until the engine finished.
func svcProviderFor(engineURL string) *Provider {
	p := &Provider{Base: &tunnel.Base{}}
	p.Cfg.Services = []setup.ServiceEntry{{Name: "llm-api", Addr: strings.TrimPrefix(engineURL, "http://")}}
	return p
}

func TestSvcProxyHonorsTimeoutQuery(t *testing.T) {
	// The engine answers after 5 s, or gives up as soon as the station drops the call.
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body) // the server sees the dropped connection only after the body is read
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
			w.WriteHeader(200)
		}
	}))
	defer engine.Close()

	r := httptest.NewRequest(http.MethodPost, "/svc/llm-api/v1/chat/completions?timeout=1", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	start := time.Now()
	svcProviderFor(engine.URL).HandleServiceProxy(w, r)
	took := time.Since(start)

	if w.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504 (%s)", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "within 1 seconds") {
		t.Errorf("the answer should name the limit: %s", w.Body)
	}
	if took > 3*time.Second {
		t.Errorf("took %v: the 1 s limit was not applied", took)
	}
}

// The query still goes on to the engine, and a call that answers inside its limit is untouched.
func TestSvcProxyWithinTheLimitAndQueryPassedOn(t *testing.T) {
	var query string
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer engine.Close()

	r := httptest.NewRequest(http.MethodPost, "/svc/llm-api/v1/chat/completions?timeout=30", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	svcProviderFor(engine.URL).HandleServiceProxy(w, r)
	if w.Code != 200 || w.Body.String() != `{"ok":true}` {
		t.Fatalf("a call inside its limit: %d %s", w.Code, w.Body)
	}
	if query != "timeout=30" {
		t.Errorf("the engine saw query %q, want timeout=30", query)
	}
}

// Without a usable ?timeout= there is still no limit (an image may take minutes).
func TestSvcProxyNoLimitWithoutAUsableTimeout(t *testing.T) {
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1200 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer engine.Close()

	for _, q := range []string{"", "?timeout=0", "?timeout=-3", "?timeout=abc"} {
		r := httptest.NewRequest(http.MethodPost, "/svc/llm-api/v1/chat/completions"+q, strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		svcProviderFor(engine.URL).HandleServiceProxy(w, r)
		if w.Code != 200 {
			t.Errorf("query %q: status = %d, want 200 (no limit)", q, w.Code)
		}
	}
}
