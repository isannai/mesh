package station

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/isannai/mesh/pkg/setup"
)

// A docker engine is ready when its probe answers AND isannd says it started
// the engine. An answer from something else at the engine's address does not
// make a stopped engine ready; an isannd that does not say (older, or an
// engine it does not manage) is trusted on the answer alone, as before.
func TestPollServiceViaIsanndNeedsAStartedEngine(t *testing.T) {
	old := isanndProbeBaseURL
	defer func() { isanndProbeBaseURL = old }()

	cases := []struct {
		name  string
		reply string
		ready bool
	}{
		{"answers and started", `{"http_probed":true,"http_ok":true,"engine_started":true,"model":"m.gguf"}`, true},
		{"answers but not started", `{"http_probed":true,"http_ok":true,"engine_started":false,"model":"m.gguf"}`, false},
		{"no answer", `{"http_probed":true,"http_ok":false,"engine_started":true}`, false},
		{"isannd does not say", `{"http_probed":true,"http_ok":true,"model":"m.gguf"}`, true},
		{"no answer, isannd does not say", `{"http_probed":true,"http_ok":false}`, false},
	}
	for _, c := range cases {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(c.reply))
		}))
		isanndProbeBaseURL = ts.URL
		info, alive, _, _ := pollServiceViaIsannd("llama", setup.ServiceInfo{Name: "llm-api"})
		ts.Close()
		if alive != c.ready || info.ServerReady != c.ready {
			t.Errorf("%s: alive=%v server_ready=%v, want %v", c.name, alive, info.ServerReady, c.ready)
		}
		if !c.ready && info.Model != "" {
			t.Errorf("%s: a service that is not ready must not carry a model: %q", c.name, info.Model)
		}
	}
}
