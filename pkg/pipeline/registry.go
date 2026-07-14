package pipeline

import (
	"context"
	"net/http"
	"sync"

	"github.com/isannai/mesh/pkg/glog"
)

// InputItem is one upstream value reaching a node's input handle.
type InputItem struct {
	FromID string `json:"fromId"`
	Value  any    `json:"value"`
}

// NodeCaller abstracts an in-process call to a provider node service,
// bypassing broker's HTTP mux. Implemented by broker; consumed by AI
// entities (llmNode, sdNode) via ExecCtx.NodeCaller.
//
// The request's URL.Path (and query) identify the service endpoint to hit
// on the provider — scheme/host are ignored. The returned Response.Body
// is bound to the underlying QUIC stream; closing it closes the stream.
type NodeCaller interface {
	CallNode(ctx context.Context, nodeID, service string, req *http.Request) (*http.Response, error)
}

// ExecCtx is the per-node execution context passed to Entity.Execute.
// It is NOT safe for concurrent writes — the runner executes steps serially.
type ExecCtx struct {
	Graph          *Graph
	StepResults    map[string]any
	InputData      any                    // primary input edge (non-config)
	InputsByHandle map[string][]InputItem // ALL incoming edges grouped by handle
	AuthHeaders    map[string]string
	NetworkNodes   []any
	HTTPClient     *http.Client
	BaseURL        string     // legacy: broker self URL — AI entities now prefer NodeCaller
	NodeCaller     NodeCaller // direct in-process call into broker (no HTTP loopback)
	Logger         *glog.Logger
}

// Entity is a node type implementation. Each registered entity handles one
// node.type and declares its input/output port schema for static validation.
//
// Entities own their own data schema: each implementation parses the raw
// Node.Data (json.RawMessage) into an internal struct before execution.
type Entity interface {
	Type() string                // e.g. "aiNode", "inputNode"
	Inputs() map[string]string   // handle → typeName ("any"|"text"|"json"|"object"|"image"|"x|y")
	Outputs() map[string]string  // handle → typeName
	Execute(ctx context.Context, node Node, ec *ExecCtx) (any, error)
}

// Registry is a thread-safe entity lookup by node type.
type Registry struct {
	mu       sync.RWMutex
	entities map[string]Entity
}

// NewRegistry constructs an empty entity registry.
func NewRegistry() *Registry {
	return &Registry{entities: make(map[string]Entity)}
}

// Register installs an entity, overwriting any previous entry for the type.
func (r *Registry) Register(e Entity) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entities[e.Type()] = e
}

// Get returns the entity for a node type, or nil when unregistered.
func (r *Registry) Get(nodeType string) Entity {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.entities[nodeType]
}

// Types returns all registered entity type names (sorted is not guaranteed).
func (r *Registry) Types() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.entities))
	for t := range r.entities {
		out = append(out, t)
	}
	return out
}

// Describe returns input/output schemas for every registered entity.
// Used by /v1/pipeline/entities for frontend discovery.
func (r *Registry) Describe() map[string]EntityDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]EntityDescriptor, len(r.entities))
	for t, e := range r.entities {
		out[t] = EntityDescriptor{
			Type:    t,
			Inputs:  e.Inputs(),
			Outputs: e.Outputs(),
		}
	}
	return out
}

// EntityDescriptor is the public shape of an entity's schema.
type EntityDescriptor struct {
	Type    string            `json:"type"`
	Inputs  map[string]string `json:"inputs"`
	Outputs map[string]string `json:"outputs"`
}
