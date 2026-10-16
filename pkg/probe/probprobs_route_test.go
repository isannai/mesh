package probe

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/isannai/mesh/pkg/engine/manifest"
	"github.com/isannai/mesh/pkg/rvnodes"
)

// The reference check asks the service's DEFAULT chat run for candidates, the same run
// the quiz uses. A node whose default run does not declare logprobs and top-logprobs
// cannot answer: nothing is submitted, and the check fails as no_probs.

type routeNode struct {
	mu    sync.Mutex
	def   []string // param names of the default run
	posts []routePost
}

type routePost struct {
	Path string
	Run  map[string]any
}

func params(names []string) []manifest.RunParam {
	out := make([]manifest.RunParam, 0, len(names))
	for _, n := range names {
		out = append(out, manifest.RunParam{Name: n, Type: "string"})
	}
	return out
}

func (c *routeNode) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case strings.HasSuffix(r.URL.Path, "/run-schema"):
		_ = json.NewEncoder(w).Encode(map[string]any{
			"default":  manifest.RunSpec{Path: "/v1/chat/completions", Params: params(c.def)},
			"variants": []manifest.RunSpec{},
		})
	case strings.HasSuffix(r.URL.Path, "/v1/jobs") && r.Method == http.MethodPost:
		var body struct {
			Path string         `json:"path"`
			Run  map[string]any `json:"run"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		c.posts = append(c.posts, routePost{Path: body.Path, Run: body.Run})
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"job_id": "j1", "queue_max": 8})
	default:
		http.NotFound(w, r)
	}
}

func routeTarget() Target {
	return Target{Node: rvnodes.Node{ID: "n1"}, Service: rvnodes.Service{Name: "llm-api"}}
}

func (c *routeNode) submit(t *testing.T) (routePost, error) {
	t.Helper()
	srv := httptest.NewServer(c)
	t.Cleanup(srv.Close)
	_, err := NewFirer(srv.URL).SubmitProbs(routeTarget(), "Q: x A:")
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.posts) == 0 {
		return routePost{}, err
	}
	return c.posts[len(c.posts)-1], err
}

var chatWithProbs = []string{"prompt", "max-tokens", "temperature", "stop", "logprobs", "top-logprobs", "cache-prompt"}

// The default run declares the candidate options: one route, no path named.
func TestProbeAsksTheDefaultRunWhenItDeclaresLogprobs(t *testing.T) {
	post, err := (&routeNode{def: chatWithProbs}).submit(t)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if post.Path != "" {
		t.Fatalf("named run path %q, want the default run (no path)", post.Path)
	}
	for k, want := range map[string]any{"logprobs": true, "top-logprobs": float64(refProbs), "max-tokens": float64(refPredict), "temperature": float64(0)} {
		if post.Run[k] != want {
			t.Errorf("run[%q] = %v, want %v", k, post.Run[k], want)
		}
	}
	if stop, _ := post.Run["stop"].([]any); len(stop) != 1 || stop[0] != "." {
		t.Errorf("stop = %v, want [.]", post.Run["stop"])
	}
	// A reused prompt cache moves the probabilities by a few percent.
	if v, ok := post.Run["cache-prompt"]; !ok || v != false {
		t.Errorf("cache-prompt = %v, want false", post.Run["cache-prompt"])
	}
}

// A name the service does not declare is not sent (here: no stop).
func TestProbeSendsOnlyDeclaredNames(t *testing.T) {
	post, err := (&routeNode{def: []string{"prompt", "logprobs", "top-logprobs"}}).submit(t)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	for _, k := range []string{"stop", "max-tokens", "temperature", "cache-prompt"} {
		if _, ok := post.Run[k]; ok {
			t.Errorf("run carries %q the service did not declare: %v", k, post.Run)
		}
	}
}

// No candidate options on the default run: the node cannot answer, and nothing is submitted.
func TestProbeWithoutLogprobsSubmitsNothing(t *testing.T) {
	for _, def := range [][]string{
		{"prompt", "max-tokens", "temperature"},
		{"prompt", "logprobs"}, // top-logprobs is missing: the candidates would be one token wide
	} {
		c := &routeNode{def: def}
		post, err := c.submit(t)
		if err != errNoProbeRun {
			t.Fatalf("def %v: err = %v, want errNoProbeRun", def, err)
		}
		if post.Run != nil {
			t.Errorf("def %v: a job was submitted anyway: %v", def, post)
		}
	}
}

// What llama-server returns for a chat request with logprobs (captured from a real
// server, 2026-10-16): the candidates are under choices[0].logprobs.content, and the
// answer text is the message content. parseTokenProbs reads it into tokens and
// candidate probabilities, keeping only the tokens that make up the text.
func TestParseTokenProbsReadsTheChatShape(t *testing.T) {
	body := `{"choices":[{"finish_reason":"length","index":0,
	  "message":{"role":"assistant","content":"12 + 7"},
	  "logprobs":{"content":[
	    {"token":"1","logprob":-0.004,"top_logprobs":[{"token":"1","logprob":-0.004},{"token":"The","logprob":-5.5}]},
	    {"token":"2","logprob":-0.08,"top_logprobs":[{"token":"2","logprob":-0.08},{"token":"9","logprob":-2.56}]},
	    {"token":" +","logprob":0,"top_logprobs":[{"token":" +","logprob":0}]},
	    {"token":" ","logprob":0,"top_logprobs":[{"token":" ","logprob":0}]},
	    {"token":"7","logprob":0,"top_logprobs":[{"token":"7","logprob":0}]},
	    {"token":" =","logprob":0,"top_logprobs":[{"token":" =","logprob":0}]}]}}]}`
	a, err := parseTokenProbs([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if a.Text != "12 + 7" {
		t.Fatalf("text = %q", a.Text)
	}
	if len(a.Tokens) != 5 {
		t.Fatalf("%d tokens, want 5 (a token past the text is not the answer)", len(a.Tokens))
	}
	first := a.Tokens[0]
	if first.Text != "1" || first.Cands["1"] < 0.99 || first.Cands["The"] > 0.01 {
		t.Errorf("first token = %+v", first)
	}
}
