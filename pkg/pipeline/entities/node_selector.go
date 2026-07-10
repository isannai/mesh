package entities

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/isannai/mesh/pkg/pipeline"
)

type nodeSelectorData struct {
	Strategy      string `json:"strategy"`      // "fixed" | "first_online"
	NodeID        string `json:"nodeId,omitempty"`
	Service       string `json:"service,omitempty"`
	Model         string `json:"model,omitempty"`
	Gpu           string `json:"gpu,omitempty"`
	AuthSignature string `json:"authSignature,omitempty"`
	AuthMessage   string `json:"authMessage,omitempty"`
}

// NodeSelector emits {strategy, nodeId, service, model, gpu, auth} for
// downstream aiNode consumption.
//
//   - "fixed"        → use data.nodeId verbatim
//   - "first_online" → scan ctx.NetworkNodes filtered by service/model/gpu
type NodeSelector struct{}

func (NodeSelector) Type() string               { return "nodeSelectorNode" }
func (NodeSelector) Inputs() map[string]string  { return map[string]string{} }
func (NodeSelector) Outputs() map[string]string { return map[string]string{"default": "object"} }

func (NodeSelector) Execute(_ context.Context, node pipeline.Node, ec *pipeline.ExecCtx) (any, error) {
	var d nodeSelectorData
	if len(node.Data) > 0 {
		_ = json.Unmarshal(node.Data, &d)
	}

	var nodeID string
	switch d.Strategy {
	case "fixed":
		nodeID = d.NodeID
	case "first_online":
		nodeID = findFirstOnline(ec.NetworkNodes, d.Service, d.Model, d.Gpu)
	}

	var auth map[string]string
	if d.AuthSignature != "" || d.AuthMessage != "" {
		auth = map[string]string{
			"signature": d.AuthSignature,
			"message":   d.AuthMessage,
		}
	}

	return map[string]any{
		"strategy": d.Strategy,
		"nodeId":   nodeID,
		"service":  d.Service,
		"model":    d.Model,
		"gpu":      d.Gpu,
		"auth":     auth,
	}, nil
}

// findFirstOnline scans `nodes` (raw decoded JSON) for the first online
// node matching the optional service/model/gpu filters. Returns "" when
// no match. nodes elements are expected to follow the rendezvous /v1/nodes
// shape (online bool, services []{name,model}, hardware.gpus []{name}).
func findFirstOnline(nodes []any, service, model, gpu string) string {
	for _, raw := range nodes {
		n, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if online, _ := n["online"].(bool); !online {
			continue
		}
		svcs := asAnySlice(n["services"])
		if service != "" && !anyServiceMatches(svcs, "name", service) && !anyServiceMatches(svcs, "service", service) {
			continue
		}
		if model != "" && !anyServiceMatches(svcs, "model", model) {
			continue
		}
		if gpu != "" {
			hw, _ := n["hardware"].(map[string]any)
			gpus := asAnySlice(hw["gpus"])
			if !anyContainsCI(gpus, "name", gpu) {
				continue
			}
		}
		if id, ok := n["id"].(string); ok && id != "" {
			return id
		}
	}
	return ""
}

func asAnySlice(v any) []any {
	if v == nil {
		return nil
	}
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

func anyServiceMatches(svcs []any, key, want string) bool {
	for _, s := range svcs {
		m, ok := s.(map[string]any)
		if !ok {
			continue
		}
		if str, _ := m[key].(string); str == want {
			return true
		}
	}
	return false
}

func anyContainsCI(items []any, key, want string) bool {
	w := strings.ToLower(want)
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if str, _ := m[key].(string); strings.Contains(strings.ToLower(str), w) {
			return true
		}
	}
	return false
}
