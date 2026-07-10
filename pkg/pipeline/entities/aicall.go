package entities

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/isannai/mesh/pkg/pipeline"
)

// aiCallRequest captures every field the broker call helper needs, so each
// entity (llmNode, sdNode, etc.) only has to assemble its own body and
// endpoint resolution logic before delegating the network bits.
type aiCallRequest struct {
	NodeID       string                 // resolved provider node id (required)
	Service      string                 // service name on the provider (required)
	Endpoint     string                 // HTTP path on the service (required)
	Method       string                 // HTTP method (default POST)
	Body         map[string]any         // JSON body
	WaitMode     string                 // "sync" → ?wait=true (default); anything else skipped
	SelectorAuth map[string]string      // overrides ec.AuthHeaders when both signature & message set
}

// resolveNodeID picks the target provider node id using the same precedence
// the JS aiService had: connected nodeSelector → entity-config nodeId →
// first online networkNode supporting the requested service.
func resolveNodeID(ec *pipeline.ExecCtx, configured string, service string) (string, map[string]string) {
	if items := ec.InputsByHandle["node"]; len(items) > 0 {
		if m, ok := items[0].Value.(map[string]any); ok {
			id, _ := m["nodeId"].(string)
			var auth map[string]string
			if a, _ := m["auth"].(map[string]any); a != nil {
				auth = make(map[string]string, len(a))
				for k, v := range a {
					if s, _ := v.(string); s != "" {
						auth[k] = s
					}
				}
			}
			if id != "" {
				return id, auth
			}
		}
	}
	if configured != "" {
		return configured, nil
	}
	return findFirstOnline(ec.NetworkNodes, service, "", ""), nil
}

// resolveOptions returns the options map flowing into the AI node, taking
// the connected `optionsNode` value when present and falling back to the
// entity's own data.options.
func resolveOptions(ec *pipeline.ExecCtx, fallback map[string]any) map[string]any {
	if items := ec.InputsByHandle["options"]; len(items) > 0 {
		if m, ok := items[0].Value.(map[string]any); ok {
			return m
		}
	}
	if fallback != nil {
		return fallback
	}
	return map[string]any{}
}

// applyTextInput populates body["messages"] when upstream produced a chat
// payload, else body["prompt"] from string-or-stringified upstream value.
// Used by both LLM and SD when the primary input edge carries the prompt.
func applyTextInput(ec *pipeline.ExecCtx, body map[string]any) {
	val := primaryInputValue(ec)
	if val == nil {
		return
	}
	if m, ok := val.(map[string]any); ok {
		if msgs, ok := m["messages"].([]any); ok {
			body["messages"] = msgs
			return
		}
	}
	if s, ok := val.(string); ok {
		body["prompt"] = s
		return
	}
	body["prompt"] = jsonStringify(val)
}

// primaryInputValue returns the first value bound to the "input" handle,
// falling back to ec.InputData (which is the first non-config edge anyway).
func primaryInputValue(ec *pipeline.ExecCtx) any {
	if items := ec.InputsByHandle["input"]; len(items) > 0 {
		return items[0].Value
	}
	return ec.InputData
}

// callBroker invokes the broker's in-process NodeCaller to reach the target
// provider service, bypassing the HTTP mux loopback. All AI entities funnel
// through this single function so auth, header construction, and error
// formatting stay consistent.
func callBroker(ctx context.Context, ec *pipeline.ExecCtx, req aiCallRequest) (any, error) {
	if req.NodeID == "" {
		return nil, fmt.Errorf("no node available for service %q", req.Service)
	}
	if req.Service == "" {
		return nil, fmt.Errorf("service is required")
	}
	if req.Endpoint == "" {
		return nil, fmt.Errorf("endpoint is required for service %q", req.Service)
	}
	if ec.NodeCaller == nil {
		return nil, fmt.Errorf("pipeline: NodeCaller not configured")
	}

	method := strings.ToUpper(req.Method)
	if method == "" {
		method = http.MethodPost
	}

	// Service-relative path (NodeCaller takes service as a separate argument).
	path := req.Endpoint
	wait := req.WaitMode
	if wait == "" {
		wait = "sync"
	}
	if wait == "sync" {
		if strings.Contains(path, "?") {
			path += "&wait=true"
		} else {
			path += "?wait=true"
		}
	}

	var body io.Reader
	if method != http.MethodGet {
		raw, err := json.Marshal(req.Body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		body = bytes.NewReader(raw)
	}

	// Scheme/host are ignored by the NodeCaller — it only looks at path + headers.
	httpReq, err := http.NewRequestWithContext(ctx, method, path, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if req.SelectorAuth != nil && req.SelectorAuth["signature"] != "" && req.SelectorAuth["message"] != "" {
		httpReq.Header.Set("Authorization", "ISANN "+req.SelectorAuth["signature"])
		httpReq.Header.Set("X-ISANN-Message", req.SelectorAuth["message"])
	} else {
		for k, v := range ec.AuthHeaders {
			httpReq.Header.Set(k, v)
		}
	}

	resp, err := ec.NodeCaller.CallNode(ctx, req.NodeID, req.Service, httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s call failed: %w", req.Service, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := string(respBody)
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		return nil, fmt.Errorf("%s returned %d: %s", req.Service, resp.StatusCode, snippet)
	}

	var result any
	if err := json.Unmarshal(respBody, &result); err != nil {
		// Non-JSON body (e.g., raw image): describe rather than embed.
		return map[string]any{
			"_contentType": resp.Header.Get("Content-Type"),
			"_size":        len(respBody),
		}, nil
	}
	if m, ok := result.(map[string]any); ok {
		if _, has := m["job_id"]; has {
			m["_nodeId"] = req.NodeID
			m["_service"] = req.Service
		}
	}
	return result, nil
}
