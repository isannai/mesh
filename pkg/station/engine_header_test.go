package station

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/isannai/mesh/pkg/setup"
	"github.com/isannai/mesh/pkg/tunnel"
)

// SEC-17: a direct /svc call went to the engine with every header the caller
// sent but four hop-by-hop ones, signature and entry token included. The engine
// is a third-party image that may log them.
func TestDirectCallReachesTheEngineWithoutCallerCredentials(t *testing.T) {
	var got http.Header
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(200)
	}))
	defer engine.Close()

	p := &Provider{Base: &tunnel.Base{}}
	p.Cfg.Services = []setup.ServiceEntry{{Name: "llm-api", Addr: strings.TrimPrefix(engine.URL, "http://")}}

	r := httptest.NewRequest(http.MethodPost, "/svc/llm-api/v1/chat/completions", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "ISANN 0xsig")
	r.Header.Set("X-ISANN-Message", "bWVzc2FnZQ==")
	r.Header.Set("X-ISANN-Credential", "ianacc_token")
	w := httptest.NewRecorder()
	p.HandleServiceProxy(w, r)
	if got == nil {
		t.Fatalf("the engine was not reached: %d %s", w.Code, w.Body)
	}

	for _, k := range []string{"Authorization", "X-ISANN-Message", "X-ISANN-Credential"} {
		if v := got.Get(k); v != "" {
			t.Errorf("engine received %s: %q", k, v)
		}
	}
	if got.Get("Content-Type") != "application/json" {
		t.Errorf("the request's own headers did not arrive: %v", got)
	}
}
