package rvnodes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Both wire shapes have to decode: the RV answers with a bare array unless
// page/limit is supplied, and isannd proxies the body verbatim.
func TestDecodeBothShapes(t *testing.T) {
	array := `[{"id":"0xaaa","addr":"203.0.113.9:7443","auth_mode":"public"}]`
	paged := `{"nodes":[{"id":"0xaaa","addr":"203.0.113.9:7443","auth_mode":"public"}],"total":1,"page":1,"limit":50}`

	for name, body := range map[string]string{"array": array, "paged": paged} {
		nodes, err := Decode([]byte(body))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(nodes) != 1 || nodes[0].ID != "0xaaa" {
			t.Fatalf("%s: got %+v", name, nodes)
		}
	}
}

func TestDecodeLeadingWhitespace(t *testing.T) {
	nodes, err := Decode([]byte("\n  [{\"id\":\"0xaaa\",\"addr\":\"1.2.3.4:1\"}]"))
	if err != nil || len(nodes) != 1 {
		t.Fatalf("got %v, %v", nodes, err)
	}
}

// An empty auth_mode and the legacy "open" both mean the node admits anonymous
// callers — the receiving gate treats them that way, so the directory reader
// must agree or probes get aimed at the wrong half of the network.
func TestIsPublic(t *testing.T) {
	cases := map[string]bool{
		"public": true, "open": true, "": true, "PUBLIC": true, " public ": true,
		"protected": false, "Protected": false,
	}
	for mode, want := range cases {
		if got := (Node{AuthMode: mode}).IsPublic(); got != want {
			t.Errorf("auth_mode %q: IsPublic = %v, want %v", mode, got, want)
		}
	}
}

func TestSlash24(t *testing.T) {
	cases := map[string]string{
		"203.0.113.9:7443": "203.0.113",
		"203.0.113.9":      "203.0.113",
		"10.0.0.255:1":     "10.0.0",
		"[::1]:7443":       "", // IPv6 has no /24 analogue here
		"garbage":          "",
		"":                 "",
	}
	for addr, want := range cases {
		if got := (Node{Addr: addr}).Slash24(); got != want {
			t.Errorf("addr %q: Slash24 = %q, want %q", addr, got, want)
		}
	}
}

// Nodes sharing a /24 must land in one bucket (they get fired at together),
// and an unparseable address must NOT collapse them all into a shared bucket —
// that would make unrelated nodes look like one owner's farm.
func TestGroupBySlash24(t *testing.T) {
	nodes := []Node{
		{ID: "a", Addr: "203.0.113.1:1"},
		{ID: "b", Addr: "203.0.113.2:1"},
		{ID: "c", Addr: "198.51.100.1:1"},
		{ID: "d", Addr: "garbage"},
		{ID: "e", Addr: "[::1]:1"},
	}
	g := GroupBySlash24(nodes)
	if len(g["203.0.113"]) != 2 {
		t.Errorf("203.0.113 bucket = %v, want 2 nodes", g["203.0.113"])
	}
	if len(g["198.51.100"]) != 1 {
		t.Errorf("198.51.100 bucket = %v, want 1", g["198.51.100"])
	}
	if len(g["node:d"]) != 1 || len(g["node:e"]) != 1 {
		t.Errorf("unparseable addresses must each get their own bucket, got %v", g)
	}
}

// Firing at a service that is still loading its weights would score a healthy
// node as "did not answer", so ServerLoading must not qualify.
func TestTextService(t *testing.T) {
	t.Run("picks ready text engine", func(t *testing.T) {
		n := Node{Services: []Service{
			{Name: "sd-api", Engine: "sd", ServerReady: true},
			{Name: "llm-api", Engine: "llama", ServerReady: true},
		}}
		s, ok := n.TextService()
		if !ok || s.Name != "llm-api" {
			t.Fatalf("got %+v, %v", s, ok)
		}
	})

	t.Run("loading does not count", func(t *testing.T) {
		n := Node{Services: []Service{
			{Name: "llm-api", Engine: "llama", ServerLoading: true},
		}}
		if _, ok := n.TextService(); ok {
			t.Error("a loading engine was treated as ready")
		}
	})

	t.Run("no text engine", func(t *testing.T) {
		n := Node{Services: []Service{{Name: "sd-api", Engine: "sd", ServerReady: true}}}
		if _, ok := n.TextService(); ok {
			t.Error("an image engine was returned as a text service")
		}
	})

	// 🔴 The shape live directories actually have. `engine` is omitempty and
	// stations do not set it, so judging on it alone made EVERY node read as
	// "serves no text" and the prober found nothing to fire at.
	t.Run("no engine falls back to the service name", func(t *testing.T) {
		n := Node{Services: []Service{
			{Name: "llm-api", ServerReady: true, Model: "qwen2.5-14b.gguf"},
		}}
		s, ok := n.TextService()
		if !ok || s.Name != "llm-api" {
			t.Fatalf("got %+v, %v", s, ok)
		}
	})

	t.Run("vllm-api by name", func(t *testing.T) {
		n := Node{Services: []Service{{Name: "vllm-api", ServerReady: true}}}
		if _, ok := n.TextService(); !ok {
			t.Error("vllm-api was not recognised by name")
		}
	})

	// The fallback must not become "anything ending in -api".
	t.Run("name fallback does not sweep in other services", func(t *testing.T) {
		for _, name := range []string{"sd-api", "clip-api", "whisper-api"} {
			n := Node{Services: []Service{{Name: name, ServerReady: true}}}
			if _, ok := n.TextService(); ok {
				t.Errorf("%q was treated as a text service", name)
			}
		}
	})

	// A declared engine is the more specific statement and wins over the name.
	t.Run("engine wins over name", func(t *testing.T) {
		n := Node{Services: []Service{{Name: "llm-api", Engine: "sd", ServerReady: true}}}
		if _, ok := n.TextService(); ok {
			t.Error("a declared non-text engine was overridden by the name")
		}
	})
}

func TestFetch(t *testing.T) {
	var gotPath, gotSession string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		gotSession = r.Header.Get("X-ISANN-Session")
		_ = json.NewEncoder(w).Encode([]Node{
			{ID: "0xaaa", Addr: "203.0.113.9:7443", AuthMode: "public",
				Services: []Service{{Name: "llm-api", Engine: "llama", ServerReady: true, MaxQueue: 8}}},
		})
	}))
	defer srv.Close()

	nodes, err := Fetch(srv.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	// 🔴 The bridge path, not /internal/api/list/nodes. The latter is
	// operator-gated, and a background prober cannot depend on a session that
	// expires — it would silently stop finding targets.
	if gotPath != "/internal/rv/v1/nodes?online=true" {
		t.Errorf("request path = %q", gotPath)
	}
	if gotSession != "" {
		t.Errorf("a session header was sent (%q); the bridge takes none", gotSession)
	}
	if len(nodes) != 1 || nodes[0].Services[0].MaxQueue != 8 {
		t.Fatalf("got %+v", nodes)
	}
	// max_queue is nested per service, not top level — a reader that looks for
	// it on the node would silently see zero.
	if s, ok := nodes[0].TextService(); !ok || s.MaxQueue != 8 {
		t.Errorf("text service = %+v, %v", s, ok)
	}
}

func TestFetchErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"no rv configured"}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	if _, err := Fetch(srv.URL, false); err == nil {
		t.Fatal("want an error for a non-200 response")
	}
}
