// Package rvnodes decodes the RV directory (`/v1/nodes`) into typed nodes.
//
// The wire shape is re-declared in several places across the repos — the node
// CLI's table, the daemon's id resolver, the gate's sync, and this repo's own
// broker search index (pkg/control/search.go) — and they have already drifted:
// the gate still decodes `online`, `last_seen` and `status`, none of which the
// RV emits any more, so every node it sees reads as offline. This package
// exists so the prober does not add one more copy of that mistake.
//
// Fetching goes through the local isannd node-bridge, never straight to the RV:
// the daemon owns that connection, its TLS posture and the source-IP guard. The
// path is the one the control broker already uses (`fetchRVPath`), which means
// no session token and no operator gate — see Fetch.
package rvnodes

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Node is one entry of the RV directory.
//
// Only the fields a caller acts on are declared; unknown ones are dropped by
// encoding/json, so RV-side additions do not break this. Notably absent:
// `online` and `status`. `online` is a *filter* on the RV side, not a field
// (see Fetch), and per-service volatile state lives on /v1/metrics.
type Node struct {
	ID   string `json:"id"`
	Role string `json:"role,omitempty"`
	Addr string `json:"addr"`
	// ConnectedAt is when the node's CURRENT control connection was opened —
	// it comes from the RV's live connection registry, not from the node's own
	// claim, and it resets whenever the node reconnects.
	//
	// That reset is the point: it is what "has been up continuously for N
	// hours" can be measured against. RFC3339; absent when the node has no
	// live control connection.
	ConnectedAt  string    `json:"connected_at,omitempty"`
	StartedAt    string    `json:"started_at,omitempty"`
	Version      string    `json:"version,omitempty"`
	OwnerAddress string    `json:"owner_address,omitempty"`
	AuthMode     string    `json:"auth_mode,omitempty"`
	TPMVerified  bool      `json:"tpm_verified,omitempty"`
	Services     []Service `json:"services,omitempty"`
}

// Connected parses ConnectedAt. ok=false when the node has no live control
// connection, which makes it unschedulable rather than merely idle.
func (n Node) Connected() (time.Time, bool) {
	s := strings.TrimSpace(n.ConnectedAt)
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// Service is one engine a node serves.
//
// Name is what goes in the URL — `/svc/<Name>/v1/jobs`. Engine is the engine
// package ("llama", "vllm", "sd"), which is a different thing and not
// interchangeable with it.
type Service struct {
	Name          string `json:"name"`
	Type          string `json:"type,omitempty"`
	Engine        string `json:"engine,omitempty"`
	Model         string `json:"model,omitempty"`
	ModelHash     string `json:"model_hash,omitempty"`
	// ModelArch is the architecture family the node itself declares —
	// "sd15" / "sdxl" / "sd3" / "pony" / "flux", from the package.json that
	// `isann model pull --arch` wrote. Empty for text engines, which have no
	// arch hierarchy, and empty from any node older than the isannd release
	// that started stamping it, so a caller still needs a fallback.
	//
	// It decides the resolution an image probe may ask for, and that is not a
	// preference: SDXL at 512x512 returns duplicated subjects and broken
	// anatomy, SD 1.5 at 1024x1024 the same in reverse. Guessing it wrong
	// fails an honest node for the caller's mistake.
	ModelArch     string `json:"model_arch,omitempty"`
	ServerReady   bool   `json:"server_ready,omitempty"`
	ServerLoading bool   `json:"server_loading,omitempty"`
	MaxQueue      int    `json:"max_queue,omitempty"`
	Concurrency   int    `json:"concurrency,omitempty"`
	QueueDisabled bool   `json:"queue_disabled,omitempty"`
}

// AuthModePublic is the mode that admits anonymous inference.
//
// "open" is the legacy spelling and still arrives from older nodes; an empty
// value also means public, because a node that advertises nothing is treated as
// open by the receiving gate (cmd/isannd/admin_auth.go). IsPublic folds all
// three so callers do not each re-derive it — getting this wrong in the
// permissive direction would aim probes at nodes that reject them, and in the
// restrictive direction would silently skip most of the network.
const AuthModePublic = "public"

// IsPublic reports whether this node admits anonymous callers.
func (n Node) IsPublic() bool {
	switch strings.ToLower(strings.TrimSpace(n.AuthMode)) {
	case "", AuthModePublic, "open":
		return true
	}
	return false
}

// textEngines are the engine names that serve text completion.
var textEngines = map[string]bool{"llama": true, "vllm": true, "llm": true}

// textServiceNames is the fallback for nodes that advertise no engine.
//
// 🔴 Not a nicety — without it NOTHING is eligible. The station DOES populate
// `engine`; the RV's /v1/nodes builder rebuilt each service from a field
// whitelist that omitted it, so it never reached a caller. A live directory row
// from an RV predating that fix looks like:
//
//	{"name":"llm-api","launcher":"docker","model":"…","server_ready":true,…}
//
// Judging on `Engine` alone makes every node in the network read as "serves no
// text", and the only symptom is `N nodes, 0 eligible` — which says nothing
// about why.
//
// The NAME is the sounder key anyway: it is the URL segment (`/svc/<name>/…`),
// so it is part of the contract and it actually exists. Listed explicitly so
// that sd-api and clip-api are not swept in by a "-api" suffix rule.
var textServiceNames = map[string]bool{"llm-api": true, "vllm-api": true}

// knownEngine reports whether an engine name is one this package can classify.
//
// 🔴 An engine we do not RECOGNISE is not the same as no engine, and collapsing
// the two is how a working node disappears. While `engine` never reached a
// caller, every service fell through to the name check; now that it does, a
// lookup that answers false for an unlisted name — "comfyui" against the
// "comfy" this table holds, an engine package added after this build — would
// reject a node the name check was catching yesterday. So an unknown engine
// defers to the name; only a KNOWN one is allowed to be the final word.
func knownEngine(e string) bool { return textEngines[e] || imageEngines[e] }

// isTextService reports whether a service serves text completion.
//
// A recognised engine wins — it is the more specific statement, and it is what
// lets a node named "chat-api" be classified at all. Anything else answers by
// name, which is the URL segment (`/svc/<name>/…`) and therefore part of the
// contract.
func isTextService(s Service) bool {
	if e := strings.ToLower(strings.TrimSpace(s.Engine)); knownEngine(e) {
		return textEngines[e]
	}
	return textServiceNames[strings.ToLower(strings.TrimSpace(s.Name))]
}

// TextService returns a running text service. ok=false when the node serves no
// ready text engine.
//
// ServerLoading is not enough to shoot at: the container is up but the weights
// are still being read, and a request lands in a queue that may outlive the
// response deadline. That would score as "did not answer" against a node doing
// nothing wrong.
func (n Node) TextService() (Service, bool) {
	for _, s := range n.Services {
		if s.ServerReady && isTextService(s) {
			return s, true
		}
	}
	return Service{}, false
}

// imageEngines / imageServiceNames mirror the text pair, and the name fallback
// carries the same weight here — see knownEngine.
var imageEngines = map[string]bool{"sd": true, "comfy": true}
var imageServiceNames = map[string]bool{"sd-api": true}

// isImageService is the image half of isTextService, with the same precedence.
func isImageService(s Service) bool {
	if e := strings.ToLower(strings.TrimSpace(s.Engine)); knownEngine(e) {
		return imageEngines[e]
	}
	return imageServiceNames[strings.ToLower(strings.TrimSpace(s.Name))]
}

// ImageService returns a running image service. ok=false when the node serves
// none.
//
// Deliberately separate from TextService rather than a parameterised lookup: a
// node commonly runs both, and the two tracks fire different requests at
// different cadences. Collapsing them would make "which service is this shot
// for" ambiguous at exactly the point it must not be.
func (n Node) ImageService() (Service, bool) {
	for _, s := range n.Services {
		if s.ServerReady && isImageService(s) {
			return s, true
		}
	}
	return Service{}, false
}

// Slash24 returns the /24 prefix of the node's address ("203.0.113.9:7443" →
// "203.0.113"). Empty when the address is not IPv4.
//
// Two opposite rules need this. Nodes in one /24 must be fired at SIMULTANEOUSLY
// (a sybil farm behind one GPU fails a concurrent burst it would pass serially),
// while a re-check panel must be drawn from DIFFERENT /24s (or one owner's five
// machines form their own majority). Recording it at fire time is the point:
// derive it later from a stored address and you get the address the node has
// now, not the one it had then.
func (n Node) Slash24() string {
	host := n.Addr
	if h, _, err := net.SplitHostPort(n.Addr); err == nil {
		host = h
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return ""
	}
	v4 := ip.To4()
	if v4 == nil {
		return "" // IPv6 has no meaningful /24 analogue here
	}
	return fmt.Sprintf("%d.%d.%d", v4[0], v4[1], v4[2])
}

// GroupBySlash24 buckets nodes by /24. Nodes with no usable prefix each get
// their own bucket keyed by node id, so an IPv6 or malformed address is fired
// at alone rather than silently lumped with every other unparseable one.
func GroupBySlash24(nodes []Node) map[string][]Node {
	out := map[string][]Node{}
	for _, n := range nodes {
		k := n.Slash24()
		if k == "" {
			k = "node:" + n.ID
		}
		out[k] = append(out[k], n)
	}
	return out
}

// fetchTimeout bounds one directory read. The bridge's own call to the RV is
// capped at 10s, so this only needs to outlive that.
const fetchTimeout = 20 * time.Second

// bridgePath is the isannd node-bridge prefix that reverse-proxies the RV's
// REST API. Everything under it is loopback-guarded and TLS-terminated by the
// daemon.
//
// 🔴 This is NOT /internal/api/list/nodes. That route is operator-gated, which
// would make a background prober depend on someone having run
// `isann auth unlock` recently — and sessions expire, so the prober would
// silently stop finding targets one day.
//
// The bridge is not automatically session-free either: isannd wraps its whole
// internal mux, bridge routes included, in a secure-by-default auth gate, and
// this path had to be named as an explicit exception there (isannd's
// internalAuthClass). Before that it answered 401 to every caller who did not
// hold a session — including pkg/control's search index, which reaches the same
// directory the same way. An isannd older than that fix still 401s.
const bridgePath = "/internal/rv"

// Fetch reads the RV directory through the local isannd node-bridge.
//
// onlineOnly asks the RV to drop nodes it has not heard from — its threshold
// there is 90 SECONDS, not the 15 minutes some design notes assume, which
// matters because it decides who is even a candidate.
//
// No session, no credential: the bridge is loopback-only and read-only here.
//
// There is no auth_mode filter on the RV side and this does not add one: callers
// filter with IsPublic after the fact. Adding a query parameter would mean
// changing and redeploying the RV, and the list is small enough that the
// filtering is free.
func Fetch(isanndURL string, onlineOnly bool) ([]Node, error) {
	q := url.Values{}
	if onlineOnly {
		q.Set("online", "true")
	}
	endpoint := strings.TrimRight(isanndURL, "/") + bridgePath + "/v1/nodes"
	if len(q) > 0 {
		endpoint += "?" + q.Encode()
	}

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	c := &http.Client{Timeout: fetchTimeout}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("list nodes: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list nodes: %s: %s", resp.Status, snippet(body))
	}
	return Decode(body)
}

// Decode parses a /v1/nodes body in either shape the RV emits.
//
// Without page/limit it answers with a bare array; with either it wraps in
// {nodes,total,page,limit}. isannd proxies the body verbatim, so both reach a
// caller unchanged and both have to be accepted — a decoder that assumes one
// works until the day someone adds paging.
func Decode(body []byte) ([]Node, error) {
	trimmed := strings.TrimLeft(string(body), " \t\r\n")
	if strings.HasPrefix(trimmed, "[") {
		var nodes []Node
		if err := json.Unmarshal(body, &nodes); err != nil {
			return nil, fmt.Errorf("decode node array: %w", err)
		}
		return nodes, nil
	}
	var paged struct {
		Nodes []Node `json:"nodes"`
	}
	if err := json.Unmarshal(body, &paged); err != nil {
		return nil, fmt.Errorf("decode node page: %w", err)
	}
	return paged.Nodes, nil
}

// snippet trims a response body for an error message.
func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
